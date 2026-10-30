package core

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"sync"

	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/signer"
)

const swapTaskVersion = "onchain-swap-task-v1"
const swapPrefix = "onchain-task:"

type SwapCommand struct {
	ID      string           `json:"id,omitempty"`
	Digest  string           `json:"digest,omitempty"`
	Request *onchain.Request `json:"request,omitempty"`
}
type SwapTaskPolicy struct {
	CredentialLease string              `json:"credentialLease,omitempty"`
	Version         string              `json:"version"`
	Proposal        signer.SwapProposal `json:"proposal"`
	Digest          string              `json:"digest"`
}
type SwapEvent struct {
	State    string `json:"state"`
	At       int64  `json:"at"`
	Previous string `json:"previous"`
	Digest   string `json:"digest"`
}
type SwapExecution struct {
	Version        string               `json:"version"`
	ProposalDigest string               `json:"proposalDigest"`
	BootID         string               `json:"bootId"`
	State          string               `json:"state"`
	Signed         []byte               `json:"signed,omitempty"`
	Signature      string               `json:"signature,omitempty"`
	Attempted      bool                 `json:"attempted"`
	Observation    *onchain.Observation `json:"observation,omitempty"`
	Events         []SwapEvent          `json:"events"`
}
type SwapReview struct {
	Proposal signer.SwapProposal `json:"proposal"`
	Revision int64               `json:"revision"`
	Digest   string              `json:"digest"`
	Approval string              `json:"approval"`
}
type OnchainView struct {
	Version       string      `json:"version"`
	Available     bool        `json:"available"`
	LiveExecution bool        `json:"liveExecution"`
	ProfileID     string      `json:"profileId"`
	Revision      int64       `json:"revision"`
	Review        *SwapReview `json:"review,omitempty"`
	Tasks         []AgentTask `json:"tasks"`
	Runs          []AgentRun  `json:"runs"`
}
type swapVenue interface {
	Prepare(context.Context, onchain.Quote) (onchain.Quote, error)
	Simulate(context.Context, onchain.Quote) (onchain.Simulation, error)
	Send(context.Context, onchain.Quote, []byte) (bool, error)
	Reconcile(context.Context, onchain.Quote, []byte) (onchain.Observation, error)
}
type swapEntry struct {
	lease  string
	review SwapReview
	boot   string
}
type onchainRuntime struct {
	credentialLease bool
	mu              sync.Mutex
	venue           func(Account) (swapVenue, error)
	signer          func(context.Context, signer.Request) (signer.Response, error)
	quotes          map[string]swapEntry
}

