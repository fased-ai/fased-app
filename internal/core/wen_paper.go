package core

// WEN paper work is an unsigned local codec rehearsal, not a market/settlement
// simulator. Only the pinned public fixture supplies identities; no endpoint,
// wallet, private key, AI tool or financial action can be attached.
import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/fased-ai/fased-app/internal/wenpm1"
)

const wenPaperVersion = "wen-pm1-paper-v1"

type WENPaperInput struct {
	BudgetSOL       string `json:"budgetSOL"`
	Selection       string `json:"selection"`
	ValidForSeconds int64  `json:"validForSeconds"`
}
type WENPaperPlan struct {
	WENPaperInput
	Version       string `json:"version"`
	FixtureSource string `json:"fixtureSource"`
	FixtureSHA256 string `json:"fixtureSha256"`
	ExpiresAt     int64  `json:"expiresAt"`
	Digest        string `json:"digest"`
	Approved      bool   `json:"approved"`
}
type WENPaperInstruction struct {
	Operation      string `json:"operation"`
	RequestSHA256  string `json:"requestSha256"`
	TemplateSHA256 string `json:"templateSha256"`
}
type WENPaperResult struct {
	PlanDigest    string                `json:"planDigest"`
	FixtureSource string                `json:"fixtureSource"`
	FixtureSHA256 string                `json:"fixtureSha256"`
	Commitment    string                `json:"commitment"`
	Instructions  []WENPaperInstruction `json:"instructions"`
	AccountSHA256 string                `json:"accountSha256"`
	ReceiptSHA256 string                `json:"receiptSha256"`
	Receipt       wenpm1.Receipt        `json:"receipt"`
}

func wenHash(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }
func wenAmount(input string) (uint64, error) {
	parts := strings.Split(input, ".")
	if len(parts) > 2 || len(parts[0]) != 1 || parts[0][0] < '0' || parts[0][0] > '1' {
		return 0, errors.New("paper budget must be 0.01–1 SOL, decimal with at most nine fractional digits")
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if len(fraction) == 0 || len(fraction) > 9 {
			return 0, errors.New("invalid SOL precision")
		}
	}
	for _, c := range fraction {
		if c < '0' || c > '9' {
			return 0, errors.New("invalid SOL decimal")
		}
	}
	whole, _ := strconv.ParseUint(parts[0], 10, 64)
	padded := fraction + strings.Repeat("0", 9-len(fraction))
	value, _ := strconv.ParseUint(padded, 10, 64)
	value += whole * 1000000000
	if value < 10000000 || value > 1000000000 {
		return 0, errors.New("paper budget must be 0.01–1 SOL")
	}
	return value, nil
}
func wenPlanDigest(t AgentTask) string {
	p := *t.WEN
	p.Digest = ""
	p.Approved = false
	b, _ := json.Marshal(struct {
		ID, Profile, Name string
		Interval          int64
		MaxRuns           int
		Plan              WENPaperPlan
	}{t.ID, t.ProfileID, t.Name, t.IntervalSeconds, t.MaxRuns, p})
	return wenHash(b)
}
func validateWEN(t AgentTask, now int64, approved bool) error {
	p := t.WEN
	if p == nil || p.Version != wenPaperVersion || p.FixtureSource != wenpm1.FixtureSource || p.FixtureSHA256 != wenpm1.FixtureSHA256 || p.Digest != wenPlanDigest(t) || (approved && !p.Approved) || p.ExpiresAt <= now || p.ValidForSeconds < 300 || p.ValidForSeconds > 1800 || t.IntervalSeconds != 60 || t.MaxRuns != 1 || t.AI != nil || t.News != nil || t.BotID != "" {
		return errors.New("paper plan changed, expired or unapproved; prepare a fresh review")
	}
	if _, e := wenAmount(p.BudgetSOL); e != nil {
		return e
	}
	if len(p.Selection) != 1 || p.Selection[0] < '0' || p.Selection[0] > '4' {
		return errors.New("select one BTC range 0–4")
	}
	return nil
}
func changeWEN(d *agentDocument, c AgentCommand, profile, id string, now int64) error {
	if c.Action == "wen-paper-prepare" {
		if c.WEN == nil || !validAgentText(c.Name, 80, true) || len(d.Tasks) >= 100 || c.BotID != "" || c.NewsSourceID != "" || c.Limits != nil || c.ConnectionRef != "" {
			return errors.New("paper entry requires a name and bounded standalone inputs")
		}
		count, total := 0, 1
		for _, t := range d.Tasks {
			total += t.MaxRuns
			if t.WEN != nil {
				count++
			}
		}
		if count >= 20 || total > 200 {
			return errors.New("retained paper task capacity reached")
		}
		t := AgentTask{ID: id, ProfileID: profile, Name: strings.TrimSpace(c.Name), IntervalSeconds: 60, MaxRuns: 1, State: "review", WEN: &WENPaperPlan{WENPaperInput: *c.WEN, Version: wenPaperVersion, FixtureSource: wenpm1.FixtureSource, FixtureSHA256: wenpm1.FixtureSHA256, ExpiresAt: now + c.WEN.ValidForSeconds}}
		t.WEN.Digest = wenPlanDigest(t)
		if e := validateWEN(t, now, false); e != nil {
			return e
		}
		d.Tasks = append(d.Tasks, t)
		return nil
	}
	for i, t := range d.Tasks {
		if t.ID != c.ID || t.ProfileID != profile || t.WEN == nil {
			continue
		}
		if c.Action == "wen-paper-cancel" {
			if t.State == "completed" || t.State == "cancelled" || t.State == "held" {
				return errors.New("paper task is already terminal")
			}
			d.Tasks[i].State = "cancelled"
			d.Tasks[i].NextAt = 0
			return nil
		}
		if t.State != "review" || c.ConsentDigest != t.WEN.Digest {
			return errors.New("review the exact paper plan before approval")
		}
		if err := validateWEN(t, now, false); err != nil {
			return err
		}
		d.Tasks[i].WEN.Approved = true
		d.Tasks[i].State = "scheduled"
		d.Tasks[i].NextAt = now + 60
		return nil
	}
	return errors.New("profile paper task not found")
}

