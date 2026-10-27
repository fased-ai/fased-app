package core

import (
	"context"
	"encoding/json"
	"github.com/fased-ai/fased-app/internal/signer"
	"strings"
	"testing"
)

func TestManagedNetworkAccountRPCLifecycle(t *testing.T) {
	dir := fixtureDir(t)
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { s.Close() }()
	claimFixture(t, s)
	ref := testRef()
	ref.Version = 2
	ref.Network = "devnet"
	current := ref
	connect := func() {
		s.fixture = func(_ context.Context, r signer.Request) (signer.Response, error) {
			if r.Action != "status" {
				t.Fatal("unexpected signer action", r.Action)
			}
			return signer.Response{Status: signer.Status{FixtureOnly: true, Locked: true, RecoveryRequired: true, Account: &current}}, nil
		}
	}
	connect()
	r := defaultAccounts()
	a := publicRecord()
	a.Version = 3
	a.Mode = "fixture-managed"
	a.Network = ref.Network
	a.Signer = &ref
	r.Accounts = []Account{a}
	v, e := s.UpdateAccounts(t.Context(), r)
	if e != nil {
		t.Fatal(e)
	}
	if v.AccountStates[a.ID] != "current-fixture" || v.Fixture.Status.PublicIdentity.Version != 2 {
		t.Fatal("public view mismatch")
	}
	config := RPCConfigInput{AccountID: a.ID, Revision: v.Record.Revision, Endpoint: "https://devnet.helius-rpc.com", Network: "devnet"}
	if _, e = s.ConfigureAccountRPC(t.Context(), config); e == nil {
		t.Fatal("ordinary RPC admission opened")
	}
	s.rpcAdmission = true
	v, e = s.ConfigureAccountRPC(t.Context(), config)
	if e != nil {
		t.Fatal(e)
	}
	c := RPCCredentialCommand{AccountID: a.ID, Revision: v.Record.Revision, Action: "save", Key: disposableProviderKey, Passphrase: "Disposable managed RPC password"}
	saved, e := s.RPCCredential(t.Context(), c)
	if e != nil || !saved.Saved || saved.LiveRPC {
		t.Fatal(saved, e)
	}
	c.Key = ""
	c.Ref = saved.Ref
	c.Action = "verify"
	if _, e = s.RPCCredential(t.Context(), c); e != nil {
		t.Fatal(e)
	}
	c.Action = "check"
	called := false
	if _, e = s.rpcCredential(t.Context(), c, func(_ context.Context, _ RPCCheckRequest, _ []byte, valid func() error) (RPCCheckResult, error) {
		called = true
		if e := valid(); e != nil {
			return RPCCheckResult{}, e
		}
		current.Epoch = strings.Repeat("9", 32)
		return RPCCheckResult{}, nil
	}); e == nil || !called {
		t.Fatal("in-flight recovery did not invalidate RPC result")
	}
	c.Action = "verify"
	current.Epoch = strings.Repeat("9", 32)
	if _, e = s.RPCCredential(t.Context(), c); e == nil {
		t.Fatal("recovered epoch reused RPC credential")
	}
	config.Revision = v.Record.Revision
	if _, e = s.ConfigureAccountRPC(t.Context(), config); e == nil {
		t.Fatal("stale epoch configured RPC")
	}
	stale, e := s.Accounts(t.Context())
	if e != nil || stale.AccountStates[a.ID] != "stale" {
		t.Fatal(stale, e)
	}
	// Network references cannot become fixture mandates.
	invalid := v.Record
	invalid.Proposals = []ProposalDraft{{AccountID: a.ID, Proposal: testDraft(ref, strings.Repeat("b", 32))}}
	if _, e = s.UpdateAccounts(t.Context(), invalid); e == nil {
		t.Fatal("network key received fixture proposal")
	}
	current = ref
	before, _ := json.Marshal(v.Record)
	s.Close()
	s, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	after, e := s.Accounts(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(after.Record)
	if string(before) != string(raw) || after.AccountStates[a.ID] != "unverified" {
		t.Fatal("restart rewrote record or trusted missing signer")
	}
	connect()
	if _, e = s.RPCCredential(t.Context(), c); e == nil {
		t.Fatal("restart reopened RPC admission")
	}
	s.rpcAdmission = true
	if _, e = s.RPCCredential(t.Context(), c); e != nil {
		t.Fatal(e)
	}
}
func TestManagedKeyMigrationPreservesAndRejectsUnprotectedState(t *testing.T) {
	for _, variant := range []string{"preserve", "paused", "unprotected"} {
		t.Run(variant, func(t *testing.T) {
			dir := fixtureDir(t)
			s, e := OpenStore(dir)
			if e != nil {
				t.Fatal(e)
			}
			var before string
			s.db.QueryRow("SELECT document FROM accounts WHERE id=1").Scan(&before)
			if variant == "paused" {
				if _, e = s.db.Exec("UPDATE update_pause SET phase='paused' WHERE id=1"); e != nil {
					t.Fatal(e)
				}
			}
			if variant == "unprotected" {
				r := defaultAccounts()
				a := publicRecord()
				a.Version = 3
				r.Accounts = []Account{a}
				raw, _ := json.Marshal(r)
				if _, e = s.db.Exec("UPDATE accounts SET document=? WHERE id=1", string(raw)); e != nil {
					t.Fatal(e)
				}
			}
			if _, e = s.db.Exec("PRAGMA user_version=21"); e != nil {
				t.Fatal(e)
			}
			s.Close()
			s, e = OpenStore(dir)
			if variant != "preserve" {
				if e == nil {
					s.Close()
					t.Fatal("unsafe migration admitted")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			var after string
			var schema int
			s.db.QueryRow("SELECT document FROM accounts WHERE id=1").Scan(&after)
			s.db.QueryRow("PRAGMA user_version").Scan(&schema)
			if before != after || schema != 22 {
				t.Fatal("migration changed records or missed reader floor")
			}
		})
	}
}
