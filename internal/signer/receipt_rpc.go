package signer

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"sync"
	"time"

	"github.com/fased-ai/fased-app/internal/onchain"
	"golang.org/x/crypto/argon2"
)

const MaxReceiptRPCLease = 15 * time.Minute

type receiptRPCLease struct {
	mu     sync.Mutex
	ref    string
	key    []byte
	issued time.Time
	until  time.Time
	timer  *time.Timer
	now    func() time.Time
}

func (l *receiptRPCLease) close() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.clear()
}
func (l *receiptRPCLease) cap(deadline time.Time) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.check() != nil {
		return
	}
	if deadline.Before(l.until) {
		l.until = deadline
		if l.timer != nil {
			l.timer.Stop()
		}
		delay := deadline.Sub(l.now())
		if delay <= 0 {
			l.clear()
			return
		}
		l.timer = time.AfterFunc(delay, l.close)
	}
}
func (l *receiptRPCLease) clear() {
	clear(l.key)
	l.key = nil
	if l.timer != nil {
		l.timer.Stop()
		l.timer = nil
	}
	l.until = time.Time{}
}
func (l *receiptRPCLease) valid() error {
	if l == nil {
		return ErrLocked
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.check()
}
func (l *receiptRPCLease) check() error {
	now := l.now()
	if len(l.key) == 0 || now.Before(l.issued) || !now.Before(l.until) {
		l.clear()
		return ErrLocked
	}
	return nil
}
func (l *receiptRPCLease) borrow(ctx context.Context, purpose string) ([]byte, error) {
	if ctx.Err() != nil || purpose != "rpc" {
		return nil, ErrUnavailable
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if e := l.check(); e != nil {
		return nil, e
	}
	return append([]byte(nil), l.key...), nil
}
func (v *Vault) lockReceiptRPC() { v.receiptRPCLease.close() }
func (v *Vault) receiptRPCStatus() *ReceiptRPCStatus {
	if v.ledger == nil || v.ledger.receiptRPC == nil || v.record == nil || v.ledger.receiptRPC.Account != v.record.Ref {
		return nil
	}
	e := v.ledger.receiptRPC
	s := &ReceiptRPCStatus{Ref: e.Ref, Origin: e.Origin, Network: e.Account.Network, Saved: true, Locked: true}
	if l := v.receiptRPCLease; l != nil {
		l.mu.Lock()
		if l.ref == e.Ref && l.check() == nil {
			s.Locked = false
			s.UnlockUntil = l.until
		}
		l.mu.Unlock()
	}
	return s
}
func validReceiptRPCSecret(key []byte) bool {
	if len(key) < 24 || len(key) > 256 {
		return false
	}
	for _, b := range key {
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '-') {
			return false
		}
	}
	return true
}
func receiptRPCGCM(password, salt []byte) (cipher.AEAD, error) {
	key := argon2.IDKey(password, salt, 3, 64*1024, 2, 32)
	defer clear(key)
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(block)
}

// ProvisionReceiptRPC is protected owner administration. It authenticates the
// current vault without unlocking its seed and rotates only read credentials.
func (v *Vault) ProvisionReceiptRPC(origin string, key, password []byte) (ReceiptRPCStatus, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed || v.receiptRPCFactory == nil || v.record == nil || v.ledger == nil || v.store.failedState() || v.ledger.activeUpdate != "" || !managedRefValid(v.record.Ref) || !receiptRPCOrigin(origin, v.record.Ref.Network) || !validReceiptRPCSecret(key) {
		return ReceiptRPCStatus{}, ErrUnavailable
	}
	v.lock()
	seed, e := unseal(*v.record, password, "vault")
	defer clear(seed)
	if e != nil {
		return ReceiptRPCStatus{}, e
	}
	env := receiptRPCEnvelope{Version: 1, Purpose: "receipt-rpc", Ref: randomID(), Account: v.record.Ref, Origin: origin, Salt: randomBytes(16), Nonce: randomBytes(12)}
	gcm, e := receiptRPCGCM(password, env.Salt)
	if e != nil {
		return ReceiptRPCStatus{}, ErrCredential
	}
	env.Ciphertext = gcm.Seal(nil, env.Nonce, key, env.aad())
	if e = v.ledger.append(v, journalRecord{Kind: "onchain-rpc-credential", ReceiptRPC: &env}); e != nil {
		return ReceiptRPCStatus{}, e
	}
	return *v.receiptRPCStatus(), nil
}

// UnlockReceiptRPC decrypts only a read-only RPC credential. It never unlocks
// the wallet, clears recovery holds, grants approval, or signs a transaction.
func (v *Vault) UnlockReceiptRPC(password []byte, lifetime time.Duration) (ReceiptRPCStatus, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.lockReceiptRPC()
	if v.closed || v.receiptRPCFactory == nil || v.record == nil || v.ledger == nil || v.ledger.receiptRPC == nil || v.store.failedState() || v.ledger.activeUpdate != "" || lifetime < time.Second || lifetime > MaxReceiptRPCLease {
		return ReceiptRPCStatus{}, ErrUnavailable
	}
	env := v.ledger.receiptRPC
	if env.Account != v.record.Ref || !validPassword(password) {
		return ReceiptRPCStatus{}, ErrCredential
	}
	gcm, e := receiptRPCGCM(password, env.Salt)
	if e != nil {
		return ReceiptRPCStatus{}, ErrCredential
	}
	plain, e := gcm.Open(nil, env.Nonce, env.Ciphertext, env.aad())
	if e != nil || !validReceiptRPCSecret(plain) {
		clear(plain)
		return ReceiptRPCStatus{}, ErrCredential
	}
	lease := &receiptRPCLease{ref: env.Ref, key: plain, issued: v.now(), until: v.now().Add(lifetime), now: v.now}
	v.receiptRPCLease = lease
	observer, e := v.receiptRPCFactory(onchain.CredentialAccess{RPCOrigin: env.Origin, Network: env.Account.Network, Borrow: lease.borrow, Valid: lease.valid})
	if e != nil || observer == nil {
		lease.close()
		return ReceiptRPCStatus{}, errors.New("receipt RPC reader unavailable")
	}
	v.swapObserver = observer
	lease.mu.Lock()
	lease.timer = time.AfterFunc(lifetime, lease.close)
	lease.mu.Unlock()
	return *v.receiptRPCStatus(), nil
}
func (v *Vault) validReceiptRPCBinding(p SwapProposal) error {
	if v.receiptRPCFactory == nil {
		if p.ReceiptRPC != nil || v.ledger != nil && v.ledger.receiptRPC != nil {
			return ErrUnavailable
		}
		return nil
	}
	s := v.receiptRPCStatus()
	if p.ReceiptRPC == nil || s == nil || s.Locked || p.ReceiptRPC.Ref != s.Ref || p.ReceiptRPC.Origin != s.Origin {
		return errors.New("receipt RPC credential changed or locked")
	}
	return nil
}

type closedReceiptObserver struct{}

func (closedReceiptObserver) Reconcile(context.Context, onchain.Quote, []byte) (onchain.Observation, error) {
	return onchain.Observation{}, ErrLocked
}

// OpenManagedSwapCredentialsFixture is a trusted in-process disposable fixture
// constructor. Ordinary Serve/Open never configure credentials or a transport.
func OpenManagedSwapCredentialsFixture(dir, id, network string, factory func(onchain.CredentialAccess) (SwapObserver, error)) (*Vault, error) {
	if !managedNetwork(network) || factory == nil {
		return nil, ErrUnavailable
	}
	v, e := openVaultNetwork(dir, id, false, network)
	if e != nil {
		return nil, e
	}
	v.receiptRPCFactory = factory
	v.swapObserver = closedReceiptObserver{}
	return v, nil
}
