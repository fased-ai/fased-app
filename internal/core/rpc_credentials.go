package core

// RPC credentials are separate from account metadata and wallet authority.
// No plaintext is retained after an operation. Authenticated checks use only
// compiled fixtures or dormant provider routing exercised through test-only TLS.
// Ordinary credential admission and provider network dispatch remain disabled.
import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type RPCCredentialCommand struct {
	AccountID  string               `json:"accountId"`
	Revision   int64                `json:"revision"`
	Ref        string               `json:"ref"`
	Action     string               `json:"action"`
	Key        string               `json:"key,omitempty"`
	Passphrase string               `json:"passphrase,omitempty"`
	Backup     *RPCCredentialBackup `json:"backup,omitempty"`
}
type RPCCredentialView struct {
	CanVerify        bool                 `json:"canVerify"`
	CanCheck         bool                 `json:"canCheck"`
	Backup           *RPCCredentialBackup `json:"backup,omitempty"`
	BackupVerified   bool                 `json:"backupVerified,omitempty"`
	Ref              string               `json:"ref"`
	Saved            bool                 `json:"saved"`
	Stale            bool                 `json:"stale"`
	Locked           bool                 `json:"locked"`
	OSVaultAvailable bool                 `json:"osVaultAvailable,omitempty"`
	OSProtected      bool                 `json:"osProtected,omitempty"`
	TestOnly         bool                 `json:"testOnly"`
	LiveRPC          bool                 `json:"liveRPC"`
	Check            *RPCCheckResult      `json:"check,omitempty"`
}
type rpcVault struct {
	Version     int    `json:"version"`
	OSProtected bool   `json:"osProtected,omitempty"`
	Ref         string `json:"ref"`
	Binding     string `json:"binding"`
	Salt        []byte `json:"salt"`
	Nonce       []byte `json:"nonce"`
	Ciphertext  []byte `json:"ciphertext"`
}

