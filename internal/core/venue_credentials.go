package core

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/update"
)

// Venue credentials are not wallet authority. RPC retains its existing encrypted
// format; Jupiter has a separate purpose-bound envelope and never receives the
// RPC key. Neither is decrypted by ordinary startup.
type VenueCredentialCommand struct {
	AccountID  string `json:"accountId"`
	Revision   int64  `json:"revision"`
	Action     string `json:"action"`
	Ref        string `json:"ref"`
	Key        string `json:"key,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
	Seconds    int    `json:"seconds,omitempty"`
}
type VenueCredentialView struct {
	RPCReady      bool   `json:"rpcReady"`
	Ref           string `json:"ref"`
	Saved         bool   `json:"saved"`
	Stale         bool   `json:"stale"`
	Locked        bool   `json:"locked"`
	UnlockUntil   int64  `json:"unlockUntil,omitempty"`
	LiveExecution bool   `json:"liveExecution"`
}
type venueEnvelope struct {
	Version    int    `json:"version"`
	Purpose    string `json:"purpose"`
	Ref        string `json:"ref"`
	Binding    string `json:"binding"`
	Salt       []byte `json:"salt"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}
type venueLease struct {
	id, account, binding, rpcRef, venueRef, rpcHash, venueHash string
	rpc, jupiter                                               []byte
	until                                                      time.Time
}
type venueCredentials struct {
	generation uint64
	closed     bool
	mu         sync.Mutex
	lease      *venueLease
	timer      *time.Timer
}

