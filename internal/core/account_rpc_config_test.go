package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestRPCConfigPersistenceAndExplicitMutation(t *testing.T) {
	dir := fixtureDir(t)
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	claimFixture(t, s)
	r := defaultAccounts()
	r.Accounts = []Account{publicRecord()}
	v, err := s.UpdateAccounts(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	id := v.Record.Accounts[0].ID
	in := RPCConfigInput{AccountID: id, Revision: v.Record.Revision, Endpoint: FixtureRPCURL, Network: signer.FixtureNetwork}
	for _, endpoint := range []string{"https://secret@rpc.fased.invalid/solana", FixtureRPCURL + "?key=secret", "https://example.com"} {
		bad := in
		bad.Endpoint = endpoint
		if _, err = s.ConfigureAccountRPC(t.Context(), bad); err == nil {
			t.Fatal("nonfixture URL persisted")
		}
	}
	v, err = s.ConfigureAccountRPC(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if v.Record.Accounts[0].RPC == nil {
		t.Fatal("not saved")
	}
	if _, err = s.ConfigureAccountRPC(t.Context(), in); !errors.Is(err, ErrConflict) {
		t.Fatal("stale save accepted", err)
	}
	// An older client omitting the optional field must not erase configuration.
	edit := v.Record
	edit.Accounts[0].RPC = nil
	edit.Accounts[0].Name = "Renamed"
	v, err = s.UpdateAccounts(t.Context(), edit)
	if err != nil || v.Record.Accounts[0].RPC == nil {
		t.Fatal("rename erased config", err)
	}
	before := v.Record
	s.Close()
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.accountsRecord(t.Context())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("restart changed records", err)
	}
	result, err := s.CheckAccountRPC(t.Context(), RPCCheckRequest{AccountID: id, AccountRevision: &after.Revision})
	if err != nil || result.Endpoint != FixtureRPCURL || result.Account.RPC == nil {
		t.Fatal("saved check failed", err)
	}
	c := pauseCommand(t, s, 1)
	if _, err = s.UpdatePause(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConfigureAccountRPC(t.Context(), RPCConfigInput{AccountID: id, Revision: after.Revision, Remove: true}); !errors.Is(err, ErrCorePaused) {
		t.Fatal("pause bypass", err)
	}
	if _, err = s.UpdatePause(t.Context(), update.CorePauseCommand{Action: "release", ProbeID: c.ProbeID, Aborted: aborted(c)}); err != nil {
		t.Fatal(err)
	}
	v, err = s.ConfigureAccountRPC(t.Context(), RPCConfigInput{AccountID: id, Revision: after.Revision, Remove: true})
	if err != nil || v.Record.Accounts[0].RPC != nil {
		t.Fatal("remove failed", err)
	}
	if _, err = s.CheckAccountRPC(t.Context(), RPCCheckRequest{AccountID: id, AccountRevision: &v.Record.Revision}); err == nil {
		t.Fatal("removed config reused")
	}
}

func TestRPCConfigManagedIdentityAndClear(t *testing.T) {
	s := fixtureStore(t)
	claimFixture(t, s)
	ref := testRef()
	offline := false
	s.fixture = func(_ context.Context, r signer.Request) (signer.Response, error) {
		if r.Action != "status" {
			t.Fatal("signer mutation")
		}
		if offline {
			return signer.Response{}, errors.New("offline")
		}
		return signer.Response{Status: signer.Status{Account: &ref, FixtureOnly: true, Locked: true, RecoveryRequired: true}}, nil
	}
	a := publicRecord()
	a.Mode = "fixture-managed"
	a.Signer = &ref
	a.PublicKey = ref.PublicKey
	r := defaultAccounts()
	r.Accounts = []Account{a}
	v, err := s.UpdateAccounts(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	v, err = s.ConfigureAccountRPC(t.Context(), RPCConfigInput{AccountID: a.ID, Revision: v.Record.Revision, Endpoint: FixtureRPCURL, Network: ref.Network})
	if err != nil {
		t.Fatal(err)
	}
	offline = true
	if _, err = s.ConfigureAccountRPC(t.Context(), RPCConfigInput{AccountID: a.ID, Revision: v.Record.Revision, Endpoint: FixtureRPCURL, Network: ref.Network}); err == nil {
		t.Fatal("offline save accepted")
	}
	cleared, err := s.ConfigureAccountRPC(t.Context(), RPCConfigInput{AccountID: a.ID, Revision: v.Record.Revision, Remove: true})
	if err != nil || cleared.Record.Accounts[0].RPC != nil {
		t.Fatal("offline removal refused", err)
	}
	offline = false
	v, err = s.ConfigureAccountRPC(t.Context(), RPCConfigInput{AccountID: a.ID, Revision: cleared.Record.Revision, Endpoint: FixtureRPCURL, Network: ref.Network})
	if err != nil {
		t.Fatal(err)
	}
	ref.Epoch = strings.Repeat("8", 32)
	r = v.Record
	r.Accounts[0].Signer = &ref
	v, err = s.UpdateAccounts(t.Context(), r)
	if err != nil || v.Record.Accounts[0].RPC != nil {
		t.Fatal("rebind carried RPC", err)
	}
	r = v.Record
	r.Accounts[0].RPC = &AccountRPC{1, FixtureRPCURL, ref.Network}
	if _, err = s.UpdateAccounts(t.Context(), r); err == nil {
		t.Fatal("generic update bypassed RPC command")
	}
}

func TestRPCSchemaMigrationPreservesAndRefuses(t *testing.T) {
	for _, kind := range []string{"normal", "paused", "unprotected"} {
		t.Run(kind, func(t *testing.T) {
			dir := fixtureDir(t)
			s, err := OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			claimFixture(t, s)
			r := defaultAccounts()
			r.Accounts = []Account{publicRecord()}
			v, err := s.UpdateAccounts(t.Context(), r)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "paused" {
				if _, err = s.UpdatePause(t.Context(), pauseCommand(t, s, 1)); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "unprotected" {
				v.Record.Accounts[0].RPC = &AccountRPC{1, FixtureRPCURL, signer.FixtureNetwork}
				b, _ := json.Marshal(v.Record)
				if _, err = s.db.Exec("UPDATE accounts SET document=? WHERE id=1", string(b)); err != nil {
					t.Fatal(err)
				}
			}
			var raw string
			s.db.QueryRow("SELECT document FROM accounts WHERE id=1").Scan(&raw)
			if _, err = s.db.Exec("PRAGMA user_version=19"); err != nil {
				t.Fatal(err)
			}
			s.Close()
			n, err := OpenStore(dir)
			if kind != "normal" {
				if err == nil {
					n.Close()
					t.Fatal("unsafe migration accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer n.Close()
			var after string
			var schema int
			n.db.QueryRow("SELECT document FROM accounts WHERE id=1").Scan(&after)
			n.db.QueryRow("PRAGMA user_version").Scan(&schema)
			if raw != after || schema != 20 {
				t.Fatal("migration rewrote accounts or wrong schema")
			}
			record, _ := n.accountsRecord(t.Context())
			if record.Revision != v.Record.Revision {
				t.Fatal("migration changed revision")
			}
		})
	}
}

func TestRPCConfigBackup(t *testing.T) {
	s := fixtureStore(t)
	claimFixture(t, s)
	r := defaultAccounts()
	r.Accounts = []Account{publicRecord()}
	v, err := s.UpdateAccounts(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	v, err = s.ConfigureAccountRPC(t.Context(), RPCConfigInput{AccountID: r.Accounts[0].ID, Revision: v.Record.Revision, Endpoint: FixtureRPCURL, Network: signer.FixtureNetwork})
	if err != nil {
		t.Fatal(err)
	}
	p := pausedBackup(t, s)
	if _, err = s.BackupMetadata(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(s.dir, "lifecycle", p.IntentID, "metadata.sqlite")+"?mode=ro&immutable=1")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var raw string
	var rev int64
	var schema int
	if err = db.QueryRow("SELECT document,revision FROM accounts WHERE id=1").Scan(&raw, &rev); err != nil {
		t.Fatal(err)
	}
	db.QueryRow("PRAGMA user_version").Scan(&schema)
	var record AccountsRecord
	if json.Unmarshal([]byte(raw), &record) != nil || schema != 20 || rev != v.Record.Revision || record.Accounts[0].RPC.Endpoint != FixtureRPCURL {
		t.Fatal("backup lost RPC or reader floor")
	}
}
