//go:build linux

package signer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const testSignerID = "11111111111111111111111111111111"

func managedFixture(t *testing.T, dir, network string) *Vault {
	t.Helper()
	v, e := openVaultNetwork(dir, testSignerID, false, network)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { v.Close() })
	return v
}
func TestManagedKeyLifecycle(t *testing.T) {
	for _, network := range []string{"devnet", "mainnet-beta"} {
		t.Run(network, func(t *testing.T) {
			dir := fixtureDir(t)
			v := managedFixture(t, dir, network)
			seed := bytes.Repeat([]byte{7}, 32)
			defer wipe(seed)
			if e := v.importManagedSeed(seed, fixturePassword); e != nil {
				t.Fatal(e)
			}
			first := v.Status()
			if first.Account == nil || first.Account.Version != 2 || first.Account.Network != network || !first.Locked || !first.RecoveryRequired || first.WalletSigner || first.FinancialExecution || first.UnattendedUnlock {
				t.Fatal("unsafe custody status", first)
			}
			if ValidAccountRef(*first.Account) {
				t.Fatal("managed key gained fixture authority")
			}
			if e := v.Unlock(fixturePassword, time.Minute); e != nil {
				t.Fatal(e)
			}
			if _, e := v.ExportFixtureSeed(); e == nil {
				t.Fatal("fixture export admitted network key")
			}
			if _, e := v.FixtureState(); e == nil {
				t.Fatal("fixture ledger exposed")
			}
			if e := v.SetFixturePolicy(FixturePolicy{Account: *first.Account}); e == nil {
				t.Fatal("fixture policy admitted")
			}
			backup, e := v.Backup(backupPassword)
			if e != nil {
				t.Fatal(e)
			}
			defer wipe(backup)
			if e = v.VerifyBackup(backup, backupPassword); e != nil {
				t.Fatal(e)
			}
			v.Close()
			if old, e := Open(dir, testSignerID); e == nil {
				old.Close()
				t.Fatal("ordinary fixture reader admitted managed state")
			}
			wrong := "devnet"
			if network == wrong {
				wrong = "mainnet-beta"
			}
			if wrongVault, e := openVaultNetwork(dir, testSignerID, false, wrong); e == nil {
				wrongVault.Close()
				t.Fatal("wrong network opened key")
			}
			v = managedFixture(t, dir, network)
			if s := v.Status(); !s.Locked || *s.Account != *first.Account {
				t.Fatal("restart changed identity or unlocked")
			}
			restored := managedFixture(t, fixtureDir(t), network)
			if e = restored.Restore(backup, backupPassword, fixturePassword); e != nil {
				t.Fatal(e)
			}
			after := restored.Status()
			if !after.Locked || !after.RecoveryRequired || after.Account.Epoch == first.Account.Epoch || !sameWallet(*after.Account, *first.Account) {
				t.Fatal("recovery identity drift")
			}
			if e = restored.Unlock(fixturePassword, time.Minute); e != nil {
				t.Fatal(e)
			}
			if e = restored.VerifyBackup(backup, backupPassword); e != nil {
				t.Fatal(e)
			}
			other := managedFixture(t, fixtureDir(t), wrong)
			if e = other.Restore(backup, backupPassword, fixturePassword); e == nil {
				t.Fatal("cross-network restore admitted")
			}
			for _, field := range []string{"network", "version", "public-key", "ciphertext"} {
				var doc envelope
				json.Unmarshal(backup, &doc)
				switch field {
				case "network":
					doc.Ref.Network = wrong
				case "version":
					doc.Version = 1
				case "public-key":
					doc.Ref.PublicKey = testSignerID + testSignerID
				case "ciphertext":
					doc.Ciphertext[0] ^= 1
				}
				bad, _ := json.Marshal(doc)
				if e = restored.VerifyBackup(bad, backupPassword); e == nil {
					t.Fatal("tampering accepted", field)
				}
			}
		})
	}
}
func TestManagedKeyPublicationAndRetainedHistory(t *testing.T) {
	for _, phase := range []string{"staged", "journal:managed-key-init:durable", "published"} {
		t.Run(phase, func(t *testing.T) {
			dir := fixtureDir(t)
			v := managedFixture(t, dir, "devnet")
			v.store.crash = func(p string) error {
				if p == phase {
					return errors.New("interrupted")
				}
				return nil
			}
			if e := v.Create(fixturePassword); e == nil {
				t.Fatal("interruption not injected")
			}
			pending, e := os.ReadFile(filepath.Join(dir, "vault.pending"))
			if e != nil {
				t.Fatal(e)
			}
			var original envelope
			if e = json.Unmarshal(pending, &original); e != nil {
				t.Fatal(e)
			}
			v.Close()
			if old, e := Open(dir, testSignerID); e == nil {
				old.Close()
				t.Fatal("ordinary reader repaired managed pending state")
			}
			unchanged, e := os.ReadFile(filepath.Join(dir, "vault.pending"))
			if e != nil || !bytes.Equal(pending, unchanged) {
				t.Fatal("pending original changed")
			}
			v = managedFixture(t, dir, "devnet")
			if s := v.Status(); s.Account == nil || *s.Account != original.Ref || !s.Locked {
				t.Fatal("interrupted identity changed")
			}
			v.Close()
			if e = os.Remove(filepath.Join(dir, "vault.json")); e != nil {
				t.Fatal(e)
			}
			v = managedFixture(t, dir, "devnet")
			if e = v.Create(fixturePassword); e == nil {
				t.Fatal("missing vault reset retained history")
			}
		})
	}
}
func TestManagedKeyAdmissionAndHistoryRefusal(t *testing.T) {
	fixture := openFixture(t, fixtureDir(t))
	if e := fixture.importManagedSeed(bytes.Repeat([]byte{9}, 32), fixturePassword); e == nil {
		t.Fatal("ordinary vault admitted managed import")
	}
	for _, network := range []string{"solana", "testnet", "devnet ", FixtureNetwork} {
		if v, e := openVaultNetwork(fixtureDir(t), testSignerID, false, network); e == nil {
			v.Close()
			t.Fatal("unknown managed network", network)
		}
	}
	v := managedFixture(t, fixtureDir(t), "devnet")
	if e := v.ImportFixtureSeed(bytes.Repeat([]byte{9}, 32), fixturePassword); e == nil {
		t.Fatal("fixture import entered managed vault")
	}
	if e := v.Create(fixturePassword); e != nil {
		t.Fatal(e)
	}
	ref := *v.Status().Account
	for _, r := range []journalRecord{
		{Kind: "init", Account: &ref, MissingHistory: true},
		{Kind: "managed-key-init", Account: &ref},
		{Kind: "managed-key-init", Account: &ref, MissingHistory: true, Grant: &FixtureGrant{}},
	} {
		if _, e := managedHistory([]journalRecord{r}); e == nil {
			t.Fatal("invalid history admitted")
		}
	}
}

