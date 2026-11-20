package core

// Controlled WEN mining readiness connects accepted wallet policies and the
// versioned WEN strategy/PM1 contracts to the existing Task/result worker.
// Ordinary startup leaves mining activation strictly OFF (s.wenMining == nil).
import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"sync"

	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/wenpm1"
	"github.com/fased-ai/fased-app/internal/wenstrategy"
)

const (
	wenMiningTaskVersion    = "wen-mining-task-v1"
	wenMiningFeature        = "agents-wen-mining-v1"
	wenMiningPrefix         = "wen-mining:"
	miningActivationEnabled = false
)

type WENMiningRequest struct {
	AccountID            string `json:"accountId"`
	ProposalID           string `json:"proposalId,omitempty"`
	StrategyDigest       string `json:"strategyDigest"`
	AmountLamports       string `json:"amountLamports"`
	WalletBudgetLamports string `json:"walletBudgetLamports"`
	MaxOperationLamports string `json:"maxOperationLamports"`
	PolicyExpiresAt      int64  `json:"policyExpiresAt"`
}

type WENMiningCommand struct {
	ID      string            `json:"id,omitempty"`
	Digest  string            `json:"digest,omitempty"`
	Request *WENMiningRequest `json:"request,omitempty"`
}

type WENMiningWalletPolicy struct {
	AccountID            string            `json:"accountId"`
	Network              string            `json:"network"`
	PublicKey            string            `json:"publicKey"`
	OwnerAddress         string            `json:"ownerAddress"`
	Signer               signer.AccountRef `json:"signer"`
	RPCSHA256            string            `json:"rpcSha256"`
	ProposalID           string            `json:"proposalId,omitempty"`
	ProposalDigest       string            `json:"proposalDigest,omitempty"`
	WalletBudgetLamports string            `json:"walletBudgetLamports"`
	MaxOperationLamports string            `json:"maxOperationLamports"`
	PolicyExpiresAt      int64             `json:"policyExpiresAt"`
}

type WENMiningOperation struct {
	ID                   string                `json:"id"`
	ProfileID            string                `json:"profileId"`
	InstanceID           string                `json:"instanceId"`
	StrategyDigest       string                `json:"strategyDigest"`
	StrategyName         string                `json:"strategyName"`
	Market               wenstrategy.Market    `json:"market"`
	Policy               WENMiningWalletPolicy `json:"policy"`
	AmountLamports       string                `json:"amountLamports"`
	AllocationsLamports  [5]string             `json:"allocationsLamports"`
	WeightsBps           [5]int                `json:"weightsBps"`
	EstimatedFeeLamports string                `json:"estimatedFeeLamports"`
	Slot                 uint64                `json:"slot"`
	FixtureSource        string                `json:"fixtureSource"`
	FixtureSHA256        string                `json:"fixtureSha256"`
	CreatedAt            int64                 `json:"createdAt"`
	ExpiresAt            int64                 `json:"expiresAt"`
}

type WENMiningPrepared struct {
	Commitment   string                `json:"commitment"`
	Nonce        uint64                `json:"nonce"`
	Selection    uint8                 `json:"selection"`
	Instructions []WENPaperInstruction `json:"instructions"`
}

type WENMiningObservation struct {
	State         string          `json:"state"`
	Slot          uint64          `json:"slot"`
	AccountSHA256 string          `json:"accountSha256,omitempty"`
	ReceiptSHA256 string          `json:"receiptSha256,omitempty"`
	Receipt       *wenpm1.Receipt `json:"receipt,omitempty"`
}

type WENMiningTaskPolicy struct {
	Version   string             `json:"version"`
	Operation WENMiningOperation `json:"operation"`
	Prepared  WENMiningPrepared  `json:"prepared"`
	Digest    string             `json:"digest"`
}

type WENMiningEvent struct {
	State    string `json:"state"`
	At       int64  `json:"at"`
	Previous string `json:"previous"`
	Digest   string `json:"digest"`
}

type WENMiningExecution struct {
	Version         string                `json:"version"`
	OperationDigest string                `json:"operationDigest"`
	BootID          string                `json:"bootId"`
	State           string                `json:"state"`
	Attempted       bool                  `json:"attempted"`
	Observation     *WENMiningObservation `json:"observation,omitempty"`
	Events          []WENMiningEvent      `json:"events"`
}

type WENMiningReview struct {
	Operation WENMiningOperation `json:"operation"`
	Prepared  WENMiningPrepared  `json:"prepared"`
	Revision  int64              `json:"revision"`
	Digest    string             `json:"digest"`
}

type WENMiningRecovery struct {
	TaskID    string `json:"taskId"`
	Status    string `json:"status"`
	Cancel    bool   `json:"cancel"`
	Reconcile bool   `json:"reconcile"`
	Pause     bool   `json:"pause"`
	Resume    bool   `json:"resume"`
}

type WENMiningView struct {
	Version          string              `json:"version"`
	Available        bool                `json:"available"`
	MiningActivation bool                `json:"miningActivation"`
	LiveExecution    bool                `json:"liveExecution"`
	ProfileID        string              `json:"profileId"`
	Revision         int64               `json:"revision"`
	Review           *WENMiningReview    `json:"review,omitempty"`
	Recovery         []WENMiningRecovery `json:"recovery,omitempty"`
	Tasks            []AgentTask         `json:"tasks"`
	Runs             []AgentRun          `json:"runs"`
}

// ControlledWENMiningVenue is an explicit engineering acceptance boundary.
// Ordinary builds never configure a venue; mining activation stays held.
type ControlledWENMiningVenue interface {
	Prepare(ctx context.Context, op WENMiningOperation) (WENMiningPrepared, uint64, string, error)
	Submit(ctx context.Context, op WENMiningOperation, prepared WENMiningPrepared) (bool, error)
	Reconcile(ctx context.Context, op WENMiningOperation, prepared WENMiningPrepared) (WENMiningObservation, error)
}

type wenMiningEntry struct {
	review WENMiningReview
	boot   string
}

type wenMiningRuntime struct {
	mu      sync.Mutex
	venue   func(Account) (ControlledWENMiningVenue, error)
	reviews map[string]wenMiningEntry
}

