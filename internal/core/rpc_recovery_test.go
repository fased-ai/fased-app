package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRPCRecoveryBackupRestoreAndRefusals(t *testing.T) {
	dir := fixtureDir(t)
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { s.Close() }()
	c := rpcSecretSetup(t, s)
	key := c.Key
	v, e := s.RPCCredential(t.Context(), c)
	if e != nil {
		t.Fatal(e)
	}
	c.Key = ""
	c.Ref = v.Ref
	c.Action = "backup"
	v, e = s.RPCCredential(t.Context(), c)
	if e != nil || v.Backup == nil {
		t.Fatal(v, e)
	}
	b := v.Backup
	raw, _ := json.Marshal(b)
	if bytes.Contains(raw, []byte(key)) || bytes.Contains(raw, []byte(c.Passphrase)) {
		t.Fatal("plaintext backup")
	}
	path := filepath.Join(dir, "exports", "rpc.json")
	if e = WriteRPCBackup(path, b); e != nil {
		t.Fatal(e)
	}
	if e = WriteRPCBackup(path, b); e == nil {
		t.Fatal("backup overwritten")
	}
	saved, _ := ReadPrivateFile(path)
	var restoredBackup RPCCredentialBackup
	if json.Unmarshal(saved, &restoredBackup) != nil {
		t.Fatal("bad backup")
	}
	verify := c
	verify.Action = "verify-backup"
	verify.Backup = &restoredBackup
	if v, e = s.RPCCredential(t.Context(), verify); e != nil || !v.BackupVerified {
		t.Fatal(v, e)
	}
	original, _ := os.ReadFile(s.rpcVaultPath(c.AccountID))
	overwrite := verify
	overwrite.Action = "restore"
	if _, e = s.RPCCredential(t.Context(), overwrite); e == nil {
		t.Fatal("existing vault overwritten")
	}
	after, _ := os.ReadFile(s.rpcVaultPath(c.AccountID))
	if !bytes.Equal(original, after) {
		t.Fatal("vault changed")
	}
	c.Action = "remove"
	if _, e = s.RPCCredential(t.Context(), c); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	restore := verify
	restore.Action = "restore"
	restore.Ref = ""
	for _, mode := range []string{"password", "instance", "account", "binding", "ciphertext", "revision"} {
		bad := restore
		copy := restoredBackup
		copy.Vault.Ciphertext = append([]byte(nil), copy.Vault.Ciphertext...)
		bad.Backup = &copy
		switch mode {
		case "password":
			bad.Passphrase = "Wrong disposable password 123"
		case "instance":
			copy.InstanceID = "other"
		case "account":
			copy.AccountID = "other"
		case "binding":
			copy.Vault.Binding = "bad"
		case "ciphertext":
			copy.Vault.Ciphertext[0] ^= 1
		case "revision":
			bad.Revision++
		}
		if _, e = s.RPCCredential(t.Context(), bad); e == nil {
			t.Fatal("bad restore accepted", mode)
		}
		if _, e = os.Stat(s.rpcVaultPath(c.AccountID)); !errors.Is(e, os.ErrNotExist) {
			t.Fatal("refusal created vault")
		}
	}
	v, e = s.RPCCredential(t.Context(), restore)
	if e != nil || !v.Saved || !v.Locked || v.Ref == b.Vault.Ref {
		t.Fatal(v, e)
	}
	check := c
	check.Ref = v.Ref
	check.Action = "check"
	if v, e = s.RPCCredential(t.Context(), check); e != nil || v.Check == nil {
		t.Fatal("restored key unusable", e)
	}
	c.Ref = check.Ref
	c.Action = "remove"
	if _, e = s.RPCCredential(t.Context(), c); e != nil {
		t.Fatal(e)
	}
	if _, e = s.UpdatePause(t.Context(), pauseCommand(t, s, 1)); e != nil {
		t.Fatal(e)
	}
	if _, e = s.RPCCredential(t.Context(), restore); !errors.Is(e, ErrCorePaused) {
		t.Fatal("pause bypass", e)
	}
}

func TestRPCRecoveryPrivateExport(t *testing.T) {
	s := fixtureStore(t)
	c := rpcSecretSetup(t, s)
	v, e := s.RPCCredential(t.Context(), c)
	if e != nil {
		t.Fatal(e)
	}
	c.Key = ""
	c.Ref = v.Ref
	c.Action = "backup"
	v, e = s.RPCCredential(t.Context(), c)
	if e != nil {
		t.Fatal(e)
	}
	root := fixtureDir(t)
	path := filepath.Join(root, "backup.json")
	if e = WriteRPCBackup(path, v.Backup); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(path)
	if e = WriteRPCBackup(path, v.Backup); e == nil {
		t.Fatal("overwritten")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("existing bytes changed")
	}
	if runtime.GOOS != "windows" {
		broad := filepath.Join(root, "public")
		os.Mkdir(broad, 0755)
		if e = WriteRPCBackup(filepath.Join(broad, "backup.json"), v.Backup); e == nil {
			t.Fatal("public parent accepted")
		}
		link := filepath.Join(root, "link.json")
		if e = os.Symlink(path, link); e != nil {
			t.Fatal(e)
		}
		if e = WriteRPCBackup(link, v.Backup); e == nil {
			t.Fatal("symlink destination accepted")
		}
	}
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if len(entry.Name()) >= 12 && entry.Name()[:12] == ".rpc-backup-" {
			t.Fatal("scratch leaked")
		}
	}
}