func TestManagedOwnerDispatchAndPublicView(t *testing.T) {
	v := managedFixture(t, fixtureDir(t), "devnet")
	call := func(action string, owner bool, password, document []byte) Response {
		return execute(v, Request{Action: action, Password: bytes.Clone(password), Document: bytes.Clone(document), UnlockSeconds: 60}, owner)
	}
	seed := bytes.Repeat([]byte{4}, 32)
	defer wipe(seed)
	if r := call("import-managed-seed", false, fixturePassword, seed); r.Error == "" || v.Status().Account != nil {
		t.Fatal("app admitted key")
	}
	if r := call("import-managed-seed", true, fixturePassword, seed); r.Error != "" {
		t.Fatal(r.Error)
	}
	view := call("status", false, nil, nil).Status.PublicView()
	if view.PublicIdentity == nil || view.PublicIdentity.Version != 2 || view.PublicIdentity.Network != "devnet" || !view.PublicIdentity.FixtureOnly || view.WalletSigner || !view.Locked {
		t.Fatal("invalid read-only public projection")
	}
	for _, action := range []string{"fixture-state", "fixture-policy", "fixture-approve-proposal", "fixture-update-preflight", "fixture-recover-authority", "fixture-execute", "export-fixture-seed"} {
		if r := call(action, true, nil, nil); r.Error == "" {
			t.Fatal("fixture action admitted", action)
		}
	}
	if r := call("unlock", true, fixturePassword, nil); r.Error != "" {
		t.Fatal(r.Error)
	}
	backup := call("backup", true, backupPassword, nil)
	defer wipe(backup.Document)
	if backup.Error != "" {
		t.Fatal(backup.Error)
	}
	if r := call("verify-backup", true, backupPassword, backup.Document); r.Error != "" {
		t.Fatal(r.Error)
	}
	if r := call("lock", true, nil, nil); r.Error != "" || !r.Status.Locked {
		t.Fatal("owner lock failed")
	}
	ordinary := openFixture(t, fixtureDir(t))
	if r := execute(ordinary, Request{Action: "import-managed-seed", Password: bytes.Clone(fixturePassword), Document: bytes.Clone(seed)}, true); r.Error == "" {
		t.Fatal("ordinary mode admitted managed key")
	}
}