func swapPolicyDigest(p SwapTaskPolicy) string { p.Digest = ""; return onchain.Hash(p) }
func swapReviewDigest(r SwapReview, boot string) string {
	r.Digest = ""
	r.Approval = ""
	return onchain.Hash(struct {
		Review SwapReview
		Boot   string
	}{r, boot})
}
func swapRPCBinding(a Account) string { return onchain.Hash(a) }
func swapEvent(ex *SwapExecution, state string, now int64) {
	previous := ""
	if len(ex.Events) > 0 {
		previous = ex.Events[len(ex.Events)-1].Digest
	}
	e := SwapEvent{State: state, At: now, Previous: previous}
	e.Digest = onchain.Hash(e)
	ex.Events = append(ex.Events, e)
	ex.State = state
}
func swapProjection(d agentDocument, revision int64, profile string, available bool) OnchainView {
	v := OnchainView{Version: swapTaskVersion, Available: available, ProfileID: profile, Revision: revision, Tasks: []AgentTask{}, Runs: []AgentRun{}}
	for _, t := range d.Tasks {
		if t.ProfileID == profile && t.Swap != nil {
			v.Tasks = append(v.Tasks, t)
		}
	}
	for _, r := range d.Runs {
		if r.ProfileID == profile && r.Swap != nil {
			v.Runs = append(v.Runs, r)
		}
	}
	return v
}
func swapAccount(records AccountsRecord, id string) (Account, error) {
	for _, a := range records.Accounts {
		if a.ID == id && a.Version >= 2 && (a.Network == "mainnet-beta" || a.Network == "devnet") && hexValue(a.PublicKey, 32) && a.RPC != nil && a.RPC.Network == a.Network {
			return a, nil
		}
	}
	return Account{}, errors.New("select a network wallet")
}
func (s *Store) swapSnapshot(ctx context.Context, profile string) (agentDocument, int64, error) {
	d, r, e := readAgents(ctx, s.db)
	if e != nil {
		return d, r, e
	}
	if agentProfile(d, profile) < 0 {
		return d, r, errors.New("agent not found")
	}
	return d, r, nil
}
func (s *Store) swapCurrentAccount(ctx context.Context, q onchain.Quote) error {
	accounts, e := s.accountsRecord(ctx)
	if e != nil {
		return e
	}
	a, e := swapAccount(accounts, q.AccountID)
	if e != nil || a.Network != q.Network || swapRPCBinding(a) != q.RPCSHA256 {
		return errors.New("wallet or RPC changed; preserve the original order")
	}
	key, _ := hex.DecodeString(a.PublicKey)
	if onchain.Base58(key) != q.Owner {
		return errors.New("wallet identity changed")
	}
	return nil
}
func (s *Store) swapSigner(ctx context.Context, r signer.Request) (signer.Response, error) {
	if s.onchain == nil || s.onchain.signer == nil {
		return signer.Response{}, errors.New("Onchain execution disabled")
	}
	if r.Action != "status" && r.Action != "swap-sign" && r.Action != "swap-receipt" && r.Action != "swap-reconcile" && r.Action != "swap-cancel" {
		return signer.Response{}, errors.New("owner approval is unavailable through the core")
	}
	out, e := s.onchain.signer(ctx, r)
	if e != nil {
		return out, e
	}
	if out.Error != "" {
		return out, errors.New(out.Error)
	}
	return out, nil
}
func (s *Store) onchainView(ctx context.Context, profile, id string) (OnchainView, error) {
	d, r, e := s.swapSnapshot(ctx, profile)
	if e != nil {
		return OnchainView{}, e
	}
	v := swapProjection(d, r, profile, s.onchain != nil)
	if id == "" || s.onchain == nil {
		return v, nil
	}
	entry, ok := s.onchain.quotes[id]
	if !ok || entry.review.Proposal.Quote.ProfileID != profile || entry.boot != s.bootID {
		return v, errors.New("review unavailable; get a new quote")
	}
	review := entry.review
	review.Approval = "required"
	reply, e := s.swapSigner(ctx, signer.Request{Action: "swap-receipt", PublicInput: signer.PublicInput{OperationID: id}})
	if e == nil && reply.Swap != nil && reply.Swap.Proposal.Digest == review.Proposal.Digest && reply.Swap.State == "approved" {
		review.Approval = "approved"
	}
	v.Review = &review
	return v, nil
}

