package core

import (
	"errors"
	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/signer"
	"strings"
)

// Frozen production validator from main30dd562 (pre-recovery reader).
// Kept independent from the new validator to prove actual old-version refusal.
func preRecoveryStorageReader(d agentDocument) error {
	tasks := map[string]AgentTask{}
	for _, t := range d.Tasks {
		tag := strings.HasPrefix(t.ID, swapPrefix)
		if tag != (t.Swap != nil) {
			return errors.New("swap task policy erased")
		}
		if t.Swap == nil {
			continue
		}
		p := t.Swap
		q := p.Proposal.Quote
		if p.Version != swapTaskVersion || (p.CredentialLease != "" && (!validConnectionRef(p.CredentialLease) || p.Proposal.Version != signer.ManagedCredentialSwapVersion)) || (p.Proposal.Version == signer.ManagedCredentialSwapVersion && p.CredentialLease == "") || signer.ValidateSwapHistory(p.Proposal) != nil || p.Digest != swapPolicyDigest(*p) || p.Proposal.Digest != signer.SwapProposalDigest(p.Proposal) || !p.Proposal.FixtureOnly || onchain.ValidateQuote(q, 0) != nil || q.ProfileID != t.ProfileID || t.ID != swapPrefix+q.ID || t.MaxRuns != 1 || t.Runs != 1 || t.NextAt != 0 || t.AI != nil || t.News != nil || t.Calendar != nil || t.Watch != nil || t.WEN != nil || t.StrategyPlan != nil || t.BotID != "" || t.Playbook != nil || (t.State != "held" && t.State != "completed" && t.State != "paused") {
			return errors.New("swap task binding changed")
		}
		if _, ok := tasks[t.ID]; ok {
			return errors.New("duplicate swap task")
		}
		tasks[t.ID] = t
	}
	seen := map[string]bool{}
	for _, run := range d.Runs {
		tag := strings.HasPrefix(run.TaskID, swapPrefix)
		if tag != (run.Swap != nil) {
			return errors.New("swap result policy erased")
		}
		if run.Swap == nil {
			continue
		}
		t, ok := tasks[run.TaskID]
		x := run.Swap
		if !ok || seen[run.TaskID] || run.ID != run.TaskID+":1" || run.ProfileID != t.ProfileID || run.Strategy != swapTaskVersion || run.Outcome != "swap-"+x.State || x.Version != swapTaskVersion || x.ProposalDigest != t.Swap.Proposal.Digest || len(x.Events) < 1 || len(x.Events) > 16 || run.AIRequestID != "" || run.Paper != nil || run.WEN != nil || run.BotID != "" || run.News != nil || run.Watch != nil || run.Analysis != nil || run.Quantity != 0 || run.PriceCents != 0 || run.CostCents != 0 {
			return errors.New("swap result binding changed")
		}
		previous := ""
		at := int64(0)
		for i, event := range x.Events {
			if !validSwapState(event.State) || (i > 0 && !preRecoveryTransition(x.Events[i-1].State, event.State)) {
				return errors.New("invalid swap state transition")
			}
			hash := event.Digest
			event.Digest = ""
			if event.At < at || event.Previous != previous || hash != onchain.Hash(event) {
				return errors.New("swap event history changed")
			}
			at = event.At
			previous = hash
		}
		if !validSwapState(x.State) || x.Events[0].State != "queued" || x.Events[len(x.Events)-1].State != x.State {
			return errors.New("swap event state differs")
		}
		if x.State == "cancelled" && (len(x.Signed) != 0 || x.Signature != "" || x.Attempted || x.Observation != nil) {
			return errors.New("cancelled swap contains signed or attempted execution")
		}
		if len(x.Signed) > 0 {
			sig, e := onchain.VerifyPacket(t.Swap.Proposal.Quote, x.Signed)
			if e != nil || sig != x.Signature {
				return errors.New("swap signed original changed")
			}
		} else if x.Signature != "" || x.Attempted {
			return errors.New("swap attempt lacks signed original")
		}
		if x.State == "confirmed" || x.State == "failed" {
			if t.State != "completed" || x.Observation == nil || x.Observation.State != x.State || len(x.Signed) == 0 || x.Observation.Slot < t.Swap.Proposal.Quote.ObservedSlot {
				return errors.New("swap terminal receipt missing")
			}
			if x.State == "confirmed" {
				n, e := onchain.Uint(x.Observation.Output)
				minimum, _ := onchain.Uint(t.Swap.Proposal.Quote.MinimumOutput)
				if e != nil || n < minimum || x.Observation.Input != t.Swap.Proposal.Quote.Amount {
					return errors.New("settlement exceeds swap bounds")
				}
			}
		} else if x.Observation != nil {
			return errors.New("nonterminal settlement evidence")
		} else if t.State == "completed" {
			return errors.New("unconfirmed swap marked complete")
		}
		seen[run.TaskID] = true
	}
	for id := range tasks {
		if !seen[id] {
			return errors.New("swap outbox result missing")
		}
	}
	return nil
}

func preRecoveryTransition(a, b string) bool {
	switch a {
	case "queued":
		return b == "paused" || b == "validating" || b == "expired" || b == "cancelled"
	case "paused":
		return b == "queued" || b == "cancelled"
	case "validating":
		return b == "signing" || b == "simulation-failed" || b == "cancelled"
	case "signing":
		return b == "signed" || b == "unknown" || b == "cancelled"
	case "signed":
		return b == "sending" || b == "expired" || b == "unknown"
	case "sending":
		return b == "submitted" || b == "unknown" || b == "not-submitted"
	case "unknown":
		return b == "unknown" || b == "confirmed" || b == "failed" || b == "cancelled"
	case "submitted", "not-submitted", "expired":
		return b == "unknown" || b == "confirmed" || b == "failed"
	}
	return false
}
