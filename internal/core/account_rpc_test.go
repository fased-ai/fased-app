package core

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/signer"
)

func TestAccountRPCBindings(t *testing.T) {
	for _, kind := range []string{"current", "held", "watch", "stale", "unavailable", "missing", "revision", "network", "changed-during-check", "epoch-during-check"} {
		t.Run(kind, func(t *testing.T) {
			s := fixtureStore(t)
			claimFixture(t, s)
			ref := testRef()
			unavailable := false
			s.fixture = func(_ context.Context, r signer.Request) (signer.Response, error) {
				if r.Action != "status" {
					t.Fatal("unexpected signer action", r.Action)
				}
				if unavailable {
					return signer.Response{}, errors.New("offline")
				}
				return signer.Response{Status: signer.Status{Account: &ref, FixtureOnly: true, Locked: true, RecoveryRequired: kind == "held"}}, nil
			}
			record := defaultAccounts()
			a := publicRecord()
			a.Mode = "fixture-managed"
			a.Signer = &ref
			a.PublicKey = ref.PublicKey
			if kind == "watch" {
				a.Mode = "watch"
				a.Signer = nil
			}
			record.Accounts = []Account{a}
			view, err := s.UpdateAccounts(t.Context(), record)
			if err != nil {
				t.Fatal(err)
			}
			before := view.Record
			rev := before.Revision
			input := rpcInput()
			input.AccountID = a.ID
			input.AccountRevision = &rev
			switch kind {
			case "stale":
				ref.Epoch = strings.Repeat("8", 32)
			case "unavailable":
				unavailable = true
			case "missing":
				input.AccountID = strings.Repeat("9", 32)
			case "revision":
				rev++
			case "network":
				input.Network = "devnet"
			}
			calls := 0
			result, err := s.checkAccountRPC(t.Context(), input, func(ctx context.Context, r RPCCheckRequest) (RPCCheckResult, error) {
				calls++
				if kind == "changed-during-check" {
					changed := before
					changed.Accounts = nil
					if _, e := s.UpdateAccounts(ctx, changed); e != nil {
						t.Fatal(e)
					}
				}
				if kind == "epoch-during-check" {
					ref.Epoch = strings.Repeat("8", 32)
				}
				return CheckFixtureRPC(ctx, r)
			})
			good := kind == "current" || kind == "held" || kind == "watch"
			if good {
				if err != nil || result.Account == nil || result.Account.ID != a.ID || *result.AccountRevision != before.Revision || result.LiveRPC || !result.FixtureOnly {
					t.Fatal("invalid bound result", result, err)
				}
				state := "current-fixture"
				if kind == "watch" {
					state = "public-record-only"
				}
				if result.AccountState != state {
					t.Fatal("wrong state")
				}
			} else {
				if err == nil || result.Account != nil || !result.CheckedAt.IsZero() {
					t.Fatal("invalid binding accepted", result, err)
				}
				during := strings.Contains(kind, "during-check")
				if !during && calls != 0 {
					t.Fatal("invalid identity dispatched")
				}
			}
			if kind != "changed-during-check" {
				after, e := s.accountsRecord(t.Context())
				if e != nil || !reflect.DeepEqual(before, after) {
					t.Fatal("check changed account record", e)
				}
			}
		})
	}
}

func TestAccountRPCPartialBindingAndRestart(t *testing.T) {
	dir := fixtureDir(t)
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	claimFixture(t, s)
	record := defaultAccounts()
	record.Accounts = []Account{publicRecord()}
	view, err := s.UpdateAccounts(t.Context(), record)
	if err != nil {
		t.Fatal(err)
	}
	input := rpcInput()
	input.AccountID = record.Accounts[0].ID
	if _, err = s.CheckAccountRPC(t.Context(), input); err == nil {
		t.Fatal("partial binding accepted")
	}
	input.AccountRevision = &view.Record.Revision
	s.Close()
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.CheckAccountRPC(t.Context(), input)
	if err != nil || result.Account == nil || result.Account.ID != input.AccountID {
		t.Fatal("restart lost account binding", err)
	}
}