// Reserve result/inbox bytes and slots across every workspace mutation, so a
// later reminder or chat cannot consume a reviewed paper task's completion room.
func wenPaperStorage(d agentDocument) (int, error) {
	count := 0
	for _, t := range d.Tasks {
		if t.WEN != nil && (t.State == "review" || t.State == "scheduled" || t.State == "paused") {
			count++
		}
	}
	if len(d.Runs)+count > 200 || len(d.Notices)+count > 200 {
		return 0, errors.New("paper journal/inbox capacity reserved")
	}
	return count * 8192, nil
}
func runWENPaper(d *agentDocument, i int, now int64) error {
	t := &d.Tasks[i]
	if len(d.Runs) >= 200 || len(d.Notices) >= 200 {
		return errors.New("paper journal full")
	}
	run := AgentRun{ID: t.ID + ":1", ProfileID: t.ProfileID, TaskID: t.ID, Strategy: wenPaperVersion, At: now, Outcome: "paper-pending"}
	if validateWEN(*t, now, true) != nil {
		run.Outcome = "paper-held"
		t.State = "held"
	} else {
		result, err := rehearseWEN(*t)
		if err != nil {
			return err
		}
		run.WEN = &result
		t.State = "completed"
	}
	t.Runs = 1
	t.NextAt = 0
	d.Runs = append(d.Runs, run)
	d.Notices = append(d.Notices, AgentNotice{ID: run.ID, ProfileID: t.ProfileID, RunID: run.ID, At: now, Text: t.Name + ": " + run.Outcome + ". Fixture only; no settlement, rewards, signing or submission."})
	return nil
}
func rehearseWEN(t AgentTask) (WENPaperResult, error) {
	ledger, golden, err := wenpm1.CanonicalFixture()
	if err != nil {
		return WENPaperResult{}, err
	}
	amount, err := wenAmount(t.WEN.BudgetSOL)
	if err != nil {
		return WENPaperResult{}, err
	}
	selection := t.WEN.Selection[0] - '0'
	// Public deterministic fixture salt and nonce, never a live commitment secret.
	salt := wenpm1.Key(sha256.Sum256([]byte("wen-paper-salt:" + t.ID)))
	nonceBytes := sha256.Sum256([]byte(t.ID))
	nonce := binary.LittleEndian.Uint64(nonceBytes[:8])
	values := wenpm1.CommitmentValues{Slot: golden.Slot, Owner: golden.Owner, Amount: amount, Nonce: nonce, Selection: selection, Salt: salt}
	commitment, err := wenpm1.Commitment(ledger.Binding, values)
	if err != nil {
		return WENPaperResult{}, err
	}
	result := WENPaperResult{PlanDigest: t.WEN.Digest, FixtureSource: wenpm1.FixtureSource, FixtureSHA256: wenpm1.FixtureSHA256, Commitment: commitment.String(), Instructions: []WENPaperInstruction{}}
	requests := []wenpm1.Request{wenpm1.Open{Slot: golden.Slot}, wenpm1.Commit{Slot: golden.Slot, Amount: amount, Nonce: nonce, Commitment: commitment}, wenpm1.Reveal{Slot: golden.Slot, Entry: 0, Selection: selection, Salt: salt}}
	for n, request := range requests {
		accounts := map[string]wenpm1.Key{}
		if n == 1 {
			accounts["owner"] = golden.Owner
			accounts["system"] = wenpm1.SystemProgram()
		}
		template, e := wenpm1.BuildInstruction(ledger.Binding, request, accounts)
		if e != nil {
			return result, e
		}
		if _, e = wenpm1.DecodeRequest(template.Data, ledger.Binding); e != nil {
			return result, e
		}
		b, e := json.Marshal(template)
		if e != nil {
			return result, e
		}
		result.Instructions = append(result.Instructions, WENPaperInstruction{Operation: []string{"open", "commit", "reveal"}[n], RequestSHA256: wenHash(template.Data), TemplateSHA256: wenHash(b)})
	}
	// Synthetic *pending* entry only. Do not copy boundary-vector balances or
	// simulate WEN economics, recorder votes, payouts, claimability or returns.
	ledger.EligibleStake = 0
	ledger.LifetimeCommitted = 0
	ledger.Annual = wenpm1.Annual{}
	ledger.Rounds = []wenpm1.Round{{Slot: golden.Slot, Entries: []wenpm1.Entry{{Owner: golden.Owner, Nonce: nonce, Amount: amount, Commitment: commitment, Selection: &selection}}, TotalStake: amount}}
	data, err := wenpm1.EncodeAccount(ledger)
	if err != nil {
		return result, err
	}
	decoded, err := wenpm1.ReadAccount(ledger.Binding.Ledger, ledger.Binding.Program, data, ledger.Binding)
	if err != nil {
		return result, err
	}
	receipt, err := wenpm1.ProjectReceipt(decoded, golden.Slot, 0)
	if err != nil {
		return result, err
	}
	bytes, err := wenpm1.EncodeReceipt(receipt)
	if err != nil {
		return result, err
	}
	result.Receipt, err = wenpm1.DecodeReceipt(bytes, ledger.Binding)
	result.AccountSHA256 = wenHash(data)
	result.ReceiptSHA256 = wenHash(bytes)
	return result, err
}
func migrateWENPaper(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var phase string
	if err = tx.QueryRowContext(ctx, "SELECT phase FROM update_pause WHERE id=1").Scan(&phase); err != nil {
		return err
	}
	if phase == "paused" {
		return errors.New("core metadata paused; finish recovery before WEN paper migration")
	}
	if _, err = tx.ExecContext(ctx, "PRAGMA user_version=10"); err != nil {
		return err
	}
	return tx.Commit()
}
