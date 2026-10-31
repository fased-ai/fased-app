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
	Backup         *RPCCredentialBackup `json:"backup,omitempty"`
	BackupVerified bool                 `json:"backupVerified,omitempty"`
	Ref            string               `json:"ref"`
	Saved          bool                 `json:"saved"`
	Stale          bool                 `json:"stale"`
	Locked         bool                 `json:"locked"`
	TestOnly       bool                 `json:"testOnly"`
	LiveRPC        bool                 `json:"liveRPC"`
	Check          *RPCCheckResult      `json:"check,omitempty"`
}
type rpcVault struct {
	Version    int    `json:"version"`
	Ref        string `json:"ref"`
	Binding    string `json:"binding"`
	Salt       []byte `json:"salt"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
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
func (s *Store) rpcAAD(id string, v rpcVault) []byte {
	prefix := "fased-rpc-vault-v1:"
	if v.Version == 2 {
		prefix = "fased-rpc-vault-v2:"
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
	return s.rpcCredential(ctx, c, func(ctx context.Context, in RPCCheckRequest, key []byte, valid func() error) (RPCCheckResult, error) {
		if in.Endpoint == FixtureRPCURL {
			return checkCredentialRPC(ctx, in, key, valid)
		}
		return checkProviderRPC(ctx, in, key, valid, newPublicRPCTransport())
	})
}
func (s *Store) rpcCredential(ctx context.Context, c RPCCredentialCommand, check func(context.Context, RPCCheckRequest, []byte, func() error) (RPCCheckResult, error)) (RPCCredentialView, error) {
	if c.Action == "save" || c.Action == "remove" || c.Action == "restore" {
		s.lockVenueCredentials()
	}
	fail := errors.New("RPC credential operation refused")
	view := RPCCredentialView{Locked: true, TestOnly: true}
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
	} else if !validPassword(c.Passphrase) || (c.Action == "save" && c.Key == "") || (c.Action != "save" && c.Key != "") {
		return view, fail
	}
	if c.Action == "check" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
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
	if version == 2 && !s.rpcAdmission && (c.Action == "save" || c.Action == "verify" || c.Action == "check" || c.Action == "restore") {
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
	var restored []byte
	if recovering {
		if c.Action == "restore" && exists {
			return view, errors.New("preserve the existing RPC vault; remove it explicitly before restore")
		}
		restored, e = s.openRPCBackup(c.Backup, *account, c.Passphrase)
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
		key := passwordHash(c.Passphrase, v.Salt)
		block, _ := aes.NewCipher(key)
		clear(key)
		gcm, _ := cipher.NewGCM(block)
		plain, err := gcm.Open(nil, v.Nonce, v.Ciphertext, s.rpcAAD(c.AccountID, v))
		defer clear(plain)
		if err != nil || !validRPCSecret(v.Version, string(plain)) {
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
	} else if c.Action != "save" && c.Action != "restore" {
		return view, fail
	}
	if c.Action == "backup" {
		if view.Stale {
			return view, fail
		}
		view.Backup = &RPCCredentialBackup{Version: v.Version, InstanceID: s.instanceID, AccountID: account.ID, Vault: v}
		view.BackupVerified = true
		return view, nil
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
		return RPCCredentialView{Locked: true, TestOnly: true}, nil
	}
	v = rpcVault{Version: version, Binding: rpcBinding(*account), Salt: make([]byte, 16), Nonce: make([]byte, 12)}
	if v.Ref, e = randomToken(); e != nil {
		return view, fail
	}
	if _, e = rand.Read(v.Salt); e != nil {
		return view, fail
	}
	if _, e = rand.Read(v.Nonce); e != nil {
		return view, fail
	}
	key := passwordHash(c.Passphrase, v.Salt)
	block, _ := aes.NewCipher(key)
	clear(key)
	gcm, _ := cipher.NewGCM(block)
	plain := []byte(c.Key)
	if c.Action == "restore" {
		plain = append([]byte(nil), restored...)
	}
	defer clear(plain)
	v.Ciphertext = gcm.Seal(nil, v.Nonce, plain, s.rpcAAD(c.AccountID, v))
	if WritePrivateJSON(s.rpcVaultPath(c.AccountID), v) != nil {
		return view, fail
	}
	return RPCCredentialView{Ref: v.Ref, Saved: true, Locked: true, TestOnly: version == 1}, nil
}
