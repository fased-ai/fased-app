// Package signer is an engineering custody fixture. It has no transaction or
// arbitrary-message signing API, chain adapter, real-key admission, or automatic unlock.
package signer

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/fased-ai/fased-app/internal/update"

	"golang.org/x/crypto/argon2"
)

const FixtureNetwork = "disposable-fixture:v1"
const MaxUnlock = time.Hour
const maxDocument = 16 * 1024

var (
	ErrLocked      = errors.New("fixture vault is locked")
	ErrCredential  = errors.New("invalid credential or damaged encrypted document")
	ErrUnavailable = errors.New("real wallets and financial execution are unavailable")
)

// AccountRef is public, versioned, and changes authority epoch on restore.
// No persona, app password or app database entry confers signer authority.
type AccountRef struct {
	Version   int    `json:"version"`
	SignerID  string `json:"signerId"`
	WalletID  string `json:"walletId"`
	Epoch     string `json:"epoch"`
	Network   string `json:"network"`
	PublicKey string `json:"publicKey"`
}

type envelope struct {
	FreshManaged     bool       `json:"freshManaged,omitempty"`
	Version          int        `json:"version"`
	Purpose          string     `json:"purpose"`
	KDF              string     `json:"kdf"`
	Ref              AccountRef `json:"account"`
	RecoveryRequired bool       `json:"recoveryRequired"`
	Salt             []byte     `json:"salt"`
	Nonce            []byte     `json:"nonce"`
	Ciphertext       []byte     `json:"ciphertext"`
}

type Status struct {
	SwapReady          bool        `json:"swapReady,omitempty"`
	UpdateHeld         bool        `json:"updateHeld"`
	FixtureOnly        bool        `json:"fixtureOnly"`
	WalletSigner       bool        `json:"walletSigner"`
	FinancialExecution bool        `json:"financialExecution"`
	Account            *AccountRef `json:"account,omitempty"`
	Locked             bool        `json:"locked"`
	UnlockUntil        time.Time   `json:"unlockUntil,omitempty"`
	RecoveryRequired   bool        `json:"recoveryRequired"`
	UnattendedUnlock   bool        `json:"unattendedUnlock"`
}

type Vault struct {
	swapObserver SwapObserver
	mu           sync.Mutex
	store        *storage
	signerID     string
	network      string
	managedRef   *AccountRef
	record       *envelope
	ledger       *ledger
	seed         []byte
	until        time.Time
	timer        *time.Timer
	now          func() time.Time
	closed       bool
	bootID       string
}

func validPassword(p []byte) bool { return utf8.Valid(p) && utf8.RuneCount(p) >= 12 && len(p) <= 1024 }
func randomID() string            { return hex.EncodeToString(randomBytes(16)) }
func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}
func wipe(b []byte) { clear(b) } // Best effort; Go does not guarantee removal of every runtime copy.

func decode(data []byte, out any) error {
	if len(data) == 0 || len(data) > maxDocument {
		return errors.New("invalid document size")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return errors.New("invalid document")
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing document data")
	}
	return nil
}

func (e envelope) aad() []byte {
	e.Ciphertext = nil
	b, _ := json.Marshal(e)
	return b
}

func (e envelope) validate(purpose string) error {
	if !validEnvelopeIdentity(e) || e.Purpose != purpose || e.KDF != "argon2id-m65536-t3-p2-aes256gcm-v1" ||
		len(e.Salt) != 16 || len(e.Nonce) != 12 || len(e.Ciphertext) != ed25519.SeedSize+16 {
		return errors.New("unsupported or damaged fixture document")
	}
	b, err := hex.DecodeString(e.Ref.PublicKey)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return errors.New("invalid public identity")
	}
	return nil
}
func validID(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 16 && s == hex.EncodeToString(b)
}