func validJupiterKey(key []byte) bool {
	if len(key) < 1 || len(key) > 512 {
		return false
	}
	for _, c := range key {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}
func validVenueEnvelope(v venueEnvelope) bool {
	return v.Version == 1 && v.Purpose == "jupiter" && validConnectionRef(v.Ref) && hexValue(v.Binding, 32) && len(v.Salt) == 16 && len(v.Nonce) == 12 && len(v.Ciphertext) >= 17 && len(v.Ciphertext) <= 528
}
func (s *Store) venuePath(id string) string {
	return filepath.Join(s.dir, "venue-credential-"+id+".json")
}
func venueBinding(a Account, rpc rpcVault) string {
	return onchain.Hash(struct {
		Account string
		RPC     string
	}{rpcBinding(a), rpc.Ref})
}
func (s *Store) venueAAD(id string, v venueEnvelope) []byte {
	return []byte("fased-venue-jupiter-v1:" + s.instanceID + ":" + id + ":" + v.Ref + ":" + v.Binding)
}
func (s *Store) readVenue(id string) (v venueEnvelope, err error) {
	info, err := os.Lstat(s.venuePath(id))
	if err != nil {
		return v, err
	}
	if !info.Mode().IsRegular() || info.Size() > 2048 {
		return v, errors.New("invalid encrypted venue credential")
	}
	b, err := ReadPrivateFile(s.venuePath(id))
	if err != nil {
		return v, err
	}
	if update.StrictJSONBounded(b, &v, 2048) != nil || !validVenueEnvelope(v) {
		return v, errors.New("invalid encrypted venue credential")
	}
	return v, nil
}
func openCredential(ciphertext, nonce, salt, aad []byte, password string) ([]byte, error) {
	k := passwordHash(password, salt)
	defer clear(k)
	b, e := aes.NewCipher(k)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return nil, e
	}
	return g.Open(nil, nonce, ciphertext, aad)
}
func (s *Store) closeVenueCredentials() {
	s.venueCredentials.mu.Lock()
	defer s.venueCredentials.mu.Unlock()
	s.venueCredentials.closed = true
	s.venueCredentials.lock()
}
func (s *Store) lockVenueCredentials() {
	s.venueCredentials.mu.Lock()
	defer s.venueCredentials.mu.Unlock()
	s.venueCredentials.lock()
}
func (v *venueCredentials) lock() {
	v.generation++
	if v.timer != nil {
		v.timer.Stop()
		v.timer = nil
	}
	if v.lease != nil {
		clear(v.lease.rpc)
		clear(v.lease.jupiter)
		v.lease = nil
	}
}
func (s *Store) venueLeaseID(a Account) string {
	s.venueCredentials.mu.Lock()
	defer s.venueCredentials.mu.Unlock()
	l := s.venueCredentials.lease
	if s.venueCredentials.closed || l == nil || l.account != a.ID || l.binding != rpcBinding(a) || !s.now().Before(l.until) {
		return ""
	}
	return l.id
}
func (s *Store) venueAccess(a Account) (onchain.CredentialAccess, error) {
	fail := errors.New("unlock connection keys for this wallet")
	id := s.venueLeaseID(a)
	if id == "" || a.RPC == nil {
		return onchain.CredentialAccess{}, fail
	}
	// Never hold the secret mutex while waiting for SQLite or the signer. Vault
	// operations take the SQL fence first; this avoids reversing their lock order.
	valid := func() error {
		if s.venueLeaseID(a) != id {
			return fail
		}
		r, e := s.accountsRecord(context.Background())
		if e != nil {
			return fail
		}
		current, e := swapAccount(r, a.ID)
		if e != nil || rpcBinding(current) != rpcBinding(a) {
			return fail
		}
		var phase string
		if s.db.QueryRowContext(context.Background(), "SELECT phase FROM update_pause WHERE id=1").Scan(&phase) != nil || phase == "paused" {
			return fail
		}
		if a.Signer != nil {
			status := s.fixtureView(context.Background())
			if status.Status == nil || status.Status.Account == nil || *status.Status.Account != *a.Signer || status.Status.UpdateHeld {
				return fail
			}
		}
		rpc, e := s.readRPCVault(a.ID)
		if e != nil {
			return fail
		}
		venue, e := s.readVenue(a.ID)
		if e != nil {
			return fail
		}
		s.venueCredentials.mu.Lock()
		defer s.venueCredentials.mu.Unlock()
		l := s.venueCredentials.lease
		if l == nil || l.id != id || l.rpcRef != rpc.Ref || l.venueRef != venue.Ref || l.rpcHash != onchain.Hash(rpc) || l.venueHash != onchain.Hash(venue) || rpc.Binding != rpcBinding(a) || venue.Binding != venueBinding(a, rpc) || !s.now().Before(l.until) {
			return fail
		}
		return nil
	}
	borrow := func(ctx context.Context, purpose string) ([]byte, error) {
		if ctx.Err() != nil || valid() != nil {
			return nil, fail
		}
		s.venueCredentials.mu.Lock()
		defer s.venueCredentials.mu.Unlock()
		l := s.venueCredentials.lease
		if l == nil || l.id != id || !s.now().Before(l.until) {
			return nil, fail
		}
		switch purpose {
		case "rpc":
			return append([]byte(nil), l.rpc...), nil
		case "jupiter":
			return append([]byte(nil), l.jupiter...), nil
		}
		return nil, fail
	}
	return onchain.CredentialAccess{RPCOrigin: a.RPC.Endpoint, Network: a.Network, Borrow: borrow, Valid: valid}, nil
}
func (s *Store) VenueCredential(ctx context.Context, c VenueCredentialCommand) (VenueCredentialView, error) {
	return s.venueCredential(ctx, c, nil)
}
func (s *Store) venueCredential(ctx context.Context, c VenueCredentialCommand, admittedGeneration *uint64) (VenueCredentialView, error) {
	fail := errors.New("connection key operation refused")
	view := VenueCredentialView{Locked: true}
	if !hexValue(c.AccountID, 16) || (c.Action != "status" && c.Action != "save" && c.Action != "unlock" && c.Action != "lock" && c.Action != "remove") {
		return view, fail
	}
	if c.Action == "status" || c.Action == "lock" {
		if c.Key != "" || c.Passphrase != "" || c.Seconds != 0 {
			return view, fail
		}
	} else if !validPassword(c.Passphrase) || (c.Action == "save") != (c.Key != "") || c.Action != "unlock" && c.Seconds != 0 {
		return view, fail
	}
	if c.Action == "unlock" && (c.Seconds < 1 || c.Seconds > 300) {
		return view, fail
	}
	s.venueCredentials.mu.Lock()
	initialGeneration := s.venueCredentials.generation
	closed := s.venueCredentials.closed
	s.venueCredentials.mu.Unlock()
	if closed || admittedGeneration != nil && *admittedGeneration != initialGeneration {
		return view, ErrUnauthorized
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return view, fail
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "UPDATE instance SET revision=revision WHERE id=1"); e != nil {
		return view, metadataWriteError(e)
	}
	var raw string
	var revision int64
	if tx.QueryRowContext(ctx, "SELECT document,revision FROM accounts WHERE id=1").Scan(&raw, &revision) != nil {
		return view, fail
	}
	if revision != c.Revision {
		return view, ErrConflict
	}
	var records AccountsRecord
	if json.Unmarshal([]byte(raw), &records) != nil {
		return view, fail
	}
	var a Account
	for _, candidate := range records.Accounts {
		if candidate.ID == c.AccountID {
			a = candidate
			break
		}
	}
	if a.ID == "" || !managedNetworkAccount(a) {
		return view, fail
	}
	rpc, rpcErr := s.readRPCVault(a.ID)
	rpcReady := rpcErr == nil && rpc.Version == 2 && a.RPC != nil && validProviderRPC(a)
	v, e := s.readVenue(a.ID)
	saved := e == nil
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return view, fail
	}
	view.RPCReady = rpcReady
	view.Ref = v.Ref
	view.Saved = saved
	view.Stale = !rpcReady || rpc.Binding != rpcBinding(a) || saved && v.Binding != venueBinding(a, rpc)
	s.venueCredentials.mu.Lock()
	if l := s.venueCredentials.lease; saved && !view.Stale && l != nil && l.account == a.ID && l.binding == rpcBinding(a) && l.rpcHash == onchain.Hash(rpc) && l.venueHash == onchain.Hash(v) && s.now().Before(l.until) {
		view.Locked = false
		view.UnlockUntil = l.until.Unix()
	}
	s.venueCredentials.mu.Unlock()
	if c.Action == "status" {
		return view, nil
	}
	if c.Ref != view.Ref {
		return view, ErrConflict
	}
	// A failed unlock also removes the previous lease. Ciphertext is retained.
	s.venueCredentials.mu.Lock()
	if s.venueCredentials.closed || s.venueCredentials.generation != initialGeneration {
		s.venueCredentials.mu.Unlock()
		return view, fail
	}
	s.venueCredentials.lock()
	generation := s.venueCredentials.generation
	s.venueCredentials.mu.Unlock()
	view.Locked = true
	view.UnlockUntil = 0
	if c.Action == "lock" {
		return view, nil
	}
	// Retiring a retained Jupiter ciphertext authenticates that ciphertext alone.
	// RPC removal/rotation/password changes must never strand this operation.
	var key []byte
	if saved {
		key, e = openCredential(v.Ciphertext, v.Nonce, v.Salt, s.venueAAD(a.ID, v), c.Passphrase)
		defer clear(key)
		if e != nil || !validJupiterKey(key) {
			return view, fail
		}
	}
	if c.Action == "remove" {
		if !saved || os.Remove(s.venuePath(a.ID)) != nil {
			return view, fail
		}
		return VenueCredentialView{Locked: true, RPCReady: rpcReady}, nil
	}
	if !s.rpcAdmission || s.onchain == nil {
		return view, errors.New("live connection keys disabled")
	}
	if !rpcReady || a.Signer == nil || s.fixture == nil {
		return view, fail
	}
	status := s.fixtureView(ctx)
	if status.Status == nil || status.Status.Account == nil || *status.Status.Account != *a.Signer {
		return view, fail
	}
	rpcKey, e := openCredential(rpc.Ciphertext, rpc.Nonce, rpc.Salt, s.rpcAAD(a.ID, rpc), c.Passphrase)
	defer clear(rpcKey)
	if e != nil || !validRPCSecret(2, string(rpcKey)) || rpc.Binding != rpcBinding(a) {
		return view, fail
	}

	if c.Action == "save" {
		if !validJupiterKey([]byte(c.Key)) {
			return view, fail
		}
		v = venueEnvelope{Version: 1, Purpose: "jupiter", Binding: venueBinding(a, rpc), Salt: make([]byte, 16), Nonce: make([]byte, 12)}
		if v.Ref, e = randomToken(); e != nil {
			return view, fail
		}
		if _, e = rand.Read(v.Salt); e != nil {
			return view, fail
		}
		if _, e = rand.Read(v.Nonce); e != nil {
			return view, fail
		}
		k := passwordHash(c.Passphrase, v.Salt)
		block, _ := aes.NewCipher(k)
		clear(k)
		g, _ := cipher.NewGCM(block)
		plain := []byte(c.Key)
		defer clear(plain)
		v.Ciphertext = g.Seal(nil, v.Nonce, plain, s.venueAAD(a.ID, v))
		if WritePrivateJSON(s.venuePath(a.ID), v) != nil {
			return view, fail
		}
		return VenueCredentialView{Ref: v.Ref, Saved: true, Locked: true, RPCReady: true}, nil
	}
	if !saved || view.Stale {
		return view, fail
	}
	id, e := randomToken()
	if e != nil {
		return view, fail
	}
	s.venueCredentials.mu.Lock()
	defer s.venueCredentials.mu.Unlock()
	if s.venueCredentials.closed || s.venueCredentials.generation != generation || ctx.Err() != nil {
		return view, fail
	}
	s.venueCredentials.lease = &venueLease{id: id, account: a.ID, binding: rpcBinding(a), rpcRef: rpc.Ref, venueRef: v.Ref, rpcHash: onchain.Hash(rpc), venueHash: onchain.Hash(v), rpc: append([]byte(nil), rpcKey...), jupiter: append([]byte(nil), key...), until: s.now().Add(time.Duration(c.Seconds) * time.Second)}
	s.venueCredentials.timer = time.AfterFunc(time.Duration(c.Seconds)*time.Second, func() {
		s.venueCredentials.mu.Lock()
		defer s.venueCredentials.mu.Unlock()
		if s.venueCredentials.lease != nil && s.venueCredentials.lease.id == id {
			s.venueCredentials.lock()
		}
	})
	view.Locked = false
	view.UnlockUntil = s.venueCredentials.lease.until.Unix()
	return view, nil
}

