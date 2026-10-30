package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/update"
	"io"
	"math/big"
	"strings"
	"unicode/utf8"
)

// StrategyPlan is a paper-only limit-buy policy. Amounts use SOL atoms (1e9)
// and USDC micros (1e6); no mint, quote, signing or venue authority is implied.
type StrategyPlan struct {
	Version           string `json:"version"`
	AccountID         string `json:"accountId"`
	Network           string `json:"network"`
	Market            string `json:"market"`
	QuantityAtoms     int64  `json:"quantityAtoms"`
	LimitPriceMicros  int64  `json:"limitPriceMicros"`
	OrderBudgetMicros int64  `json:"orderBudgetMicros"`
	TotalBudgetMicros int64  `json:"totalBudgetMicros"`
}
type StrategyBinding struct {
	AccountID       string `json:"accountId"`
	Network         string `json:"network"`
	PublicKey       string `json:"publicKey"`
	ReferenceSHA256 string `json:"referenceSha256"`
}
type StrategyVersion struct {
	Version     int             `json:"version"`
	Name        string          `json:"name"`
	CreatedAt   int64           `json:"createdAt"`
	Plan        StrategyPlan    `json:"plan"`
	Binding     StrategyBinding `json:"binding"`
	Digest      string          `json:"digest"`
	SpentMicros int64           `json:"spentMicros"`
}
type AgentStrategy struct {
	ID        string            `json:"id"`
	ProfileID string            `json:"profileId"`
	Name      string            `json:"name"`
	Versions  []StrategyVersion `json:"versions"`
}
type StrategyTaskPolicy struct {
	Version         string          `json:"version"`
	StrategyID      string          `json:"strategyId"`
	StrategyVersion int             `json:"strategyVersion"`
	StrategySHA256  string          `json:"strategySha256"`
	Plan            StrategyPlan    `json:"plan"`
	Binding         StrategyBinding `json:"binding"`
	ExpiresAt       int64           `json:"expiresAt"`
	Digest          string          `json:"digest"`
}
type StrategyPaperResult struct {
	Version            string `json:"version"`
	StrategyID         string `json:"strategyId"`
	StrategyVersion    int    `json:"strategyVersion"`
	StrategySHA256     string `json:"strategySha256"`
	ConsentDigest      string `json:"consentDigest"`
	Market             string `json:"market"`
	Network            string `json:"network"`
	AccountID          string `json:"accountId"`
	QuantityAtoms      int64  `json:"quantityAtoms"`
	PriceMicros        int64  `json:"priceMicros"`
	CostMicros         int64  `json:"costMicros"`
	BudgetBeforeMicros int64  `json:"budgetBeforeMicros"`
	BudgetAfterMicros  int64  `json:"budgetAfterMicros"`
	Source             string `json:"source"`
	ObservedAt         int64  `json:"observedAt"`
}

const strategyPlanVersion = "paper-limit-buy-v1"
const strategyTaskVersion = "strategy-task-v1"
const strategyTaskPrefix = "strategy-task:"
const strategyResultVersion = "strategy-paper-result-v1"
const strategyFeature = "agents-versioned-strategies-v1"
const strategyStateLimit = agentDocumentLimit

