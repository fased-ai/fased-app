package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/fased-ai/fased-app/internal/signer"
	"os"
	"path/filepath"
	"testing"
)

func rpcSecretSetup(t *testing.T, s *Store) RPCCredentialCommand {
	t.Helper()
	claimFixture(t, s)
	r := defaultAccounts()
	r.Accounts = []Account{publicRecord()}
	v, e := s.UpdateAccounts(t.Context(), r)
	if e != nil {
		t.Fatal(e)
	}
	v, e = s.ConfigureAccountRPC(t.Context(), RPCConfigInput{AccountID: r.Accounts[0].ID, Revision: v.Record.Revision, Endpoint: FixtureRPCURL, Network: signer.FixtureNetwork})
	if e != nil {
		t.Fatal(e)
	}
	return RPCCredentialCommand{AccountID: r.Accounts[0].ID, Revision: v.Record.Revision, Action: "save", Key: "fased-rpc-test-disposable-secret-012345", Passphrase: "Disposable RPC vault password 123"}
}
func TestRPCCredentialPersistenceRedactionAndRemoval(t *testing.T) {
	dir := fixtureDir(t)
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { s.Close() }()
	c := rpcSecretSetup(t, s)
	v, e := s.RPCCredential(t.Context(), c)
	if e != nil || !v.Saved || !v.Locked || v.LiveRPC {
		t.Fatal(v, e)
	}
	original := c
	public, _ := json.Marshal(v)
	if bytes.Contains(public, []byte(c.Key)) || bytes.Contains(public, []byte(c.Passphrase)) {
		t.Fatal("secret response")
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if !entry.IsDir() {
			b, _ := os.ReadFile(filepath.Join(dir, entry.Name()))
			if bytes.Contains(b, []byte(c.Key)) || bytes.Contains(b, []byte(c.Passphrase)) {
				t.Fatal("plaintext persisted", entry.Name())
			}
		}
	}
	s.Close()
	s, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	c.Action = "verify"
	c.Ref = v.Ref
	c.Key = ""
	if _, e = s.RPCCredential(t.Context(), c); e != nil {
		t.Fatal(e)
	}
	wrong := c
	wrong.Passphrase = "Wrong disposable password 123"
	if _, e = s.RPCCredential(t.Context(), wrong); e == nil {
		t.Fatal("wrong password")
	}
	if _, e = s.RPCCredential(t.Context(), original); !errors.Is(e, ErrConflict) {
		t.Fatal("replacement without ref", e)
	}
	rotate := original
	rotate.Ref = v.Ref
	rotate.Key += "-rotated"
	v, e = s.RPCCredential(t.Context(), rotate)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.RPCCredential(t.Context(), c); !errors.Is(e, ErrConflict) {
		t.Fatal("stale ref")
	}
	c.Ref = v.Ref
	c.Action = "remove"
	if v, e = s.RPCCredential(t.Context(), c); e != nil || v.Saved {
		t.Fatal(v, e)
	}
	if _, e = os.Stat(s.rpcVaultPath(c.AccountID)); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("not removed")
	}
}
func TestRPCCredentialBindingTamperPauseAndBackup(t *testing.T) {
	s := fixtureStore(t)
	c := rpcSecretSetup(t, s)
	bad := c
	bad.Key = "sk-real-not-admitted"
	if _, e := s.RPCCredential(t.Context(), bad); e == nil {
		t.Fatal("real key")
	}
	v, e := s.RPCCredential(t.Context(), c)
	if e != nil {
		t.Fatal(e)
	}
	c.Key = ""
	c.Ref = v.Ref
	c.Action = "verify"
	raw, _ := ReadPrivateFile(s.rpcVaultPath(c.AccountID))
	env, e := s.readRPCVault(c.AccountID)
	if e != nil {
		t.Fatal(e)
	}
	env.Ciphertext[0] ^= 1
	WritePrivateJSON(s.rpcVaultPath(c.AccountID), env)
	if _, e = s.RPCCredential(t.Context(), c); e == nil {
		t.Fatal("tamper")
	}
	os.WriteFile(s.rpcVaultPath(c.AccountID), raw, 0600)
	before := s.instanceID
	s.instanceID = "other-instance"
	if _, e = s.RPCCredential(t.Context(), c); e == nil {
		t.Fatal("instance swap")
	}
	s.instanceID = before
	records, _ := s.accountsRecord(t.Context())
	records.Accounts[0].PublicKey = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	changed, e := s.UpdateAccounts(t.Context(), records)
	if e != nil {
		t.Fatal(e)
	}
	c.Revision = changed.Record.Revision
	if _, e = s.RPCCredential(t.Context(), c); e == nil {
		t.Fatal("changed identity")
	}
	status := c
	status.Action = "status"
	status.Passphrase = ""
	v, e = s.RPCCredential(t.Context(), status)
	if e != nil || !v.Stale {
		t.Fatal(v, e)
	}
	p := pausedBackup(t, s)
	if _, e = s.BackupMetadata(t.Context(), p); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(s.dir, "lifecycle", p.IntentID, "metadata.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(b, raw) || bytes.Contains(b, env.Ciphertext) || bytes.Contains(b, []byte("fased-rpc-test-disposable-secret-012345")) {
		t.Fatal("vault leaked into metadata backup")
	}
	c.Action = "remove"
	if _, e = s.RPCCredential(t.Context(), c); !errors.Is(e, ErrCorePaused) {
		t.Fatal("pause", e)
	}
	after, _ := ReadPrivateFile(s.rpcVaultPath(c.AccountID))
	if !bytes.Equal(raw, after) {
		t.Fatal("pause mutated vault")
	}
}

