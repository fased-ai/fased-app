package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/signer"
)

func TestSwapRecoveryExpiredUnsignedCancellation(t *testing.T) {
	h := newSwapHarness(t)
	p := h.quote(t)
	h.submit(t, p)
	h.store.now = func() time.Time { return time.Unix(p.Proposal.Quote.ExpiresAt+1, 0) }
	if e := h.store.advanceOnchain(t.Context()); e != nil {
		t.Fatal(e)
	}
	v, e := h.store.Agents(t.Context(), h.profile, "")
	if e != nil {
		t.Fatal(e)
	}
	next, e := h.store.ChangeAgents(t.Context(), AgentCommand{Action: "swap-cancel", ProfileID: h.profile, Revision: v.Revision, Swap: &SwapCommand{ID: v.Onchain.Tasks[0].ID}})
	if e != nil {
		t.Fatal("expired unsigned approval has no cancellation path", e)
	}
	if next.Onchain.Runs[0].Swap.State != "cancelled" || next.Onchain.Tasks[0].State != "completed" || h.chain.Submissions != 0 {
		t.Fatal("unsigned cancellation not resolved", next.Onchain)
	}
}

func TestSwapRecoveryUnsignedRestartAndSimulationFailure(t *testing.T) {
	for _, mode := range []string{"restart", "simulation"} {
		t.Run(mode, func(t *testing.T) {
			h := newSwapHarness(t)
			p := h.quote(t)
			h.submit(t, p)
			if mode == "restart" {
				dir := h.store.dir
				h.store.Close()
				var e error
				h.store, e = OpenStore(dir)
				if e != nil {
					t.Fatal(e)
				}
				t.Cleanup(func() { h.store.Close() })
				h.connect()
			} else {
				h.chain.SimulationFails = true
				if e := h.store.advanceOnchain(t.Context()); e != nil {
					t.Fatal(e)
				}
			}
			v := h.command(t, "swap-view", SwapCommand{})
			expected := "expired"
			if mode == "simulation" {
				expected = "simulation-failed"
			}
			if v.Runs[0].Swap.State != expected || !v.Recovery[0].Cancel || h.chain.Submissions != 0 {
				t.Fatal("unsigned recovery unavailable", v)
			}
			proof, e := h.vault.SwapReceipt(p.Proposal.Quote.ID)
			if e != nil || proof.State != "approved" {
				t.Fatal("core falsely cancelled signer approval", proof, e)
			}
			v = h.command(t, "swap-cancel", SwapCommand{ID: v.Tasks[0].ID})
			proof, e = h.vault.SwapReceipt(p.Proposal.Quote.ID)
			if e != nil || proof.State != "cancelled" || v.Tasks[0].State != "completed" || v.Recovery[0].Cancel || v.Recovery[0].Reconcile {
				t.Fatal("not independently cancelled", v, e)
			}
		})
	}
}

func TestSwapRecoverySignedExpiryAndMissingHistoryRetainOriginal(t *testing.T) {
	h := newSwapHarness(t)
	p := h.quote(t)
	h.submit(t, p)
	peer := h.store.onchain.signer
	h.store.onchain.signer = func(ctx context.Context, r signer.Request) (signer.Response, error) {
		out, e := peer(ctx, r)
		if r.Action == "swap-sign" && e == nil {
			h.store.now = func() time.Time { return time.Unix(p.Proposal.Quote.ExpiresAt+1, 0) }
		}
		return out, e
	}
	if e := h.store.advanceOnchain(t.Context()); e != nil {
		t.Fatal(e)
	}
	v := h.command(t, "swap-view", SwapCommand{})
	original := append([]byte(nil), v.Runs[0].Swap.Signed...)
	if v.Runs[0].Swap.State != "expired" || len(original) == 0 || v.Recovery[0].Status != "awaiting-receipt" || v.Recovery[0].Cancel || !v.Recovery[0].Reconcile {
		t.Fatal("signed expiry misrepresented", v)
	}
	_, e := h.store.ChangeAgents(t.Context(), AgentCommand{Action: "swap-cancel", ProfileID: h.profile, Revision: v.Revision, Swap: &SwapCommand{ID: v.Tasks[0].ID}})
	if e == nil {
		t.Fatal("signed original cancellable")
	}
	h.chain.Unknown = true
	v = h.command(t, "swap-reconcile", SwapCommand{ID: v.Tasks[0].ID})
	if v.Runs[0].Swap.State != "unknown" || !bytes.Equal(original, v.Runs[0].Swap.Signed) || v.Tasks[0].State != "held" || h.chain.Submissions != 0 {
		t.Fatal("absence discarded signed original", v)
	}
	if e = h.store.advanceOnchain(t.Context()); e != nil || h.chain.Submissions != 0 {
		t.Fatal("expired signed original dispatched", e)
	}
	saveSwapEvidence(t, "signed-expiry-held", v)
}