// Trusted construction only, after the installed identity has been established.
// No HTTP, CLI, environment or ordinary startup path supplies this factory.
func (s *Store) configureCredentialVenue(factory func(onchain.CredentialAccess) (swapVenue, error)) error {
	if s.onchain == nil || factory == nil || !s.rpcAdmission {
		return errors.New("controlled installed venue required")
	}
	s.onchain.credentialLease = true
	s.onchain.venue = func(a Account) (swapVenue, error) {
		access, e := s.venueAccess(a)
		if e != nil {
			return nil, e
		}
		return factory(access)
	}
	return nil
}
func (s *Store) validTaskCredential(ctx context.Context, p *SwapTaskPolicy) bool {
	if p.CredentialLease == "" {
		return s.onchain != nil && !s.onchain.credentialLease
	}
	records, e := s.accountsRecord(ctx)
	if e != nil {
		return false
	}
	a, e := swapAccount(records, p.Proposal.Quote.AccountID)
	if e != nil || p.CredentialLease != s.venueLeaseID(a) {
		return false
	}
	access, e := s.venueAccess(a)
	return e == nil && access.Valid() == nil
}

// Existing ciphertext and raw documents are preserved. Prior readers cannot
// silently drop the new purpose-bound credential/approval dependency.
func migrateVenueCredentials(ctx context.Context, db *sql.DB) error {
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var phase, raw string
	if tx.QueryRowContext(ctx, "SELECT phase FROM update_pause WHERE id=1").Scan(&phase) != nil || phase == "paused" {
		return ErrCorePaused
	}
	if e = tx.QueryRowContext(ctx, "SELECT document FROM agent_workspace WHERE id=1").Scan(&raw); e != nil {
		return e
	}
	if e = onchainFields([]byte(raw), false); e != nil {
		return e
	}
	var d agentDocument
	if json.Unmarshal([]byte(raw), &d) != nil {
		return errors.New("invalid workspace")
	}
	for _, t := range d.Tasks {
		if t.Swap != nil && (t.Swap.CredentialLease != "" || t.Swap.Proposal.ReceiptRPC != nil) {
			return errors.New("unprotected credential-bound swap")
		}
	}
	if e = validateOnchainStorage(d); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "PRAGMA user_version=33"); e != nil {
		return e
	}
	return tx.Commit()
}