func rpcBinding(a Account) string {
	a.Name = ""
	b, _ := json.Marshal(a)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func testRPCCredential(s string) bool {
	return strings.HasPrefix(s, "fased-rpc-test-") && len(s) >= 30 && len(s) <= 256 && !strings.ContainsAny(s, " \r\n\t")
}
func (s *Store) rpcVaultPath(id string) string {
	return filepath.Join(s.dir, "rpc-credential-"+id+".json")
}
func deriveRPCOSVaultKey(osKey, salt []byte) []byte {
	h := sha256.New()
	h.Write([]byte("fased-rpc-os-vault-v1:"))
	h.Write(osKey)
	h.Write(salt)
	return h.Sum(nil)
}
func (s *Store) rpcAAD(id string, v rpcVault) []byte {
	prefix := "fased-rpc-vault-v1:"
	if v.Version == 2 {
		prefix = "fased-rpc-vault-v2:"
		if v.OSProtected {
			prefix = "fased-rpc-vault-os-v2:"
		}
	}
	return []byte(prefix + s.instanceID + ":" + id + ":" + v.Ref + ":" + v.Binding)
}
func (s *Store) readRPCVault(id string) (rpcVault, error) {
	var v rpcVault
	p := s.rpcVaultPath(id)
	info, e := os.Lstat(p)
	if e != nil {
		return v, e
	}
	if !info.Mode().IsRegular() || info.Size() > 2048 {
		return v, errors.New("invalid vault")
	}
	b, e := ReadPrivateFile(p)
	if e != nil {
		return v, e
	}
	if json.Unmarshal(b, &v) != nil || !validRPCVault(v) {
		return v, errors.New("invalid vault")
	}
	return v, nil
}
func (s *Store) RPCCredential(ctx context.Context, c RPCCredentialCommand) (RPCCredentialView, error) {
	transport := newPublicRPCTransport()
	defer transport.transport.CloseIdleConnections()
	var check func(context.Context, RPCCheckRequest, []byte, func() error) (RPCCheckResult, error)
	if transport.enabled && s.rpcAdmission {
		check = func(ctx context.Context, in RPCCheckRequest, key []byte, valid func() error) (RPCCheckResult, error) {
			if in.Endpoint == FixtureRPCURL {
				return checkCredentialRPC(ctx, in, key, valid)
			}
			return checkProviderRPC(ctx, in, key, valid, transport)
		}
	}
	return s.rpcCredential(ctx, c, check)
}
func (s *Store) rpcCredential(ctx context.Context, c RPCCredentialCommand, check func(context.Context, RPCCheckRequest, []byte, func() error) (RPCCheckResult, error)) (RPCCredentialView, error) {
	if c.Action == "save" || c.Action == "remove" || c.Action == "restore" {
		s.lockVenueCredentials()
	}
	fail := errors.New("RPC credential operation refused")
	osVaultAvailable := s.osVaultSecretsAvailable(ctx)
	view := RPCCredentialView{Locked: true, TestOnly: true, OSVaultAvailable: osVaultAvailable}
	if !hexValue(c.AccountID, 16) || (c.Action != "status" && c.Action != "save" && c.Action != "verify" && c.Action != "check" && c.Action != "remove" && c.Action != "backup" && c.Action != "verify-backup" && c.Action != "restore") {
		return view, fail
	}
	recovering := c.Action == "restore" || c.Action == "verify-backup"
	if recovering != (c.Backup != nil) {
		return view, fail
	}
	if c.Action == "status" {
		if c.Key != "" || c.Passphrase != "" {
			return view, fail
		}
	} else if (c.Action == "save" && c.Key == "") || (c.Action != "save" && c.Key != "") || (c.Passphrase != "" && !validPassword(c.Passphrase)) || ((recovering || c.Action == "backup") && !validPassword(c.Passphrase)) {
		return view, fail
	}
	if c.Action == "check" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}
	var preBackup, preOpen, preSeal preDerivedKDF
	defer preBackup.clear()
	defer preOpen.clear()
	defer preSeal.clear()
	if c.Passphrase != "" && validPassword(c.Passphrase) {
		if recovering && c.Backup != nil && len(c.Backup.Vault.Salt) == 16 {
			preBackup = derivePasswordKeyForSalt(c.Passphrase, c.Backup.Vault.Salt)
		}
		if c.Action != "status" {
			if rv, err := s.readRPCVault(c.AccountID); err == nil && !rv.OSProtected {
				preOpen = derivePasswordKeyForSalt(c.Passphrase, rv.Salt)
			}
		}
		if c.Action == "save" || c.Action == "restore" || c.Action == "backup" {
			if p, err := deriveNewPasswordKey(c.Passphrase); err == nil {
				preSeal = p
			}
		}
	}
	// SQLite's write fence serializes configuration/identity changes and vault
	// operations across processes, and refuses mutations during update pause.
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
	if e = tx.QueryRowContext(ctx, "SELECT document,revision FROM accounts WHERE id=1").Scan(&raw, &revision); e != nil {
		return view, fail
	}
	if c.Revision != revision {
		return view, ErrConflict
	}
	var records AccountsRecord
	if json.Unmarshal([]byte(raw), &records) != nil {
		return view, fail
	}
	var account *Account
	for i := range records.Accounts {
		if records.Accounts[i].ID == c.AccountID {
			account = &records.Accounts[i]
			break
		}
	}
	if account == nil {
		return view, fail
	}
	version := 1
	if account.Version == 2 || (managedNetworkAccount(*account) || ordinaryNetworkAccount(*account)) {
		version = 2
		view.TestOnly = false
	}
	if check == nil && version == 1 && account.RPC != nil && account.RPC.Endpoint == FixtureRPCURL {
		check = checkCredentialRPC
	}
	view.CanVerify = version == 1 || s.rpcAdmission
	view.CanCheck = view.CanVerify && check != nil
	configurationOnly := (c.Action == "save" || c.Action == "restore") && s.controlledOrdinaryAccount(ctx, *account)
	if version == 2 && !s.rpcAdmission && !configurationOnly && (c.Action == "save" || c.Action == "verify" || c.Action == "check" || c.Action == "restore") {
		return view, errors.New("live RPC credentials disabled")
	}
	if c.Action == "save" && !validRPCSecret(version, c.Key) {
		return view, fail
	}
	if (c.Action == "save" || c.Action == "verify" || c.Action == "check" || c.Action == "restore") && (account.Mode == "fixture-managed" || ordinaryNetworkAccount(*account)) {
		live := s.fixtureView(ctx)
		if account.Signer == nil || live.Status == nil || live.Status.Account == nil || *live.Status.Account != *account.Signer {
			return view, fail
		}
	}
	v, e := s.readRPCVault(c.AccountID)
	exists := e == nil
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return view, errors.New("RPC vault unavailable; preserve it before recovery")
	}
	if exists {
		view.TestOnly = v.Version == 1
		view.Ref = v.Ref
		view.Saved = true
		view.OSProtected = v.OSProtected
		view.Stale = account.RPC == nil || v.Binding != rpcBinding(*account)
		if account.Mode == "fixture-managed" || ordinaryNetworkAccount(*account) {
			live := s.fixtureView(ctx)
			// The saved metadata may still name the pre-recovery epoch. Public
			// status must not present its encrypted credential as current.
			view.Stale = view.Stale || !live.Available || account.Signer == nil || live.Status == nil || live.Status.Account == nil || *live.Status.Account != *account.Signer
		}
	}
	if c.Action == "status" {
		return view, nil
	}
	if c.Ref != view.Ref {
		return view, ErrConflict
	}
	if c.Action != "remove" && (account.RPC == nil || (version == 1 && account.RPC.Endpoint != FixtureRPCURL) || (version == 2 && !validProviderRPC(*account)) || account.RPC.Network != account.Network) {
		return view, fail
	}
	if version == 2 && (c.Action == "save" || c.Action == "check" || c.Action == "restore") && !rpcProviderRequiresKey(rpcProvider(account.RPC.Endpoint, account.Network)) {
		return view, fail
	}
	if c.Action == "save" && c.Passphrase == "" && (version != 2 || !osVaultAvailable) {
		return view, fail
	}
	var restored []byte
	if recovering {
		if c.Action == "restore" && exists {
			return view, errors.New("preserve the existing RPC vault; remove it explicitly before restore")
		}
		restored, e = s.openRPCBackupWithPreKey(c.Backup, *account, c.Passphrase, preBackup)
		if e != nil {
			return view, e
		}
		defer clear(restored)
		if c.Action == "verify-backup" {
			view.BackupVerified = true
			return view, nil
		}
	}
	if exists {
		openSecret := c.Passphrase
		if c.Action == "backup" {
			if v.OSProtected {
				openSecret = ""
			}
		} else if c.Action != "save" {
			if v.OSProtected {
				if c.Passphrase != "" {
					return view, fail
				}
				openSecret = ""
			} else if !validPassword(c.Passphrase) {
				return view, fail
			}
		}
		plain, err := s.openRPCVaultKeyWithPreKey(ctx, c.AccountID, openSecret, v, preOpen)
		defer clear(plain)
		if err != nil {
			return view, fail
		}

		if c.Action == "check" {
			if view.Stale || check == nil || v.Version != version {
				return view, fail
			}
			sealed, _ := json.Marshal(v)
			valid := func() error {
				if ctx.Err() != nil {
					return fail
				}
				current, e := s.readRPCVault(c.AccountID)
				if e != nil {
					return fail
				}
				encoded, _ := json.Marshal(current)
				if sha256.Sum256(encoded) != sha256.Sum256(sealed) {
					return fail
				}
				if account.Mode == "fixture-managed" || ordinaryNetworkAccount(*account) {
					live := s.fixtureView(ctx)
					if account.Signer == nil || live.Status == nil || live.Status.Account == nil || *live.Status.Account != *account.Signer {
						return fail
					}
				}
				return nil
			}
			result, e := check(ctx, RPCCheckRequest{Endpoint: account.RPC.Endpoint, Network: account.RPC.Network}, plain, valid)
			if e != nil || valid() != nil {
				return view, errors.New("Authenticated RPC check failed")
			}
			result.Account = account
			result.AccountRevision = &revision
			view.Check = &result
			return view, nil
		}
		if c.Action == "backup" {
			if view.Stale {
				return view, fail
			}
			backupVault := v
			if v.OSProtected {
				backupVault, err = s.sealRPCVaultEnvelopeWithPreKey(ctx, v.Version, account.ID, v.Ref, v.Binding, c.Passphrase, plain, false, preSeal)
				if err != nil {
					return view, fail
				}
			}
			view.Backup = &RPCCredentialBackup{Version: backupVault.Version, InstanceID: s.instanceID, AccountID: account.ID, Vault: backupVault}
			view.BackupVerified = true
			return view, nil
		}
	} else if c.Action != "save" && c.Action != "restore" {
		return view, fail
	}
	if c.Action == "verify" {
		if view.Stale {
			return view, fail
		}
		return view, nil
	}
	if c.Action == "remove" {
		if os.Remove(s.rpcVaultPath(c.AccountID)) != nil {
			return view, fail
		}
		return RPCCredentialView{Locked: true, TestOnly: true, CanVerify: view.CanVerify, CanCheck: view.CanCheck, OSVaultAvailable: osVaultAvailable}, nil
	}
	plain := []byte(c.Key)
	if c.Action == "restore" {
		plain = append([]byte(nil), restored...)
	}
	defer clear(plain)
	v, err := s.sealRPCVaultEnvelopeWithPreKey(ctx, version, c.AccountID, "", rpcBinding(*account), c.Passphrase, plain, osVaultAvailable, preSeal)
	if err != nil {
		return view, fail
	}
	if WritePrivateJSON(s.rpcVaultPath(c.AccountID), v) != nil {
		return view, fail
	}
	return RPCCredentialView{Ref: v.Ref, Saved: true, Locked: true, OSVaultAvailable: osVaultAvailable, OSProtected: v.OSProtected, TestOnly: version == 1, CanVerify: view.CanVerify, CanCheck: view.CanCheck}, nil
}