func TestSwapRecoveryHTTPSLockedRestartRestoresIndependentReceipt(t *testing.T) {
	for _, state := range []string{"confirmed", "failed"} {
		t.Run(state, func(t *testing.T) {
			h, tls, _ := setupCredentialSwapHarness(t, fixtureStore(t), true)
			p := h.quote(t)
			h.approveCredential(t, p)
			h.chain.Unknown = true
			if e := h.store.advanceOnchain(t.Context()); e != nil {
				t.Fatal(e)
			}
			v := h.command(t, "swap-view", SwapCommand{})
			original := append([]byte(nil), v.Runs[0].Swap.Signed...)
			cipher, e := os.ReadFile(h.store.venuePath(p.Proposal.Quote.AccountID))
			if e != nil {
				t.Fatal(e)
			}
			h.chain.Unknown = false
			if state == "failed" {
				h.chain.SettlementMode = "failed"
			}
			proof, e := h.vault.ReconcileSwap(t.Context(), p.Proposal.Quote.ID)
			if e != nil || proof.State != state {
				t.Fatal(proof, e)
			}
			h.vault.Lock()
			dir := h.store.dir
			h.store.Close()
			h.store, e = OpenStore(dir)
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { h.store.Close() })
			h.connectCredential(t, tls)
			reads := len(tls.Requests())
			v = h.command(t, "swap-reconcile", SwapCommand{ID: v.Tasks[0].ID})
			after, e := os.ReadFile(h.store.venuePath(p.Proposal.Quote.AccountID))
			if e != nil || !bytes.Equal(cipher, after) || v.Runs[0].Swap.State != state || !bytes.Equal(original, v.Runs[0].Swap.Signed) || len(tls.Requests()) != reads || h.chain.Submissions != 1 || !h.vault.Status().RecoveryRequired {
				t.Fatal("locked recovery changed custody/original or read RPC", v, e)
			}
			saveSwapEvidence(t, "https-locked-recovery-"+state, v)
		})
	}
}

func TestSwapRecoveryRejectsConflictingStoredProof(t *testing.T) {
	for _, mode := range []string{"proposal", "packet", "observation", "wallet"} {
		t.Run(mode, func(t *testing.T) {
			h := newSwapHarness(t)
			p := h.quote(t)
			h.submit(t, p)
			h.chain.Unknown = true
			if e := h.store.advanceOnchain(t.Context()); e != nil {
				t.Fatal(e)
			}
			h.chain.Unknown = false
			if _, e := h.vault.ReconcileSwap(t.Context(), p.Proposal.Quote.ID); e != nil {
				t.Fatal(e)
			}
			v := h.command(t, "swap-view", SwapCommand{})
			before, _ := json.Marshal(v)
			peer := h.store.onchain.signer
			h.store.onchain.signer = func(ctx context.Context, r signer.Request) (signer.Response, error) {
				out, e := peer(ctx, r)
				if r.Action == "swap-receipt" && out.Swap != nil {
					b, _ := json.Marshal(out.Swap)
					var copy signer.SwapReceipt
					json.Unmarshal(b, &copy)
					out.Swap = &copy
					switch mode {
					case "proposal":
						copy.Proposal.Digest = "different"
					case "packet":
						copy.Signed[1] ^= 1
					case "observation":
						copy.Observation = nil
					}
				}
				if r.Action == "status" && mode == "wallet" {
					a := *out.Status.Account
					a.WalletID = "different"
					out.Status.Account = &a
				}
				return out, e
			}
			if _, e := h.store.ChangeAgents(t.Context(), AgentCommand{Action: "swap-reconcile", ProfileID: h.profile, Revision: v.Revision, Swap: &SwapCommand{ID: v.Tasks[0].ID}}); e == nil {
				t.Fatal("conflicting proof admitted")
			}
			after, _ := json.Marshal(h.command(t, "swap-view", SwapCommand{}))
			if !bytes.Equal(before, after) || h.chain.Submissions != 1 {
				t.Fatal("refusal changed original")
			}
		})
	}
}

