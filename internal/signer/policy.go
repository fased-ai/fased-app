package signer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

const FixtureAsset = "fixture-credit:v1"

var ErrHeld = errors.New("fixture operation held; reconcile its original identity before new execution")

// All amounts are unsigned integer fixture units, encoded as strings to avoid
// JSON/JavaScript rounding. These types deliberately cannot describe a real
// chain transaction, arbitrary bytes to sign, RPC URL or executable strategy.
type FixturePolicy struct {
	Account      AccountRef `json:"account"`
	Asset        string     `json:"asset"`
	Budget       uint64     `json:"budget,string"`
	MaxOperation uint64     `json:"maxOperation,string"`
	Destinations []string   `json:"destinations"`
	ExpiresAt    int64      `json:"expiresAt"`
}

type FixtureGrant struct {
	Account      AccountRef `json:"account"`
	ID           string     `json:"id"`
	BotID        string     `json:"botId"`
	Asset        string     `json:"asset"`
	Budget       uint64     `json:"budget,string"`
	MaxOperation uint64     `json:"maxOperation,string"`
	Destinations []string   `json:"destinations"`
	ExpiresAt    int64      `json:"expiresAt"`
	Revoked      bool       `json:"revoked"`
}

type FixtureIntent struct {
	Account     AccountRef `json:"account"`
	OperationID string     `json:"operationId"`
	GrantID     string     `json:"grantId"`
	BotID       string     `json:"botId"`
	Action      string     `json:"action"`
	Asset       string     `json:"asset"`
	Destination string     `json:"destination"`
	Units       uint64     `json:"units,string"`
	ExpiresAt   int64      `json:"expiresAt"`
}

type FixtureOperation struct {
	Intent       FixtureIntent `json:"intent"`
	State        string        `json:"state"`
	Original     []byte        `json:"original,omitempty"`
	OriginalHash string        `json:"originalHash,omitempty"`
}

type FixtureSummary struct {
	Account            AccountRef     `json:"account"`
	FixtureOnly        bool           `json:"fixtureOnly"`
	FinancialExecution bool           `json:"financialExecution"`
	MissingHistory     bool           `json:"missingHistory"`
	RecoveryRequired   bool           `json:"recoveryRequired"`
	Policy             *FixturePolicy `json:"policy,omitempty"`
	GrantCount         int            `json:"grantCount"`
	OperationCount     int            `json:"operationCount"`
	Spent              uint64         `json:"spent,string"`
	Held               uint64         `json:"held,string"`
}

// PublicInput is the only JSON accepted by the helper's --request-stdin flag.
// Credentials, encrypted backups and authority booleans have no fields here.
type PublicInput struct {
	Account     *AccountRef    `json:"account,omitempty"`
	Policy      *FixturePolicy `json:"policy,omitempty"`
	Grant       *FixtureGrant  `json:"grant,omitempty"`
	Intent      *FixtureIntent `json:"intent,omitempty"`
	GrantID     string         `json:"grantId,omitempty"`
	OperationID string         `json:"operationId,omitempty"`
}

type ledger struct {
	main, mock     *journal
	ref            AccountRef
	missingHistory bool
	policy         *FixturePolicy
	grants         map[string]FixtureGrant
	ops            map[string]FixtureOperation
	receipts       map[string]journalRecord
	lastTime       int64
}

func newLedger() *ledger {
	return &ledger{grants: map[string]FixtureGrant{}, ops: map[string]FixtureOperation{}, receipts: map[string]journalRecord{}}
}

func validRef(r AccountRef) bool {
	b, err := hex.DecodeString(r.PublicKey)
	return r.Version == 1 && r.Network == FixtureNetwork && validID(r.SignerID) && validID(r.WalletID) && validID(r.Epoch) && err == nil && len(b) == 32 && r.PublicKey == hex.EncodeToString(b)
}
func sameWallet(a, b AccountRef) bool {
	return a.Version == b.Version && a.WalletID == b.WalletID && a.Network == b.Network && a.PublicKey == b.PublicKey
}
func allowedDestination(s string) bool { return s == "fixture:treasury" || s == "fixture:reject" }
func destinationsValid(d []string) bool {
	if len(d) < 1 || len(d) > 2 {
		return false
	}
	seen := map[string]bool{}
	for _, s := range d {
		if !allowedDestination(s) || seen[s] {
			return false
		}
		seen[s] = true
	}
	return true
}
func contains(d []string, s string) bool {
	for _, v := range d {
		if v == s {
			return true
		}
	}
	return false
}
func subset(a, b []string) bool {
	for _, s := range a {
		if !contains(b, s) {
			return false
		}
	}
	return true
}
func terminal(s string) bool { return s == "confirmed" || s == "rejected" || s == "not-submitted" }
func original(i FixtureIntent) ([]byte, string) {
	b, _ := json.Marshal(i)
	h := sha256.Sum256(append([]byte("fased-fixture-original:v1\x00"), b...))
	return b, hex.EncodeToString(h[:])
}
func clone[T any](v T) T {
	b, _ := json.Marshal(v)
	var c T
	if json.Unmarshal(b, &c) != nil {
		panic("fixture clone")
	}
	return c
}

