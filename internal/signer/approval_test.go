//go:build linux

package signer

import (
	"errors"
	"os"
	"testing"
	"time"
)

func proposalFor(p FixturePolicy, g FixtureGrant) FixtureProposal {
	return FixtureProposal{Version: 1, ID: g.ID, Policy: p, Grant: g}
}
func TestProposalApprovalIsOwnerOnlyAtomicAndReplaySafe(t *testing.T) {
	c, v := protocolFixture(t, os.Geteuid(), os.Geteuid())
	if e := v.Create(fixturePassword); e != nil {
		t.Fatal(e)
	}
	ref := *v.Status().Account
	p := FixturePolicy{Account: ref, Asset: FixtureAsset, Budget: 100, MaxOperation: 50, Destinations: []string{"fixture:treasury"}, ExpiresAt: time.Now().Add(time.Hour).Unix()}
	g := FixtureGrant{Account: ref, ID: randomID(), BotID: randomID(), Asset: p.Asset, Budget: 50, MaxOperation: 50, Destinations: p.Destinations, ExpiresAt: p.ExpiresAt}
	proposal := proposalFor(p, g)
	r := protocolRequest(t, c, false, Request{Action: "fixture-approve-proposal", PublicInput: PublicInput{Proposal: &proposal}})
	if r.Error != "owner administration denied" {
		t.Fatal("app approval", r.Error)
	}
	if _, e := v.ApproveProposal(proposal); !errors.Is(e, ErrLocked) {
		t.Fatal("locked approval", e)
	}
	if e := v.Unlock(fixturePassword, time.Minute); e != nil {
		t.Fatal(e)
	}
	bad := proposal
	bad.Grant.Budget = 101
	if _, e := v.ApproveProposal(bad); e == nil {
		t.Fatal("bad approval")
	}
	if v.ledger.policy != nil || len(v.ledger.grants) != 0 {
		t.Fatal("partial policy/grant committed")
	}
	r = protocolRequest(t, c, true, Request{Action: "fixture-approve-proposal", PublicInput: PublicInput{Proposal: &proposal}})
	if r.Error != "" || r.Approvals[0].State != "approved" {
		t.Fatal(r.Error)
	}
	seq := v.ledger.main.seq
	if _, e := v.ApproveProposal(proposal); e != nil {
		t.Fatal(e)
	}
	if v.ledger.main.seq != seq {
		t.Fatal("replay wrote new authority")
	}
	bad = proposal
	bad.Policy.Budget++
	if _, e := v.ApproveProposal(bad); e == nil {
		t.Fatal("approval body changed")
	}
	if e := v.RevokeFixtureGrant(ref, g.ID); e != nil {
		t.Fatal(e)
	}
	r = protocolRequest(t, c, false, Request{Action: "fixture-approvals", PublicInput: PublicInput{ProposalIDs: []string{proposal.ID}}})
	if r.Error != "" || r.Approvals[0].State != "revoked" {
		t.Fatal("revocation receipt")
	}
	if result, e := v.ApproveProposal(proposal); e != nil || result[0].State != "revoked" {
		t.Fatal("approval retry reactivated grant", e)
	}
}
func TestApprovalJournalInterruptionAndExpiry(t *testing.T) {
	dir := fixtureDir(t)
	v, p, g := configuredFixture(t, dir)
	g.ID = randomID()
	proposal := proposalFor(p, g)
	v.store.crash = func(s string) error {
		if s == "journal:approval:durable" {
			return errors.New("disposable approval interruption")
		}
		return nil
	}
	if _, e := v.ApproveProposal(proposal); e == nil {
		t.Fatal("missing interruption")
	}
	v.Close()
	v = openFixture(t, dir)
	if !v.Status().Locked {
		t.Fatal("restart unlocked")
	}
	r, e := v.ApprovalReceipts([]string{g.ID})
	if e != nil || r[0].Digest != ProposalDigest(proposal) || r[0].State != "approved" {
		t.Fatal("durable approval lost", e)
	}
	v.now = func() time.Time { return time.Unix(g.ExpiresAt, 0) }
	r, e = v.ApprovalReceipts([]string{g.ID})
	if e != nil || r[0].State != "expired" {
		t.Fatal("expiry not reflected")
	}
}

func TestApprovalGrantConflictDoesNotPartiallyChangePolicy(t *testing.T) {
	v, p, g := configuredFixture(t, fixtureDir(t))
	p.Budget = 101
	if _, e := v.ApproveProposal(proposalFor(p, g)); e == nil {
		t.Fatal("existing grant identity accepted")
	}
	s, e := v.FixtureState()
	if e != nil || s.Policy.Budget != 100 {
		t.Fatal("failed approval partially changed wallet policy", e)
	}
}