// ConfigureControlledWENMining attaches a compiled controlled test venue to a
// Store. Ordinary runtime startup never calls this; miningActivation stays OFF.
func (s *Store) ConfigureControlledWENMining(venue func(Account) (ControlledWENMiningVenue, error)) error {
	if s == nil || venue == nil || miningActivationEnabled {
		return signer.ErrUnavailable
	}
	s.wenMining = &wenMiningRuntime{
		venue:   venue,
		reviews: map[string]wenMiningEntry{},
	}
	return nil
}

// ConfigureControlledWENMining exposes the controlled acceptance constructor on
// Server without opening any HTTP, CLI, or environment activation path.
func (server *Server) ConfigureControlledWENMining(venue func(Account) (ControlledWENMiningVenue, error)) error {
	if server == nil || server.Store == nil {
		return signer.ErrUnavailable
	}
	return server.Store.ConfigureControlledWENMining(venue)
}

func wenMiningHash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func wenMiningPolicyDigest(p WENMiningTaskPolicy) string {
	p.Digest = ""
	return wenMiningHash(p)
}

func wenMiningReviewDigest(r WENMiningReview, boot string) string {
	r.Digest = ""
	return wenMiningHash(struct {
		Review WENMiningReview
		Boot   string
	}{r, boot})
}

func wenMiningEvent(ex *WENMiningExecution, state string, now int64) {
	previous := ""
	if len(ex.Events) > 0 {
		previous = ex.Events[len(ex.Events)-1].Digest
	}
	e := WENMiningEvent{State: state, At: now, Previous: previous}
	e.Digest = wenMiningHash(e)
	ex.Events = append(ex.Events, e)
	ex.State = state
}

func parseUintLamports(s string) (uint64, error) {
	if len(s) == 0 || len(s) > 20 || (len(s) > 1 && s[0] == '0') {
		return 0, errors.New("canonical unsigned lamports required")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("canonical unsigned lamports required")
		}
	}
	return strconv.ParseUint(s, 10, 64)
}

func (s *Store) wenMiningProjection(d agentDocument, revision int64, profile string, available bool) WENMiningView {
	v := WENMiningView{
		Version:          wenMiningTaskVersion,
		Available:        available,
		MiningActivation: miningActivationEnabled,
		LiveExecution:    false,
		ProfileID:        profile,
		Revision:         revision,
		Tasks:            []AgentTask{},
		Runs:             []AgentRun{},
	}
	for _, t := range d.Tasks {
		if t.ProfileID == profile && t.WENMining != nil {
			v.Tasks = append(v.Tasks, t)
		}
	}
	for _, r := range d.Runs {
		if r.ProfileID == profile && r.WENMining != nil {
			v.Runs = append(v.Runs, r)
			rec := wenMiningRecovery(r.TaskID, *r.WENMining, available)
			for _, task := range v.Tasks {
				if task.ID == r.TaskID {
					rec.Resume = available && r.WENMining.State == "paused" && r.WENMining.BootID == s.bootID && s.now().Unix() < task.WENMining.Operation.ExpiresAt
				}
			}
			v.Recovery = append(v.Recovery, rec)
		}
	}
	return v
}

func wenMiningCancelable(x WENMiningExecution) bool {
	return !x.Attempted && x.Observation == nil && (x.State == "queued" || x.State == "paused" || x.State == "expired" || x.State == "limit-held" || x.State == "binding-held")
}

func wenMiningRecovery(id string, x WENMiningExecution, available bool) WENMiningRecovery {
	r := WENMiningRecovery{TaskID: id, Status: x.State}
	terminal := x.State == "confirmed" || x.State == "failed" || x.State == "cancelled"
	if x.State == "unknown" || (!terminal && x.Attempted) {
		r.Status = "awaiting-receipt"
	}
	r.Cancel = available && wenMiningCancelable(x)
	r.Pause = available && x.State == "queued"
	r.Reconcile = available && !terminal && x.Attempted
	return r
}

func validWENMiningState(state string) bool {
	switch state {
	case "queued", "paused", "validating", "prepared", "sending", "submitted", "unknown", "confirmed", "failed", "expired", "limit-held", "binding-held", "not-submitted", "cancelled":
		return true
	}
	return false
}

func validWENMiningTransition(a, b string) bool {
	switch a {
	case "queued":
		return b == "paused" || b == "validating" || b == "expired" || b == "limit-held" || b == "binding-held" || b == "cancelled"
	case "paused":
		return b == "queued" || b == "expired" || b == "cancelled"
	case "validating":
		return b == "prepared" || b == "expired" || b == "limit-held" || b == "binding-held"
	case "prepared":
		return b == "sending" || b == "expired" || b == "limit-held" || b == "binding-held"
	case "sending":
		return b == "submitted" || b == "unknown" || b == "not-submitted"
	case "submitted", "not-submitted":
		return b == "unknown" || b == "confirmed" || b == "failed"
	case "unknown":
		return b == "unknown" || b == "confirmed" || b == "failed"
	case "expired", "limit-held", "binding-held":
		return b == "cancelled"
	}
	return false
}