func seal(seed, password []byte, ref AccountRef, purpose string, recovery bool) (envelope, error) {
	return sealOrigin(seed, password, ref, purpose, recovery, false)
}
func sealOrigin(seed, password []byte, ref AccountRef, purpose string, recovery, fresh bool) (envelope, error) {
	if !validPassword(password) {
		return envelope{}, errors.New("use 12 or more characters, at most 1024 bytes")
	}
	e := envelope{Version: VaultFormat, Purpose: purpose, KDF: "argon2id-m65536-t3-p2-aes256gcm-v1", Ref: ref, RecoveryRequired: recovery, Salt: randomBytes(16), Nonce: randomBytes(12)}
	if ref.Version == 2 {
		e.Version = 2
		if fresh && purpose == "vault" {
			e.Version = 3
			e.FreshManaged = true
		}
	}
	key := argon2.IDKey(password, e.Salt, 3, 64*1024, 2, 32)
	defer wipe(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return envelope{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return envelope{}, err
	}
	e.Ciphertext = gcm.Seal(nil, e.Nonce, seed, e.aad())
	return e, nil
}

func unseal(e envelope, password []byte, purpose string) ([]byte, error) {
	if err := e.validate(purpose); err != nil {
		return nil, err
	}
	if !validPassword(password) {
		return nil, ErrCredential
	}
	key := argon2.IDKey(password, e.Salt, 3, 64*1024, 2, 32)
	defer wipe(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	seed, err := gcm.Open(nil, e.Nonce, e.Ciphertext, e.aad())
	if err != nil {
		return nil, ErrCredential
	}
	private := ed25519.NewKeyFromSeed(seed)
	defer wipe(private)
	if hex.EncodeToString(private[ed25519.SeedSize:]) != e.Ref.PublicKey {
		wipe(seed)
		return nil, ErrCredential
	}
	return seed, nil
}

// Open requires an existing private directory. Installed role validation belongs
// to the Linux transport; same-UID unit fixtures do not establish OS isolation.
func Open(dir, signerID string) (*Vault, error) { return openVault(dir, signerID, false) }
func openVault(dir, signerID string, readOnly bool) (*Vault, error) {
	return openVaultNetwork(dir, signerID, readOnly, "")
}
func openVaultNetwork(dir, signerID string, readOnly bool, network string) (*Vault, error) {
	if network != "" && !managedNetwork(network) {
		return nil, ErrUnavailable
	}
	if !validID(signerID) {
		return nil, errors.New("invalid signer identity")
	}
	var s *storage
	var err error
	if network != "" {
		s, err = openNetworkStorage(dir, readOnly, network)
	} else if readOnly {
		s, err = openStorageReadonly(dir)
	} else {
		s, err = openStorage(dir)
	}
	if err != nil {
		return nil, err
	}
	v := &Vault{store: s, signerID: signerID, network: network, now: time.Now, bootID: update.NewProbe()}
	b, err := s.read()
	if err == nil {
		var e envelope
		if err = decode(b, &e); err == nil {
			err = e.validate("vault")
			if err == nil && !envelopeForNetwork(e, network) {
				err = ErrUnavailable
			}
		}
		if err == nil && e.Ref.SignerID != signerID {
			err = errors.New("vault belongs to another signer identity")
		}
		if err == nil {
			v.record = &e
		}
	}
	if err != nil && !errors.Is(err, errAbsent) {
		s.close()
		return nil, err
	}
	if err = v.openLedger(); err != nil {
		s.close()
		return nil, err
	}
	return v, nil
}

func (v *Vault) Close() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return nil
	}
	v.closed = true
	v.lock()
	return v.store.close()
}
func (v *Vault) lock() {
	if v.timer != nil {
		v.timer.Stop()
		v.timer = nil
	}
	wipe(v.seed)
	v.seed = nil
	v.until = time.Time{}
}
func (v *Vault) Lock() { v.mu.Lock(); defer v.mu.Unlock(); v.lock() }
func (v *Vault) unlocked() bool {
	if len(v.seed) != 0 && !v.now().Before(v.until) {
		v.lock()
	}
	return len(v.seed) == ed25519.SeedSize
}

func (v *Vault) Status() Status {
	v.mu.Lock()
	defer v.mu.Unlock()
	s := Status{SwapReady: v.managedSwapEligible(), FixtureOnly: true, Locked: !v.unlocked(), UnlockUntil: v.until, UpdateHeld: v.store.failedState() || v.ledger != nil && v.ledger.activeUpdate != ""}
	if v.record != nil {
		ref := v.record.Ref
		s.Account = &ref
		s.RecoveryRequired = v.recoveryRequired()
	}
	return s
}

func (v *Vault) save(e envelope) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err = v.store.create(b); err != nil {
		return err
	}
	v.record = &e
	if err = v.openLedger(); err != nil {
		v.store.poison()
		return err
	}
	return nil
}

func (v *Vault) Create(password []byte) error {
	seed := randomBytes(ed25519.SeedSize)
	defer wipe(seed)
	return v.createSeed(seed, password, false)
}

// ImportFixtureSeed accepts only a raw disposable Ed25519 seed. Import never
// inherits permissions or unlocks; fresh identity and a recovery hold are durable.
func (v *Vault) ImportFixtureSeed(seed, password []byte) error {
	if v.network != "" {
		return ErrUnavailable
	}
	if len(seed) != ed25519.SeedSize {
		return errors.New("expected a 32-byte disposable seed")
	}
	return v.createSeed(seed, password, true)
}