func (l *ledger) totals(grantID string) (spent, held uint64, err error) {
	for _, op := range l.ops {
		if grantID != "" && op.Intent.GrantID != grantID {
			continue
		}
		n := op.Intent.Units
		switch op.State {
		case "confirmed":
			if ^uint64(0)-spent < n {
				return 0, 0, errors.New("fixture accounting overflow")
			}
			spent += n
		case "reserved", "prepared", "sending":
			if ^uint64(0)-held < n {
				return 0, 0, errors.New("fixture accounting overflow")
			}
			held += n
		case "rejected", "not-submitted":
		default:
			return 0, 0, errors.New("unknown fixture operation state")
		}
	}
	return
}
func within(budget, amount, spent, held uint64) bool {
	return spent <= budget && held <= budget-spent && amount <= budget-spent-held
}

func (l *ledger) authorize(i FixtureIntent, at int64) error {
	p := l.policy
	g, ok := l.grants[i.GrantID]
	if l.missingHistory || p == nil || !ok || g.Revoked || i.Account != l.ref || g.Account != l.ref || p.Account != l.ref ||
		!validID(i.OperationID) || !validID(i.BotID) || i.BotID != g.BotID || i.Action != "fixture-transfer" || i.Asset != FixtureAsset || i.Asset != g.Asset || i.Asset != p.Asset ||
		i.Units == 0 || i.Units > g.MaxOperation || i.Units > p.MaxOperation || !contains(g.Destinations, i.Destination) || !contains(p.Destinations, i.Destination) ||
		i.ExpiresAt <= at || i.ExpiresAt > g.ExpiresAt || g.ExpiresAt <= at || p.ExpiresAt <= at {
		return errors.New("fixture operation exceeds independent policy or grant")
	}
	return nil
}