func (s *Store) wenMiningAccount(ctx context.Context, accountID, proposalID string, now int64) (Account, signer.AccountRef, string, *signer.FixtureProposal, error) {
	records, err := s.accountsRecord(ctx)
	if err != nil {
		return Account{}, signer.AccountRef{}, "", nil, err
	}
	var account *Account
	for i := range records.Accounts {
		if records.Accounts[i].ID == accountID {
			account = &records.Accounts[i]
			break
		}
	}
	if account == nil || account.Signer == nil || account.RPC == nil || account.RPC.Network != account.Network || !hexValue(account.PublicKey, 32) {
		return Account{}, signer.AccountRef{}, "", nil, errors.New("select an attached wallet with configured RPC")
	}
	live := s.fixtureView(ctx)
	if !live.Available || live.Status == nil || live.Status.Account == nil || *live.Status.Account != *account.Signer || live.Status.UpdateHeld || live.Status.WalletSigner || live.Status.FinancialExecution {
		return Account{}, signer.AccountRef{}, "", nil, errors.New("protected wallet signer identity unavailable or stale")
	}
	if account.Version == 1 && live.Status.RecoveryRequired {
		return Account{}, signer.AccountRef{}, "", nil, errors.New("wallet recovery required before mining authorization")
	}
	if account.Version == 4 && !s.controlledOrdinaryAccount(ctx, *account) {
		return Account{}, signer.AccountRef{}, "", nil, errors.New("controlled ordinary wallet admission required")
	}
	key, _ := hex.DecodeString(account.PublicKey)
	ownerAddress := onchain.Base58(key)
	var approved *signer.FixtureProposal
	if proposalID != "" {
		var draft *ProposalDraft
		for i := range records.Proposals {
			if records.Proposals[i].Proposal.ID == proposalID && records.Proposals[i].AccountID == account.ID {
				draft = &records.Proposals[i]
				break
			}
		}
		if draft == nil {
			return Account{}, signer.AccountRef{}, "", nil, errors.New("accepted wallet policy proposal not found")
		}
		reply, err := s.fixture(ctx, signer.Request{Action: "fixture-approvals", PublicInput: signer.PublicInput{ProposalIDs: []string{proposalID}}})
		if err != nil || len(reply.Approvals) != 1 {
			return Account{}, signer.AccountRef{}, "", nil, errors.New("signer approval verification unavailable")
		}
		receipt := reply.Approvals[0]
		digest := signer.ProposalDigest(draft.Proposal)
		if receipt.ProposalID != proposalID || receipt.State != "approved" || receipt.Digest != digest || draft.Proposal.Grant.Revoked || draft.Proposal.Grant.ExpiresAt <= now || draft.Proposal.Policy.ExpiresAt <= now {
			return Account{}, signer.AccountRef{}, "", nil, errors.New("accepted wallet policy approval is missing, revoked, or expired")
		}
		p := draft.Proposal
		approved = &p
	}
	return *account, *live.Status.Account, ownerAddress, approved, nil
}

func wenMiningWalletLedger(d agentDocument, accountID string, excludeTaskID string) (spent, unsettled, fees uint64) {
	for _, r := range d.Runs {
		if r.WENMining == nil || r.TaskID == excludeTaskID {
			continue
		}
		var task *AgentTask
		for i := range d.Tasks {
			if d.Tasks[i].ID == r.TaskID {
				task = &d.Tasks[i]
				break
			}
		}
		if task == nil || task.WENMining == nil || task.WENMining.Operation.Policy.AccountID != accountID {
			continue
		}
		amt, err := parseUintLamports(task.WENMining.Operation.AmountLamports)
		if err != nil {
			continue
		}
		fee, _ := parseUintLamports(task.WENMining.Operation.EstimatedFeeLamports)
		switch r.WENMining.State {
		case "confirmed":
			spent += amt
			fees += fee
		case "queued", "paused", "validating", "prepared", "sending", "submitted", "unknown":
			unsettled += amt
			fees += fee
		}
	}
	return spent, unsettled, fees
}

func checkWENMiningPolicyLimits(d agentDocument, op WENMiningOperation, doc wenstrategy.Document, excludeTaskID string, now int64) error {
	if now >= op.Policy.PolicyExpiresAt || now >= op.ExpiresAt {
		return errors.New("mining policy or review expired")
	}
	amount, err := parseUintLamports(op.AmountLamports)
	if err != nil || amount == 0 {
		return errors.New("positive lamport amount required")
	}
	budget, err := parseUintLamports(op.Policy.WalletBudgetLamports)
	if err != nil || budget == 0 {
		return errors.New("invalid wallet budget limit")
	}
	maxOp, err := parseUintLamports(op.Policy.MaxOperationLamports)
	if err != nil || maxOp == 0 || maxOp > budget {
		return errors.New("invalid per-operation wallet limit")
	}
	if amount > maxOp {
		return errors.New("operation exceeds wallet per-operation limit")
	}
	spent, unsettled, fees := wenMiningWalletLedger(d, op.Policy.AccountID, excludeTaskID)
	totalAfter := new(big.Int).Add(new(big.Int).SetUint64(spent), new(big.Int).SetUint64(unsettled))
	totalAfter.Add(totalAfter, new(big.Int).SetUint64(amount))
	if totalAfter.Cmp(new(big.Int).SetUint64(budget)) > 0 {
		return errors.New("operation exceeds accepted wallet lifetime budget")
	}
	fee, err := parseUintLamports(op.EstimatedFeeLamports)
	if err != nil {
		return errors.New("invalid estimated fee")
	}
	avail := uint64(0)
	if budget > spent+unsettled+fees {
		avail = budget - spent - unsettled - fees
	}
	state := wenstrategy.BudgetState{
		TotalTurnoverLamports:    strconv.FormatUint(spent+unsettled, 10),
		DailyTurnoverLamports:    strconv.FormatUint(spent+unsettled, 10),
		UnsettledLamports:        strconv.FormatUint(unsettled, 10),
		UserFeesPaidLamports:     strconv.FormatUint(fees, 10),
		UserFeeHoldsLamports:     "0",
		ActualDeductionsLamports: "0",
		DeductionHoldsLamports:   "0",
		AvailableLamports:        strconv.FormatUint(avail, 10),
	}
	res := wenstrategy.Reservation{
		PrincipalLamports:          op.AmountLamports,
		UserFeeLamports:            strconv.FormatUint(fee, 10),
		WorstCaseDeductionLamports: strconv.FormatUint(amount/10, 10),
	}
	return wenstrategy.CheckLimits(doc.Limits, state, res)
}

func (s *Store) loadWENMiningStrategy(ctx context.Context, profile, digest string) (WENVersion, wenstrategy.Document, error) {
	if !hexValue(digest, 32) {
		return WENVersion{}, wenstrategy.Document{}, errors.New("select a saved WEN strategy version")
	}
	lib, err := s.WENResearch(ctx, profile)
	if err != nil {
		return WENVersion{}, wenstrategy.Document{}, err
	}
	for _, v := range lib.Versions {
		if v.Digest == digest {
			accepted, err := wenstrategy.Decode([]byte(v.Raw))
			if err != nil || accepted.Digest != digest {
				return WENVersion{}, wenstrategy.Document{}, errors.New("saved WEN strategy contract failed validation")
			}
			if accepted.Document.Execution.Runner != "fased" {
				return WENVersion{}, wenstrategy.Document{}, errors.New("strategy runner must be fased")
			}
			return v, accepted.Document, nil
		}
	}
	return WENVersion{}, wenstrategy.Document{}, errors.New("saved WEN strategy version not found")
}

