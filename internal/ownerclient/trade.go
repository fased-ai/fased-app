package ownerclient

import (
	"errors"
	"github.com/fased-ai/fased-app/internal/signer"
	"time"
)

func (c *Client) validateTradeResponse(raw []byte, in signer.OwnerInput) (signer.OwnerResult, error) {
	var r signer.OwnerResult
	p, err := in.Intent.TradeProposal()
	if err != nil || decode(raw, &r) != nil || r.Intent != c.Intent || r.SignerID != c.Peer.SignerID || r.Status.Account == nil || *r.Status.Account != p.Account || !r.Status.FixtureOnly || !r.Status.AdmissionHeld || !r.Status.RecoveryRequired || r.Status.WalletSigner || r.Status.FinancialExecution || r.Status.UpdateHeld || r.Status.Custody == nil || !r.Status.Custody.Valid() || r.Status.Custody.Profile != signer.OwnerCustodyProfile || r.Status.Custody.Provenance != signer.ControlledCustodyProfile || len(r.Backup) != 0 || r.BackupVerified || r.Fingerprint != "" {
		return r, errors.New("invalid protected trade completion")
	}
	if r.Swap != nil && (signer.ValidateSwapHistory(r.Swap.Proposal) != nil || r.Swap.Proposal.Digest != p.Digest || r.Swap.Proposal.Account != p.Account) {
		return r, errors.New("trade receipt changed")
	}
	if in.Phase != "approve" && !r.ReadOnly {
		return r, errors.New("inspection changed trade authority")
	}
	if !r.ReadOnly && (!r.Status.SwapReady || r.Swap == nil || r.Swap.State != "approved" || r.Swap.ApprovalAvailable == nil || !*r.Swap.ApprovalAvailable || r.Status.Locked || time.Now().Unix() >= p.Quote.ExpiresAt) {
		return r, errors.New("trade approval is unavailable or expired")
	}
	return r, nil
}
