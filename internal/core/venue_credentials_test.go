package core

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/onchaintest"
	"github.com/fased-ai/fased-app/internal/signer"
)

const venuePassword = "disposable venue vault password 123"

func setupCredentialSwapHarness(t *testing.T, s *Store, seed bool) (*swapHarness, *onchaintest.HTTPS, VenueCredentialCommand) {
	t.Helper()
	if os.Getuid() == 0 {
		t.Fatal("non-root fixture required")
	}
	if runtime.GOOS != "linux" {
		t.Skip("managed custody is Linux-only")
	}
	dir := filepath.Join(t.TempDir(), "signer")
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	tls := onchaintest.NewHTTPS(t, "https://mainnet.helius-rpc.com")
	v, e := signer.OpenManagedSwapCredentialsFixture(dir, strings.Repeat("a", 32), "mainnet-beta", func(a onchain.CredentialAccess) (signer.SwapObserver, error) {
		return onchain.NewControlledAuthenticatedVerifier(tls.Transport, a)
	})
	if e != nil {
		t.Fatal(e)
	}
	pw := []byte("disposable-managed-onchain-password")
	if e = v.Create(pw); e != nil {
		t.Fatal(e)
	}
	ref := *v.Status().Account
	s.custodyNetwork = ref.Network
	s.rpcAdmission = true
	s.fixture = func(context.Context, signer.Request) (signer.Response, error) {
		return signer.Response{Status: v.Status()}, nil
	}
	r, e := s.accountsRecord(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	r.Accounts = append(r.Accounts, Account{Version: 3, ID: strings.Repeat("c", 32), Name: "Wallet", Mode: "fixture-managed", Network: ref.Network, PublicKey: ref.PublicKey, Signer: &ref})
	if _, e = s.UpdateAccounts(t.Context(), r); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ConfigureAccountRPC(t.Context(), RPCConfigInput{AccountID: strings.Repeat("c", 32), Revision: r.Revision + 1, Endpoint: tls.RPCOrigin, Network: ref.Network}); e != nil {
		t.Fatal(e)
	}
	h := &swapHarness{s, v, tls.Chain, pw, "trading-analyst"}
	t.Cleanup(func() { v.Close() })
	h.vault = v
	h.chain = tls.Chain
	if _, e = v.ProvisionReceiptRPC(tls.RPCOrigin, []byte(tls.RPCKey), h.password); e != nil {
		t.Fatal(e)
	}
	if _, e = v.UnlockReceiptRPC(h.password, 5*time.Minute); e != nil {
		t.Fatal(e)
	}
	h.connectCredential(t, tls)
	r, e = s.accountsRecord(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	c := VenueCredentialCommand{AccountID: strings.Repeat("c", 32), Revision: r.Revision, Action: "save", Key: tls.JupiterKey, Passphrase: venuePassword}
	if seed {
		if _, e = s.RPCCredential(t.Context(), RPCCredentialCommand{AccountID: c.AccountID, Revision: c.Revision, Action: "save", Key: tls.RPCKey, Passphrase: venuePassword}); e != nil {
			t.Fatal(e)
		}
		saved, e := s.VenueCredential(t.Context(), c)
		if e != nil {
			t.Fatal(e)
		}
		c.Ref = saved.Ref
		c.Action = "unlock"
		c.Key = ""
		c.Seconds = 300
		if _, e = s.VenueCredential(t.Context(), c); e != nil {
			t.Fatal(e)
		}
	}
	return h, tls, c
}
func (h *swapHarness) connectCredential(t *testing.T, tls *onchaintest.HTTPS) {
	t.Helper()
	h.store.rpcAdmission = true
	h.store.custodyNetwork = "mainnet-beta"
	h.store.fixture = func(context.Context, signer.Request) (signer.Response, error) {
		return signer.Response{Status: h.vault.Status()}, nil
	}
	h.connect()
	if e := h.store.configureCredentialVenue(func(a onchain.CredentialAccess) (swapVenue, error) {
		return onchain.NewControlledAuthenticatedClient(tls.Transport, a)
	}); e != nil {
		t.Fatal(e)
	}
}
func (h *swapHarness) approveCredential(t *testing.T, p *SwapReview) {
	t.Helper()
	if e := h.vault.Unlock(h.password, time.Minute); e != nil {
		t.Fatal(e)
	}
	if _, e := h.vault.UnlockReceiptRPC(h.password, 5*time.Minute); e != nil {
		t.Fatal(e)
	}
	h.submit(t, p)
}
func TestVenueCredentialHTTPSJourneyIndependentReceipt(t *testing.T) {
	h, tls, _ := setupCredentialSwapHarness(t, fixtureStore(t), true)
	p := h.quote(t)
	if p.Proposal.Version != signer.ManagedCredentialSwapVersion || p.Proposal.ReceiptRPC == nil {
		t.Fatal("missing independent reader binding")
	}
	h.approveCredential(t, p)
	if e := h.store.advanceOnchain(t.Context()); e != nil {
		t.Fatal(e)
	}
	v := h.command(t, "swap-view", SwapCommand{})
	if len(v.Runs) != 1 || v.Runs[0].Swap.State != "confirmed" || v.Runs[0].Swap.Observation.Output != "100000000" || h.chain.Submissions != 1 {
		t.Fatal("joined result", v)
	}
	if !h.vault.Status().RecoveryRequired {
		t.Fatal("general recovery hold cleared")
	}
	reads := 0
	for _, m := range tls.Requests() {
		if m == "getTransaction" {
			reads++
		}
	}
	if reads < 2 {
		t.Fatal("app and signer did not independently read original", tls.Requests())
	}
	if e := h.store.advanceOnchain(t.Context()); e != nil || h.chain.Submissions != 1 {
		t.Fatal("duplicate submission", e)
	}
	saveSwapEvidence(t, "credential-https-confirmed", v)
}
func TestVenueCredentialPersistencePurposeAndRevocation(t *testing.T) {
	h, tls, c := setupCredentialSwapHarness(t, fixtureStore(t), true)
	r, _ := h.store.accountsRecord(t.Context())
	a, _ := swapAccount(r, c.AccountID)
	access, e := h.store.venueAccess(a)
	if e != nil {
		t.Fatal(e)
	}
	for _, purpose := range []string{"rpc", "jupiter"} {
		key, e := access.Borrow(t.Context(), purpose)
		if e != nil {
			t.Fatal(e)
		}
		if purpose == "rpc" && string(key) != tls.RPCKey || purpose == "jupiter" && string(key) != tls.JupiterKey {
			t.Fatal("purpose substitution")
		}
		clear(key)
	}
	if _, e = access.Borrow(t.Context(), "wallet"); e == nil {
		t.Fatal("wallet authority borrowed")
	}
	filepath.WalkDir(h.store.dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		if !entry.IsDir() {
			b, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			for _, secret := range []string{tls.RPCKey, tls.JupiterKey, venuePassword} {
				if bytes.Contains(b, []byte(secret)) {
					t.Fatal("plaintext stored", path)
				}
			}
		}
		return nil
	})
	before, _ := os.ReadFile(h.store.venuePath(a.ID))
	c.Action = "lock"
	c.Passphrase = ""
	c.Seconds = 0
	if v, e := h.store.VenueCredential(t.Context(), c); e != nil || !v.Locked {
		t.Fatal(v, e)
	}
	if access.Valid() == nil {
		t.Fatal("old lease survived lock")
	}
	after, _ := os.ReadFile(h.store.venuePath(a.ID))
	if !bytes.Equal(before, after) {
		t.Fatal("locking changed ciphertext")
	}
	c.Action = "unlock"
	c.Passphrase = venuePassword
	c.Seconds = 60
	if _, e = h.store.VenueCredential(t.Context(), c); e != nil {
		t.Fatal(e)
	}
	access, e = h.store.venueAccess(a)
	if e != nil {
		t.Fatal(e)
	}
	if access.Valid() != nil {
		t.Fatal("renewed lease rejected")
	}
	c.Passphrase = "wrong disposable password"
	if _, e = h.store.VenueCredential(t.Context(), c); e == nil || access.Valid() == nil {
		t.Fatal("failed unlock retained authority")
	}
	if !bytes.Equal(before, after) {
		t.Fatal("ciphertext changed")
	}
}
func TestVenueCredentialRefusesChangedCiphertextAndQueuedLease(t *testing.T) {
	for _, change := range []string{"ciphertext", "rpc-rotation", "signout", "expiry", "network"} {
		t.Run(change, func(t *testing.T) {
			h, _, c := setupCredentialSwapHarness(t, fixtureStore(t), true)
			p := h.quote(t)
			h.approveCredential(t, p)
			r, _ := h.store.accountsRecord(t.Context())
			a, _ := swapAccount(r, c.AccountID)
			access, e := h.store.venueAccess(a)
			if e != nil {
				t.Fatal(e)
			}
			switch change {
			case "ciphertext":
				v, _ := h.store.readVenue(a.ID)
				v.Ciphertext[0] ^= 1
				if e = WritePrivateJSON(h.store.venuePath(a.ID), v); e != nil {
					t.Fatal(e)
				}
			case "rpc-rotation":
				v, _ := h.store.readRPCVault(a.ID)
				_, e = h.store.RPCCredential(t.Context(), RPCCredentialCommand{AccountID: a.ID, Revision: r.Revision, Ref: v.Ref, Action: "save", Key: strings.Repeat("z", 32), Passphrase: venuePassword})
				if e != nil {
					t.Fatal(e)
				}
			case "signout":
				if e = h.store.Logout(t.Context(), strings.Repeat("a", 43)); e != nil {
					t.Fatal(e)
				}
			case "expiry":
				h.store.now = func() time.Time { return time.Now().Add(6 * time.Minute) }
			case "network":
				r.Accounts[len(r.Accounts)-1].RPC.Endpoint = "https://solana-mainnet.g.alchemy.com"
				b, _ := json.Marshal(r)
				h.store.db.Exec("UPDATE accounts SET document=? WHERE id=1", string(b))
			}
			if access.Valid() == nil {
				t.Fatal("changed/revoked lease accepted")
			}
			if e = h.store.advanceOnchain(t.Context()); e != nil {
				t.Fatal(e)
			}
			if h.chain.Submissions != 0 {
				t.Fatal("revoked queued approval sent")
			}
		})
	}
}
func TestVenueCredentialUnknownRestartRetainsOriginal(t *testing.T) {
	h, tls, c := setupCredentialSwapHarness(t, fixtureStore(t), true)
	p := h.quote(t)
	h.approveCredential(t, p)
	h.chain.Unknown = true

	if e := h.store.advanceOnchain(t.Context()); e != nil {
		t.Fatal(e)
	}
	v := h.command(t, "swap-view", SwapCommand{})
	packet := append([]byte(nil), v.Runs[0].Swap.Signed...)
	if v.Runs[0].Swap.State != "unknown" || len(packet) == 0 || h.chain.Submissions != 1 {
		t.Fatal("missing uncertain original")
	}
	sealed, _ := os.ReadFile(h.store.venuePath(c.AccountID))
	dir := h.store.dir
	h.store.Close()
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	h.store = s
	h.connectCredential(t, tls)
	status := c
	status.Action = "status"
	status.Passphrase = ""
	status.Seconds = 0
	got, e := s.VenueCredential(t.Context(), status)
	if e != nil || !got.Saved || !got.Locked {
		t.Fatal(got, e)
	}
	after, _ := os.ReadFile(s.venuePath(c.AccountID))
	if !bytes.Equal(sealed, after) {
		t.Fatal("restart changed encrypted config")
	}
	if e = s.advanceOnchain(t.Context()); e != nil || h.chain.Submissions != 1 {
		t.Fatal("restart replayed", e)
	}
	if _, e = s.VenueCredential(t.Context(), c); e != nil {
		t.Fatal(e)
	}
	h.chain.Unknown = false
	final := h.command(t, "swap-reconcile", SwapCommand{ID: v.Tasks[0].ID})
	if final.Runs[0].Swap.State != "confirmed" || !bytes.Equal(packet, final.Runs[0].Swap.Signed) || h.chain.Submissions != 1 {
		t.Fatal("original not retained")
	}
	saveSwapEvidence(t, "credential-https-restart", final)
}

func TestVenueCredentialRetireAfterRPCPasswordChange(t *testing.T) {
	h, tls, c := setupCredentialSwapHarness(t, fixtureStore(t), true)
	rpc, _ := h.store.readRPCVault(c.AccountID)
	remove := RPCCredentialCommand{AccountID: c.AccountID, Revision: c.Revision, Ref: rpc.Ref, Action: "remove", Passphrase: venuePassword}
	if _, e := h.store.RPCCredential(t.Context(), remove); e != nil {
		t.Fatal(e)
	}
	status := c
	status.Action = "status"
	status.Passphrase = ""
	status.Seconds = 0
	v, e := h.store.VenueCredential(t.Context(), status)
	if e != nil || !v.Saved || !v.Stale || !v.Locked {
		t.Fatal("retained credential unavailable", v, e)
	}
	// Reconnect RPC with a new password. Retiring the previous Jupiter envelope
	// still uses its original password; no manual file deletion is required.
	newPassword := "different disposable RPC password 123"
	if _, e = h.store.RPCCredential(t.Context(), RPCCredentialCommand{AccountID: c.AccountID, Revision: c.Revision, Action: "save", Key: tls.RPCKey, Passphrase: newPassword}); e != nil {
		t.Fatal(e)
	}
	c.Action = "remove"
	c.Seconds = 0
	if retired, err := h.store.VenueCredential(t.Context(), c); err != nil || !retired.RPCReady || retired.Saved || !retired.Locked {
		t.Fatal("cannot retire/reconnect old ciphertext", retired, err)
	}
	c.Action = "save"
	c.Ref = ""
	c.Key = tls.JupiterKey
	c.Passphrase = newPassword
	saved, e := h.store.VenueCredential(t.Context(), c)
	if e != nil {
		t.Fatal(e)
	}
	c.Ref = saved.Ref
	c.Action = "unlock"
	c.Key = ""
	c.Seconds = 60
	if v, e = h.store.VenueCredential(t.Context(), c); e != nil || v.Locked {
		t.Fatal("cannot reconnect", v, e)
	}
}

func TestVenueCredentialSchema32PreservesRecordsAndRefusesUnprotectedPolicy(t *testing.T) {
	s := fixtureStore(t)
	var before string
	var revision int64
	if e := s.db.QueryRow("SELECT document,revision FROM agent_workspace WHERE id=1").Scan(&before, &revision); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec("PRAGMA user_version=32"); e != nil {
		t.Fatal(e)
	}
	dir := s.dir
	s.Close()
	next, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	var after string
	var gotRevision int64
	var schema int
	next.db.QueryRow("SELECT document,revision FROM agent_workspace WHERE id=1").Scan(&after, &gotRevision)
	next.db.QueryRow("PRAGMA user_version").Scan(&schema)
	if before != after || revision != gotRevision || schema != 33 {
		t.Fatal("migration rewrote records or revision")
	}
	h, _, _ := setupCredentialSwapHarness(t, fixtureStore(t), true)
	p := h.quote(t)
	h.approveCredential(t, p)
	if _, e = h.store.db.Exec("PRAGMA user_version=32"); e != nil {
		t.Fatal(e)
	}
	dir = h.store.dir
	h.store.Close()
	if old, e := OpenStore(dir); e == nil {
		old.Close()
		t.Fatal("unprotected credential-dependent policy migrated")
	}
}

func TestVenueCredentialInFlightUnlockCannotOutliveRevocation(t *testing.T) {
	for _, action := range []string{"logout", "close"} {
		t.Run(action, func(t *testing.T) {
			h, _, c := setupCredentialSwapHarness(t, fixtureStore(t), true)
			h.store.venueCredentials.mu.Lock()
			initial := h.store.venueCredentials.generation
			h.store.venueCredentials.mu.Unlock()
			done := make(chan error, 1)
			go func() { _, e := h.store.VenueCredential(t.Context(), c); done <- e }()
			deadline := time.Now().Add(5 * time.Second)
			for {
				h.store.venueCredentials.mu.Lock()
				started := h.store.venueCredentials.generation > initial
				h.store.venueCredentials.mu.Unlock()
				if started {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("unlock never reached revocation fence")
				}
				time.Sleep(time.Millisecond)
			}
			if action == "logout" {
				if e := h.store.Logout(t.Context(), strings.Repeat("x", 43)); e != nil {
					t.Fatal(e)
				}
			} else {
				if e := h.store.Close(); e != nil {
					t.Fatal(e)
				}
			}
			<-done
			h.store.venueCredentials.mu.Lock()
			defer h.store.venueCredentials.mu.Unlock()
			if h.store.venueCredentials.lease != nil {
				t.Fatal("in-flight unlock retained decrypted keys after", action)
			}
			if action == "close" && !h.store.venueCredentials.closed {
				t.Fatal("closed store may admit new leases")
			}
		})
	}
}

func TestVenueCredentialHTTPUsesExistingOwnerAndCSRFBoundary(t *testing.T) {
	s := fixtureStore(t)
	token := claimFixture(t, s)
	_, _, c := setupCredentialSwapHarness(t, s, true)
	h, e := NewHTTPHandler(s, "http://127.0.0.1:7331", "", fstest.MapFS{"index.html": {Data: []byte("fixture")}})
	if e != nil {
		t.Fatal(e)
	}
	c.Action = "status"
	c.Passphrase = ""
	c.Seconds = 0
	body, _ := json.Marshal(c)
	for _, test := range []struct {
		token, origin, csrf string
		want                int
	}{
		{"", "http://127.0.0.1:7331", "", 401},
		{token, "http://127.0.0.1:7331", "", 403},
		{token, "https://untrusted.invalid", csrfToken(token), 403},
		{token, "http://127.0.0.1:7331", csrfToken(token), 200},
	} {
		w := apiRequest(h, "PUT", "/api/v1/accounts/venue-credential", string(body), test.token, test.origin, test.csrf)
		if w.Code != test.want {
			t.Fatal(w.Code, test.want, w.Body.String())
		}
		if bytes.Contains(w.Body.Bytes(), []byte(venuePassword)) {
			t.Fatal("secret response")
		}
	}
}

type venueSlowBody struct {
	io.ReadCloser
	entered chan struct{}
	once    sync.Once
}

func (b *venueSlowBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.entered) })
	return b.ReadCloser.Read(p)
}

