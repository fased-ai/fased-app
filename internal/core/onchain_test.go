package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/onchaintest"
	"github.com/fased-ai/fased-app/internal/signer"
)

type swapHarness struct {
	store    *Store
	vault    *signer.Vault
	chain    *onchaintest.Chain
	password []byte
	profile  string
}

func newSwapHarness(t *testing.T) *swapHarness {
	t.Helper()
	return setupSwapHarness(t, fixtureStore(t))
}
func setupSwapHarness(t *testing.T, s *Store) *swapHarness {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "signer")
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	v, e := signer.Open(dir, strings.Repeat("a", 32))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { v.Close() })
	pw := []byte("disposable-onchain-test-password")
	if e = v.Create(pw); e != nil {
		t.Fatal(e)
	}
	if e = v.Unlock(pw, time.Minute); e != nil {
		t.Fatal(e)
	}
	chain := &onchaintest.Chain{}
	client := onchaintest.Client(chain)
	if e = v.ConfigureSwapObserver(client); e != nil {
		t.Fatal(e)
	}
	ref := *v.Status().Account
	r, e := s.accountsRecord(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	r.Accounts = append(r.Accounts, Account{Version: 2, ID: strings.Repeat("c", 32), Name: "Disposable wallet", Mode: "watch", Network: "mainnet-beta", PublicKey: ref.PublicKey})
	s.rpcAdmission = true
	if _, e = s.UpdateAccounts(t.Context(), r); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ConfigureAccountRPC(t.Context(), RPCConfigInput{AccountID: strings.Repeat("c", 32), Revision: r.Revision + 1, Endpoint: "https://mainnet.helius-rpc.com", Network: "mainnet-beta"}); e != nil {
		t.Fatal(e)
	}
	h := &swapHarness{s, v, chain, pw, "trading-analyst"}
	h.connect()
	return h
}
func (h *swapHarness) connect() {
	h.store.onchain = &onchainRuntime{venue: func(a Account) (swapVenue, error) { return onchaintest.Client(h.chain), nil }, quotes: map[string]swapEntry{}, signer: func(ctx context.Context, r signer.Request) (out signer.Response, e error) {
		switch r.Action {
		case "status":
			out.Status = h.vault.Status()
		case "swap-receipt":
			out.Swap, e = h.vault.SwapReceipt(r.OperationID)
		case "swap-cancel":
			out.Swap, e = h.vault.CancelSwap(r.OperationID)
		case "swap-sign":
			out.Swap, e = h.vault.SignSwap(r.OperationID)
		case "swap-reconcile":
			out.Swap, e = h.vault.ReconcileSwap(ctx, r.OperationID)
		default:
			e = os.ErrPermission
		}
		return
	}}
}
func (h *swapHarness) command(t *testing.T, action string, c SwapCommand) OnchainView {
	t.Helper()
	v, e := h.store.Agents(t.Context(), h.profile, "")
	if e != nil {
		t.Fatal(e)
	}
	next, e := h.store.ChangeAgents(t.Context(), AgentCommand{Action: action, ProfileID: h.profile, Revision: v.Revision, Swap: &c})
	if e != nil {
		t.Fatal(e)
	}
	if next.Onchain == nil {
		t.Fatal("missing swap view")
	}
	return *next.Onchain
}
func (h *swapHarness) quote(t *testing.T) *SwapReview {
	v := h.command(t, "swap-quote", SwapCommand{Request: &onchain.Request{AccountID: strings.Repeat("c", 32), InputMint: onchain.USDC, OutputMint: onchain.SOL, Amount: "12500000", SlippageBPS: 50, MaxFeeLamports: 5000}})
	if v.Review == nil {
		t.Fatal("missing review")
	}
	return v.Review
}
func (h *swapHarness) submit(t *testing.T, review *SwapReview) OnchainView {
	t.Helper()
	if _, e := h.vault.ApproveSwap(review.Proposal); e != nil {
		t.Fatal(e)
	}
	return h.command(t, "swap-submit", SwapCommand{ID: review.Proposal.Quote.ID, Digest: review.Digest})
}
func saveSwapEvidence(t *testing.T, name string, value any) {
	t.Helper()
	root := os.Getenv("FASED_ONCHAIN_EVIDENCE")
	if root == "" {
		return
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	b, _ := json.MarshalIndent(value, "", "  ")
	if e := os.WriteFile(filepath.Join(root, name+".json"), b, 0600); e != nil {
		t.Fatal(e)
	}
}
func TestOnchainReviewedSwapJoinsExistingTasksAndResults(t *testing.T) {
	h := newSwapHarness(t)
	before, e := h.store.Agents(t.Context(), h.profile, "")
	if e != nil {
		t.Fatal(e)
	}
	review := h.quote(t)
	v, e := h.store.Agents(t.Context(), h.profile, "")
	if e != nil || v.Revision != before.Revision || len(v.Tasks) != len(before.Tasks) || h.chain.Submissions != 0 {
		t.Fatal("quote mutated records or submitted", e)
	}
	quoted := h.command(t, "swap-review", SwapCommand{ID: review.Proposal.Quote.ID, Digest: review.Digest})
	if quoted.Revision != before.Revision {
		t.Fatal("review mutated state")
	}
	queued := h.submit(t, review)
	if len(queued.Tasks) != 1 || len(queued.Runs) != 1 || queued.Runs[0].Swap.State != "queued" || h.chain.Submissions != 0 {
		t.Fatal("review did not queue exactly one inert task")
	}
	if e = h.store.advanceOnchain(t.Context()); e != nil {
		t.Fatal(e)
	}
	got := h.command(t, "swap-view", SwapCommand{})
	if got.Runs[0].Swap.State != "confirmed" || got.Tasks[0].State != "completed" || got.Runs[0].Swap.Observation.Output != "100000000" || h.chain.Submissions != 1 {
		t.Fatal("swap not reconciled", got.Runs[0].Swap, h.chain.Submissions)
	}
	if e = h.store.advanceOnchain(t.Context()); e != nil || h.chain.Submissions != 1 {
		t.Fatal("duplicate submission", e)
	}
	saveSwapEvidence(t, "confirmed-journey", got)
}
func TestOnchainUnknownSubmissionSurvivesRestartAndNeverResends(t *testing.T) {
	h := newSwapHarness(t)
	review := h.quote(t)
	h.submit(t, review)
	h.chain.DropSubmission = true
	h.chain.Unknown = true
	if e := h.store.advanceOnchain(t.Context()); e != nil {
		t.Fatal(e)
	}
	v := h.command(t, "swap-view", SwapCommand{})
	if v.Runs[0].Swap.State != "unknown" || len(v.Runs[0].Swap.Signed) == 0 || h.chain.Submissions != 1 {
		t.Fatal("unknown signed original lost")
	}
	packet := append([]byte(nil), v.Runs[0].Swap.Signed...)
	dir := h.store.dir
	if e := h.store.Close(); e != nil {
		t.Fatal(e)
	}
	next, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { next.Close() })
	h.store = next
	h.connect()
	if e = next.advanceOnchain(t.Context()); e != nil || h.chain.Submissions != 1 {
		t.Fatal("restart replayed order", e)
	}
	h.chain.Unknown = false
	final := h.command(t, "swap-reconcile", SwapCommand{ID: v.Tasks[0].ID})
	if final.Runs[0].Swap.State != "confirmed" || string(final.Runs[0].Swap.Signed) != string(packet) || h.chain.Submissions != 1 {
		t.Fatal("original reconciliation changed bytes or resubmitted")
	}
	saveSwapEvidence(t, "unknown-restart", final)
}
func TestOnchainRefusesUnapprovedWrongProfileChangedRPCAndExpiredQuote(t *testing.T) {
	for _, mode := range []string{"approval", "profile", "rpc", "expiry"} {
		t.Run(mode, func(t *testing.T) {
			h := newSwapHarness(t)
			review := h.quote(t)
			if mode != "approval" {
				if _, e := h.vault.ApproveSwap(review.Proposal); e != nil {
					t.Fatal(e)
				}
			}
			if mode == "profile" {
				h.profile = "news-researcher"
			}
			if mode == "rpc" {
				r, _ := h.store.accountsRecord(t.Context())
				r.Accounts[0].Name = "Changed wallet metadata"
				if _, e := h.store.UpdateAccounts(t.Context(), r); e != nil {
					t.Fatal(e)
				}
			}
			if mode == "expiry" {
				h.store.now = func() time.Time { return time.Unix(review.Proposal.Quote.ExpiresAt+1, 0) }
			}
			v, _ := h.store.Agents(t.Context(), h.profile, "")
			_, e := h.store.ChangeAgents(t.Context(), AgentCommand{Action: "swap-submit", ProfileID: h.profile, Revision: v.Revision, Swap: &SwapCommand{ID: review.Proposal.Quote.ID, Digest: review.Digest}})
			if e == nil || h.chain.Submissions != 0 {
				t.Fatal("unsafe swap admitted", mode)
			}
			got, _ := h.store.Agents(t.Context(), h.profile, "")
			if len(got.Tasks) != 0 {
				t.Fatal("failed submit wrote task")
			}
		})
	}
}
func TestOnchainQueuedConsentCannotCrossRestart(t *testing.T) {
	h := newSwapHarness(t)
	r := h.quote(t)
	h.submit(t, r)
	dir := h.store.dir
	h.store.Close()
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	h.store = s
	h.connect()
	if e = s.advanceOnchain(t.Context()); e != nil {
		t.Fatal(e)
	}
	v := h.command(t, "swap-view", SwapCommand{})
	if v.Runs[0].Swap.State != "cancelled" || h.chain.Submissions != 0 {
		t.Fatal("queued consent replayed after boot")
	}
	saveSwapEvidence(t, "cancelled-on-restart", v)
}