func TestRPCCredentialCorruptVaultPreserved(t *testing.T) {
	s := fixtureStore(t)
	c := rpcSecretSetup(t, s)
	path := s.rpcVaultPath(c.AccountID)
	raw := []byte("malformed existing vault")
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := s.RPCCredential(t.Context(), c); e == nil {
		t.Fatal("corrupt vault overwritten")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(raw, after) {
		t.Fatal("corrupt vault lost")
	}
}

func TestRPCCredentialManagedReference(t *testing.T) {
	s := fixtureStore(t)
	c := rpcSecretSetup(t, s)
	ref := testRef()
	offline := false
	s.fixture = func(_ context.Context, r signer.Request) (signer.Response, error) {
		if r.Action != "status" {
			t.Fatal("signer mutation")
		}
		if offline {
			return signer.Response{}, errors.New("offline")
		}
		return signer.Response{Status: signer.Status{Account: &ref, FixtureOnly: true, Locked: true}}, nil
	}
	records, _ := s.accountsRecord(t.Context())
	a := &records.Accounts[0]
	a.Mode = "fixture-managed"
	a.Signer = &ref
	a.PublicKey = ref.PublicKey
	v, e := s.UpdateAccounts(t.Context(), records)
	if e != nil {
		t.Fatal(e)
	}
	v, e = s.ConfigureAccountRPC(t.Context(), RPCConfigInput{AccountID: c.AccountID, Revision: v.Record.Revision, Endpoint: FixtureRPCURL, Network: ref.Network})
	if e != nil {
		t.Fatal(e)
	}
	c.Revision = v.Record.Revision
	secret, e := s.RPCCredential(t.Context(), c)
	if e != nil {
		t.Fatal(e)
	}
	c.Key = ""
	c.Ref = secret.Ref
	c.Action = "verify"
	offline = true
	if _, e = s.RPCCredential(t.Context(), c); e == nil {
		t.Fatal("offline reference accepted")
	}
	offline = false
	ref.Epoch = "88888888888888888888888888888888"
	if _, e = s.RPCCredential(t.Context(), c); e == nil {
		t.Fatal("changed epoch accepted")
	}
	c.Action = "remove"
	if _, e = s.RPCCredential(t.Context(), c); e != nil {
		t.Fatal("stale secret cannot be removed", e)
	}
}