// apply validates the transition as well as its recorded time. Replay uses the
// same checks as live append; no persisted app counter is trusted.
func (l *ledger) apply(r journalRecord) error {
	if r.At < l.lastTime {
		return errors.New("fixture clock moved backwards")
	}
	switch r.Kind {
	case "init":
		if l.ref.Version != 0 || r.Account == nil || !validRef(*r.Account) {
			return errors.New("invalid journal identity")
		}
		l.ref = *r.Account
		l.missingHistory = r.MissingHistory
	case "policy":
		p := r.Policy
		if p == nil || p.Account != l.ref || p.Asset != FixtureAsset || p.Budget == 0 || p.MaxOperation == 0 || p.MaxOperation > p.Budget || !destinationsValid(p.Destinations) || p.ExpiresAt <= r.At || l.missingHistory {
			return errors.New("invalid fixture wallet policy")
		}
		s, h, e := l.totals("")
		if e != nil || !within(p.Budget, 0, s, h) {
			return errors.New("policy cannot roll back spent or reserved budget")
		}
		l.policy = clone(p)
	case "grant":
		g := r.Grant
		p := l.policy
		if g == nil || p == nil || len(l.grants) >= 256 || g.Account != l.ref || !validID(g.ID) || !validID(g.BotID) || g.Revoked || g.Asset != FixtureAsset ||
			g.Budget == 0 || g.Budget > p.Budget || g.MaxOperation == 0 || g.MaxOperation > g.Budget || g.MaxOperation > p.MaxOperation || !destinationsValid(g.Destinations) || !subset(g.Destinations, p.Destinations) || g.ExpiresAt <= r.At || g.ExpiresAt > p.ExpiresAt {
			return errors.New("invalid bounded fixture grant")
		}
		if _, ok := l.grants[g.ID]; ok {
			return errors.New("grant identity is immutable and never reused")
		}
		l.grants[g.ID] = clone(*g)
	case "revoke":
		g, ok := l.grants[r.ID]
		if !ok {
			return errors.New("unknown fixture grant")
		}
		g.Revoked = true
		l.grants[r.ID] = g
	case "reserve":
		if r.Intent == nil || len(l.ops) >= 1024 {
			return errors.New("invalid intent or fixture history capacity")
		}
		i := *r.Intent
		if _, ok := l.ops[i.OperationID]; ok {
			return errors.New("operation identity already used")
		}
		if err := l.authorize(i, r.At); err != nil {
			return err
		}
		s, h, e := l.totals("")
		gs, gh, ge := l.totals(i.GrantID)
		if e != nil || ge != nil || !within(l.policy.Budget, i.Units, s, h) || !within(l.grants[i.GrantID].Budget, i.Units, gs, gh) {
			return errors.New("fixture budget already spent or reserved")
		}
		l.ops[i.OperationID] = FixtureOperation{Intent: i, State: "reserved"}
	case "prepare", "sending", "outcome":
		op, ok := l.ops[r.ID]
		if !ok {
			return errors.New("unknown fixture operation")
		}
		switch r.Kind {
		case "prepare":
			b, h := original(op.Intent)
			if op.State != "reserved" || !bytes.Equal(r.Original, b) || r.OriginalHash != h {
				return errors.New("original differs from reserved intent")
			}
			if err := l.authorize(op.Intent, r.At); err != nil {
				return err
			}
			op.State = "prepared"
			op.Original = bytes.Clone(b)
			op.OriginalHash = h
		case "sending":
			if op.State != "prepared" {
				return errors.New("original must be durable before send")
			}
			if err := l.authorize(op.Intent, r.At); err != nil {
				return err
			}
			op.State = "sending"
		case "outcome":
			if terminal(op.State) {
				return errors.New("terminal fixture outcome is immutable")
			}
			proof, found := l.receipts[r.ID]
			if r.Outcome == "not-submitted" {
				if found {
					return errors.New("mock receipt exists; cannot release its spend")
				}
			} else if (r.Outcome != "confirmed" && r.Outcome != "rejected") || !found || proof.OriginalHash != op.OriginalHash || proof.Outcome != r.Outcome || op.State != "sending" {
				return errors.New("fixture outcome requires matching durable mock proof")
			}
			op.State = r.Outcome
		}
		l.ops[r.ID] = op
	case "rebind":
		if r.Account == nil || !validRef(*r.Account) || !sameWallet(*r.Account, l.ref) || r.Account.Epoch == l.ref.Epoch || l.missingHistory {
			return errors.New("recovery requires retained wallet history")
		}
		_, h, e := l.totals("")
		if e != nil || h != 0 {
			return ErrHeld
		}
		for id, g := range l.grants {
			g.Revoked = true
			l.grants[id] = g
		}
		l.ref = *r.Account
		l.policy = nil // New owner policy/grants, lifetime spent remains.
	default:
		return errors.New("unknown fixture journal transition")
	}
	l.lastTime = r.At
	return nil
}

func (l *ledger) append(v *Vault, r journalRecord) error {
	if r.At == 0 {
		r.At = v.now().Unix()
	}
	next := *l
	next.policy = clone(l.policy)
	next.grants = clone(l.grants)
	next.ops = clone(l.ops)
	if err := next.apply(r); err != nil {
		return err
	}
	if err := v.store.appendJournal(l.main, r); err != nil {
		return err
	}
	next.main = l.main
	*l = next
	return nil
}