func TestSwapRecoverySchema33PreservesOldCancelledRecord(t *testing.T) {
	h := newSwapHarness(t)
	p := h.quote(t)
	v := h.submit(t, p)
	h.command(t, "swap-cancel", SwapCommand{ID: v.Tasks[0].ID})
	var raw string
	var revision int64
	h.store.db.QueryRow("SELECT document,revision FROM agent_workspace WHERE id=1").Scan(&raw, &revision)
	var d agentDocument
	json.Unmarshal([]byte(raw), &d)
	d.Tasks[0].State = "held"
	b, _ := json.Marshal(d)
	raw = string(b)
	if _, e := h.store.db.Exec("UPDATE agent_workspace SET document=? WHERE id=1", raw); e != nil {
		t.Fatal(e)
	}
	if _, e := h.store.db.Exec("PRAGMA user_version=33"); e != nil {
		t.Fatal(e)
	}
	dir := h.store.dir
	h.store.Close()
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	var after string
	var got int64
	var schema int
	s.db.QueryRow("SELECT document,revision FROM agent_workspace WHERE id=1").Scan(&after, &got)
	s.db.QueryRow("PRAGMA user_version").Scan(&schema)
	if after != raw || got != revision || schema != StateSchema {
		t.Fatal("migration rewrote old consent")
	}
}

func TestSwapRecoveryPausedResumeEligibility(t *testing.T) {
	for _, mode := range []string{"restart", "expiry"} {
		t.Run(mode, func(t *testing.T) {
			h := newSwapHarness(t)
			p := h.quote(t)
			v := h.submit(t, p)
			agents, err := h.store.Agents(t.Context(), h.profile, "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = h.store.ChangeAgents(t.Context(), AgentCommand{Action: "task-pause", ProfileID: h.profile, Revision: agents.Revision, ID: v.Tasks[0].ID}); err != nil {
				t.Fatal(err)
			}
			paused := h.command(t, "swap-view", SwapCommand{})
			if !paused.Recovery[0].Resume || paused.Recovery[0].Pause {
				t.Fatal("fresh pause cannot resume", paused)
			}
			if mode == "restart" {
				dir := h.store.dir
				h.store.Close()
				h.store, err = OpenStore(dir)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { h.store.Close() })
				h.connect()
			} else {
				h.store.now = func() time.Time { return time.Unix(p.Proposal.Quote.ExpiresAt+1, 0) }
			}
			paused = h.command(t, "swap-view", SwapCommand{})
			if paused.Recovery[0].Resume || !paused.Recovery[0].Cancel || paused.Runs[0].Swap.State != "paused" || h.chain.Submissions != 0 {
				t.Fatal("stale pause offers invalid resume", paused)
			}
			h.command(t, "swap-cancel", SwapCommand{ID: v.Tasks[0].ID})
		})
	}
}

