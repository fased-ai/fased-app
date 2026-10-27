package core

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Backups contain ciphertext only and remain bound to the original instance,
// wallet and RPC settings. Recovery never grants signing or provider authority.
type RPCCredentialBackup struct {
	Version    int      `json:"version"`
	InstanceID string   `json:"instanceId"`
	AccountID  string   `json:"accountId"`
	Vault      rpcVault `json:"vault"`
}

func validRPCVault(v rpcVault) bool {
	return (v.Version == 1 || v.Version == 2) && len(v.Ref) == 43 && hexValue(v.Binding, 32) && len(v.Salt) == 16 && len(v.Nonce) == 12 && len(v.Ciphertext) >= 16 && len(v.Ciphertext) <= 272
}
func (s *Store) openRPCBackup(b *RPCCredentialBackup, a Account, password string) ([]byte, error) {
	fail := errors.New("RPC backup does not match this wallet or password")
	if b == nil || b.Version != b.Vault.Version || b.InstanceID != s.instanceID || b.AccountID != a.ID || a.RPC == nil || !validRPCVault(b.Vault) || b.Vault.Binding != rpcBinding(a) || b.Vault.Version != a.RPC.Version {
		return nil, fail
	}
	key := passwordHash(password, b.Vault.Salt)
	block, _ := aes.NewCipher(key)
	clear(key)
	gcm, _ := cipher.NewGCM(block)
	plain, e := gcm.Open(nil, b.Vault.Nonce, b.Vault.Ciphertext, s.rpcAAD(a.ID, b.Vault))
	if e != nil || !validRPCSecret(b.Vault.Version, string(plain)) {
		clear(plain)
		return nil, fail
	}
	return plain, nil
}

// Publish a private encrypted backup without clobbering an existing file.
func WriteRPCBackup(path string, b *RPCCredentialBackup) error {
	if b == nil || b.Version != b.Vault.Version || !hexValue(b.AccountID, 16) || !validRPCVault(b.Vault) {
		return errors.New("invalid RPC backup")
	}
	if e := EnsurePrivateDirectory(filepath.Dir(path)); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".rpc-backup-*")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	info, e := f.Stat()
	if e == nil {
		e = protectPath(name, info)
	}
	if e != nil {
		f.Close()
		return e
	}
	e = json.NewEncoder(f).Encode(b)
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Link(name, path)
}
