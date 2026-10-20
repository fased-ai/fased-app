package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/signer"
)

func TestAccountRegistryRequiresOwnerSession(t *testing.T) {
	s := fixtureStore(t)
	token := claimFixture(t, s)
	h, e := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	for _, authenticated := range []bool{false, true} {
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7331/api/v1/accounts", nil)
		if authenticated {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		expected := 401
		if authenticated {
			expected = 200
		}
		if w.Code != expected {
			t.Fatalf("authenticated=%v: got %d want %d", authenticated, w.Code, expected)
		}
	}
}

func testRef() signer.AccountRef {
	return signer.AccountRef{Version: 1, SignerID: strings.Repeat("1", 32), WalletID: strings.Repeat("2", 32), Epoch: strings.Repeat("3", 32), Network: signer.FixtureNetwork, PublicKey: strings.Repeat("4", 64)}
}
func publicRecord() Account {
	return Account{Version: 1, ID: strings.Repeat("a", 32), Name: "Disposable watch", Mode: "watch", Network: signer.FixtureNetwork, PublicKey: strings.Repeat("4", 64)}
}
func testDraft(ref signer.AccountRef, id string) signer.FixtureProposal {
	p := signer.FixturePolicy{Account: ref, Asset: signer.FixtureAsset, Budget: 100, MaxOperation: 10, Destinations: []string{"fixture:treasury"}, ExpiresAt: time.Now().Add(time.Hour).Unix()}
	g := signer.FixtureGrant{Account: ref, ID: id, BotID: strings.Repeat("b", 32), Asset: p.Asset, Budget: 50, MaxOperation: 10, Destinations: p.Destinations, ExpiresAt: p.ExpiresAt}
	return signer.FixtureProposal{Version: 1, ID: id, Policy: p, Grant: g}
}
func TestPublicAccountsRevisionRestartAndMigration(t *testing.T) {
	dir := fixtureDir(t)
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { s.Close() }()
	token := claimFixture(t, s)
	setup, e := s.Setup(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	setup.Settings.Host = "vps"
	if _, e = s.UpdateSetup(t.Context(), setup.Revision, setup.Settings); e != nil {
		t.Fatal(e)
	}
	// A typed disposable schema-1 DB with the exact preceding tables/data.
	if _, e = s.db.Exec("DROP TRIGGER update_pause_instance; DROP TABLE update_pause; DROP TABLE accounts; PRAGMA user_version=1;"); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	if !s.Session(t.Context(), token) {
		t.Fatal("migration revoked owner session")
	}
	after, e := s.Setup(t.Context())
	if e != nil || after.Settings.Host != "vps" || after.Revision != 1 {
		t.Fatal("migration changed setup", e)
	}
	v, e := s.Accounts(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	r := v.Record
	r.Accounts = append(r.Accounts, publicRecord())
	updated, e := s.UpdateAccounts(t.Context(), r)
	if e != nil || updated.Record.Revision != 1 {
		t.Fatal(e)
	}
	if _, e = s.UpdateAccounts(t.Context(), r); !errors.Is(e, ErrConflict) {
		t.Fatal("stale revision accepted", e)
	}
	s.Close()
	s, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	v, e = s.Accounts(t.Context())
	if e != nil || len(v.Record.Accounts) != 1 || v.AccountStates[r.Accounts[0].ID] != "public-record-only" || v.Fixture.Available {
		t.Fatal("restart/public-only capability", e)
	}
}
func TestAccountsBindLiveEpochAndNeverTrustAppApproval(t *testing.T) {
	s := fixtureStore(t)
	ref := testRef()
	calls := []string{}
	receipt := signer.ApprovalReceipt{ProposalID: strings.Repeat("c", 32), State: "not-approved"}
	s.fixture = func(_ context.Context, r signer.Request) (signer.Response, error) {
		calls = append(calls, r.Action)
		switch r.Action {
		case "status":
			return signer.Response{Status: signer.Status{FixtureOnly: true, Locked: true, Account: &ref}}, nil
		case "fixture-approvals":
			return signer.Response{Approvals: []signer.ApprovalReceipt{receipt}}, nil
		}
		t.Fatal("core attempted signer mutation")
		return signer.Response{}, nil
	}
	r := defaultAccounts()
	a := publicRecord()
	a.Mode = "fixture-managed"
	a.Signer = &ref
	r.Accounts = []Account{a}
	p := testDraft(ref, receipt.ProposalID)
	r.Proposals = []ProposalDraft{{AccountID: a.ID, Proposal: p}}
	v, e := s.UpdateAccounts(t.Context(), r)
	if e != nil {
		t.Fatal(e)
	}
	if v.Proposals[0].Receipt.State != "not-approved" {
		t.Fatal("draft conferred authority")
	}
	receipt.Digest = signer.ProposalDigest(p)
	receipt.State = "approved"
	v, e = s.Accounts(t.Context())
	if e != nil || v.Proposals[0].Receipt.State != "approved" {
		t.Fatal("verified signer receipt not shown", e)
	}
	receipt.Digest = strings.Repeat("f", 64)
	v, e = s.Accounts(t.Context())
	if e != nil || v.Proposals[0].Receipt.State != "mismatched" {
		t.Fatal("app proposal body mismatch hidden")
	}
	r = v.Record
	r.Proposals[0].Proposal.Grant.Budget++
	if _, e = s.UpdateAccounts(t.Context(), r); e == nil {
		t.Fatal("proposal identity mutated")
	}
	ref.Epoch = strings.Repeat("5", 32)
	v, e = s.Accounts(t.Context())
	if e != nil || v.AccountStates[a.ID] != "stale" {
		t.Fatal("stale account shown current")
	}
	r = v.Record
	fresh := testDraft(*r.Accounts[0].Signer, strings.Repeat("d", 32))
	r.Proposals = append(r.Proposals, ProposalDraft{AccountID: a.ID, Proposal: fresh})
	if _, e = s.UpdateAccounts(t.Context(), r); e == nil {
		t.Fatal("new proposal accepted against stale epoch")
	}
	for _, action := range calls {
		if action != "status" && action != "fixture-approvals" {
			t.Fatal(action)
		}
	}
}
func TestAccountsHTTPRejectsSecretsApprovalFlagsAndCSRF(t *testing.T) {
	s := fixtureStore(t)
	token := claimFixture(t, s)
	h, e := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	for _, body := range []string{`{"version":1,"revision":0,"accounts":[],"proposals":[],"approved":true}`, `{"version":1,"revision":0,"accounts":[],"proposals":[],"privateKey":"never accepted"}`} {
		r := httptest.NewRequest("PUT", "http://127.0.0.1:7331/api/v1/accounts", bytes.NewBufferString(body))
		r.Header.Set("Origin", "http://127.0.0.1:7331")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-CSRF-Token", csrfToken(token))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal("secret/asserted approval accepted", w.Code)
		}
	}
	b, _ := json.Marshal(defaultAccounts())
	r := httptest.NewRequest("PUT", "http://127.0.0.1:7331/api/v1/accounts", bytes.NewReader(b))
	r.Header.Set("Origin", "http://127.0.0.1:7331")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("CSRF bypass", w.Code)
	}
}