// BuildWENMiningPrepared builds deterministic unsigned PM1 v1 Open/Commit/Reveal
// instruction templates and commitment hash for a controlled WEN mining operation.
func BuildWENMiningPrepared(op WENMiningOperation) (WENMiningPrepared, uint64, error) {
	ledger, golden, err := wenpm1.CanonicalFixture()
	if err != nil {
		return WENMiningPrepared{}, 0, err
	}
	var selection uint8
	var amount uint64
	for i, raw := range op.AllocationsLamports {
		v, e := parseUintLamports(raw)
		if e != nil {
			return WENMiningPrepared{}, 0, e
		}
		if v > amount {
			amount = v
			selection = uint8(i)
		}
	}
	total, err := parseUintLamports(op.AmountLamports)
	if err != nil || total == 0 {
		return WENMiningPrepared{}, 0, errors.New("invalid operation amount")
	}
	salt := wenpm1.Key(sha256.Sum256([]byte("wen-mining-controlled-salt:" + op.ID)))
	nonceBytes := sha256.Sum256([]byte("wen-mining-controlled-nonce:" + op.ID))
	nonce := binary.LittleEndian.Uint64(nonceBytes[:8])
	slot := golden.Slot
	values := wenpm1.CommitmentValues{
		Slot:      slot,
		Owner:     golden.Owner,
		Amount:    total,
		Nonce:     nonce,
		Selection: selection,
		Salt:      salt,
	}
	commitment, err := wenpm1.Commitment(ledger.Binding, values)
	if err != nil {
		return WENMiningPrepared{}, 0, err
	}
	prepared := WENMiningPrepared{
		Commitment:   commitment.String(),
		Nonce:        nonce,
		Selection:    selection,
		Instructions: []WENPaperInstruction{},
	}
	requests := []wenpm1.Request{
		wenpm1.Open{Slot: slot},
		wenpm1.Commit{Slot: slot, Amount: total, Nonce: nonce, Commitment: commitment},
		wenpm1.Reveal{Slot: slot, Entry: 0, Selection: selection, Salt: salt},
	}
	names := []string{"open", "commit", "reveal"}
	for n, req := range requests {
		accounts := map[string]wenpm1.Key{}
		if n == 1 {
			accounts["owner"] = golden.Owner
			accounts["system"] = wenpm1.SystemProgram()
		}
		tmpl, e := wenpm1.BuildInstruction(ledger.Binding, req, accounts)
		if e != nil {
			return WENMiningPrepared{}, 0, e
		}
		if _, e = wenpm1.DecodeRequest(tmpl.Data, ledger.Binding); e != nil {
			return WENMiningPrepared{}, 0, e
		}
		b, e := json.Marshal(tmpl)
		if e != nil {
			return WENMiningPrepared{}, 0, e
		}
		prepared.Instructions = append(prepared.Instructions, WENPaperInstruction{
			Operation:      names[n],
			RequestSHA256:  wenHash(tmpl.Data),
			TemplateSHA256: wenHash(b),
		})
	}
	return prepared, slot, nil
}

// ProjectWENMiningReceipt builds and validates a canonical PM1 v1 receipt projection
// for controlled test reconciliation. Never invents payouts or settlement rewards.
func ProjectWENMiningReceipt(op WENMiningOperation, prepared WENMiningPrepared) (WENMiningObservation, error) {
	ledger, golden, err := wenpm1.CanonicalFixture()
	if err != nil {
		return WENMiningObservation{}, err
	}
	total, err := parseUintLamports(op.AmountLamports)
	if err != nil {
		return WENMiningObservation{}, err
	}
	salt := wenpm1.Key(sha256.Sum256([]byte("wen-mining-controlled-salt:" + op.ID)))
	commitment, err := wenpm1.Commitment(ledger.Binding, wenpm1.CommitmentValues{
		Slot:      op.Slot,
		Owner:     golden.Owner,
		Amount:    total,
		Nonce:     prepared.Nonce,
		Selection: prepared.Selection,
		Salt:      salt,
	})
	if err != nil || commitment.String() != prepared.Commitment {
		return WENMiningObservation{}, errors.New("commitment mismatch")
	}
	sel := prepared.Selection
	ledger.EligibleStake = 0
	ledger.LifetimeCommitted = 0
	ledger.Annual = wenpm1.Annual{}
	ledger.Rounds = []wenpm1.Round{{
		Slot:       op.Slot,
		Entries:    []wenpm1.Entry{{Owner: golden.Owner, Nonce: prepared.Nonce, Amount: total, Commitment: commitment, Selection: &sel}},
		TotalStake: total,
	}}
	data, err := wenpm1.EncodeAccount(ledger)
	if err != nil {
		return WENMiningObservation{}, err
	}
	decoded, err := wenpm1.ReadAccount(ledger.Binding.Ledger, ledger.Binding.Program, data, ledger.Binding)
	if err != nil {
		return WENMiningObservation{}, err
	}
	receipt, err := wenpm1.ProjectReceipt(decoded, op.Slot, 0)
	if err != nil {
		return WENMiningObservation{}, err
	}
	rawReceipt, err := wenpm1.EncodeReceipt(receipt)
	if err != nil {
		return WENMiningObservation{}, err
	}
	verified, err := wenpm1.DecodeReceipt(rawReceipt, ledger.Binding)
	if err != nil {
		return WENMiningObservation{}, err
	}
	return WENMiningObservation{
		State:         "confirmed",
		Slot:          op.Slot,
		AccountSHA256: wenHash(data),
		ReceiptSHA256: wenHash(rawReceipt),
		Receipt:       &verified,
	}, nil
}

