package signer

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/fased-ai/fased-app/internal/update"
)

// One public exact proposal, not a reusable signing permission or executable.
func NewTradeOwnerIntent(p SwapProposal) (OwnerIntent, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return OwnerIntent{}, err
	}
	i := OwnerIntent{Version: 2, InstanceID: p.Quote.InstanceID, OperationID: p.Quote.ID, Action: "approve-swap", Network: p.Quote.Network, Proposal: base64.RawURLEncoding.EncodeToString(raw)}
	return i, i.Validate()
}
func (i OwnerIntent) TradeProposal() (SwapProposal, error) {
	var p SwapProposal
	instance, instanceErr := base64.RawURLEncoding.DecodeString(i.InstanceID)
	if instanceErr != nil || len(instance) != 32 || base64.RawURLEncoding.EncodeToString(instance) != i.InstanceID || !validID(i.OperationID) {
		return p, errors.New("invalid trade identity")
	}
	if i.Version != 2 || i.Action != "approve-swap" || len(i.Proposal) == 0 || len(i.Proposal) > 12*1024 {
		return p, errors.New("invalid trade intent")
	}
	raw, err := base64.RawURLEncoding.DecodeString(i.Proposal)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != i.Proposal {
		return p, errors.New("invalid trade encoding")
	}
	if err = update.StrictJSONBounded(raw, &p, 12*1024); err != nil {
		return p, err
	}
	if err = ValidateSwapHistory(p); err != nil {
		return p, err
	}
	canonical, err := json.Marshal(p)
	if err != nil || !bytes.Equal(canonical, raw) || p.Version != OrdinarySwapVersion || p.Account.Version != 3 || i.InstanceID != p.Quote.InstanceID || i.Network != p.Quote.Network || i.OperationID != p.Quote.ID {
		return p, errors.New("trade intent does not match the exact ordinary proposal")
	}
	return p, nil
}
func (i OwnerInput) validateTradeInput() error {
	p, err := i.Intent.TradeProposal()
	if err != nil {
		return err
	}
	if i.SignerID != p.Account.SignerID || len(i.BackupPassword) != 0 || len(i.Seed) != 0 || len(i.Backup) != 0 || i.Fingerprint != "" {
		return errors.New("trade input accepts no wallet creation or recovery fields")
	}
	switch i.Phase {
	case "inspect", "cancel":
		if len(i.Unlock) != 0 || i.Confirmation != "" {
			return errors.New("trade inspection/cancellation accepts no credential")
		}
	case "approve":
		if len(i.Unlock) < 16 || len(i.Unlock) > 1024 || i.Confirmation != p.Digest {
			return errors.New("exact owner confirmation and unlock required")
		}
	default:
		return errors.New("invalid trade phase")
	}
	return nil
}