func (s *Store) changeOnchain(ctx context.Context, c AgentCommand) (OnchainView, error) {
	unexpected := c
	unexpected.Action, unexpected.ProfileID = "", ""
	unexpected.Revision = 0
	unexpected.Swap = nil
	if unexpected != (AgentCommand{}) || c.Swap == nil {
		return OnchainView{}, errors.New("one scoped swap command required")
	}
	if s.onchain == nil {
		if c.Action == "swap-view" {
			return s.onchainView(ctx, c.ProfileID, "")
		}
		return OnchainView{}, errors.New("Onchain execution disabled")
	}
	s.onchain.mu.Lock()
	defer s.onchain.mu.Unlock()
	if c.Action == "swap-view" {
		return s.onchainView(ctx, c.ProfileID, c.Swap.ID)
	}
	d, revision, e := s.swapSnapshot(ctx, c.ProfileID)
	if e != nil {
		return OnchainView{}, e
	}
	if revision != c.Revision {
		return OnchainView{}, ErrConflict
	}
	var phase string
	if e = s.db.QueryRowContext(ctx, "SELECT phase FROM update_pause WHERE id=1").Scan(&phase); e != nil {
		return OnchainView{}, e
	}
	if phase == "paused" {
		return OnchainView{}, ErrCorePaused
	}
	now := s.now().Unix()
	switch c.Action {
	case "swap-quote":
		if c.Swap.Request == nil || c.Swap.ID != "" || c.Swap.Digest != "" || !onchain.ValidRequest(*c.Swap.Request) {
			return OnchainView{}, errors.New("wallet, pair, amount and bounds required")
		}
		for id, entry := range s.onchain.quotes {
			if entry.review.Proposal.Quote.ExpiresAt <= now {
				delete(s.onchain.quotes, id)
			}
		}
		if len(s.onchain.quotes) >= 8 {
			return OnchainView{}, errors.New("too many active reviews")
		}
		accounts, e := s.accountsRecord(ctx)
		if e != nil {
			return OnchainView{}, e
		}
		a, e := swapAccount(accounts, c.Swap.Request.AccountID)
		if e != nil {
			return OnchainView{}, e
		}
		status, e := s.swapSigner(ctx, signer.Request{Action: "status"})
		if e != nil || status.Status.Account == nil || status.Status.Account.PublicKey != a.PublicKey || status.Status.UpdateHeld {
			return OnchainView{}, errors.New("protected wallet unavailable")
		}
		ref := *status.Status.Account
		if ref.Version == 2 {
			if !status.Status.SwapReady || !managedNetworkAccount(a) || a.Signer == nil || *a.Signer != ref || a.Network != ref.Network {
				return OnchainView{}, errors.New("protected swap history unavailable")
			}
		} else if ref.Version != 1 || status.Status.RecoveryRequired {
			return OnchainView{}, errors.New("wallet recovery required")
		}
		key, _ := hex.DecodeString(a.PublicKey)
		input := c.Swap.Request
		q := onchain.Quote{Version: onchain.Version, ID: randomTokenHex(), ProfileID: c.ProfileID, InstanceID: s.instanceID, AccountID: a.ID, Network: a.Network, Owner: onchain.Base58(key), RPCSHA256: swapRPCBinding(a), InputMint: input.InputMint, OutputMint: input.OutputMint, Amount: input.Amount, SlippageBPS: input.SlippageBPS, MaxFeeLamports: input.MaxFeeLamports, CreatedAt: now, ExpiresAt: now + 45}
		venue, e := s.onchain.venue(a)
		if e != nil {
			return OnchainView{}, e
		}
		q, e = venue.Prepare(ctx, q)
		if e != nil {
			return OnchainView{}, e
		}
		if onchain.ValidateQuote(q, s.now().Unix()) != nil {
			return OnchainView{}, errors.New("swap quote expired during preparation")
		}
		proposal := signer.NewSwapProposal(*status.Status.Account, q)
		lease := ""
		if s.onchain.credentialLease {
			lease = s.venueLeaseID(a)
			if lease == "" || status.Status.ReceiptRPC == nil || status.Status.ReceiptRPC.Locked || status.Status.ReceiptRPC.Origin != a.RPC.Endpoint {
				return OnchainView{}, errors.New("unlock connection keys and signer receipt reader")
			}
			proposal = signer.NewCredentialSwapProposal(*status.Status.Account, q, signer.ReceiptRPCBinding{Version: 1, Ref: status.Status.ReceiptRPC.Ref, Origin: a.RPC.Endpoint})
		}
		review := SwapReview{Proposal: proposal, Revision: revision, Approval: "required"}
		review.Digest = swapReviewDigest(review, s.bootID)
		s.onchain.quotes[q.ID] = swapEntry{review: review, boot: s.bootID, lease: lease}
		v := swapProjection(d, revision, c.ProfileID, true)
		v.Review = &review
		return v, nil
	case "swap-review", "swap-submit":
		if c.Swap.Request != nil {
			return OnchainView{}, errors.New("review cannot replace quote parameters")
		}
		entry, ok := s.onchain.quotes[c.Swap.ID]
		if !ok || entry.boot != s.bootID || entry.review.Proposal.Quote.ProfileID != c.ProfileID || entry.review.Revision != revision || c.Swap.Digest != entry.review.Digest || onchain.ValidateQuote(entry.review.Proposal.Quote, now) != nil {
			return OnchainView{}, errors.New("review changed or expired; get a new quote")
		}
		if s.onchain.credentialLease {
			r, e := s.accountsRecord(ctx)
			if e != nil {
				return OnchainView{}, e
			}
			a, e := swapAccount(r, entry.review.Proposal.Quote.AccountID)
			if e != nil || entry.lease == "" || entry.lease != s.venueLeaseID(a) {
				return OnchainView{}, errors.New("connection keys changed; review a fresh swap")
			}
		}
		if e = s.swapCurrentAccount(ctx, entry.review.Proposal.Quote); e != nil {
			return OnchainView{}, e
		}
		if c.Action == "swap-review" {
			return s.onchainView(ctx, c.ProfileID, c.Swap.ID)
		}
		receipt, e := s.swapSigner(ctx, signer.Request{Action: "swap-receipt", PublicInput: signer.PublicInput{OperationID: c.Swap.ID}})
		if e != nil || receipt.Swap == nil || receipt.Swap.State != "approved" || receipt.Swap.Proposal.Digest != entry.review.Proposal.Digest {
			return OnchainView{}, errors.New("protected owner approval required")
		}
		tx, e := s.db.BeginTx(ctx, nil)
		if e != nil {
			return OnchainView{}, e
		}
		defer tx.Rollback()
		d, r, e := readAgents(ctx, tx)
		if e != nil {
			return OnchainView{}, e
		}
		if r != revision {
			return OnchainView{}, ErrConflict
		}
		for _, task := range d.Tasks {
			if task.ID == swapPrefix+c.Swap.ID {
				return OnchainView{}, errors.New("swap review already used")
			}
		}
		if len(d.Tasks) >= 100 || len(d.Runs) >= 190 || len(d.Notices) >= 190 {
			return OnchainView{}, errors.New("task/result capacity reached")
		}
		p := &SwapTaskPolicy{Version: swapTaskVersion, Proposal: entry.review.Proposal, CredentialLease: entry.lease}
		p.Digest = swapPolicyDigest(*p)
		task := AgentTask{ID: swapPrefix + c.Swap.ID, ProfileID: c.ProfileID, Name: "Swap " + swapSymbol(p.Proposal.Quote.InputMint) + " / " + swapSymbol(p.Proposal.Quote.OutputMint), IntervalSeconds: 60, MaxRuns: 1, Runs: 1, State: "held", Swap: p}
		run := AgentRun{ID: task.ID + ":1", TaskID: task.ID, ProfileID: c.ProfileID, Strategy: swapTaskVersion, At: now, Outcome: "swap-queued", Swap: &SwapExecution{Version: swapTaskVersion, ProposalDigest: p.Proposal.Digest, BootID: s.bootID}}
		swapEvent(run.Swap, "queued", now)
		d.Tasks = append(d.Tasks, task)
		d.Runs = append(d.Runs, run)
		if e = saveAgents(ctx, tx, d, r); e != nil {
			return OnchainView{}, e
		}
		if e = tx.Commit(); e != nil {
			return OnchainView{}, e
		}
		delete(s.onchain.quotes, c.Swap.ID)
		return s.onchainView(ctx, c.ProfileID, "")
	case "swap-cancel":
		if c.Swap.Request != nil || c.Swap.Digest != "" {
			return OnchainView{}, errors.New("one unsigned order required")
		}
		t, r, e := s.originalSwap(ctx, c.ProfileID, c.Swap.ID)
		if e != nil {
			return OnchainView{}, e
		}
		if r.Swap.State != "queued" && r.Swap.State != "paused" {
			return OnchainView{}, errors.New("signed orders require reconciliation")
		}
		receipt, e := s.swapSigner(ctx, signer.Request{Action: "swap-cancel", PublicInput: signer.PublicInput{OperationID: t.Swap.Proposal.Quote.ID}})
		if e != nil || receipt.Swap == nil || receipt.Swap.State != "cancelled" {
			return OnchainView{}, errors.New("signer cancellation unavailable")
		}
		if e = s.writeSwap(ctx, c.ProfileID, c.Swap.ID, func(t *AgentTask, r *AgentRun) error { swapEvent(r.Swap, "cancelled", s.now().Unix()); return nil }); e != nil {
			return OnchainView{}, e
		}
		return s.onchainView(ctx, c.ProfileID, "")
	case "swap-reconcile":
		if c.Swap.Request != nil || c.Swap.Digest != "" {
			return OnchainView{}, errors.New("one original order identity required")
		}
		if e = s.reconcileOnchain(ctx, c.ProfileID, c.Swap.ID); e != nil {
			return OnchainView{}, e
		}
		return s.onchainView(ctx, c.ProfileID, "")
	default:
		return OnchainView{}, errors.New("unsupported swap command")
	}
}
func swapSymbol(mint string) string {
	if mint == onchain.SOL {
		return "wSOL"
	}
	return "USDC"
}
func randomTokenHex() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic("entropy unavailable")
	}
	return hex.EncodeToString(b)
}

