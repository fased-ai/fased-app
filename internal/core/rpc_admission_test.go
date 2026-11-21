package core

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/onchain"
)

func loopbackRPCTLSTransport(t *testing.T, server *httptest.Server, expectedHost string) *publicRPCTransport {
	t.Helper()
	rt := newPublicRPCTransport()
	rt.enabled = true
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	rt.transport.TLSClientConfig.RootCAs = roots
	rt.transport.TLSClientConfig.ServerName = "example.com"
	rt.transport.DialContext = func(ctx context.Context, network, destination string) (net.Conn, error) {
		if destination != expectedHost+":443" {
			t.Fatalf("unexpected dial destination: %s", destination)
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	t.Cleanup(rt.transport.CloseIdleConnections)
	return rt
}

func loopbackSolanaRPCServer(t *testing.T, network, expectedHost, expectedQueryKey, address string) *httptest.Server {
	t.Helper()
	mint := onchain.Base58(bytes.Repeat([]byte{3}, 32))
	sig := onchain.Base58(bytes.Repeat([]byte{6}, 64))
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int               `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Error("invalid JSON-RPC request")
			return
		}
		if r.Method != http.MethodPost || r.Host != expectedHost || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Errorf("unexpected wire request: method=%s host=%s", r.Method, r.Host)
		}
		if expectedQueryKey != "" {
			if r.URL.Path != "/" || r.URL.Query().Get("api-key") != expectedQueryKey || len(r.URL.Query()) != 1 {
				t.Errorf("unexpected keyed RPC path/query: %s", r.URL.String())
			}
		} else if (r.URL.Path != "" && r.URL.Path != "/") || r.URL.RawQuery != "" {
			t.Errorf("unexpected keyless RPC path/query: %s", r.URL.String())
		}
		var result any
		switch request.Method {
		case "getGenesisHash":
			result = solanaGenesis(network)
		case "getHealth":
			result = "ok"
		case "getBalance":
			result = map[string]any{"context": map[string]any{"slot": 100}, "value": uint64(9007199254740993)}
		case "getTokenAccountsByOwner":
			var filter struct {
				Program string `json:"programId"`
			}
			_ = json.Unmarshal(request.Params[1], &filter)
			entries := []any{}
			if filter.Program == onchain.Token {
				entries = append(entries, map[string]any{
					"pubkey": onchain.Base58(bytes.Repeat([]byte{4}, 32)),
					"account": map[string]any{
						"owner":      filter.Program,
						"executable": false,
						"data": map[string]any{
							"parsed": map[string]any{
								"type": "account",
								"info": map[string]any{
									"owner": address,
									"mint":  mint,
									"state": "initialized",
									"tokenAmount": map[string]any{
										"amount":   "4200000",
										"decimals": 6,
									},
								},
							},
						},
					},
				})
			}
			result = map[string]any{"context": map[string]any{"slot": 100}, "value": entries}
		case "getSignaturesForAddress":
			result = []any{map[string]any{
				"signature":          sig,
				"slot":               99,
				"err":                nil,
				"blockTime":          nil,
				"confirmationStatus": "finalized",
			}}
		case "getTransaction":
			result = walletTransactionFixture()
		default:
			t.Errorf("unexpected JSON-RPC method: %s", request.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	t.Cleanup(server.Close)
	return server
}

func TestRPCActivationDefaultsAndSafetyBoundaries(t *testing.T) {
	t.Parallel()
	if !rpcActivationEnabled {
		t.Fatal("rpcActivationEnabled must be true")
	}
	s, err := openStoreWithTaskSecrets(fixtureDir(t), &testTaskSecrets{key: bytes.Repeat([]byte{17}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !s.rpcAdmission {
		t.Fatal("ordinary store did not activate read-only RPC admission")
	}
	h := s.Health()
	if h.Capabilities["walletSigner"] || h.Capabilities["financialExecution"] || h.Capabilities["updater"] {
		t.Fatalf("unexpected sensitive capability enabled: %+v", h.Capabilities)
	}
	if !h.Capabilities["aiConnections"] || !h.Capabilities["ownerAuthentication"] || !h.Capabilities["persistentSetup"] {
		t.Fatalf("missing baseline capabilities: %+v", h.Capabilities)
	}
	claimFixture(t, s)
	address := onchain.Base58(bytes.Repeat([]byte{2}, 32))
	view, err := s.SetupAccount(t.Context(), AccountSetupCommand{Action: "watch", Name: "Devnet Watch", Network: "devnet", Address: address})
	if err != nil {
		t.Fatal(err)
	}
	if !view.Setup.NetworkWatchEnabled {
		t.Fatal("network watch disabled on ordinary store")
	}
	var gen uint64
	if _, err := s.venueCredential(t.Context(), VenueCredentialCommand{AccountID: view.Record.Accounts[0].ID, Revision: view.Record.Revision, Action: "status"}, &gen); err == nil {
		t.Fatal("venue credential opened without onchain controller")
	}
	f := fixtureStore(t)
	if f.rpcAdmission {
		t.Fatal("fixture store must keep rpcAdmission closed")
	}
}

func TestSolanaPublicRPCLiveCheckAndKeylessReadWallet(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		network, endpoint, host string
	}{
		{"devnet", "https://api.devnet.solana.com", "api.devnet.solana.com"},
		{"mainnet-beta", "https://api.mainnet-beta.solana.com", "api.mainnet-beta.solana.com"},
	} {
		t.Run(tc.network, func(t *testing.T) {
			s, err := openStoreWithTaskSecrets(fixtureDir(t), &testTaskSecrets{unavailable: true})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			claimFixture(t, s)
			address := onchain.Base58(bytes.Repeat([]byte{2}, 32))
			v, err := s.SetupAccount(t.Context(), AccountSetupCommand{
				Action:  "watch",
				Name:    "Public Watch",
				Network: tc.network,
				Address: address,
			})
			if err != nil {
				t.Fatal(err)
			}
			acct := v.Record.Accounts[0]
			server := loopbackSolanaRPCServer(t, tc.network, tc.host, "", address)
			rt := loopbackRPCTLSTransport(t, server, tc.host)

			checkResult, err := checkPublicRPC(t.Context(), RPCCheckRequest{Endpoint: tc.endpoint, Network: tc.network}, rt)
			if err != nil || checkResult.FixtureOnly || !checkResult.LiveRPC || checkResult.GenesisHash != solanaGenesis(tc.network) {
				t.Fatalf("unexpected public RPC check result=%+v err=%v", checkResult, err)
			}
			v, err = s.ConfigureAccountRPC(t.Context(), RPCConfigInput{
				AccountID: acct.ID,
				Revision:  v.Record.Revision,
				Endpoint:  tc.endpoint,
				Network:   tc.network,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.RPCCredential(t.Context(), RPCCredentialCommand{
				AccountID:  acct.ID,
				Revision:   v.Record.Revision,
				Action:     "save",
				Key:        disposableProviderKey,
				Passphrase: "Disposable backup password 123",
			}); err == nil {
				t.Fatal("keyless Solana public endpoint accepted an API key")
			}

			s.walletReadFixture = func(ctx context.Context, a Account, addr string, _ []byte, valid func() error, selection *WalletSelection) (WalletSnapshot, error) {
				return walletRPC(ctx, a.RPC.Endpoint, rt, valid, false, addr, a.Network, selection)
			}
			if _, err := s.ReadWallet(t.Context(), WalletReadCommand{AccountID: acct.ID, Revision: v.Record.Revision, Ref: "unexpected-ref"}); err == nil {
				t.Fatal("keyless read accepted non-empty credential ref")
			}
			if _, err := s.ReadWallet(t.Context(), WalletReadCommand{AccountID: acct.ID, Revision: v.Record.Revision, Passphrase: "unexpected-password-123"}); err == nil {
				t.Fatal("keyless read accepted non-empty passphrase")
			}
			snap, err := s.ReadWallet(t.Context(), WalletReadCommand{AccountID: acct.ID, Revision: v.Record.Revision})
			if err != nil {
				t.Fatal(err)
			}
			if snap.FixtureOnly || !snap.LiveRPC || snap.CredentialRef != "" || snap.Binding == "" || snap.Lamports != "9007199254740993" || len(snap.Tokens) != 1 || len(snap.Activity) != 1 {
				t.Fatalf("unexpected keyless wallet snapshot: %+v", snap)
			}
			detailSnap, err := s.ReadWallet(t.Context(), WalletReadCommand{
				AccountID: acct.ID,
				Revision:  v.Record.Revision,
				Selection: &WalletSelection{
					WalletActivity: snap.Activity[0],
					Binding:        snap.Binding,
					FetchedAt:      snap.FetchedAt,
					ExpiresAt:      snap.ExpiresAt,
				},
			})
			if err != nil || detailSnap.Transaction == nil || !detailSnap.LiveRPC || detailSnap.FixtureOnly {
				t.Fatalf("unexpected keyless transaction detail: %+v err=%v", detailSnap, err)
			}
			if _, err := s.ReadWallet(t.Context(), WalletReadCommand{AccountID: acct.ID, Revision: v.Record.Revision + 1}); err == nil {
				t.Fatal("stale revision accepted on keyless read")
			}
			cleared, err := s.ConfigureAccountRPC(t.Context(), RPCConfigInput{AccountID: acct.ID, Revision: v.Record.Revision, Remove: true})
			if err != nil || cleared.Record.Accounts[0].RPC != nil {
				t.Fatalf("failed to remove public RPC config: %v", err)
			}
		})
	}
}

func TestOSProtectedRPCCredentialVaultLifecycle(t *testing.T) {
	t.Parallel()
	dir := fixtureDir(t)
	osSecrets := &testTaskSecrets{key: bytes.Repeat([]byte{91}, 32)}
	s, err := openStoreWithTaskSecrets(dir, osSecrets)
	if err != nil {
		t.Fatal(err)
	}
	claimFixture(t, s)
	address := onchain.Base58(bytes.Repeat([]byte{2}, 32))
	v, err := s.SetupAccount(t.Context(), AccountSetupCommand{
		Action:  "watch",
		Name:    "Helius Watch",
		Network: "devnet",
		Address: address,
	})
	if err != nil {
		_ = s.Close()
		t.Fatal(err)
	}
	acctID := v.Record.Accounts[0].ID
	v, err = s.ConfigureAccountRPC(t.Context(), RPCConfigInput{
		AccountID: acctID,
		Revision:  v.Record.Revision,
		Endpoint:  "https://devnet.helius-rpc.com",
		Network:   "devnet",
	})
	if err != nil {
		_ = s.Close()
		t.Fatal(err)
	}
	rev := v.Record.Revision

	initial, err := s.RPCCredential(t.Context(), RPCCredentialCommand{AccountID: acctID, Revision: rev, Action: "status"})
	if err != nil || !initial.OSVaultAvailable || initial.Saved || initial.OSProtected {
		_ = s.Close()
		t.Fatalf("unexpected initial status: %+v err=%v", initial, err)
	}

	// Passwordless save seals via OS vault key without persisting plaintext.
	saved, err := s.RPCCredential(t.Context(), RPCCredentialCommand{
		AccountID: acctID,
		Revision:  rev,
		Action:    "save",
		Key:       disposableProviderKey,
	})
	if err != nil || !saved.Saved || !saved.OSProtected || !saved.OSVaultAvailable || !saved.CanVerify || !saved.CanCheck || saved.Ref == "" {
		_ = s.Close()
		t.Fatalf("unexpected passwordless save: %+v err=%v", saved, err)
	}
	diskBytes, err := os.ReadFile(s.rpcVaultPath(acctID))
	if err != nil || bytes.Contains(diskBytes, []byte(disposableProviderKey)) || !strings.Contains(string(diskBytes), `"osProtected":true`) {
		_ = s.Close()
		t.Fatalf("unexpected on-disk OS vault: err=%v body=%s", err, string(diskBytes))
	}

	// Verify & loopback TLS check work with empty passphrase; non-empty fake passphrase fails.
	if _, err := s.RPCCredential(t.Context(), RPCCredentialCommand{AccountID: acctID, Revision: rev, Ref: saved.Ref, Action: "verify", Passphrase: "unexpected-password-123"}); err == nil {
		_ = s.Close()
		t.Fatal("OS-protected verify accepted non-empty passphrase")
	}
	verified, err := s.RPCCredential(t.Context(), RPCCredentialCommand{AccountID: acctID, Revision: rev, Ref: saved.Ref, Action: "verify"})
	if err != nil || !verified.Saved || !verified.OSProtected {
		_ = s.Close()
		t.Fatalf("passwordless verify failed: %+v err=%v", verified, err)
	}

	server := loopbackSolanaRPCServer(t, "devnet", "devnet.helius-rpc.com", disposableProviderKey, address)
	rt := loopbackRPCTLSTransport(t, server, "devnet.helius-rpc.com")
	checked, err := s.rpcCredential(t.Context(), RPCCredentialCommand{
		AccountID: acctID,
		Revision:  rev,
		Ref:       saved.Ref,
		Action:    "check",
	}, func(ctx context.Context, in RPCCheckRequest, key []byte, valid func() error) (RPCCheckResult, error) {
		return checkProviderRPC(ctx, in, key, valid, rt)
	})
	if err != nil || checked.Check == nil || !checked.Check.LiveRPC || checked.Check.FixtureOnly {
		_ = s.Close()
		t.Fatalf("passwordless check failed: %+v err=%v", checked, err)
	}

	// ReadWallet works passwordlessly via OS-protected vault (and refuses fake passphrase).
	s.walletReadFixture = func(ctx context.Context, a Account, addr string, key []byte, valid func() error, selection *WalletSelection) (WalletSnapshot, error) {
		transport := &providerRPCTransport{origin: a.RPC.Endpoint, provider: "helius", key: key, valid: valid, next: rt}
		return walletRPC(ctx, a.RPC.Endpoint, transport, valid, false, addr, a.Network, selection)
	}
	if _, err := s.ReadWallet(t.Context(), WalletReadCommand{AccountID: acctID, Revision: rev, Ref: saved.Ref, Passphrase: "unexpected-password-123"}); err == nil {
		_ = s.Close()
		t.Fatal("OS-protected ReadWallet accepted non-empty passphrase")
	}
	snap, err := s.ReadWallet(t.Context(), WalletReadCommand{AccountID: acctID, Revision: rev, Ref: saved.Ref})
	if err != nil || !snap.LiveRPC || snap.FixtureOnly || snap.CredentialRef != saved.Ref {
		_ = s.Close()
		t.Fatalf("passwordless ReadWallet failed: %+v err=%v", snap, err)
	}
	_ = s.Close()

	// Wrong OS key cannot verify or read after restart.
	wrongOS, err := openStoreWithTaskSecrets(dir, &testTaskSecrets{key: bytes.Repeat([]byte{99}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrongOS.RPCCredential(t.Context(), RPCCredentialCommand{AccountID: acctID, Revision: rev, Ref: saved.Ref, Action: "verify"}); err == nil {
		_ = wrongOS.Close()
		t.Fatal("wrong OS key verified RPC credential")
	}
	if _, err := wrongOS.ReadWallet(t.Context(), WalletReadCommand{AccountID: acctID, Revision: rev, Ref: saved.Ref}); err == nil {
		_ = wrongOS.Close()
		t.Fatal("wrong OS key opened RPC credential")
	}
	_ = wrongOS.Close()

	// Matching OS key restores passwordless verify & ReadWallet after restart, plus portable passphrase-encrypted backup/restore and 1-click removal.
	reopened, err := openStoreWithTaskSecrets(dir, osSecrets)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reopened.walletReadFixture = func(ctx context.Context, a Account, addr string, key []byte, valid func() error, selection *WalletSelection) (WalletSnapshot, error) {
		transport := &providerRPCTransport{origin: a.RPC.Endpoint, provider: "helius", key: key, valid: valid, next: rt}
		return walletRPC(ctx, a.RPC.Endpoint, transport, valid, false, addr, a.Network, selection)
	}
	restoredStatus, err := reopened.RPCCredential(t.Context(), RPCCredentialCommand{AccountID: acctID, Revision: rev, Action: "status"})
	if err != nil || !restoredStatus.Saved || !restoredStatus.OSProtected || !restoredStatus.OSVaultAvailable || restoredStatus.Ref != saved.Ref {
		t.Fatalf("reopened OS vault status mismatch: %+v err=%v", restoredStatus, err)
	}
	if _, err := reopened.RPCCredential(t.Context(), RPCCredentialCommand{AccountID: acctID, Revision: rev, Ref: saved.Ref, Action: "verify"}); err != nil {
		t.Fatal("reopened passwordless verify failed:", err)
	}
	reopenedSnap, err := reopened.ReadWallet(t.Context(), WalletReadCommand{AccountID: acctID, Revision: rev, Ref: saved.Ref})
	if err != nil || reopenedSnap.Lamports != "9007199254740993" || !reopenedSnap.LiveRPC {
		t.Fatalf("reopened passwordless ReadWallet failed: %+v err=%v", reopenedSnap, err)
	}

	// Portable backups ALWAYS require a backup password.
	if _, err := reopened.RPCCredential(t.Context(), RPCCredentialCommand{AccountID: acctID, Revision: rev, Ref: saved.Ref, Action: "backup"}); err == nil {
		t.Fatal("OS-protected backup succeeded without backup password")
	}
	const backupPassword = "Portable RPC backup passphrase 123"
	backed, err := reopened.RPCCredential(t.Context(), RPCCredentialCommand{AccountID: acctID, Revision: rev, Ref: saved.Ref, Action: "backup", Passphrase: backupPassword})
	if err != nil || !backed.BackupVerified || backed.Backup == nil || backed.Backup.Vault.OSProtected {
		t.Fatalf("portable backup from OS vault failed: %+v err=%v", backed, err)
	}
	if _, err := reopened.RPCCredential(t.Context(), RPCCredentialCommand{AccountID: acctID, Revision: rev, Ref: saved.Ref, Action: "verify-backup", Passphrase: backupPassword, Backup: backed.Backup}); err != nil {
		t.Fatal("portable backup verify failed:", err)
	}

	// 1-click passwordless removal.
	removed, err := reopened.RPCCredential(t.Context(), RPCCredentialCommand{AccountID: acctID, Revision: rev, Ref: saved.Ref, Action: "remove"})
	if err != nil || removed.Saved || removed.OSProtected || !removed.OSVaultAvailable {
		t.Fatalf("passwordless remove failed: %+v err=%v", removed, err)
	}
	if _, err := os.Stat(reopened.rpcVaultPath(acctID)); !os.IsNotExist(err) {
		t.Fatalf("vault file remained after remove: %v", err)
	}

	// Restore portable backup using backupPassword.
	if _, err := reopened.RPCCredential(t.Context(), RPCCredentialCommand{AccountID: acctID, Revision: rev, Action: "restore", Backup: backed.Backup}); err == nil {
		t.Fatal("restore accepted empty passphrase")
	}
	restoredVault, err := reopened.RPCCredential(t.Context(), RPCCredentialCommand{AccountID: acctID, Revision: rev, Action: "restore", Passphrase: backupPassword, Backup: backed.Backup})
	if err != nil || !restoredVault.Saved || restoredVault.OSProtected {
		t.Fatalf("portable backup restore failed: %+v err=%v", restoredVault, err)
	}
	restoredSnap, err := reopened.ReadWallet(t.Context(), WalletReadCommand{AccountID: acctID, Revision: rev, Ref: restoredVault.Ref, Passphrase: backupPassword})
	if err != nil || !restoredSnap.LiveRPC || restoredSnap.Lamports != "9007199254740993" {
		t.Fatalf("restored ReadWallet failed: %+v err=%v", restoredSnap, err)
	}
}