func strategyHash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func strategyVersionHash(id, profile string, v StrategyVersion) string {
	v.Digest = ""
	v.SpentMicros = 0
	return strategyHash(struct {
		Domain, ID, Profile string
		Value               StrategyVersion
	}{"fased-strategy-version-v1", id, profile, v})
}
func strategyConsent(t AgentTask) string {
	p := *t.StrategyPlan
	p.Digest = ""
	return strategyHash(struct {
		Domain, Profile, Name string
		Interval              int64
		Runs                  int
		Policy                StrategyTaskPolicy
	}{"fased-strategy-task-v1", t.ProfileID, t.Name, t.IntervalSeconds, t.MaxRuns, p})
}
func validStrategyPlan(p StrategyPlan) bool {
	return p.Version == strategyPlanVersion && validAgentText(p.AccountID, 128, true) && (p.Network == "devnet" || p.Network == "mainnet-beta") && p.Market == "SOL/USDC" && p.QuantityAtoms >= 1 && p.QuantityAtoms <= 1_000_000_000_000 && p.LimitPriceMicros >= 1 && p.LimitPriceMicros <= 1_000_000_000_000 && p.OrderBudgetMicros >= 1 && p.OrderBudgetMicros <= p.TotalBudgetMicros && p.TotalBudgetMicros <= 1_000_000_000_000_000
}
func strategyBinding(a Account) (StrategyBinding, error) {
	if (a.Version != 2 && a.Version != 3) || (a.Mode != "watch" && a.Mode != "fixture-managed") || (a.Network != "devnet" && a.Network != "mainnet-beta") || !hexValue(a.PublicKey, 32) {
		return StrategyBinding{}, errors.New("select a network wallet public record")
	}
	if a.Mode == "fixture-managed" && !managedNetworkAccount(a) {
		return StrategyBinding{}, errors.New("protected wallet reference is not network-bound")
	}
	identity := struct {
		Version                  int
		Mode, Network, PublicKey string
		Signer                   any
	}{a.Version, a.Mode, a.Network, a.PublicKey, a.Signer}
	return StrategyBinding{a.ID, a.Network, a.PublicKey, strategyHash(identity)}, nil
}
func readStrategyAccounts(ctx context.Context, tx *sql.Tx) (AccountsRecord, error) {
	var raw string
	var record AccountsRecord
	if err := tx.QueryRowContext(ctx, "SELECT document FROM accounts WHERE id=1").Scan(&raw); err != nil {
		return record, err
	}
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		return record, err
	}
	return record, nil
}
func boundStrategyAccount(record AccountsRecord, p StrategyPlan) (StrategyBinding, error) {
	for _, a := range record.Accounts {
		if a.ID == p.AccountID {
			b, e := strategyBinding(a)
			if e != nil || b.Network != p.Network {
				return StrategyBinding{}, errors.New("wallet network does not match strategy")
			}
			return b, nil
		}
	}
	return StrategyBinding{}, errors.New("strategy wallet is missing")
}
func findStrategy(d *agentDocument, profile, id string) *AgentStrategy {
	for i := range d.Strategies {
		if d.Strategies[i].ID == id && d.Strategies[i].ProfileID == profile {
			return &d.Strategies[i]
		}
	}
	return nil
}
func saveStrategy(d *agentDocument, c AgentCommand, profile, id string, now int64, accounts AccountsRecord) error {
	unexpected := c
	unexpected.Action, unexpected.ProfileID, unexpected.ID, unexpected.Name = "", "", "", ""
	unexpected.Revision = 0
	unexpected.Strategy = nil
	if unexpected != (AgentCommand{}) || c.Strategy == nil || !validAgentText(c.Name, 80, true) || !validStrategyPlan(*c.Strategy) {
		return errors.New("name and bounded paper strategy required")
	}
	binding, err := boundStrategyAccount(accounts, *c.Strategy)
	if err != nil {
		return err
	}
	var target *AgentStrategy
	if c.ID == "" {
		if len(d.Strategies) >= 16 {
			return errors.New("strategy capacity reached; preserve existing versions")
		}
		d.Strategies = append(d.Strategies, AgentStrategy{ID: id, ProfileID: profile, Name: c.Name})
		target = &d.Strategies[len(d.Strategies)-1]
	} else {
		target = findStrategy(d, profile, c.ID)
		if target == nil {
			return errors.New("profile strategy not found")
		}
	}
	if len(target.Versions) >= 8 {
		return errors.New("strategy version capacity reached; preserve history")
	}
	version := StrategyVersion{Version: len(target.Versions) + 1, Name: c.Name, CreatedAt: now, Plan: *c.Strategy, Binding: binding}
	version.Digest = strategyVersionHash(target.ID, profile, version)
	target.Name = c.Name
	target.Versions = append(target.Versions, version)
	for i, t := range d.Tasks {
		if t.ProfileID == profile && t.StrategyPlan != nil && t.StrategyPlan.StrategyID == target.ID && (t.State == "scheduled" || t.State == "paused") {
			d.Tasks[i].State = "held"
			d.Tasks[i].NextAt = 0
		}
	}
	return nil
}
func reviewStrategyTask(d agentDocument, c AgentCommand, profile string, revision, now int64) (*TaskReview, error) {
	unexpected := c
	unexpected.Action, unexpected.ProfileID, unexpected.Name, unexpected.StrategyID, unexpected.ConsentDigest = "", "", "", "", ""
	unexpected.Revision, unexpected.IntervalSeconds, unexpected.ExpiresAt = 0, 0, 0
	unexpected.MaxRuns, unexpected.StrategyVersion = 0, 0
	if unexpected != (AgentCommand{}) || !validAgentText(c.Name, 80, true) || c.IntervalSeconds < 60 || c.IntervalSeconds > 86400 || c.MaxRuns < 1 || c.MaxRuns > 20 || c.IntervalSeconds*int64(c.MaxRuns) > 7*86400 {
		return nil, errors.New("review a bounded strategy schedule without other action bindings")
	}
	reserved := c.MaxRuns
	for _, task := range d.Tasks {
		reserved += task.MaxRuns
	}
	if reserved > 200 || len(d.Tasks) >= 100 || len(d.Runs) >= 200 || len(d.Notices) >= 200 {
		return nil, errors.New("task/result capacity reached; preserve history")
	}
	strategy := findStrategy(&d, profile, c.StrategyID)
	if strategy == nil || c.StrategyVersion != len(strategy.Versions) {
		return nil, errors.New("select the current strategy version")
	}
	version := strategy.Versions[c.StrategyVersion-1]
	if version.SpentMicros >= version.Plan.TotalBudgetMicros {
		return nil, errors.New("strategy allocation is exhausted; review a new version")
	}
	expires := now + 600
	if c.Action == "task-reviewed-create" {
		expires = c.ExpiresAt
		if expires <= now || expires > now+600 {
			return nil, errors.New("task review expired; review again")
		}
	}
	policy := &StrategyTaskPolicy{Version: strategyTaskVersion, StrategyID: strategy.ID, StrategyVersion: version.Version, StrategySHA256: version.Digest, Plan: version.Plan, Binding: version.Binding, ExpiresAt: expires + c.IntervalSeconds*int64(c.MaxRuns)}
	task := AgentTask{ProfileID: profile, Name: c.Name, IntervalSeconds: c.IntervalSeconds, MaxRuns: c.MaxRuns, StrategyPlan: policy}
	policy.Digest = strategyConsent(task)
	review := &TaskReview{Version: "task-review-v1", Revision: revision, ProfileID: profile, Name: c.Name, Text: "", IntervalSeconds: c.IntervalSeconds, MaxRuns: c.MaxRuns, ExpiresAt: expires, StrategyPlan: policy}
	review.Digest = strategyHash(review)
	return review, nil
}
func strategyPolicyVersion(d *agentDocument, t AgentTask, current bool, now int64) (*StrategyVersion, error) {
	p := t.StrategyPlan
	if p == nil || p.Version != strategyTaskVersion || p.Digest != strategyConsent(t) || t.ProfileID == "" || t.IntervalSeconds < 60 || t.IntervalSeconds > 86400 || t.MaxRuns < 1 || t.MaxRuns > 20 || t.IntervalSeconds*int64(t.MaxRuns) > 7*86400 || t.Text != "" || t.BotID != "" || t.Calendar != nil || t.AI != nil || t.News != nil || t.Watch != nil || t.WEN != nil || t.Playbook != nil {
		return nil, errors.New("strategy task policy changed")
	}
	strategy := findStrategy(d, t.ProfileID, p.StrategyID)
	if strategy == nil || p.StrategyVersion < 1 || p.StrategyVersion > len(strategy.Versions) {
		return nil, errors.New("strategy version missing")
	}
	v := &strategy.Versions[p.StrategyVersion-1]
	if p.StrategySHA256 != v.Digest || p.Plan != v.Plan || p.Binding != v.Binding || !validStrategyPlan(p.Plan) {
		return nil, errors.New("strategy task binding changed")
	}
	if current && (p.StrategyVersion != len(strategy.Versions) || p.ExpiresAt <= now) {
		return nil, errors.New("strategy approval expired or superseded")
	}
	return v, nil
}
func appendStrategyTask(d *agentDocument, c AgentCommand, profile, id string, now int64, r *TaskReview) error {
	t := AgentTask{ID: strategyTaskPrefix + id, ProfileID: profile, Name: c.Name, IntervalSeconds: c.IntervalSeconds, MaxRuns: c.MaxRuns, NextAt: now + c.IntervalSeconds, State: "scheduled", StrategyPlan: r.StrategyPlan}
	if _, e := strategyPolicyVersion(d, t, true, now); e != nil {
		return e
	}
	d.Tasks = append(d.Tasks, t)
	return nil
}