func verifyWENMiningObservation(op WENMiningOperation, prepared WENMiningPrepared, obs WENMiningObservation) error {
	if obs.State != "confirmed" && obs.State != "failed" {
		return errors.New("non-terminal observation")
	}
	if obs.State == "failed" {
		if obs.Receipt != nil || obs.ReceiptSHA256 != "" {
			return errors.New("failed observation carries a confirmed receipt")
		}
		return nil
	}
	if obs.Receipt == nil || !hexValue(obs.ReceiptSHA256, 32) || !hexValue(obs.AccountSHA256, 32) || obs.Slot != op.Slot {
		return errors.New("confirmed mining receipt is incomplete")
	}
	ledger, _, err := wenpm1.CanonicalFixture()
	if err != nil {
		return err
	}
	raw, err := wenpm1.EncodeReceipt(*obs.Receipt)
	if err != nil || wenHash(raw) != obs.ReceiptSHA256 {
		return errors.New("mining receipt digest mismatch")
	}
	if _, err := wenpm1.DecodeReceipt(raw, ledger.Binding); err != nil {
		return errors.New("mining receipt failed canonical PM1 validation")
	}
	total, err := parseUintLamports(op.AmountLamports)
	if err != nil || obs.Receipt.Amount != total || obs.Receipt.Slot != op.Slot || obs.Receipt.Nonce != prepared.Nonce || obs.Receipt.Selection == nil || *obs.Receipt.Selection != prepared.Selection {
		return errors.New("mining receipt does not match approved operation")
	}
	return nil
}

func (s *Store) wenMiningView(ctx context.Context, profile, id string) (WENMiningView, error) {
	d, rev, err := s.swapSnapshot(ctx, profile)
	if err != nil {
		return WENMiningView{}, err
	}
	v := s.wenMiningProjection(d, rev, profile, s.wenMining != nil)
	if id == "" || s.wenMining == nil {
		return v, nil
	}
	entry, ok := s.wenMining.reviews[id]
	if !ok || entry.review.Operation.ProfileID != profile || entry.boot != s.bootID {
		return v, errors.New("mining review unavailable; prepare a fresh review")
	}
	review := entry.review
	v.Review = &review
	return v, nil
}