func TestOnchainSchema30MigrationPreservesDocumentAndRejectsErasure(t *testing.T) {
	s := fixtureStore(t)
	var raw string
	var revision int64
	if e := s.db.QueryRow("SELECT document,revision FROM agent_workspace WHERE id=1").Scan(&raw, &revision); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec("PRAGMA user_version=30"); e != nil {
		t.Fatal(e)
	}
	dir := s.dir
	s.Close()
	next, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	var got string
	var rev int64
	var schema int
	next.db.QueryRow("SELECT document,revision FROM agent_workspace WHERE id=1").Scan(&got, &rev)
	next.db.QueryRow("PRAGMA user_version").Scan(&schema)
	if got != raw || rev != revision || schema != StateSchema {
		t.Fatal("migration rewrote existing records")
	}
	h := newSwapHarness(t)
	review := h.quote(t)
	h.submit(t, review)
	var body string
	h.store.db.QueryRow("SELECT document FROM agent_workspace WHERE id=1").Scan(&body)
	if e = onchainFields([]byte(body), true); e == nil {
		t.Fatal("unprotected schema accepted swap policy")
	}
	var root map[string]json.RawMessage
	json.Unmarshal([]byte(body), &root)
	var tasks []map[string]json.RawMessage
	json.Unmarshal(root["tasks"], &tasks)
	delete(tasks[0], "swap")
	root["tasks"], _ = json.Marshal(tasks)
	erased, _ := json.Marshal(root)
	var doc agentDocument
	json.Unmarshal(erased, &doc)
	if e = validateOnchainStorage(doc); e == nil {
		t.Fatal("erased policy admitted")
	}
	if e = onchainFields([]byte(strings.Replace(body, `"swap":`, `"Swap":`, 1)), false); e == nil {
		t.Fatal("aliased policy admitted")
	}
	saveSwapEvidence(t, "migration", map[string]any{"result": "PASS", "schema": schema, "rawDocumentPreserved": got == raw, "revisionPreserved": rev == revision, "erasureRefused": true})
}

