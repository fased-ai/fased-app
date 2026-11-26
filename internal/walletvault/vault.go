package walletvault

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fased-ai/fased-app/internal/vault"
)

const (
	KindSolana = "solana"
	KindWatch  = "watch"

	RolePersonal   = "personal"
	RoleAgentVault = "agent_vault"
	RoleWatch      = "watch"
)

var ErrWrongPassphrase = errors.New("incorrect vault passphrase")

type PublicWallet struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Role        string `json:"role"`
	PublicKey   string `json:"publicKey"` // Network-agnostic Solana Base58 address
	HasMnemonic bool   `json:"hasMnemonic"`
	AccountIdx  uint32 `json:"accountIndex,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
}

type sealedWalletSecret struct {
	Salt       []byte `json:"salt"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
	KDFTime    uint32 `json:"kdfTime"`
	KDFMemory  uint32 `json:"kdfMemory"`
	KDFThreads uint8  `json:"kdfThreads"`
}

type walletEntry struct {
	PublicWallet
	Sealed *sealedWalletSecret `json:"sealed,omitempty"`
}

type secretPayload struct {
	Mnemonic      string `json:"mnemonic,omitempty"`
	PrivateKeyHex string `json:"privateKeyHex"`
}

type ExportedSecret struct {
	Mnemonic         string `json:"mnemonic,omitempty"`
	PrivateKeyBase58 string `json:"privateKeyBase58"`
	KeypairJSON      string `json:"keypairJson"`
	PublicKey        string `json:"publicKey"`
}

type vaultDoc struct {
	Version int           `json:"version"`
	Wallets []walletEntry `json:"wallets"`
}

// Service manages user-created/imported encrypted wallets portably on both Local and Remote hosts.
type Service struct {
	dir        string
	now        func() time.Time
	mu         sync.Mutex
	kdfTime    uint32
	kdfMemory  uint32
	kdfThreads uint8
}

// NewService opens <dataDir>/wallets/vault.json (0o600) cleanly without modifying legacy custody.
func NewService(dataDir string, nowFn func() time.Time) (*Service, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("wallet vault requires dataDir")
	}
	if nowFn == nil {
		nowFn = time.Now
	}
	wDir := filepath.Join(dataDir, "wallets")
	if err := os.MkdirAll(wDir, 0o700); err != nil {
		return nil, err
	}
	return &Service{
		dir:        wDir,
		now:        nowFn,
		kdfTime:    vault.ProdKDFTime,
		kdfMemory:  vault.ProdKDFMemory,
		kdfThreads: vault.ProdKDFThreads,
	}, nil
}

// SetFastKDFForTests weakens KDF only inside unit tests to keep suites fast.
func (s *Service) SetFastKDFForTests() {
	s.kdfTime = 1
	s.kdfMemory = 64
	s.kdfThreads = 1
}

func (s *Service) vaultPath() string {
	return filepath.Join(s.dir, "vault.json")
}

func (s *Service) loadLocked() (*vaultDoc, error) {
	doc := &vaultDoc{Version: 1, Wallets: []walletEntry{}}
	raw, err := os.ReadFile(s.vaultPath())
	if errors.Is(err, os.ErrNotExist) {
		return doc, nil
	}
	if err != nil {
		return nil, err
	}
	if len(raw) > 0 {
		if jErr := json.Unmarshal(raw, doc); jErr != nil {
			return nil, errors.New("corrupt wallet vault")
		}
	}
	return doc, nil
}

func (s *Service) saveLocked(doc *vaultDoc) error {
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dir, ".vault-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), s.vaultPath())
}

func walletAAD(id, pub string) []byte {
	return []byte("fased-walletvault-v1:" + id + ":" + pub)
}

func newWalletID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "w_" + hex.EncodeToString(b), nil
}

func normalizeRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case RoleAgentVault:
		return RoleAgentVault
	case RoleWatch:
		return RoleWatch
	default:
		return RolePersonal
	}
}

func (s *Service) sealSecret(id, pub, passphrase string, payload secretPayload) (*sealedWalletSecret, error) {
	if len(passphrase) < 8 {
		return nil, errors.New("passphrase must be at least 8 characters")
	}
	salt, err := vault.RandomBytes(16)
	if err != nil {
		return nil, err
	}
	nonce, err := vault.RandomBytes(12)
	if err != nil {
		return nil, err
	}
	plain, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	defer clear(plain)

	key := vault.DeriveKey(passphrase, salt, s.kdfTime, s.kdfMemory, s.kdfThreads)
	defer clear(key)
	ct, err := vault.SealAESGCM(key, nonce, plain, walletAAD(id, pub))
	if err != nil {
		return nil, err
	}
	return &sealedWalletSecret{
		Salt:       salt,
		Nonce:      nonce,
		Ciphertext: ct,
		KDFTime:    s.kdfTime,
		KDFMemory:  s.kdfMemory,
		KDFThreads: s.kdfThreads,
	}, nil
}