func TestSwapRecoveryInterruptedSigningNeedsSignerProof(t *testing.T) {
	for _, signed := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-signing", true: "after-signing"}[signed], func(t *testing.T) {
			h := newSwapHarness(t)
			p := h.quote(t)
			v := h.submit(t, p)
			if err := h.store.writeSwap(t.Context(), h.profile, v.Tasks[0].ID, func(_ *AgentTask, r *AgentRun) error {
				swapEvent(r.Swap, "validating", h.store.now().Unix())
				swapEvent(r.Swap, "signing", h.store.now().Unix())
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if signed {
				if _, err := h.vault.SignSwap(p.Proposal.Quote.ID); err != nil {
					t.Fatal(err)
				}
			}
			dir := h.store.dir
			h.store.Close()
			var err error
			h.store, err = OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { h.store.Close() })
			h.connect()
			v = h.command(t, "swap-view", SwapCommand{})
			next, err := h.store.ChangeAgents(t.Context(), AgentCommand{Action: "swap-cancel", ProfileID: h.profile, Revision: v.Revision, Swap: &SwapCommand{ID: v.Tasks[0].ID}})
			if !signed {
				if err != nil || next.Onchain.Runs[0].Swap.State != "cancelled" || next.Onchain.Tasks[0].State != "completed" {
					t.Fatal("unsigned signer approval not safely revoked", next, err)
				}
			} else {
				if err == nil {
					t.Fatal("remote signed original cancelled")
				}
				h.chain.Unknown = true
				v = h.command(t, "swap-reconcile", SwapCommand{ID: v.Tasks[0].ID})
				if len(v.Runs[0].Swap.Signed) == 0 || v.Runs[0].Swap.State != "unknown" || v.Recovery[0].Cancel {
					t.Fatal("remote original not restored/held", v)
				}
			}
			if h.chain.Submissions != 0 {
				t.Fatal("interrupted signing caused dispatch")
			}
		})
	}
}

func TestSwapRecoverySchema33RefusesUnprotectedSemantics(t *testing.T) {
	for _, mode := range []string{"completion", "transition", "paused"} {
		t.Run(mode, func(t *testing.T) {
			h := newSwapHarness(t)
			p := h.quote(t)
			v := h.submit(t, p)
			if mode == "transition" {
				h.store.now = func() time.Time { return time.Unix(p.Proposal.Quote.ExpiresAt+1, 0) }
				if e := h.store.advanceOnchain(t.Context()); e != nil {
					t.Fatal(e)
				}
			}
			if mode != "paused" {
				h.command(t, "swap-cancel", SwapCommand{ID: v.Tasks[0].ID})
			}
			if mode == "transition" {
				var raw string
				h.store.db.QueryRow("SELECT document FROM agent_workspace WHERE id=1").Scan(&raw)
				var d agentDocument
				json.Unmarshal([]byte(raw), &d)
				d.Tasks[0].State = "held"
				b, _ := json.Marshal(d)
				h.store.db.Exec("UPDATE agent_workspace SET document=? WHERE id=1", string(b))
			}
			if mode == "paused" {
				h.store.db.Exec("UPDATE update_pause SET phase='paused' WHERE id=1")
			}
			h.store.db.Exec("PRAGMA user_version=33")
			dir := h.store.dir
			h.store.Close()
			if s, e := OpenStore(dir); e == nil {
				s.Close()
				t.Fatal("unprotected semantics migrated")
			}
		})
	}
}

func TestSwapRecoveryStoredSignerSettlementWithoutNewRPC(t *testing.T) {
	h := newSwapHarness(t)
	p := h.quote(t)
	h.submit(t, p)
	h.chain.Unknown = true
	if e := h.store.advanceOnchain(t.Context()); e != nil {
		t.Fatal(e)
	}
	h.chain.Unknown = false
	settled, e := h.vault.ReconcileSwap(t.Context(), p.Proposal.Quote.ID)
	if e != nil || settled.State != "confirmed" {
		t.Fatal(settled, e)
	}
	h.vault.Lock()
	h.store.onchain.venue = func(Account) (swapVenue, error) { return nil, errors.New("connection keys locked") }
	before := h.chain.Reads
	v, e := h.store.Agents(t.Context(), h.profile, "")
	if e != nil {
		t.Fatal(e)
	}
	next, e := h.store.ChangeAgents(context.Background(), AgentCommand{Action: "swap-reconcile", ProfileID: h.profile, Revision: v.Revision, Swap: &SwapCommand{ID: v.Onchain.Tasks[0].ID}})
	if e != nil {
		t.Fatal("durable independent proof incorrectly requires another RPC", e)
	}
	if next.Onchain.Runs[0].Swap.State != "confirmed" || h.chain.Reads != before || h.chain.Submissions != 1 {
		t.Fatal("stored proof not recovered exactly once")
	}
}