func TestOnchainCancelRevokesUnsignedSignerApproval(t *testing.T) {
	h := newSwapHarness(t)
	p := h.quote(t)
	v := h.submit(t, p)
	id := v.Tasks[0].ID
	v = h.command(t, "swap-cancel", SwapCommand{ID: id})
	if v.Runs[0].Swap.State != "cancelled" {
		t.Fatal("not cancelled")
	}
	if _, e := h.vault.SignSwap(p.Proposal.Quote.ID); e == nil {
		t.Fatal("cancelled approval signed")
	}
	if e := h.store.advanceOnchain(t.Context()); e != nil || h.chain.Submissions != 0 {
		t.Fatal("cancelled order submitted", e)
	}
}

func TestOnchainCancelledApprovalRecoversAfterCoreWriteInterruption(t *testing.T) {
	for _, stage := range []string{"queued", "unknown"} {
		t.Run(stage, func(t *testing.T) {
			h := newSwapHarness(t)
			p := h.quote(t)
			v := h.submit(t, p)
			id := v.Tasks[0].ID
			// The signer has independent durable storage: its cancellation can succeed
			// while this core transaction fails. The trigger is disposable test state.
			if _, e := h.store.db.Exec(`CREATE TRIGGER interrupt_swap_cancel BEFORE UPDATE ON agent_workspace BEGIN SELECT RAISE(ABORT,'interrupted cancellation commit'); END`); e != nil {
				t.Fatal(e)
			}
			_, e := h.store.ChangeAgents(t.Context(), AgentCommand{Action: "swap-cancel", ProfileID: h.profile, Revision: v.Revision, Swap: &SwapCommand{ID: id}})
			if e == nil {
				t.Fatal("core cancellation commit did not fail")
			}
			receipt, e := h.vault.SwapReceipt(p.Proposal.Quote.ID)
			if e != nil || receipt.State != "cancelled" || len(receipt.Signed) != 0 {
				t.Fatal("signer cancellation not durable", e)
			}
			if _, e = h.store.db.Exec("DROP TRIGGER interrupt_swap_cancel"); e != nil {
				t.Fatal(e)
			}
			if stage == "unknown" {
				if e = h.store.advanceOnchain(t.Context()); e != nil {
					t.Fatal(e)
				}
			}
			v = h.command(t, "swap-reconcile", SwapCommand{ID: id})
			if v.Runs[0].Swap.State != "cancelled" || v.Runs[0].Swap.Attempted || len(v.Runs[0].Swap.Signed) != 0 || h.chain.Submissions != 0 {
				t.Fatal("unsigned cancellation recovery lost", v.Runs[0].Swap)
			}
			again := h.command(t, "swap-reconcile", SwapCommand{ID: id})
			if len(again.Runs[0].Swap.Events) != len(v.Runs[0].Swap.Events) {
				t.Fatal("cancellation recovery was not idempotent")
			}
			saveSwapEvidence(t, "cancel-recovery-"+stage, again)
		})
	}
}