func (s *Service) unsealSecret(entry walletEntry, passphrase string) (secretPayload, error) {
	if entry.Sealed == nil || len(entry.Sealed.Ciphertext) == 0 {
		return secretPayload{}, errors.New("wallet does not hold a signing key")
	}
	if len(passphrase) == 0 {
		return secretPayload{}, ErrWrongPassphrase
	}
	t := entry.Sealed.KDFTime
	m := entry.Sealed.KDFMemory
	th := entry.Sealed.KDFThreads
	if t == 0 || m == 0 || th == 0 {
		t, m, th = vault.ProdKDFTime, vault.ProdKDFMemory, vault.ProdKDFThreads
	}
	key := vault.DeriveKey(passphrase, entry.Sealed.Salt, t, m, th)
	defer clear(key)
	plain, err := vault.OpenAESGCM(key, entry.Sealed.Nonce, entry.Sealed.Ciphertext, walletAAD(entry.ID, entry.PublicKey))
	if err != nil {
		return secretPayload{}, ErrWrongPassphrase
	}
	defer clear(plain)
	var out secretPayload
	if err := json.Unmarshal(plain, &out); err != nil {
		return secretPayload{}, errors.New("corrupt wallet secret payload")
	}
	return out, nil
}

// ListWallets returns public wallet records only (never secrets).
func (s *Service) ListWallets() ([]PublicWallet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	out := make([]PublicWallet, 0, len(doc.Wallets))
	for _, w := range doc.Wallets {
		out = append(out, w.PublicWallet)
	}
	return out, nil
}

