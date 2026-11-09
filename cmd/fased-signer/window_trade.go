//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/fased-ai/fased-app/internal/signer"
)

// Trade completion is journal-owned. It never reuses the wallet creation draft,
// writes a second tracker, restores a key, or retries a recorded approval.
func conductTradeWindow(ctx context.Context, c signer.Config, in signer.OwnerInput, out io.Writer, call ownerCall) (result error) {
	p, err := in.Intent.TradeProposal()
	if err != nil {
		return err
	}
	if in.Validate() != nil || c.Version != 3 || c.Profile != signer.OwnerCustodyProfile || c.Provenance != signer.ControlledCustodyProfile || p.Account.SignerID != c.SignerID {
		return signer.ErrUnavailable
	}
	status, err := call(ctx, signer.Request{Action: "status"})
	if err != nil {
		return err
	}
	st := status.Status
	if st.Account == nil || *st.Account != p.Account || !st.FixtureOnly || st.FinancialExecution || st.WalletSigner || st.UpdateHeld || !st.AdmissionHeld || !st.RecoveryRequired || st.Custody == nil || !st.Custody.Valid() || st.Custody.Profile != signer.OwnerCustodyProfile || st.Custody.Provenance != signer.ControlledCustodyProfile {
		return errors.New("trade wallet changed or unavailable")
	}
	previous, receiptErr := call(ctx, signer.Request{Action: "swap-receipt", PublicInput: signer.PublicInput{OperationID: p.Quote.ID}})
	var receipt *signer.SwapReceipt
	if receiptErr == nil {
		receipt = previous.Swap
		if receipt == nil || receipt.Proposal.Digest != p.Digest || receipt.Proposal.Account != p.Account {
			return errors.New("different original trade receipt")
		}
	}
	if receiptErr != nil && receiptErr.Error() != "swap approval required" {
		return receiptErr
	}
	attempted := false
	defer func() {
		if result != nil && attempted {
			lockCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			_, lockErr := call(lockCtx, signer.Request{Action: "lock"})
			result = errors.Join(result, lockErr)
		}
	}()
	readOnly := true
	switch in.Phase {
	case "inspect":
	case "cancel":
		if receipt != nil && receipt.State == "approved" && len(receipt.Signed) == 0 {
			cancelled, e := call(ctx, signer.Request{Action: "swap-cancel", PublicInput: signer.PublicInput{OperationID: p.Quote.ID}})
			if e != nil {
				return e
			}
			receipt = cancelled.Swap
		} else if receipt != nil && receipt.State != "cancelled" {
			return errors.New("original trade already dispatched; reconcile in Onchain")
		}
	case "approve":
		if receipt == nil {
			if time.Now().Unix() >= p.Quote.ExpiresAt {
				return errors.New("trade review expired")
			}
			ask := func(string, bool) ([]byte, error) { return bytes.Clone(in.Unlock), nil }
			confirm := func(value string) error {
				if value != "APPROVE "+in.Confirmation {
					return errors.New("trade confirmation changed")
				}
				return nil
			}
			attempted = true
			if err = conductSwapApproval(ctx, p, ask, confirm, io.Discard, io.Discard, call); err != nil {
				return err
			}
			approved, e := call(ctx, signer.Request{Action: "swap-receipt", PublicInput: signer.PublicInput{OperationID: p.Quote.ID}})
			if e != nil {
				return e
			}
			receipt = approved.Swap
			readOnly = false
		}
	default:
		return signer.ErrUnavailable
	}
	now, err := call(ctx, signer.Request{Action: "status"})
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(signer.OwnerResult{Intent: in.Intent, SignerID: c.SignerID, Status: now.Status.PublicView(), Swap: receipt, ReadOnly: readOnly})
}