func (v *Vault) openLedger() error {
	main, rs, err := v.store.readJournal("operations.log")
	mock, ms, me := v.store.readJournal("mock-receipts.log")
	if errors.Is(err, os.ErrNotExist) && errors.Is(me, os.ErrNotExist) {
		if v.record != nil {
			return errors.New("wallet history missing; preserve state, spending unavailable")
		}
		return nil
	}
	if err != nil || me != nil {
		return errors.New("incomplete or damaged fixture history; preserve both journals")
	}
	l := newLedger()
	l.main = main
	l.mock = mock
	if len(ms) == 0 || ms[0].Kind != "mock-init" || ms[0].Account == nil || !validRef(*ms[0].Account) {
		return errors.New("invalid mock receipt identity")
	}
	for _, r := range ms[1:] {
		if r.Kind != "mock-receipt" || !validID(r.ID) || (r.Outcome != "confirmed" && r.Outcome != "rejected") || len(r.OriginalHash) != 64 {
			return errors.New("invalid mock proof")
		}
		if _, ok := l.receipts[r.ID]; ok {
			return errors.New("duplicate mock submission")
		}
		l.receipts[r.ID] = r
	}
	for _, r := range rs {
		if err = l.apply(r); err != nil {
			return fmt.Errorf("fixture journal: %w", err)
		}
	}
	if !sameWallet(l.ref, *ms[0].Account) || (v.record != nil && !sameWallet(l.ref, v.record.Ref)) {
		return errors.New("fixture history belongs to another wallet")
	}
	for id, r := range l.receipts {
		op, ok := l.ops[id]
		if !ok || op.OriginalHash != r.OriginalHash || (op.State != "sending" && op.State != r.Outcome) {
			return errors.New("orphan or conflicting mock proof")
		}
	}
	v.ledger = l
	return nil
}

// Only a durable vault.pending permits initialization/resumption of the two
// empty journals. Once that pending file is removed, missing history is fatal.
// Thus a crash during first setup preserves the encrypted original and cannot
// be mistaken for permission to reset an existing wallet's counters.
func (s *storage) ensureInitialHistory(e envelope) error {
	_, _, err := s.readJournal("operations.log")
	mock, ms, me := s.readJournal("mock-receipts.log")
	if err == nil && me == nil {
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return errors.New("damaged or incomplete fixture journal during key publication")
	}
	if me != nil && !errors.Is(me, os.ErrNotExist) {
		return me
	}
	at := time.Now().Unix()
	if errors.Is(me, os.ErrNotExist) {
		mock = &journal{name: "mock-receipts.log"}
		if err = s.appendJournal(mock, journalRecord{Kind: "mock-init", Account: &e.Ref, At: at}); err != nil {
			return err
		}
	} else {
		if len(ms) != 1 || ms[0].Kind != "mock-init" || ms[0].Account == nil || *ms[0].Account != e.Ref {
			return errors.New("conflicting interrupted history initialization")
		}
		at = ms[0].At
	}
	return s.appendJournal(&journal{name: "operations.log"}, journalRecord{Kind: "init", Account: &e.Ref, MissingHistory: e.RecoveryRequired, At: at})
}

func (v *Vault) recoveryRequired() bool {
	return v.record != nil && (v.ledger == nil || v.ledger.ref != v.record.Ref || v.ledger.missingHistory)
}
func (v *Vault) fixtureReady(unlock bool) error {
	if v.closed || v.store.failedState() || v.record == nil || v.ledger == nil {
		return ErrUnavailable
	}
	if v.recoveryRequired() {
		return errors.New("wallet recovery hold requires retained history and mock reconciliation")
	}
	if unlock && !v.unlocked() {
		return ErrLocked
	}
	return nil
}

func (v *Vault) FixtureState() (*FixtureSummary, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed || v.ledger == nil {
		return nil, ErrUnavailable
	}
	l := v.ledger
	s, h, err := l.totals("")
	if err != nil {
		return nil, err
	}
	return &FixtureSummary{Account: l.ref, FixtureOnly: true, MissingHistory: l.missingHistory, RecoveryRequired: v.recoveryRequired(), Policy: clone(l.policy), GrantCount: len(l.grants), OperationCount: len(l.ops), Spent: s, Held: h}, nil
}
func (v *Vault) SetFixturePolicy(p FixturePolicy) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.fixtureReady(true); err != nil {
		return err
	}
	return v.ledger.append(v, journalRecord{Kind: "policy", Policy: &p})
}
func (v *Vault) AddFixtureGrant(g FixtureGrant) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.fixtureReady(true); err != nil {
		return err
	}
	return v.ledger.append(v, journalRecord{Kind: "grant", Grant: &g})
}
func (v *Vault) RevokeFixtureGrant(ref AccountRef, id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.fixtureReady(false); err != nil {
		return err
	}
	if ref != v.record.Ref {
		return errors.New("stale account authority")
	}
	if g, ok := v.ledger.grants[id]; ok && g.Revoked {
		return nil
	}
	return v.ledger.append(v, journalRecord{Kind: "revoke", ID: id})
}
func (v *Vault) ReserveFixture(i FixtureIntent) (*FixtureOperation, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.fixtureReady(true); err != nil {
		return nil, err
	}
	if op, ok := v.ledger.ops[i.OperationID]; ok {
		if op.Intent != i {
			return nil, errors.New("operation replay changed its immutable intent")
		}
		c := clone(op)
		return &c, nil
	}
	if err := v.ledger.append(v, journalRecord{Kind: "reserve", Intent: &i}); err != nil {
		return nil, err
	}
	op := clone(v.ledger.ops[i.OperationID])
	return &op, nil
}
func (v *Vault) FixtureOperation(id string) (*FixtureOperation, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed || v.ledger == nil {
		return nil, ErrUnavailable
	}
	op, ok := v.ledger.ops[id]
	if !ok {
		return nil, errors.New("unknown fixture operation")
	}
	c := clone(op)
	return &c, nil
}