type strategyQuote struct {
	Market, Network, Source string
	PriceMicros, ObservedAt int64
}

func compiledStrategyQuote(p StrategyTaskPolicy, now int64) strategyQuote {
	return strategyQuote{p.Plan.Market, p.Plan.Network, "compiled-paper-quote-v1", 125_000_000, now}
}
func strategyCost(quantity, price int64) (int64, error) {
	if quantity < 1 || price < 1 {
		return 0, errors.New("positive price and quantity required")
	}
	value := new(big.Int).Mul(big.NewInt(quantity), big.NewInt(price))
	value.Add(value, big.NewInt(999_999_999))
	value.Quo(value, big.NewInt(1_000_000_000))
	if !value.IsInt64() {
		return 0, errors.New("paper cost exceeds integer bound")
	}
	return value.Int64(), nil
}
func runStrategyTask(d *agentDocument, index int, now int64, available bool, accounts AccountsRecord, quote strategyQuote) error {
	if len(d.Runs) >= 200 || len(d.Notices) >= 200 {
		return errors.New("result capacity reached; no strategy evaluated")
	}
	t := &d.Tasks[index]
	p := t.StrategyPlan
	v, err := strategyPolicyVersion(d, *t, false, now)
	if err != nil {
		return err
	}
	before := v.Plan.TotalBudgetMicros - v.SpentMicros
	paper := &StrategyPaperResult{Version: strategyResultVersion, StrategyID: p.StrategyID, StrategyVersion: p.StrategyVersion, StrategySHA256: p.StrategySHA256, ConsentDigest: p.Digest, Market: p.Plan.Market, Network: p.Plan.Network, AccountID: p.Plan.AccountID, BudgetBeforeMicros: before, BudgetAfterMicros: before}
	outcome := "paper-filled"
	binding, bindingErr := boundStrategyAccount(accounts, p.Plan)
	switch {
	case !available:
		outcome = "paper-unavailable"
	case bindingErr != nil || binding != p.Binding:
		outcome = "binding-held"
	case p.ExpiresAt <= now:
		outcome = "expired-held"
	case p.StrategyVersion != len(findStrategy(d, t.ProfileID, p.StrategyID).Versions):
		outcome = "version-held"
	case quote.Source != "compiled-paper-quote-v1" || quote.Market != p.Plan.Market || quote.Network != p.Plan.Network || quote.PriceMicros < 1 || quote.PriceMicros > 1_000_000_000_000 || quote.ObservedAt > now || quote.ObservedAt < now-60:
		outcome = "price-held"
	default:
		paper.Source = quote.Source
		paper.PriceMicros = quote.PriceMicros
		paper.ObservedAt = quote.ObservedAt
		cost, e := strategyCost(p.Plan.QuantityAtoms, quote.PriceMicros)
		switch {
		case quote.PriceMicros > p.Plan.LimitPriceMicros:
			outcome = "price-skipped"
		case e != nil || cost > p.Plan.OrderBudgetMicros:
			outcome = "order-cap-held"
		case cost > before:
			outcome = "budget-held"
		default:
			paper.QuantityAtoms = p.Plan.QuantityAtoms
			paper.CostMicros = cost
			paper.BudgetAfterMicros = before - cost
			v.SpentMicros += cost
		}
	}
	id := fmt.Sprintf("%s:%d", t.ID, t.Runs+1)
	d.Runs = append(d.Runs, AgentRun{ID: id, ProfileID: t.ProfileID, TaskID: t.ID, Strategy: strategyPlanVersion, At: now, Outcome: outcome, Paper: paper})
	d.Notices = append(d.Notices, AgentNotice{ID: id, ProfileID: t.ProfileID, Text: t.Name + ": " + outcome, RunID: id, At: now})
	t.Runs++
	t.NextAt = now + t.IntervalSeconds
	if outcome != "paper-filled" && outcome != "price-skipped" {
		t.State = "held"
		t.NextAt = 0
	} else if t.Runs >= t.MaxRuns {
		t.State = "completed"
		t.NextAt = 0
	}
	return nil
}
func (s *Store) evaluateStrategyTask(ctx context.Context, tx *sql.Tx, d *agentDocument, i int, now int64) error {
	accounts, e := readStrategyAccounts(ctx, tx)
	if e != nil {
		return e
	}
	return runStrategyTask(d, i, now, s.providerFixture, accounts, compiledStrategyQuote(*d.Tasks[i].StrategyPlan, now))
}