func (s *Store) rpcSealKey(ctx context.Context, version int, passphrase string, salt []byte, osVaultAvailable bool) ([]byte, bool, error) {
	if passphrase != "" {
		if !validPassword(passphrase) {
			return nil, false, errors.New("RPC credential operation refused")
		}
		return passwordHash(passphrase, salt), false, nil
	}
	if version != 2 || !osVaultAvailable {
		return nil, false, errors.New("RPC credential operation refused")
	}
	osKey, err := s.taskSecretKey(ctx, true)
	if err != nil || len(osKey) != 32 {
		clear(osKey)
		return nil, false, errors.New("system credential protection unavailable")
	}
	derived := deriveRPCOSVaultKey(osKey, salt)
	clear(osKey)
	return derived, true, nil
}

func (s *Store) sealRPCVaultEnvelope(ctx context.Context, version int, id, ref, binding, passphrase string, plain []byte, osVaultAvailable bool) (rpcVault, error) {
	return s.sealRPCVaultEnvelopeWithPreKey(ctx, version, id, ref, binding, passphrase, plain, osVaultAvailable, preDerivedKDF{})
}

func (s *Store) sealRPCVaultEnvelopeWithPreKey(ctx context.Context, version int, id, ref, binding, passphrase string, plain []byte, osVaultAvailable bool, pre preDerivedKDF) (rpcVault, error) {
	v := rpcVault{
		Version: version,
		Ref:     ref,
		Binding: binding,
		Salt:    make([]byte, 16),
		Nonce:   make([]byte, 12),
	}
	if v.Ref == "" {
		var err error
		if v.Ref, err = randomToken(); err != nil {
			return rpcVault{}, err
		}
	}
	var key []byte
	var osProtected bool
	if passphrase != "" && len(pre.key) == 32 && len(pre.salt) == 16 {
		copy(v.Salt, pre.salt)
		key, osProtected = pre.keyFor(passphrase, v.Salt), false
	} else {
		if _, err := rand.Read(v.Salt); err != nil {
			return rpcVault{}, err
		}
		var err error
		key, osProtected, err = s.rpcSealKey(ctx, version, passphrase, v.Salt, osVaultAvailable)
		if err != nil {
			return rpcVault{}, err
		}
	}
	if _, err := rand.Read(v.Nonce); err != nil {
		clear(key)
		return rpcVault{}, err
	}
	v.OSProtected = osProtected
	block, _ := aes.NewCipher(key)
	clear(key)
	gcm, _ := cipher.NewGCM(block)
	v.Ciphertext = gcm.Seal(nil, v.Nonce, plain, s.rpcAAD(id, v))
	return v, nil
}