// ExecuteFixture is synchronous and mock-only. A prepared/sending original is
// never sent again, even with the same ID. Reconciliation can read its result;
// the caller cannot supply an outcome, bytes to sign or replacement transport.
func (v *Vault) ExecuteFixture(id string) (*FixtureOperation, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.fixtureReady(true); err != nil {
		return nil, err
	}
	l := v.ledger
	op, ok := l.ops[id]
	if !ok {
		return nil, errors.New("unknown fixture operation")
	}
	if terminal(op.State) {
		c := clone(op)
		return &c, nil
	}
	if op.State != "reserved" {
		return nil, ErrHeld
	}
	b, h := original(op.Intent)
	if err := l.append(v, journalRecord{Kind: "prepare", ID: id, Original: b, OriginalHash: h}); err != nil {
		return nil, err
	}
	if err := l.append(v, journalRecord{Kind: "sending", ID: id}); err != nil {
		return nil, err
	}
	if !v.unlocked() {
		return nil, ErrLocked
	}
	if err := l.authorize(op.Intent, v.now().Unix()); err != nil {
		return nil, err
	}
	if err := v.store.checkpoint("mock:before-send"); err != nil {
		v.store.poison()
		return nil, err
	}
	outcome := "confirmed"
	if op.Intent.Destination == "fixture:reject" {
		outcome = "rejected"
	}
	proof := journalRecord{Kind: "mock-receipt", ID: id, OriginalHash: h, Outcome: outcome, At: v.now().Unix()}
	if err := v.store.appendJournal(l.mock, proof); err != nil {
		return nil, err
	}
	l.receipts[id] = proof
	if err := v.store.checkpoint("mock:after-send"); err != nil {
		v.store.poison()
		return nil, err
	}
	if err := l.append(v, journalRecord{Kind: "outcome", ID: id, Outcome: outcome}); err != nil {
		return nil, err
	}
	result := clone(l.ops[id])
	return &result, nil
}
func (v *Vault) reconcile(id string) error {
	l := v.ledger
	op, ok := l.ops[id]
	if !ok {
		return errors.New("unknown fixture operation")
	}
	if terminal(op.State) {
		return nil
	}
	outcome := "not-submitted"
	if r, ok := l.receipts[id]; ok {
		outcome = r.Outcome
	}
	return l.append(v, journalRecord{Kind: "outcome", ID: id, Outcome: outcome})
}
func (v *Vault) ReconcileFixture(id string) (*FixtureOperation, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.fixtureReady(false); err != nil {
		return nil, err
	}
	if err := v.reconcile(id); err != nil {
		return nil, err
	}
	op := clone(v.ledger.ops[id])
	return &op, nil
}

// RecoverFixtureAuthority is owner-only over RPC. Only retained, complete mock
// history can prove this fixture's outcomes. A key-only restore cannot use it.
// This is not on-chain recovery or a generic authority-reset escape hatch.
func (v *Vault) RecoverFixtureAuthority(ref AccountRef) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed || v.store.failedState() || v.record == nil || v.ledger == nil || !v.unlocked() {
		return ErrLocked
	}
	l := v.ledger
	if ref != v.record.Ref || l.missingHistory || !sameWallet(ref, l.ref) {
		return errors.New("recovery needs exact restored account and retained history")
	}
	if l.ref == ref {
		return nil
	}
	for id := range l.ops {
		if err := v.reconcile(id); err != nil {
			return err
		}
	}
	return l.append(v, journalRecord{Kind: "rebind", Account: &ref})
}

func ParsePublicInput(document []byte) (PublicInput, error) {
	var input PublicInput
	err := decode(document, &input)
	return input, err
}