func (v *Vault) createSeed(seed, password []byte, recovery bool) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return ErrUnavailable
	}
	if v.record != nil || v.ledger != nil || v.managedRef != nil {
		return errors.New("fixture vault already exists; never overwrite wallet identity")
	}
	private := ed25519.NewKeyFromSeed(seed)
	defer wipe(private)
	ref := AccountRef{Version: 1, SignerID: v.signerID, WalletID: randomID(), Epoch: randomID(), Network: FixtureNetwork, PublicKey: hex.EncodeToString(private[ed25519.SeedSize:])}
	fresh := v.network != "" && !recovery
	if v.network != "" {
		ref.Version = 2
		ref.Network = v.network
		recovery = true
	}
	e, err := sealOrigin(seed, password, ref, "vault", recovery, fresh)
	if err != nil {
		return err
	}
	return v.save(e)
}

func (v *Vault) Unlock(password []byte, lifetime time.Duration) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.ledger != nil && v.ledger.activeUpdate != "" {
		v.lock()
		return errors.New("exclusive update intent blocks unlock")
	}
	if v.closed {
		return ErrUnavailable
	}
	// A failed unlock attempt also discards any previous in-memory credential.
	v.lock()
	if v.store.failedState() {
		return ErrUnavailable
	}
	if lifetime <= 0 || lifetime > MaxUnlock {
		return errors.New("manual unlock must be positive and at most one hour; unattended unlock is unavailable")
	}
	if v.record == nil {
		return errors.New("no fixture vault")
	}
	seed, err := unseal(*v.record, password, "vault")
	if err != nil {
		return err
	}
	v.seed = seed
	v.until = v.now().Add(lifetime)
	until := v.until
	v.timer = time.AfterFunc(lifetime, func() {
		v.mu.Lock()
		defer v.mu.Unlock()
		if v.until == until {
			v.lock()
		}
	})
	return nil
}

func (v *Vault) Backup(password []byte) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return nil, ErrUnavailable
	}
	if !v.unlocked() {
		return nil, ErrLocked
	}
	e, err := seal(v.seed, password, v.record.Ref, "backup", true)
	if err != nil {
		return nil, err
	}
	return json.Marshal(e)
}

// VerifyBackup compares an authenticated backup to the currently unlocked key.
// It never returns the seed or alters grants, epochs or recovery holds.
func (v *Vault) VerifyBackup(document, password []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return ErrUnavailable
	}
	if !v.unlocked() {
		return ErrLocked
	}
	var e envelope
	if err := decode(document, &e); err != nil {
		return err
	}
	seed, err := unseal(e, password, "backup")
	if err != nil {
		return err
	}
	defer wipe(seed)
	if !sameWallet(e.Ref, v.record.Ref) || !bytes.Equal(seed, v.seed) {
		return errors.New("backup belongs to another fixture wallet")
	}
	return nil
}

// Restore imports only a key backup into an empty signer. It never restores
// policies, journals or spending counters. Its fresh epoch and recovery
// hold requires retained history and mock reconciliation; key-only recovery stays held.
func (v *Vault) Restore(backup, backupPassword, newPassword []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.ledger != nil && v.ledger.activeUpdate != "" {
		return errors.New("exclusive update intent blocks key recovery")
	}
	if v.closed {
		return ErrUnavailable
	}
	if v.record != nil {
		return errors.New("restore requires an empty signer; overwriting state can roll back financial history")
	}
	var e envelope
	if err := decode(backup, &e); err != nil {
		return err
	}
	if !envelopeForNetwork(e, v.network) {
		return ErrUnavailable
	}
	if v.managedRef != nil && !sameWallet(*v.managedRef, e.Ref) {
		return errors.New("backup conflicts with retained key history")
	}
	seed, err := unseal(e, backupPassword, "backup")
	if err != nil {
		return err
	}
	defer wipe(seed)
	if v.ledger != nil && !sameWallet(v.ledger.ref, e.Ref) {
		return errors.New("backup conflicts with retained wallet history")
	}
	e.Ref.SignerID = v.signerID
	e.Ref.Epoch = randomID()
	restored, err := seal(seed, newPassword, e.Ref, "vault", true)
	if err != nil {
		return err
	}
	return v.save(restored)
}

// ExportFixtureSeed is owner-only through the transport. It never reaches the
// general application, UI, logs or model. These keys must never receive funds.
func (v *Vault) ExportFixtureSeed() ([]byte, error) {
	if v.network != "" {
		return nil, ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return nil, ErrUnavailable
	}
	if !v.unlocked() {
		return nil, ErrLocked
	}
	return bytes.Clone(v.seed), nil
}
