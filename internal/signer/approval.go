package signer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// FixtureProposal is public, untrusted input until the actual owner approves it
// through the protected terminal helper. It cannot carry keys or live actions.
type FixtureProposal struct {
	Version int           `json:"version"`
	ID      string        `json:"id"`
	Policy  FixturePolicy `json:"policy"`
	Grant   FixtureGrant  `json:"grant"`
}
type ApprovalReceipt struct {
	ProposalID string     `json:"proposalId"`
	Digest     string     `json:"digest"`
	Account    AccountRef `json:"account"`
	ApprovedAt int64      `json:"approvedAt"`
	State      string     `json:"state"`
}

func ValidAccountRef(r AccountRef) bool { return validRef(r) }
func ProposalDigest(p FixtureProposal) string {
	b, _ := json.Marshal(p)
	h := sha256.Sum256(append([]byte("fased-fixture-proposal:v1\x00"), b...))
	return hex.EncodeToString(h[:])
}
func ParseProposal(b []byte) (FixtureProposal, error) {
	var p FixtureProposal
	e := decode(b, &p)
	return p, e
}
func (p FixtureProposal) Validate(at int64) error {
	g, w := p.Grant, p.Policy
	if p.Version != 1 || !validID(p.ID) || g.ID != p.ID || !validID(g.BotID) || !validRef(w.Account) || g.Account != w.Account || g.Revoked ||
		w.Asset != FixtureAsset || g.Asset != FixtureAsset || w.Budget == 0 || g.Budget == 0 || g.Budget > w.Budget || w.MaxOperation == 0 || g.MaxOperation == 0 || w.MaxOperation > w.Budget || g.MaxOperation > g.Budget || g.MaxOperation > w.MaxOperation ||
		!destinationsValid(w.Destinations) || !destinationsValid(g.Destinations) || !subset(g.Destinations, w.Destinations) || g.ExpiresAt <= at || g.ExpiresAt > w.ExpiresAt || w.ExpiresAt > at+86400 {
		return errors.New("invalid bounded fixture proposal; maximum lifetime is 24 hours")
	}
	return nil
}
func (v *Vault) ApproveProposal(p FixtureProposal) ([]ApprovalReceipt, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if e := v.fixtureReady(true); e != nil {
		return nil, e
	}
	if p.Policy.Account != v.record.Ref {
		return nil, errors.New("stale proposal account authority")
	}
	if old, ok := v.ledger.approvals[p.ID]; ok {
		if old.Digest != ProposalDigest(p) {
			return nil, errors.New("approved proposal identity cannot change")
		}
		return v.approvalReceipts([]string{p.ID}), nil
	}
	if e := v.ledger.append(v, journalRecord{Kind: "approval", Proposal: &p}); e != nil {
		return nil, e
	}
	return v.approvalReceipts([]string{p.ID}), nil
}
func (v *Vault) approvalReceipts(ids []string) []ApprovalReceipt {
	result := make([]ApprovalReceipt, 0, len(ids))
	for _, id := range ids {
		r, ok := v.ledger.approvals[id]
		if !ok {
			result = append(result, ApprovalReceipt{ProposalID: id, State: "not-approved"})
			continue
		}
		g := v.ledger.grants[id]
		r.State = "approved"
		switch {
		case v.record == nil || r.Account != v.record.Ref || v.recoveryRequired():
			r.State = "stale"
		case g.Revoked:
			r.State = "revoked"
		case g.ExpiresAt <= v.now().Unix():
			r.State = "expired"
		}
		result = append(result, r)
	}
	return result
}
func (v *Vault) ApprovalReceipts(ids []string) ([]ApprovalReceipt, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed || v.store.failedState() || v.ledger == nil {
		return nil, ErrUnavailable
	}
	if len(ids) > 16 {
		return nil, errors.New("too many proposal identities")
	}
	for _, id := range ids {
		if !validID(id) {
			return nil, errors.New("invalid proposal identity")
		}
	}
	return v.approvalReceipts(ids), nil
}