func TestVenueCredentialDelayedHTTPBodyCannotCrossLogout(t *testing.T) {
	s := fixtureStore(t)
	token := claimFixture(t, s)
	_, _, c := setupCredentialSwapHarness(t, s, true)
	h, e := NewHTTPHandler(s, "http://127.0.0.1:7331", "", fstest.MapFS{"index.html": {Data: []byte("fixture")}})
	if e != nil {
		t.Fatal(e)
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	body := &venueSlowBody{ReadCloser: reader, entered: make(chan struct{})}
	r := httptest.NewRequest("PUT", "http://127.0.0.1:7331/api/v1/accounts/venue-credential", body)
	r.Header.Set("Origin", "http://127.0.0.1:7331")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-CSRF-Token", csrfToken(token))
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { h.ServeHTTP(w, r); close(done) }()
	select {
	case <-body.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not reach body")
	}
	if e = s.Logout(t.Context(), token); e != nil {
		t.Fatal(e)
	}
	data, _ := json.Marshal(c)
	if _, e = writer.Write(data); e != nil {
		t.Fatal(e)
	}
	writer.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not finish")
	}
	if w.Code == 200 {
		t.Fatal("revoked session unlocked after delayed body")
	}
	s.venueCredentials.mu.Lock()
	defer s.venueCredentials.mu.Unlock()
	if s.venueCredentials.lease != nil {
		t.Fatal("revoked session retained keys")
	}
}