func TestManagedConfigBindsVersionAndNetwork(t *testing.T) {
	c := Config{Version: 2, Mode: ManagedFixtureMode, Network: "devnet", SignerID: testSignerID, OwnerUID: 1001, AppUID: 1002, SignerUID: 1003, StateDir: "/one", SocketDir: "/two"}
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*Config){func(c *Config) { c.Version = 1 }, func(c *Config) { c.Mode = FixtureNetwork }, func(c *Config) { c.Network = "" }, func(c *Config) { c.Network = "testnet" }, func(c *Config) { c.AppUID = c.OwnerUID }} {
		bad := c
		mutate(&bad)
		if bad.Validate() == nil {
			t.Fatal("mismatched network config admitted")
		}
	}
}

func TestManagedTransportVersionAndOwnerRole(t *testing.T) {
	v := managedFixture(t, fixtureDir(t), "devnet")
	c := Config{Version: 2, Mode: ManagedFixtureMode, Network: "devnet", SignerID: testSignerID, OwnerUID: os.Geteuid(), AppUID: os.Geteuid() + 1000, SignerUID: os.Geteuid(), StateDir: "unused", SocketDir: fixtureDir(t)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, e := serveListeners(ctx, c, v)
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close()
	conn, hello := protocolConnect(t, c, true)
	if hello.Version != 2 {
		t.Fatal("unversioned custody greeting")
	}
	if e = json.NewEncoder(conn).Encode(wireRequest{Version: 1, SignerID: c.SignerID, Challenge: hello.Challenge, Request: Request{Action: "create", Password: bytes.Clone(fixturePassword)}}); e != nil {
		t.Fatal(e)
	}
	var result Response
	if readWire(conn, &result) == nil || v.Status().Account != nil {
		t.Fatal("old wire protocol created custody key")
	}
	conn.Close()
	conn, hello = protocolConnect(t, c, true)
	if e = json.NewEncoder(conn).Encode(wireRequest{Version: 2, SignerID: c.SignerID, Challenge: hello.Challenge, Request: Request{Action: "create", Password: bytes.Clone(fixturePassword)}}); e != nil {
		t.Fatal(e)
	}
	if e = readWire(conn, &result); e != nil || result.Error != "" || result.Status.Account == nil || result.Status.Account.Version != 2 {
		t.Fatal("owner network create", result, e)
	}
	conn.Close()
	app, e := net.DialUnix("unix", nil, &net.UnixAddr{Name: socketPath(c, false), Net: "unix"})
	if e != nil {
		t.Fatal(e)
	}
	defer app.Close()
	app.SetDeadline(time.Now().Add(time.Second))
	if readWire(app, &hello) == nil {
		t.Fatal("wrong kernel role received app challenge")
	}
}