func TestOnchainCancelledReceiptCannotEraseSignedOrSubmittedOriginal(t *testing.T) {
	h := newSwapHarness(t)
	p := h.quote(t)
	v := h.submit(t, p)
	h.chain.DropSubmission = true
	h.chain.Unknown = true
	if e := h.store.advanceOnchain(t.Context()); e != nil {
		t.Fatal(e)
	}
	before := h.command(t, "swap-view", SwapCommand{})
	original := append([]byte(nil), before.Runs[0].Swap.Signed...)
	bridge := h.store.onchain.signer
	h.store.onchain.signer = func(ctx context.Context, request signer.Request) (signer.Response, error) {
		reply, e := bridge(ctx, request)
		if e == nil && request.Action == "swap-receipt" {
			reply.Swap.State = "cancelled"
			reply.Swap.Signed = nil
			reply.Swap.Signature = ""
			reply.Swap.Observation = nil
		}
		return reply, e
	}
	current, e := h.store.Agents(t.Context(), h.profile, "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = h.store.ChangeAgents(t.Context(), AgentCommand{Action: "swap-reconcile", ProfileID: h.profile, Revision: current.Revision, Swap: &SwapCommand{ID: v.Tasks[0].ID}}); e == nil {
		t.Fatal("cancel receipt erased signed hold")
	}
	after := h.command(t, "swap-view", SwapCommand{})
	if after.Runs[0].Swap.State != "unknown" || string(after.Runs[0].Swap.Signed) != string(original) || !after.Runs[0].Swap.Attempted || h.chain.Submissions != 1 {
		t.Fatal("original/hold changed")
	}
}

func TestOnchainIncompleteSettlementKeepsBothOriginalsHeld(t *testing.T) {
	h := newSwapHarness(t)
	p := h.quote(t)
	h.submit(t, p)
	h.chain.SettlementMode = "missing-meta"
	if e := h.store.advanceOnchain(t.Context()); e == nil {
		t.Fatal("incomplete outcome accepted")
	}
	v := h.command(t, "swap-view", SwapCommand{})
	r := v.Runs[0].Swap
	receipt, e := h.vault.SwapReceipt(p.Proposal.Quote.ID)
	if e != nil || receipt.State != "signed" || r.State != "submitted" || !r.Attempted || len(r.Signed) == 0 || string(r.Signed) != string(receipt.Signed) || h.chain.Submissions != 1 {
		t.Fatal("incomplete evidence released an original", r, e)
	}
	h.chain.SettlementMode = ""
	done := h.command(t, "swap-reconcile", SwapCommand{ID: v.Tasks[0].ID})
	if done.Runs[0].Swap.State != "confirmed" || h.chain.Submissions != 1 {
		t.Fatal("original not reconciled")
	}
}