func findSwap(d *agentDocument, profile, id string) (int, int, error) {
	task := -1
	run := -1
	for i, t := range d.Tasks {
		if t.ProfileID == profile && t.ID == id && t.Swap != nil {
			task = i
		}
	}
	for i, r := range d.Runs {
		if r.ProfileID == profile && r.TaskID == id && r.Swap != nil {
			run = i
		}
	}
	if task < 0 || run < 0 {
		return 0, 0, errors.New("swap order not found")
	}
	return task, run, nil
}
func (s *Store) writeSwap(ctx context.Context, profile, id string, change func(*AgentTask, *AgentRun) error) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var phase string
	if e = tx.QueryRowContext(ctx, "SELECT phase FROM update_pause WHERE id=1").Scan(&phase); e != nil {
		return e
	}
	if phase == "paused" {
		return ErrCorePaused
	}
	d, revision, e := readAgents(ctx, tx)
	if e != nil {
		return e
	}
	ti, ri, e := findSwap(&d, profile, id)
	if e != nil {
		return e
	}
	if e = change(&d.Tasks[ti], &d.Runs[ri]); e != nil {
		return e
	}
	if len(d.Runs[ri].Swap.Events) > 16 {
		return errors.New("reconciliation history capacity reached")
	}
	state := d.Runs[ri].Swap.State
	d.Runs[ri].Outcome = "swap-" + state
	if state == "confirmed" || state == "failed" {
		d.Tasks[ti].State = "completed"
		noticeID := id + ":1"
		found := false
		for _, n := range d.Notices {
			if n.ID == noticeID {
				found = true
			}
		}
		if !found {
			d.Notices = append(d.Notices, AgentNotice{ID: noticeID, ProfileID: profile, RunID: noticeID, At: s.now().Unix(), Text: d.Tasks[ti].Name + ": " + state})
		}
	}
	if e = saveAgents(ctx, tx, d, revision); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) originalSwap(ctx context.Context, profile, id string) (AgentTask, AgentRun, error) {
	d, _, e := s.swapSnapshot(ctx, profile)
	if e != nil {
		return AgentTask{}, AgentRun{}, e
	}
	ti, ri, e := findSwap(&d, profile, id)
	if e != nil {
		return AgentTask{}, AgentRun{}, e
	}
	return d.Tasks[ti], d.Runs[ri], nil
}
func (s *Store) advanceOnchain(ctx context.Context) error {
	if s.onchain == nil {
		return nil
	}
	s.onchain.mu.Lock()
	defer s.onchain.mu.Unlock()
	d, _, e := readAgents(ctx, s.db)
	if e != nil {
		return e
	}
	for _, r := range d.Runs {
		if r.Swap == nil || r.Swap.State != "queued" || r.Swap.BootID != s.bootID {
			continue
		}
		t, _, e := s.originalSwap(ctx, r.ProfileID, r.TaskID)
		if e != nil {
			return e
		}
		q := t.Swap.Proposal.Quote
		set := func(state string) error {
			return s.writeSwap(ctx, r.ProfileID, r.TaskID, func(t *AgentTask, run *AgentRun) error { swapEvent(run.Swap, state, s.now().Unix()); return nil })
		}
		if !s.validTaskCredential(ctx, t.Swap) || onchain.ValidateQuote(q, s.now().Unix()) != nil || s.swapCurrentAccount(ctx, q) != nil {
			return set("expired")
		}
		if e = set("validating"); e != nil {
			return e
		}
		venue, e := s.swapVenue(ctx, q)
		if e != nil {
			return set("expired")
		}
		if _, e = venue.Simulate(ctx, q); e != nil {
			return set("simulation-failed")
		}
		if e = set("signing"); e != nil {
			return e
		}
		reply, e := s.swapSigner(ctx, signer.Request{Action: "swap-sign", PublicInput: signer.PublicInput{OperationID: q.ID}})
		if e != nil || reply.Swap == nil || reply.Swap.Proposal.Digest != t.Swap.Proposal.Digest {
			return set("unknown")
		}
		signature, e := onchain.VerifyPacket(q, reply.Swap.Signed)
		if e != nil {
			return set("unknown")
		}
		if e = s.writeSwap(ctx, r.ProfileID, r.TaskID, func(t *AgentTask, run *AgentRun) error {
			run.Swap.Signed = append([]byte(nil), reply.Swap.Signed...)
			run.Swap.Signature = signature
			swapEvent(run.Swap, "signed", s.now().Unix())
			return nil
		}); e != nil {
			return e
		}
		if !s.validTaskCredential(ctx, t.Swap) || onchain.ValidateQuote(q, s.now().Unix()) != nil || s.swapCurrentAccount(ctx, q) != nil {
			return set("expired")
		}
		if e = s.writeSwap(ctx, r.ProfileID, r.TaskID, func(t *AgentTask, run *AgentRun) error {
			run.Swap.Attempted = true
			swapEvent(run.Swap, "sending", s.now().Unix())
			return nil
		}); e != nil {
			return e
		}
		attempted, e := venue.Send(ctx, q, reply.Swap.Signed)
		if e != nil {
			if attempted {
				return set("unknown")
			}
			return set("not-submitted")
		}
		if e = set("submitted"); e != nil {
			return e
		}
		return s.reconcileOnchain(ctx, r.ProfileID, r.TaskID)
	}
	return nil
}
func (s *Store) reconcileOnchain(ctx context.Context, profile, id string) error {
	t, r, e := s.originalSwap(ctx, profile, id)
	if e != nil {
		return e
	}
	if r.Swap.State == "confirmed" || r.Swap.State == "failed" {
		return nil
	}
	q := t.Swap.Proposal.Quote
	reply, e := s.swapSigner(ctx, signer.Request{Action: "swap-receipt", PublicInput: signer.PublicInput{OperationID: q.ID}})
	if e != nil || reply.Swap == nil || reply.Swap.Proposal.Digest != t.Swap.Proposal.Digest {
		return errors.New("signed original unavailable; no resubmission permitted")
	}
	// A signer cancellation may already be durable when the core commit fails.
	// This proof revokes only an unsigned approval; it needs no market/RPC read
	// and can never discard a signed original or an attempted submission.
	if reply.Swap.State == "cancelled" {
		if len(reply.Swap.Signed) != 0 || reply.Swap.Signature != "" || reply.Swap.Observation != nil || len(r.Swap.Signed) != 0 || r.Swap.Signature != "" || r.Swap.Attempted || r.Swap.Observation != nil {
			return errors.New("cancellation conflicts with signed swap evidence; preserve original")
		}
		if r.Swap.State == "cancelled" {
			return nil
		}
		return s.writeSwap(ctx, profile, id, func(t *AgentTask, run *AgentRun) error {
			swapEvent(run.Swap, "cancelled", s.now().Unix())
			return nil
		})
	}
	if len(reply.Swap.Signed) == 0 {
		return errors.New("signed original unavailable; no resubmission permitted")
	}
	if e = s.swapCurrentAccount(ctx, q); e != nil {
		return e
	}
	signature, e := onchain.VerifyPacket(q, reply.Swap.Signed)
	if e != nil {
		return e
	}
	venue, e := s.swapVenue(ctx, q)
	if e != nil {
		return e
	}
	observation, e := venue.Reconcile(ctx, q, reply.Swap.Signed)
	if e != nil {
		return e
	}
	if observation.State != "confirmed" && observation.State != "failed" {
		return s.writeSwap(ctx, profile, id, func(t *AgentTask, run *AgentRun) error {
			run.Swap.Signed = append([]byte(nil), reply.Swap.Signed...)
			run.Swap.Signature = signature
			if run.Swap.State != "unknown" {
				swapEvent(run.Swap, "unknown", s.now().Unix())
			}
			return nil
		})
	}
	proof, e := s.swapSigner(ctx, signer.Request{Action: "swap-reconcile", PublicInput: signer.PublicInput{OperationID: q.ID}})
	if e != nil || proof.Swap == nil || proof.Swap.Proposal.Digest != t.Swap.Proposal.Digest || proof.Swap.Observation == nil || *proof.Swap.Observation != observation {
		return errors.New("independent signer settlement verification unavailable; hold original order")
	}
	return s.writeSwap(ctx, profile, id, func(t *AgentTask, run *AgentRun) error {
		run.Swap.Signed = append([]byte(nil), reply.Swap.Signed...)
		run.Swap.Signature = signature
		run.Swap.Observation = &observation
		swapEvent(run.Swap, observation.State, s.now().Unix())
		return nil
	})
}

