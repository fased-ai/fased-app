package core

// RPC credentials are separate from account metadata and wallet authority.
// No plaintext is retained after an operation. Authenticated checks use only
// compiled fixtures or test-injected controlled HTTPS, never a live transport.
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
	AccountID  string `json:"accountId"`
	Revision   int64  `json:"revision"`
	Ref        string `json:"ref"`
	Action     string `json:"action"`
	Key        string `json:"key,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
}
type RPCCredentialView struct {
	Ref      string          `json:"ref"`
	Saved    bool            `json:"saved"`
	Stale    bool            `json:"stale"`
	Locked   bool            `json:"locked"`
	TestOnly bool            `json:"testOnly"`
	LiveRPC  bool            `json:"liveRPC"`
	Check    *RPCCheckResult `json:"check,omitempty"`
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
	return []byte("fased-rpc-vault-v1:" + s.instanceID + ":" + id + ":" + v.Ref + ":" + v.Binding)
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
	if json.Unmarshal(b, &v) != nil || v.Version != 1 || len(v.Ref) != 43 || !hexValue(v.Binding, 32) || len(v.Salt) != 16 || len(v.Nonce) != 12 || len(v.Ciphertext) < 16 || len(v.Ciphertext) > 272 {
		return v, errors.New("invalid vault")
	}
	return v, nil
}
func (s *Store) RPCCredential(ctx context.Context, c RPCCredentialCommand) (RPCCredentialView, error) {
	return s.rpcCredential(ctx, c, checkCredentialRPC)
}
func (s *Store) rpcCredential(ctx context.Context, c RPCCredentialCommand, check func(context.Context, RPCCheckRequest, []byte, func() error) (RPCCheckResult, error)) (RPCCredentialView, error) {
	fail := errors.New("RPC credential operation refused")
	view := RPCCredentialView{Locked: true, TestOnly: true}
	if !hexValue(c.AccountID, 16) || (c.Action != "status" && c.Action != "save" && c.Action != "verify" && c.Action != "check" && c.Action != "remove") {
		return view, fail
	}
	if c.Action == "status" {
		if c.Key != "" || c.Passphrase != "" {
			return view, fail
		}
	} else if !validPassword(c.Passphrase) || (c.Action == "save" && !testRPCCredential(c.Key)) || (c.Action != "save" && c.Key != "") {
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
	if (c.Action == "save" || c.Action == "verify" || c.Action == "check") && account.Mode == "fixture-managed" {
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
		view.Ref = v.Ref
		view.Saved = true
		view.Stale = account.RPC == nil || v.Binding != rpcBinding(*account)
	}
	if c.Action == "status" {
		return view, nil
	}
	if c.Ref != view.Ref {
		return view, ErrConflict
	}
	if c.Action != "remove" && (account.RPC == nil || account.RPC.Endpoint != FixtureRPCURL || account.RPC.Network != account.Network) {
		return view, fail
	}
	if exists {
		key := passwordHash(c.Passphrase, v.Salt)
		block, _ := aes.NewCipher(key)
		clear(key)
		gcm, _ := cipher.NewGCM(block)
		plain, err := gcm.Open(nil, v.Nonce, v.Ciphertext, s.rpcAAD(c.AccountID, v))
		defer clear(plain)
		if err != nil || !testRPCCredential(string(plain)) {
			return view, fail
		}

		if c.Action == "check" {
			if view.Stale || check == nil {
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
				if account.Mode == "fixture-managed" {
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
	} else if c.Action != "save" {
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
		return RPCCredentialView{Locked: true, TestOnly: true}, nil
	}
	v = rpcVault{Version: 1, Binding: rpcBinding(*account), Salt: make([]byte, 16), Nonce: make([]byte, 12)}
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
	defer clear(plain)
	v.Ciphertext = gcm.Seal(nil, v.Nonce, plain, s.rpcAAD(c.AccountID, v))
	if WritePrivateJSON(s.rpcVaultPath(c.AccountID), v) != nil {
		return view, fail
	}
	return RPCCredentialView{Ref: v.Ref, Saved: true, Locked: true, TestOnly: true}, nil
}