func TestVenueCredentialAdmissionDuringLogoutIsFinallyRevoked(t *testing.T) {
	s := fixtureStore(t)
	token := claimFixture(t, s)
	_, _, c := setupCredentialSwapHarness(t, s, true)
	h, e := NewHTTPHandler(s, "http://127.0.0.1:7331", "", fstest.MapFS{"index.html": {Data: []byte("fixture")}})
	if e != nil {
		t.Fatal(e)
	}
	s.venueCredentials.mu.Lock()
	initial := s.venueCredentials.generation
	s.venueCredentials.mu.Unlock()
	// Pause logout between initial revocation and session deletion using the
	// existing provider mutex. No production fault hook or timer is introduced.
	s.provider.mu.Lock()
	released := false
	defer func() {
		if !released {
			s.provider.mu.Unlock()
		}
	}()
	done := make(chan error, 1)
	go func() { done <- s.Logout(t.Context(), token) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.venueCredentials.mu.Lock()
		started := s.venueCredentials.generation > initial
		s.venueCredentials.mu.Unlock()
		if started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("logout did not reach revocation")
		}
		time.Sleep(time.Millisecond)
	}
	b, _ := json.Marshal(c)
	w := apiRequest(h, "PUT", "/api/v1/accounts/venue-credential", string(b), token, "http://127.0.0.1:7331", csrfToken(token))
	if w.Code != 200 {
		t.Fatal("request was not admitted in the tested window", w.Code, w.Body.String())
	}
	s.provider.mu.Unlock()
	released = true
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	s.venueCredentials.mu.Lock()
	defer s.venueCredentials.mu.Unlock()
	if s.venueCredentials.lease != nil {
		t.Fatal("completed logout retained an intervening credential lease")
	}
}