// Inspect root names once, without recursively walking unrelated chat/history.
// Protected containers below retain bounded strict decoding, including aliases,
// duplicate fields and typed policies. Other sections keep their own validators.
func strategyWorkspaceFields(raw []byte) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > strategyStateLimit || !utf8.Valid(raw) {
		return nil, errors.New("invalid strategy workspace")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return nil, errors.New("invalid strategy workspace")
	}
	seen := make(map[string]bool)
	root := make(map[string]json.RawMessage)
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] {
			return nil, errors.New("ambiguous strategy workspace")
		}
		seen[name] = true
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return nil, errors.New("invalid strategy workspace")
		}
		if strings.EqualFold(name, "strategies") || strings.EqualFold(name, "tasks") || strings.EqualFold(name, "runs") {
			root[name] = value
		}
	}
	if last, err := d.Token(); err != nil || last != json.Delim('}') {
		return nil, errors.New("invalid strategy workspace")
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errors.New("trailing strategy workspace")
	}
	return root, nil
}

func strategyFields(raw []byte, unprotected bool) error {
	root, err := strategyWorkspaceFields(raw)
	if err != nil {
		return err
	}
	for key, value := range root {
		if strings.EqualFold(key, "strategies") {
			if unprotected || key != "strategies" || string(value) == "null" {
				return errors.New("unprotected or ambiguous strategy records; preserve original state")
			}
			var values []AgentStrategy
			if update.StrictJSONBounded(value, &values, strategyStateLimit) != nil {
				return errors.New("invalid strategy fields")
			}
		}
		if strings.EqualFold(key, "tasks") || strings.EqualFold(key, "runs") {
			if key != "tasks" && key != "runs" {
				return errors.New("ambiguous strategy container; preserve original state")
			}
			var rows []map[string]json.RawMessage
			if update.StrictJSONBounded(value, &rows, strategyStateLimit) != nil {
				return errors.New("invalid task records")
			}
			for _, row := range rows {
				if err := onchainRowFields(row, key, unprotected); err != nil {
					return err
				}
				if unprotected {
					for field, body := range row {
						if strings.EqualFold(field, "id") {
							var id string
							if json.Unmarshal(body, &id) == nil && strings.HasPrefix(id, strategyTaskPrefix) {
								return errors.New("unprotected strategy identity; preserve original state")
							}
						}
					}
				}
				for field, body := range row {
					target := "strategyPlan"
					if key == "runs" {
						target = "paper"
					}
					if !strings.EqualFold(field, target) {
						continue
					}
					if unprotected || field != target || string(body) == "null" {
						return errors.New("unprotected or ambiguous strategy task/result; preserve original state")
					}
					var err error
					if key == "tasks" {
						var v StrategyTaskPolicy
						err = update.StrictJSONBounded(body, &v, strategyStateLimit)
					} else {
						var v StrategyPaperResult
						err = update.StrictJSONBounded(body, &v, strategyStateLimit)
					}
					if err != nil {
						return errors.New("invalid strategy task/result fields")
					}
				}
			}
		}
	}
	return nil
}
func validateStrategyStorage(raw []byte, d agentDocument) error {
	if err := strategyFields(raw, false); err != nil {
		return err
	}
	if err := validateOnchainStorage(d); err != nil {
		return err
	}
	if len(d.Strategies) > 16 {
		return errors.New("strategy capacity exceeded")
	}
	seen := map[string]bool{}
	ledger := map[string]int64{}
	counts := map[string]int{}
	runIDs := map[string]bool{}
	for _, strategy := range d.Strategies {
		if !validConnectionRef(strategy.ID) || seen[strategy.ID] || agentProfile(d, strategy.ProfileID) < 0 || len(strategy.Versions) < 1 || len(strategy.Versions) > 8 || strategy.Name != strategy.Versions[len(strategy.Versions)-1].Name {
			return errors.New("invalid strategy identity/version history")
		}
		seen[strategy.ID] = true
		for i, v := range strategy.Versions {
			if v.Version != i+1 || !validAgentText(v.Name, 80, true) || v.CreatedAt <= 0 || !validStrategyPlan(v.Plan) || v.Binding.AccountID != v.Plan.AccountID || v.Binding.Network != v.Plan.Network || !hexValue(v.Binding.PublicKey, 32) || !hexValue(v.Binding.ReferenceSHA256, 32) || v.Digest != strategyVersionHash(strategy.ID, strategy.ProfileID, v) || v.SpentMicros < 0 || v.SpentMicros > v.Plan.TotalBudgetMicros {
				return errors.New("strategy version changed; preserve original records")
			}
		}
	}
	tasks := map[string]AgentTask{}
	for _, t := range d.Tasks {
		tagged := strings.HasPrefix(t.ID, strategyTaskPrefix)
		if tagged != (t.StrategyPlan != nil) || (tagged && !validConnectionRef(strings.TrimPrefix(t.ID, strategyTaskPrefix))) {
			return errors.New("strategy task policy erased or identity changed; preserve original state")
		}
		if t.StrategyPlan != nil {
			if _, e := strategyPolicyVersion(&d, t, false, 0); e != nil {
				return e
			}
			if t.Runs < 0 || t.Runs > t.MaxRuns || (t.State != "scheduled" && t.State != "paused" && t.State != "held" && t.State != "completed") {
				return errors.New("invalid strategy task state")
			}
			if t.StrategyPlan.StrategyVersion != len(findStrategy(&d, t.ProfileID, t.StrategyPlan.StrategyID).Versions) && (t.State == "scheduled" || t.State == "paused") {
				return errors.New("superseded strategy task retains approval")
			}
			if t.StrategyPlan.ExpiresAt <= 0 || tasks[t.ID].ID != "" {
				return errors.New("invalid strategy approval identity")
			}
			tasks[t.ID] = t
		}
	}
	for _, run := range d.Runs {
		if strings.HasPrefix(run.TaskID, strategyTaskPrefix) != (run.Paper != nil) {
			return errors.New("strategy result policy erased; preserve original state")
		}
		if run.Paper != nil {
			p := run.Paper
			t, ok := tasks[run.TaskID]
			if !ok || runIDs[run.ID] || run.ProfileID != t.ProfileID || run.Strategy != strategyPlanVersion || run.BotID != "" || run.AIRequestID != "" || run.AIDigest != "" || run.News != nil || run.WEN != nil || run.Watch != nil || run.Analysis != nil || p.Version != strategyResultVersion || p.StrategyID != t.StrategyPlan.StrategyID || p.StrategyVersion != t.StrategyPlan.StrategyVersion || p.StrategySHA256 != t.StrategyPlan.StrategySHA256 || p.ConsentDigest != t.StrategyPlan.Digest || p.Market != t.StrategyPlan.Plan.Market || p.Network != t.StrategyPlan.Plan.Network || p.AccountID != t.StrategyPlan.Plan.AccountID || p.BudgetBeforeMicros < 0 || p.BudgetBeforeMicros > t.StrategyPlan.Plan.TotalBudgetMicros || p.BudgetAfterMicros < 0 {
				return errors.New("paper result identity changed")
			}
			if p.BudgetBeforeMicros != t.StrategyPlan.Plan.TotalBudgetMicros-ledger[p.StrategySHA256] {
				return errors.New("paper allocation history changed")
			}
			count := counts[t.ID] + 1
			if run.ID != fmt.Sprintf("%s:%d", t.ID, count) {
				return errors.New("paper result order changed")
			}
			counts[t.ID] = count
			runIDs[run.ID] = true
			switch run.Outcome {
			case "paper-filled", "price-skipped", "order-cap-held", "budget-held":
				if p.Source != "compiled-paper-quote-v1" || p.ObservedAt > run.At || p.ObservedAt < run.At-60 || p.PriceMicros < 1 || p.PriceMicros > 1_000_000_000_000 {
					return errors.New("paper price provenance changed")
				}
			case "paper-unavailable", "binding-held", "expired-held", "version-held", "price-held":
				if p.Source != "" || p.PriceMicros != 0 || p.ObservedAt != 0 {
					return errors.New("held result asserts a price")
				}
			default:
				return errors.New("unknown paper result")
			}
			if run.Outcome == "paper-filled" {
				cost, e := strategyCost(t.StrategyPlan.Plan.QuantityAtoms, p.PriceMicros)
				if e != nil || cost != p.CostMicros || p.QuantityAtoms != t.StrategyPlan.Plan.QuantityAtoms || p.PriceMicros > t.StrategyPlan.Plan.LimitPriceMicros || cost > t.StrategyPlan.Plan.OrderBudgetMicros || p.BudgetAfterMicros != p.BudgetBeforeMicros-cost {
					return errors.New("paper fill exceeds consent")
				}
				ledger[p.StrategySHA256] += cost
			} else if p.CostMicros != 0 || p.QuantityAtoms != 0 || p.BudgetAfterMicros != p.BudgetBeforeMicros {
				return errors.New("unfilled result changed allocation")
			}
		}
	}
	for _, t := range tasks {
		if counts[t.ID] != t.Runs {
			return errors.New("strategy task results missing")
		}
	}
	for _, strategy := range d.Strategies {
		for _, v := range strategy.Versions {
			if ledger[v.Digest] != v.SpentMicros {
				return errors.New("strategy spend ledger differs from results")
			}
		}
	}
	return nil
}
func migrateStrategies(ctx context.Context, db *sql.DB) error {
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	phase, _, e := readPause(ctx, tx)
	if e != nil {
		return e
	}
	if phase == "paused" {
		return ErrCorePaused
	}
	var raw string
	if e = tx.QueryRowContext(ctx, "SELECT document FROM agent_workspace WHERE id=1").Scan(&raw); e != nil {
		return e
	}
	if e = strategyFields([]byte(raw), true); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "PRAGMA user_version=30"); e != nil {
		return e
	}
	return tx.Commit()
}

func strategyFutureStorage(d agentDocument) int {
	reserved := 0
	for _, task := range d.Tasks {
		if task.StrategyPlan != nil && (task.State == "scheduled" || task.State == "paused") {
			reserved += (task.MaxRuns - task.Runs) * 2048
		}
	}
	return reserved
}