// Every caller owns only one operation's plaintext and must clear it. This
// creates no unlocked session, background lease or new credential format.
func (s *Store) openRPCVaultKey(ctx context.Context, id, passphrase string, v rpcVault) ([]byte, error) {
	return s.openRPCVaultKeyWithPreKey(ctx, id, passphrase, v, preDerivedKDF{})
}

func (s *Store) openRPCVaultKeyWithPreKey(ctx context.Context, id, passphrase string, v rpcVault, pre preDerivedKDF) ([]byte, error) {
	if !validRPCVault(v) {
		return nil, errors.New("RPC locked")
	}
	var key []byte
	if v.OSProtected {
		if passphrase != "" || !s.osVaultSecretsAvailable(ctx) {
			return nil, errors.New("RPC locked")
		}
		osKey, err := s.taskSecretKey(ctx, false)
		if err != nil || len(osKey) != 32 {
			clear(osKey)
			return nil, errors.New("RPC locked")
		}
		key = deriveRPCOSVaultKey(osKey, v.Salt)
		clear(osKey)
	} else {
		if !validPassword(passphrase) {
			return nil, errors.New("RPC locked")
		}
		key = pre.keyFor(passphrase, v.Salt)
	}
	block, _ := aes.NewCipher(key)
	clear(key)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, v.Nonce, v.Ciphertext, s.rpcAAD(id, v))
	if err != nil || !validRPCSecret(v.Version, string(plain)) {
		clear(plain)
		return nil, errors.New("RPC locked")
	}
	return plain, nil
}
