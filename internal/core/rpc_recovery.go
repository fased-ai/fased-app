package core

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
)

// Backups contain ciphertext only. v1/v2 remain bound to the original instance,
// wallet and RPC settings; portable v3 backups bind to keyVersion, network and
// canonical endpoint so RPC credentials can restore across instances or epochs.
// Recovery never grants signing or provider authority.
type RPCCredentialBackup struct {
	Version    int      `json:"version"`
	Portable   bool     `json:"portable,omitempty"`
	InstanceID string   `json:"instanceId,omitempty"`
	AccountID  string   `json:"accountId,omitempty"`
	KeyVersion int      `json:"keyVersion,omitempty"`
	Network    string   `json:"network,omitempty"`
	Endpoint   string   `json:"endpoint,omitempty"`
	Vault      rpcVault `json:"vault"`
}

func validRPCVault(v rpcVault) bool {
	return (v.Version == 1 || v.Version == 2) && (!v.OSProtected || v.Version == 2) && len(v.Ref) == 43 && hexValue(v.Binding, 32) && len(v.Salt) == 16 && len(v.Nonce) == 12 && len(v.Ciphertext) >= 16 && len(v.Ciphertext) <= 272
}

func portableRPCBinding(keyVersion int, network, endpoint string) string {
	sum := sha256.Sum256([]byte("fased-rpc-portable-v3:" + strconv.Itoa(keyVersion) + ":" + network + ":" + endpoint))
	return hex.EncodeToString(sum[:])
}

func portableRPCAAD(b RPCCredentialBackup) []byte {
	return []byte("fased-rpc-portable-v3:" + strconv.Itoa(b.KeyVersion) + ":" + b.Network + ":" + b.Endpoint + ":" + b.Vault.Ref + ":" + b.Vault.Binding)
}

func validPortableRPCBackup(b RPCCredentialBackup) bool {
	return b.Version == 3 && b.Portable && b.InstanceID == "" && b.AccountID == "" &&
		(b.KeyVersion == 1 || b.KeyVersion == 2) && b.Vault.Version == b.KeyVersion &&
		!b.Vault.OSProtected && b.Network != "" && b.Endpoint != "" &&
		validRPCVault(b.Vault) && b.Vault.Binding == portableRPCBinding(b.KeyVersion, b.Network, b.Endpoint)
}

func sealPortableRPCBackupWithPreKey(keyVersion int, network, endpoint, passphrase string, plain []byte, pre preDerivedKDF) (RPCCredentialBackup, error) {
	if (keyVersion != 1 && keyVersion != 2) || network == "" || endpoint == "" || !validPassword(passphrase) || !validRPCSecret(keyVersion, string(plain)) {
		return RPCCredentialBackup{}, errors.New("RPC credential operation refused")
	}
	ref, err := randomToken()
	if err != nil {
		return RPCCredentialBackup{}, err
	}
	salt := make([]byte, 16)
	var key []byte
	if len(pre.key) == 32 && len(pre.salt) == 16 {
		copy(salt, pre.salt)
		key = pre.keyFor(passphrase, salt)
	} else {
		if _, err = rand.Read(salt); err != nil {
			return RPCCredentialBackup{}, err
		}
		key = passwordHash(passphrase, salt)
	}
	nonce := make([]byte, 12)
	if _, err = rand.Read(nonce); err != nil {
		clear(key)
		return RPCCredentialBackup{}, err
	}
	b := RPCCredentialBackup{
		Version:    3,
		Portable:   true,
		KeyVersion: keyVersion,
		Network:    network,
		Endpoint:   endpoint,
		Vault: rpcVault{
			Version:     keyVersion,
			OSProtected: false,
			Ref:         ref,
			Binding:     portableRPCBinding(keyVersion, network, endpoint),
			Salt:        salt,
			Nonce:       nonce,
		},
	}
	block, _ := aes.NewCipher(key)
	clear(key)
	gcm, _ := cipher.NewGCM(block)
	b.Vault.Ciphertext = gcm.Seal(nil, b.Vault.Nonce, plain, portableRPCAAD(b))
	return b, nil
}

func (s *Store) openRPCBackup(b *RPCCredentialBackup, a Account, password string) ([]byte, error) {
	return s.openRPCBackupWithPreKey(b, a, password, preDerivedKDF{})
}

func (s *Store) openRPCBackupWithPreKey(b *RPCCredentialBackup, a Account, password string, pre preDerivedKDF) ([]byte, error) {
	fail := errors.New("RPC backup does not match this wallet or password")
	if b == nil || a.RPC == nil || !validRPCVault(b.Vault) || b.Vault.OSProtected || !validPassword(password) {
		return nil, fail
	}
	var aad []byte
	if b.Version == 3 || b.Portable {
		if !validPortableRPCBackup(*b) || b.KeyVersion != a.RPC.Version || b.Network != a.RPC.Network || b.Endpoint != a.RPC.Endpoint {
			return nil, fail
		}
		aad = portableRPCAAD(*b)
	} else {
		if b.Version != b.Vault.Version || b.InstanceID != s.instanceID || b.AccountID != a.ID || b.KeyVersion != 0 || b.Network != "" || b.Endpoint != "" || b.Vault.Binding != rpcBinding(a) || b.Vault.Version != a.RPC.Version {
			return nil, fail
		}
		aad = s.rpcAAD(a.ID, b.Vault)
	}
	key := pre.keyFor(password, b.Vault.Salt)
	block, _ := aes.NewCipher(key)
	clear(key)
	gcm, _ := cipher.NewGCM(block)
	plain, e := gcm.Open(nil, b.Vault.Nonce, b.Vault.Ciphertext, aad)
	if e != nil || !validRPCSecret(b.Vault.Version, string(plain)) {
		clear(plain)
		return nil, fail
	}
	return plain, nil
}

// Publish a private encrypted backup without clobbering an existing file.
func WriteRPCBackup(path string, b *RPCCredentialBackup) error {
	if b == nil || (!validPortableRPCBackup(*b) && (b.Version != b.Vault.Version || b.Portable || b.KeyVersion != 0 || b.Network != "" || b.Endpoint != "" || !hexValue(b.AccountID, 16) || !validRPCVault(b.Vault))) {
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