func validateOnchainStorage(d agentDocument) error {
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
			if !validSwapState(event.State) || (i > 0 && !validSwapTransition(x.Events[i-1].State, event.State)) {
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
func (s *Store) recoverOnchain(ctx context.Context) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	phase, _, e := readPause(ctx, tx)
	if e != nil {
		return e
	}
	if phase == "paused" {
		return nil
	}
	d, revision, e := readAgents(ctx, tx)
	if e != nil {
		return e
	}
	changed := false
	for i := range d.Runs {
		r := &d.Runs[i]
		if r.Swap == nil {
			continue
		}
		switch r.Swap.State {
		case "queued", "validating", "signing", "signed", "sending", "submitted":
			state := "unknown"
			if r.Swap.State == "queued" || r.Swap.State == "validating" {
				state = "cancelled"
			}
			swapEvent(r.Swap, state, s.now().Unix())
			r.Outcome = "swap-" + state
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if e = saveAgents(ctx, tx, d, revision); e != nil {
		return e
	}
	return tx.Commit()
}

// Keep the existing SQL snapshot participant; no second journal/database owner.
func migrateOnchain(ctx context.Context, db *sql.DB) error {
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var phase string
	if e = tx.QueryRowContext(ctx, "SELECT phase FROM update_pause WHERE id=1").Scan(&phase); e != nil {
		return e
	}
	if phase == "paused" {
		return errors.New("paused state cannot migrate")
	}
	var raw string
	if e = tx.QueryRowContext(ctx, "SELECT document FROM agent_workspace WHERE id=1").Scan(&raw); e != nil {
		return e
	}
	if e = onchainFields([]byte(raw), true); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "PRAGMA user_version=31"); e != nil {
		return e
	}
	return tx.Commit()
}

func (s *Store) swapVenue(ctx context.Context, q onchain.Quote) (swapVenue, error) {
	if e := s.swapCurrentAccount(ctx, q); e != nil {
		return nil, e
	}
	r, e := s.accountsRecord(ctx)
	if e != nil {
		return nil, e
	}
	a, e := swapAccount(r, q.AccountID)
	if e != nil {
		return nil, e
	}
	return s.onchain.venue(a)
}
func swapFutureStorage(d agentDocument) int {
	n := 0
	for _, r := range d.Runs {
		if r.Swap != nil && r.Swap.State != "confirmed" && r.Swap.State != "failed" {
			n += 12000
		}
	}
	return n
}

func validSwapState(s string) bool {
	switch s {
	case "queued", "paused", "validating", "signing", "signed", "sending", "submitted", "unknown", "confirmed", "failed", "expired", "simulation-failed", "cancelled", "not-submitted":
		return true
	}
	return false
}
func validSwapTransition(a, b string) bool {
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

// configureInstalledSwapFixture attaches the existing core to the immutable
// installed signer identity. Only trusted compiled fixture constructors call it;
// ordinary startup has no venue, endpoint override, or financial dispatch.
func (s *Store) configureInstalledSwapFixture(path string, venue func(Account) (swapVenue, error)) error {
	c, e := signer.LoadConfig(path)
	if e != nil {
		return e
	}
	if c.Version != 2 || c.Network != s.custodyNetwork || os.Getuid() != c.AppUID || os.Geteuid() != c.AppUID || venue == nil {
		return errors.New("installed wallet identity required")
	}
	s.onchain = &onchainRuntime{venue: venue, quotes: map[string]swapEntry{}, signer: func(ctx context.Context, r signer.Request) (signer.Response, error) {
		return signer.Call(ctx, c, false, r)
	}}
	return nil
}