func (s *Store) changeWENMining(ctx context.Context, c AgentCommand) (WENMiningView, error) {
	unexpected := c
	unexpected.Action, unexpected.ProfileID = "", ""
	unexpected.Revision = 0
	unexpected.WENMining = nil
	if unexpected != (AgentCommand{}) || c.WENMining == nil {
		return WENMiningView{}, errors.New("one scoped WEN mining command required")
	}
	if s.wenMining == nil {
		if c.Action == "wen-mining-view" {
			return s.wenMiningView(ctx, c.ProfileID, "")
		}
		return WENMiningView{}, errors.New("WEN mining activation is disabled")
	}
	s.wenMining.mu.Lock()
	defer s.wenMining.mu.Unlock()
	if c.Action == "wen-mining-view" {
		return s.wenMiningView(ctx, c.ProfileID, c.WENMining.ID)
	}
	d, revision, err := s.swapSnapshot(ctx, c.ProfileID)
	if err != nil {
		return WENMiningView{}, err
	}
	if revision != c.Revision {
		return WENMiningView{}, ErrConflict
	}
	var phase string
	if err = s.db.QueryRowContext(ctx, "SELECT phase FROM update_pause WHERE id=1").Scan(&phase); err != nil {
		return WENMiningView{}, err
	}
	if phase == "paused" {
		return WENMiningView{}, ErrCorePaused
	}
	now := s.now().Unix()
	switch c.Action {
	case "wen-mining-review":
		req := c.WENMining.Request
		if req == nil || c.WENMining.ID != "" || c.WENMining.Digest != "" {
			return WENMiningView{}, errors.New("wallet, strategy, amount, and policy limits required")
		}
		for id, entry := range s.wenMining.reviews {
			if entry.review.Operation.ExpiresAt <= now {
				delete(s.wenMining.reviews, id)
			}
		}
		if len(s.wenMining.reviews) >= 8 {
			return WENMiningView{}, errors.New("too many active mining reviews")
		}
		account, signerRef, ownerAddress, proposal, err := s.wenMiningAccount(ctx, req.AccountID, req.ProposalID, now)
		if err != nil {
			return WENMiningView{}, err
		}
		version, doc, err := s.loadWENMiningStrategy(ctx, c.ProfileID, req.StrategyDigest)
		if err != nil {
			return WENMiningView{}, err
		}
		allocations, err := wenstrategy.CompileAllocation(req.AmountLamports, doc.Allocation.WeightsBps)
		if err != nil {
			return WENMiningView{}, err
		}
		walletBudget := req.WalletBudgetLamports
		maxOp := req.MaxOperationLamports
		policyExp := req.PolicyExpiresAt
		proposalDigest := ""
		if proposal != nil {
			proposalDigest = signer.ProposalDigest(*proposal)
			budgetVal := proposal.Policy.Budget
			if proposal.Grant.Budget < budgetVal {
				budgetVal = proposal.Grant.Budget
			}
			walletBudget = strconv.FormatUint(budgetVal, 10)
			maxOp = strconv.FormatUint(proposal.Grant.MaxOperation, 10)
			policyExp = proposal.Grant.ExpiresAt
			if proposal.Policy.ExpiresAt < policyExp {
				policyExp = proposal.Policy.ExpiresAt
			}
		}
		if policyExp <= now || policyExp > now+86400*7 {
			return WENMiningView{}, errors.New("policy expiry must be bounded in the future")
		}
		var weights [5]int
		copy(weights[:], doc.Allocation.WeightsBps)
		op := WENMiningOperation{
			ID:             randomTokenHex(),
			ProfileID:      c.ProfileID,
			InstanceID:     s.instanceID,
			StrategyDigest: version.Digest,
			StrategyName:   version.Name,
			Market:         doc.Market,
			Policy: WENMiningWalletPolicy{
				AccountID:            account.ID,
				Network:              account.Network,
				PublicKey:            account.PublicKey,
				OwnerAddress:         ownerAddress,
				Signer:               signerRef,
				RPCSHA256:            swapRPCBinding(account),
				ProposalID:           req.ProposalID,
				ProposalDigest:       proposalDigest,
				WalletBudgetLamports: walletBudget,
				MaxOperationLamports: maxOp,
				PolicyExpiresAt:      policyExp,
			},
			AmountLamports:       req.AmountLamports,
			AllocationsLamports:  allocations,
			WeightsBps:           weights,
			EstimatedFeeLamports: "5000",
			FixtureSource:        wenpm1.FixtureSource,
			FixtureSHA256:        wenpm1.FixtureSHA256,
			CreatedAt:            now,
			ExpiresAt:            now + 120,
		}
		venue, err := s.wenMining.venue(account)
		if err != nil {
			return WENMiningView{}, err
		}
		prepared, slot, feeLamports, err := venue.Prepare(ctx, op)
		if err != nil {
			return WENMiningView{}, err
		}
		op.Slot = slot
		if feeLamports != "" {
			op.EstimatedFeeLamports = feeLamports
		}
		if err = checkWENMiningPolicyLimits(d, op, doc, "", now); err != nil {
			return WENMiningView{}, err
		}
		review := WENMiningReview{Operation: op, Prepared: prepared, Revision: revision}
		review.Digest = wenMiningReviewDigest(review, s.bootID)
		s.wenMining.reviews[op.ID] = wenMiningEntry{review: review, boot: s.bootID}
		v := s.wenMiningProjection(d, revision, c.ProfileID, true)
		v.Review = &review
		return v, nil

	case "wen-mining-submit":
		if c.WENMining.Request != nil || c.WENMining.ID == "" || c.WENMining.Digest == "" {
			return WENMiningView{}, errors.New("reviewed mining operation and digest required")
		}
		entry, ok := s.wenMining.reviews[c.WENMining.ID]
		if !ok || entry.boot != s.bootID || entry.review.Operation.ProfileID != c.ProfileID || entry.review.Revision != revision || c.WENMining.Digest != entry.review.Digest || entry.review.Operation.ExpiresAt <= now {
			return WENMiningView{}, errors.New("mining review changed or expired; prepare a fresh review")
		}
		op := entry.review.Operation
		account, signerRef, _, _, err := s.wenMiningAccount(ctx, op.Policy.AccountID, op.Policy.ProposalID, now)
		if err != nil || signerRef != op.Policy.Signer || swapRPCBinding(account) != op.Policy.RPCSHA256 {
			return WENMiningView{}, errors.New("wallet identity or RPC changed; prepare a fresh review")
		}
		_, doc, err := s.loadWENMiningStrategy(ctx, c.ProfileID, op.StrategyDigest)
		if err != nil {
			return WENMiningView{}, err
		}
		if err = checkWENMiningPolicyLimits(d, op, doc, "", now); err != nil {
			return WENMiningView{}, err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return WENMiningView{}, err
		}
		defer tx.Rollback()
		d, r, err := readAgents(ctx, tx)
		if err != nil {
			return WENMiningView{}, err
		}
		if r != revision {
			return WENMiningView{}, ErrConflict
		}
		taskID := wenMiningPrefix + op.ID
		for _, task := range d.Tasks {
			if task.ID == taskID {
				return WENMiningView{}, errors.New("mining review already used")
			}
		}
		if len(d.Tasks) >= 100 || len(d.Runs) >= 190 || len(d.Notices) >= 190 {
			return WENMiningView{}, errors.New("task/result capacity reached")
		}
		policy := &WENMiningTaskPolicy{
			Version:   wenMiningTaskVersion,
			Operation: op,
			Prepared:  entry.review.Prepared,
		}
		policy.Digest = wenMiningPolicyDigest(*policy)
		task := AgentTask{
			ID:              taskID,
			ProfileID:       c.ProfileID,
			Name:            "WEN " + op.StrategyName,
			IntervalSeconds: 60,
			MaxRuns:         1,
			Runs:            1,
			State:           "held",
			WENMining:       policy,
		}
		run := AgentRun{
			ID:        taskID + ":1",
			TaskID:    taskID,
			ProfileID: c.ProfileID,
			Strategy:  wenMiningTaskVersion,
			At:        now,
			Outcome:   "wen-mining-queued",
			WENMining: &WENMiningExecution{
				Version:         wenMiningTaskVersion,
				OperationDigest: policy.Digest,
				BootID:          s.bootID,
			},
		}
		wenMiningEvent(run.WENMining, "queued", now)
		d.Tasks = append(d.Tasks, task)
		d.Runs = append(d.Runs, run)
		if err = saveAgents(ctx, tx, d, r); err != nil {
			return WENMiningView{}, err
		}
		if err = tx.Commit(); err != nil {
			return WENMiningView{}, err
		}
		delete(s.wenMining.reviews, op.ID)
		return s.wenMiningView(ctx, c.ProfileID, "")

	case "wen-mining-cancel":
		if c.WENMining.Request != nil || c.WENMining.Digest != "" {
			return WENMiningView{}, errors.New("one unattempted mining operation required")
		}
		_, run, err := s.originalWENMining(ctx, c.ProfileID, c.WENMining.ID)
		if err != nil {
			return WENMiningView{}, err
		}
		if !wenMiningCancelable(*run.WENMining) {
			return WENMiningView{}, errors.New("attempted mining operations require reconciliation")
		}
		if err = s.writeWENMining(ctx, c.ProfileID, c.WENMining.ID, func(_ *AgentTask, r *AgentRun) error {
			wenMiningEvent(r.WENMining, "cancelled", s.now().Unix())
			return nil
		}); err != nil {
			return WENMiningView{}, err
		}
		return s.wenMiningView(ctx, c.ProfileID, "")

	case "wen-mining-reconcile":
		if c.WENMining.Request != nil || c.WENMining.Digest != "" {
			return WENMiningView{}, errors.New("one original mining operation required")
		}
		if err = s.reconcileWENMining(ctx, c.ProfileID, c.WENMining.ID); err != nil {
			return WENMiningView{}, err
		}
		return s.wenMiningView(ctx, c.ProfileID, "")

	default:
		return WENMiningView{}, errors.New("unsupported WEN mining command")
	}
}

func (s *Store) controlWENMiningTask(ctx context.Context, c AgentCommand) (AgentView, error) {
	unexpected := c
	unexpected.Action, unexpected.ProfileID, unexpected.ID = "", "", ""
	unexpected.Revision = 0
	if unexpected != (AgentCommand{}) || s.wenMining == nil {
		return AgentView{}, errors.New("one available WEN mining task required")
	}
	s.wenMining.mu.Lock()
	defer s.wenMining.mu.Unlock()
	_, revision, err := s.swapSnapshot(ctx, c.ProfileID)
	if err != nil {
		return AgentView{}, err
	}
	if revision != c.Revision {
		return AgentView{}, ErrConflict
	}
	err = s.writeWENMining(ctx, c.ProfileID, c.ID, func(t *AgentTask, r *AgentRun) error {
		switch c.Action {
		case "task-pause":
			if r.WENMining.State != "queued" {
				return errors.New("attempted or terminal mining operation cannot pause")
			}
			t.State = "paused"
			wenMiningEvent(r.WENMining, "paused", s.now().Unix())
		case "task-resume", "task-run":
			if r.WENMining.State != "paused" || r.WENMining.BootID != s.bootID || s.now().Unix() >= t.WENMining.Operation.ExpiresAt {
				return errors.New("prepare a fresh mining review; never replay an expired or prior-boot operation")
			}
			t.State = "held"
			wenMiningEvent(r.WENMining, "queued", s.now().Unix())
		}
		return nil
	})
	if err != nil {
		return AgentView{}, err
	}
	return s.Agents(ctx, c.ProfileID, "")
}