// CreateSolanaWallet generates a BIP-39 mnemonic + m/44'/501'/0'/0' Ed25519 keypair,
// seals it with passphrase, and returns the mnemonic ONCE for immediate backup display.
func (s *Service) CreateSolanaWallet(name, role string, words int, passphrase string) (PublicWallet, string, error) {
	cleanName := strings.TrimSpace(name)
	if cleanName == "" || len(cleanName) > 80 {
		return PublicWallet{}, "", errors.New("valid wallet name required")
	}
	if words == 0 {
		words = 12
	}
	mnemonic, err := GenerateMnemonic(words)
	if err != nil {
		return PublicWallet{}, "", err
	}
	pk, err := KeypairFromMnemonic(mnemonic, 0)
	if err != nil {
		return PublicWallet{}, "", err
	}
	defer clear(pk)

	pubAddr := Base58Encode(pk.Public().(ed25519.PublicKey))
	id, err := newWalletID()
	if err != nil {
		return PublicWallet{}, "", err
	}
	sealed, err := s.sealSecret(id, pubAddr, passphrase, secretPayload{
		Mnemonic:      mnemonic,
		PrivateKeyHex: hex.EncodeToString(pk),
	})
	if err != nil {
		return PublicWallet{}, "", err
	}
	r := normalizeRole(role)
	if r == RoleWatch {
		r = RolePersonal
	}
	rec := PublicWallet{
		ID:          id,
		Name:        cleanName,
		Kind:        KindSolana,
		Role:        r,
		PublicKey:   pubAddr,
		HasMnemonic: true,
		CreatedAt:   s.now().UTC().Unix(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return PublicWallet{}, "", err
	}
	doc.Wallets = append(doc.Wallets, walletEntry{PublicWallet: rec, Sealed: sealed})
	if err := s.saveLocked(doc); err != nil {
		return PublicWallet{}, "", err
	}
	return rec, mnemonic, nil
}

type ImportInput struct {
	Name         string `json:"name"`
	Role         string `json:"role"`
	Mnemonic     string `json:"mnemonic,omitempty"`
	PrivateKey   string `json:"privateKey,omitempty"`
	WatchAddress string `json:"watchAddress,omitempty"`
	AccountIndex uint32 `json:"accountIndex,omitempty"`
	Passphrase   string `json:"passphrase,omitempty"`
}

// ImportWallet imports a mnemonic, Base58/JSON private key, or watch-only Solana public address.
func (s *Service) ImportWallet(in ImportInput) (PublicWallet, error) {
	cleanName := strings.TrimSpace(in.Name)
	if cleanName == "" || len(cleanName) > 80 {
		return PublicWallet{}, errors.New("valid wallet name required")
	}
	id, err := newWalletID()
	if err != nil {
		return PublicWallet{}, err
	}

	if addr := strings.TrimSpace(in.WatchAddress); addr != "" {
		validAddr, err := ValidateSolanaPublicAddress(addr)
		if err != nil {
			return PublicWallet{}, err
		}
		rec := PublicWallet{
			ID:        id,
			Name:      cleanName,
			Kind:      KindWatch,
			Role:      RoleWatch,
			PublicKey: validAddr,
			CreatedAt: s.now().UTC().Unix(),
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		doc, err := s.loadLocked()
		if err != nil {
			return PublicWallet{}, err
		}
		doc.Wallets = append(doc.Wallets, walletEntry{PublicWallet: rec})
		if err := s.saveLocked(doc); err != nil {
			return PublicWallet{}, err
		}
		return rec, nil
	}

	var pk ed25519.PrivateKey
	var cleanMnemonic string
	if m := strings.TrimSpace(in.Mnemonic); m != "" {
		cleanMnemonic = strings.Join(strings.Fields(strings.ToLower(m)), " ")
		pk, err = KeypairFromMnemonic(cleanMnemonic, in.AccountIndex)
		if err != nil {
			return PublicWallet{}, err
		}
	} else if priv := strings.TrimSpace(in.PrivateKey); priv != "" {
		pk, err = KeypairFromPrivateInput(priv)
		if err != nil {
			return PublicWallet{}, err
		}
	} else {
		return PublicWallet{}, errors.New("provide mnemonic, private key, or watch address")
	}
	defer clear(pk)

	pubAddr := Base58Encode(pk.Public().(ed25519.PublicKey))
	sealed, err := s.sealSecret(id, pubAddr, in.Passphrase, secretPayload{
		Mnemonic:      cleanMnemonic,
		PrivateKeyHex: hex.EncodeToString(pk),
	})
	if err != nil {
		return PublicWallet{}, err
	}
	r := normalizeRole(in.Role)
	if r == RoleWatch {
		r = RolePersonal
	}
	rec := PublicWallet{
		ID:          id,
		Name:        cleanName,
		Kind:        KindSolana,
		Role:        r,
		PublicKey:   pubAddr,
		HasMnemonic: cleanMnemonic != "",
		AccountIdx:  in.AccountIndex,
		CreatedAt:   s.now().UTC().Unix(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return PublicWallet{}, err
	}
	doc.Wallets = append(doc.Wallets, walletEntry{PublicWallet: rec, Sealed: sealed})
	if err := s.saveLocked(doc); err != nil {
		return PublicWallet{}, err
	}
	return rec, nil
}

// ExportSecret decrypts and returns mnemonic + Base58 + Solana JSON keypair upon passphrase verification.
func (s *Service) ExportSecret(id, passphrase string) (ExportedSecret, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return ExportedSecret{}, err
	}
	for _, w := range doc.Wallets {
		if w.ID != id {
			continue
		}
		payload, err := s.unsealSecret(w, passphrase)
		if err != nil {
			return ExportedSecret{}, err
		}
		privBytes, err := hex.DecodeString(payload.PrivateKeyHex)
		if err != nil || len(privBytes) != ed25519.PrivateKeySize {
			return ExportedSecret{}, errors.New("corrupt private key")
		}
		defer clear(privBytes)
		pk := ed25519.PrivateKey(privBytes)
		return ExportedSecret{
			Mnemonic:         payload.Mnemonic,
			PrivateKeyBase58: Base58Encode(pk),
			KeypairJSON:      KeypairToSolanaJSON(pk),
			PublicKey:        w.PublicKey,
		}, nil
	}
	return ExportedSecret{}, os.ErrNotExist
}

// SignMessage signs arbitrary message bytes with an encrypted Solana wallet upon passphrase check.
func (s *Service) SignMessage(id, passphrase string, msg []byte) ([]byte, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return nil, "", err
	}
	for _, w := range doc.Wallets {
		if w.ID != id {
			continue
		}
		payload, err := s.unsealSecret(w, passphrase)
		if err != nil {
			return nil, "", err
		}
		privBytes, err := hex.DecodeString(payload.PrivateKeyHex)
		if err != nil || len(privBytes) != ed25519.PrivateKeySize {
			return nil, "", errors.New("corrupt private key")
		}
		defer clear(privBytes)
		sig := ed25519.Sign(ed25519.PrivateKey(privBytes), msg)
		return sig, w.PublicKey, nil
	}
	return nil, "", os.ErrNotExist
}

// GetWallet returns a public wallet record by ID.
func (s *Service) GetWallet(id string) (PublicWallet, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return PublicWallet{}, false, err
	}
	for _, w := range doc.Wallets {
		if w.ID == id {
			return w.PublicWallet, true, nil
		}
	}
	return PublicWallet{}, false, nil
}

// DeleteWallet removes a wallet entry by ID.
func (s *Service) DeleteWallet(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return false, err
	}
	idx := -1
	for i := range doc.Wallets {
		if doc.Wallets[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false, nil
	}
	doc.Wallets = append(doc.Wallets[:idx], doc.Wallets[idx+1:]...)
	if err := s.saveLocked(doc); err != nil {
		return false, err
	}
	return true, nil
}