func findWENMining(d *agentDocument, profile, id string) (int, int, error) {
	task, run := -1, -1
	for i, t := range d.Tasks {
		if t.ProfileID == profile && t.ID == id && t.WENMining != nil {
			task = i
		}
	}
	for i, r := range d.Runs {
		if r.ProfileID == profile && r.TaskID == id && r.WENMining != nil {
			run = i
		}
	}
	if task < 0 || run < 0 {
		return 0, 0, errors.New("WEN mining operation not found")
	}
	return task, run, nil
}

func (s *Store) originalWENMining(ctx context.Context, profile, id string) (AgentTask, AgentRun, error) {
	d, _, err := s.swapSnapshot(ctx, profile)
	if err != nil {
		return AgentTask{}, AgentRun{}, err
	}
	ti, ri, err := findWENMining(&d, profile, id)
	if err != nil {
		return AgentTask{}, AgentRun{}, err
	}
	return d.Tasks[ti], d.Runs[ri], nil
}

func (s *Store) writeWENMining(ctx context.Context, profile, id string, change func(*AgentTask, *AgentRun) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var phase string
	if err = tx.QueryRowContext(ctx, "SELECT phase FROM update_pause WHERE id=1").Scan(&phase); err != nil {
		return err
	}
	if phase == "paused" {
		return ErrCorePaused
	}
	d, revision, err := readAgents(ctx, tx)
	if err != nil {
		return err
	}
	ti, ri, err := findWENMining(&d, profile, id)
	if err != nil {
		return err
	}
	if err = change(&d.Tasks[ti], &d.Runs[ri]); err != nil {
		return err
	}
	if len(d.Runs[ri].WENMining.Events) > 16 {
		return errors.New("mining reconciliation history capacity reached")
	}
	state := d.Runs[ri].WENMining.State
	d.Runs[ri].Outcome = "wen-mining-" + state
	if state == "cancelled" {
		d.Tasks[ti].State = "completed"
	}
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
			d.Notices = append(d.Notices, AgentNotice{
				ID:        noticeID,
				ProfileID: profile,
				RunID:     noticeID,
				At:        s.now().Unix(),
				Text:      d.Tasks[ti].Name + ": " + state,
			})
		}
	}
	if err = saveAgents(ctx, tx, d, revision); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) advanceWENMining(ctx context.Context) error {
	if s.wenMining == nil {
		return nil
	}
	s.wenMining.mu.Lock()
	defer s.wenMining.mu.Unlock()
	d, _, err := readAgents(ctx, s.db)
	if err != nil {
		return err
	}
	for _, r := range d.Runs {
		if r.WENMining == nil || r.WENMining.State != "queued" || r.WENMining.BootID != s.bootID {
			continue
		}
		t, _, err := s.originalWENMining(ctx, r.ProfileID, r.TaskID)
		if err != nil {
			return err
		}
		op := t.WENMining.Operation
		prepared := t.WENMining.Prepared
		set := func(state string) error {
			return s.writeWENMining(ctx, r.ProfileID, r.TaskID, func(_ *AgentTask, run *AgentRun) error {
				wenMiningEvent(run.WENMining, state, s.now().Unix())
				return nil
			})
		}
		now := s.now().Unix()
		if now >= op.ExpiresAt || now >= op.Policy.PolicyExpiresAt {
			return set("expired")
		}
		account, signerRef, _, _, err := s.wenMiningAccount(ctx, op.Policy.AccountID, op.Policy.ProposalID, now)
		if err != nil || signerRef != op.Policy.Signer || swapRPCBinding(account) != op.Policy.RPCSHA256 {
			return set("binding-held")
		}
		_, doc, err := s.loadWENMiningStrategy(ctx, r.ProfileID, op.StrategyDigest)
		if err != nil {
			return set("binding-held")
		}
		if err = checkWENMiningPolicyLimits(d, op, doc, r.TaskID, now); err != nil {
			return set("limit-held")
		}
		if err = set("validating"); err != nil {
			return err
		}
		venue, err := s.wenMining.venue(account)
		if err != nil {
			return set("binding-held")
		}
		if err = set("prepared"); err != nil {
			return err
		}
		// Persist Attempted=true BEFORE calling Submit so a crash/restart
		// never permits a second dispatch of the same operation.
		if err = s.writeWENMining(ctx, r.ProfileID, r.TaskID, func(_ *AgentTask, run *AgentRun) error {
			run.WENMining.Attempted = true
			wenMiningEvent(run.WENMining, "sending", s.now().Unix())
			return nil
		}); err != nil {
			return err
		}
		attempted, err := venue.Submit(ctx, op, prepared)
		if err != nil {
			if attempted {
				return set("unknown")
			}
			return set("not-submitted")
		}
		if err = set("submitted"); err != nil {
			return err
		}
		return s.reconcileWENMining(ctx, r.ProfileID, r.TaskID)
	}
	return nil
}

func (s *Store) reconcileWENMining(ctx context.Context, profile, id string) error {
	t, r, err := s.originalWENMining(ctx, profile, id)
	if err != nil {
		return err
	}
	if r.WENMining.State == "confirmed" || r.WENMining.State == "failed" || r.WENMining.State == "cancelled" {
		return nil
	}
	if !r.WENMining.Attempted {
		return errors.New("unattempted operation cannot reconcile; cancel or prepare a fresh review")
	}
	op := t.WENMining.Operation
	records, err := s.accountsRecord(ctx)
	if err != nil {
		return err
	}
	var account *Account
	for i := range records.Accounts {
		if records.Accounts[i].ID == op.Policy.AccountID {
			account = &records.Accounts[i]
			break
		}
	}
	if account == nil || account.Signer == nil || account.Signer.PublicKey != op.Policy.PublicKey {
		return errors.New("original wallet record unavailable for reconciliation")
	}
	venue, err := s.wenMining.venue(*account)
	if err != nil {
		return err
	}
	obs, err := venue.Reconcile(ctx, op, t.WENMining.Prepared)
	if err != nil {
		return err
	}
	if obs.State != "confirmed" && obs.State != "failed" {
		return s.writeWENMining(ctx, profile, id, func(_ *AgentTask, run *AgentRun) error {
			if run.WENMining.State != "unknown" {
				wenMiningEvent(run.WENMining, "unknown", s.now().Unix())
			}
			return nil
		})
	}
	if err = verifyWENMiningObservation(op, t.WENMining.Prepared, obs); err != nil {
		return err
	}
	return s.writeWENMining(ctx, profile, id, func(_ *AgentTask, run *AgentRun) error {
		o := obs
		run.WENMining.Observation = &o
		wenMiningEvent(run.WENMining, obs.State, s.now().Unix())
		return nil
	})
}

func (s *Store) recoverWENMining(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	phase, _, err := readPause(ctx, tx)
	if err != nil {
		return err
	}
	if phase == "paused" {
		return nil
	}
	d, revision, err := readAgents(ctx, tx)
	if err != nil {
		return err
	}
	changed := false
	for i := range d.Runs {
		r := &d.Runs[i]
		if r.WENMining == nil {
			continue
		}
		switch r.WENMining.State {
		case "queued", "validating", "prepared":
			wenMiningEvent(r.WENMining, "expired", s.now().Unix())
			r.Outcome = "wen-mining-expired"
			changed = true
		case "sending", "submitted":
			wenMiningEvent(r.WENMining, "unknown", s.now().Unix())
			r.Outcome = "wen-mining-unknown"
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if err = saveAgents(ctx, tx, d, revision); err != nil {
		return err
	}
	return tx.Commit()
}

func validateWENMiningStorage(d agentDocument) error {
	tasks := map[string]AgentTask{}
	for _, t := range d.Tasks {
		tag := strings.HasPrefix(t.ID, wenMiningPrefix)
		if tag != (t.WENMining != nil) {
			return errors.New("WEN mining task policy erased")
		}
		if t.WENMining == nil {
			continue
		}
		p := t.WENMining
		op := p.Operation
		if p.Version != wenMiningTaskVersion || p.Digest != wenMiningPolicyDigest(*p) || op.ProfileID != t.ProfileID || t.ID != wenMiningPrefix+op.ID || t.MaxRuns != 1 || t.Runs != 1 || t.NextAt != 0 || t.AI != nil || t.News != nil || t.Calendar != nil || t.Watch != nil || t.WEN != nil || t.Swap != nil || t.StrategyPlan != nil || t.BotID != "" || t.Playbook != nil || (t.State != "held" && t.State != "completed" && t.State != "paused") {
			return errors.New("WEN mining task binding changed")
		}
		if _, ok := tasks[t.ID]; ok {
			return errors.New("duplicate WEN mining task")
		}
		tasks[t.ID] = t
	}
	seen := map[string]bool{}
	for _, run := range d.Runs {
		tag := strings.HasPrefix(run.TaskID, wenMiningPrefix)
		if tag != (run.WENMining != nil) {
			return errors.New("WEN mining result erased")
		}
		if run.WENMining == nil {
			continue
		}
		t, ok := tasks[run.TaskID]
		x := run.WENMining
		if !ok || seen[run.TaskID] || run.ID != run.TaskID+":1" || run.ProfileID != t.ProfileID || run.Strategy != wenMiningTaskVersion || run.Outcome != "wen-mining-"+x.State || x.Version != wenMiningTaskVersion || x.OperationDigest != t.WENMining.Digest || len(x.Events) < 1 || len(x.Events) > 16 || run.AIRequestID != "" || run.Paper != nil || run.WEN != nil || run.Swap != nil || run.BotID != "" || run.News != nil || run.Watch != nil || run.Analysis != nil {
			return errors.New("WEN mining result binding changed")
		}
		previous := ""
		at := int64(0)
		for i, ev := range x.Events {
			if !validWENMiningState(ev.State) || (i > 0 && !validWENMiningTransition(x.Events[i-1].State, ev.State)) {
				return errors.New("invalid WEN mining state transition")
			}
			hash := ev.Digest
			ev.Digest = ""
			if ev.At < at || ev.Previous != previous || hash != wenMiningHash(ev) {
				return errors.New("WEN mining event history changed")
			}
			at = ev.At
			previous = hash
		}
		if !validWENMiningState(x.State) || x.Events[0].State != "queued" || x.Events[len(x.Events)-1].State != x.State {
			return errors.New("WEN mining event state differs")
		}
		if x.State == "cancelled" && (x.Attempted || x.Observation != nil) {
			return errors.New("cancelled WEN mining operation was attempted")
		}
		if x.State == "confirmed" || x.State == "failed" {
			if t.State != "completed" || x.Observation == nil || x.Observation.State != x.State || !x.Attempted {
				return errors.New("WEN mining terminal receipt missing")
			}
			if err := verifyWENMiningObservation(t.WENMining.Operation, t.WENMining.Prepared, *x.Observation); err != nil {
				return err
			}
		} else if x.Observation != nil {
			return errors.New("non-terminal WEN mining observation")
		} else if t.State == "completed" && x.State != "cancelled" {
			return errors.New("unconfirmed WEN mining task marked complete")
		}
		seen[run.TaskID] = true
	}
	for id := range tasks {
		if !seen[id] {
			return errors.New("WEN mining result missing")
		}
	}
	return nil
}

func wenMiningFutureStorage(d agentDocument) int {
	n := 0
	for _, r := range d.Runs {
		if r.WENMining != nil && r.WENMining.State != "confirmed" && r.WENMining.State != "failed" && r.WENMining.State != "cancelled" {
			n += 12000
		}
	}
	return n
}

func hasWENMiningRun(document agentDocument) bool {
	for _, run := range document.Runs {
		if run.WENMining != nil {
			return true
		}
	}
	return false
}
