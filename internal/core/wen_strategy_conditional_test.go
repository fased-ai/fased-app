package core

import (
	"encoding/json"
	"github.com/fased-ai/fased-app/internal/wenstrategy"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func conditionalProposalFixture(t *testing.T) (*Store, *HTTPHandler, string, WENStrategyProposalInput) {
	t.Helper()
	s, h, token, c := proposalFixture(t)
	card := map[string]any{"schema": "wen-strategy-change/2", "parentDigest": c.ParentDigest, "changes": map[string]any{
		"conditions": []any{map[string]any{"metric": "realizedVolBps", "lookbackMinutes": 30, "operator": "lte", "valueBps": "100"}},
		"rules":      []any{map[string]any{"when": []any{map[string]any{"metric": "returnBps", "lookbackMinutes": 30, "operator": "gte", "valueBps": "15"}}, "preset": "custom", "weightsBps": []int{0, 0, 2000, 3000, 5000}}},
	}}
	raw, _ := json.Marshal(card)
	injectRevisionAgents(t, s, func(d *agentDocument) { d.AIRequests[0].Cards[0].Text = string(raw) })
	return s, h, token, c
}
func TestWENStrategyConditionalProposal(t *testing.T) {
	_, h, token, c := conditionalProposalFixture(t)
	w := proposalRequest(h, token, c, false)
	if w.Code != 200 {
		t.Fatalf("versioned conditional proposal required: HTTP %d %s", w.Code, w.Body.String())
	}
	var result map[string]any
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || result["schema"] != "wen-strategy-proposal-review/2" {
		t.Fatal("v2 review required")
	}
}

func TestWENStrategyConditionalSave(t *testing.T) {
	s, h, token, c := conditionalProposalFixture(t)
	before := retainedRevisionState(t, s)
	original, _, _ := readAgents(t.Context(), s.db)
	r := proposalReview(t, h, token, c)
	if retainedRevisionState(t, s) != before {
		t.Fatal("review wrote records")
	}
	parent, e := wenstrategy.Decode([]byte(r.Parent.Raw))
	if e != nil {
		t.Fatal(e)
	}
	candidate, e := wenstrategy.Decode([]byte(r.Candidate.Raw))
	if e != nil {
		t.Fatal(e)
	}
	if r.Schema != "wen-strategy-proposal-review/2" || r.EntryConditions == nil || !reflect.DeepEqual(*r.EntryConditions, candidate.Document.Research.Conditions) || r.BranchAmounts == nil || len(*r.BranchAmounts) != 1 || candidate.Document.Allocation.Mode != "conditional" || len(candidate.Document.Research.Conditions) != 1 {
		t.Fatal("v2 rules/conditions not preserved")
	}
	if candidate.Document.Market != parent.Document.Market || candidate.Document.Limits != parent.Document.Limits || candidate.Document.Execution != parent.Document.Execution || candidate.Document.Research.HorizonMinutes != parent.Document.Research.HorizonMinutes || !reflect.DeepEqual(candidate.Document.Schedule, parent.Document.Schedule) {
		t.Fatal("undeclared controls changed")
	}
	branch := (*r.BranchAmounts)[0]
	compiled, e := wenstrategy.CompileAllocation(parent.Document.Limits.PerRoundLamports, candidate.Document.Allocation.Rules[0].WeightsBps)
	if e != nil || branch.AmountsLamports != compiled || !reflect.DeepEqual(branch.When, candidate.Document.Allocation.Rules[0].When) {
		t.Fatal("branch amounts differ from F1", e)
	}
	w := proposalRequest(h, token, r, true)
	var out WENStrategyRevisionResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	if out.Library.Revision != c.LibraryRevision+1 || out.AgentRevision != c.Revision+1 || out.Library.Versions[1].Raw != r.Candidate.Raw || out.Library.Versions[1].Origin != "wen-chat-ai-revision-v2:"+out.Receipt.ID || !strings.Contains(out.Receipt.Text, `"schema":"wen-strategy-proposal-receipt/2"`) {
		t.Fatal("atomic v2 source receipt")
	}
	after, _, e := readAgents(t.Context(), s.db)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(after.AIRequests, original.AIRequests) || !reflect.DeepEqual(after.ResearchTurns, original.ResearchTurns) || !reflect.DeepEqual(after.Tasks, original.Tasks) || !reflect.DeepEqual(after.Bots, original.Bots) {
		t.Fatal("save changed authority, tasks or original research")
	}
	if len(after.Messages) != len(original.Messages)+1 {
		t.Fatal("source message overwritten")
	}
	n, e := OpenStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer n.Close()
	n.now = s.now
	library, e := n.WENResearch(t.Context(), c.Profile)
	if e != nil || library.Versions[1].Raw != r.Candidate.Raw {
		t.Fatal("restart lost conditional candidate", e)
	}
	before = retainedRevisionState(t, n)
	if w = proposalRequest(h, token, r, true); w.Code == 200 || before != retainedRevisionState(t, n) {
		t.Fatal("duplicate saved")
	}
	dir, e := filepath.Abs("../../.evidence/f3-conditional-proposals")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(map[string]any{"raw": r.Candidate.Raw, "digest": r.Candidate.Digest, "amountsLamports": r.AmountsLamports, "branchAmounts": r.BranchAmounts})
	if e = os.WriteFile(filepath.Join(dir, "canonical-candidate.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
}

func TestWENStrategyConditionalRefusals(t *testing.T) {
	for _, tc := range []struct{ name, override string }{
		{"null-rules", `{"rules":null}`},
		{"null-conditions", `{"conditions":null}`},
		{"empty", `{}`},
		{"unknown-market", `{"market":{}}`},
		{"budget-expansion", `{"limits":{"totalTurnoverLamports":"999999999999999"}}`},
		{"execution-grant", `{"execution":{"mode":"bounded-auto"}}`},
		{"nested-tool", `{"conditions":[{"metric":"returnBps","lookbackMinutes":30,"operator":"gte","valueBps":"1","url":"https://example.test"}]}`},
		{"unsupported-metric", `{"conditions":[{"metric":"confidence","lookbackMinutes":30,"operator":"gte","valueBps":"1"}]}`},
		{"null-condition", `{"conditions":[null]}`},
		{"empty-rule", `{"rules":[{"when":[],"preset":"wide","weightsBps":[2000,2000,2000,2000,2000]}]}`},
		{"null-rule", `{"rules":[null]}`},
		{"bad-sum", `{"rules":[{"when":[{"metric":"returnBps","lookbackMinutes":30,"operator":"gte","valueBps":"1"}],"preset":"custom","weightsBps":[2000,2000,2000,2000,2001]}]}`},
		{"dust", `{"rules":[{"when":[{"metric":"returnBps","lookbackMinutes":30,"operator":"gte","valueBps":"1"}],"preset":"custom","weightsBps":[1,1999,2000,3000,3000]}]}`},
		{"duplicate", `{"conditions":[],"conditions":[]}`},
		{"alias", `{"Rules":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, h, token, c := conditionalProposalFixture(t)
			c.Changes = tc.override
			before := retainedRevisionState(t, s)
			w := proposalRequest(h, token, c, false)
			if w.Code == 200 || before != retainedRevisionState(t, s) {
				t.Fatal("invalid conditional revision admitted", w.Code, w.Body.String())
			}
		})
	}
	for _, mode := range []string{"schema1-rules", "unknown-schema", "oversized-card", "nine-rules", "33-conditions", "preview-tamper", "condition-preview-tamper", "downgrade-review", "changed-source", "stale-revision", "expired", "paused"} {
		t.Run(mode, func(t *testing.T) {
			s, h, token, c := conditionalProposalFixture(t)
			switch mode {
			case "schema1-rules":
				injectRevisionAgents(t, s, func(d *agentDocument) {
					d.AIRequests[0].Cards[0].Text = strings.Replace(d.AIRequests[0].Cards[0].Text, "wen-strategy-change/2", "wen-strategy-change/1", 1)
				})
			case "unknown-schema":
				injectRevisionAgents(t, s, func(d *agentDocument) {
					d.AIRequests[0].Cards[0].Text = strings.Replace(d.AIRequests[0].Cards[0].Text, "wen-strategy-change/2", "wen-strategy-change/3", 1)
				})
			case "oversized-card":
				injectRevisionAgents(t, s, func(d *agentDocument) {
					v := &d.AIRequests[0].Cards[0]
					v.Text += strings.Repeat(" ", 1001-len(v.Text))
				})
			case "nine-rules":
				rule := wenstrategy.Rule{When: []wenstrategy.Condition{{Metric: "returnBps", LookbackMinutes: 30, Operator: "gte", ValueBps: "1"}}, Preset: "wide", WeightsBps: []int{2000, 2000, 2000, 2000, 2000}}
				rules := []wenstrategy.Rule{}
				for range 9 {
					rules = append(rules, rule)
				}
				b, _ := json.Marshal(map[string]any{"rules": rules})
				c.Changes = string(b)
			case "33-conditions":
				conditions := []wenstrategy.Condition{}
				for range 33 {
					conditions = append(conditions, wenstrategy.Condition{Metric: "returnBps", LookbackMinutes: 30, Operator: "gte", ValueBps: "1"})
				}
				b, _ := json.Marshal(map[string]any{"conditions": conditions})
				c.Changes = string(b)
			}
			if mode == "schema1-rules" || mode == "unknown-schema" || mode == "oversized-card" || mode == "nine-rules" || mode == "33-conditions" {
				before := retainedRevisionState(t, s)
				w := proposalRequest(h, token, c, false)
				if w.Code == 200 || before != retainedRevisionState(t, s) {
					t.Fatal("unsupported card/limit accepted", w.Code)
				}
				return
			}
			r := proposalReview(t, h, token, c)
			switch mode {
			case "condition-preview-tamper":
				(*r.EntryConditions)[0].ValueBps = "999"
			case "downgrade-review":
				r.Schema = "wen-strategy-proposal-review/1"
			case "preview-tamper":
				(*r.BranchAmounts)[0].AmountsLamports[0] = "999999999"
			case "changed-source":
				injectRevisionAgents(t, s, func(d *agentDocument) { d.AIRequests[0].Cards[0].Text += " " })
			case "stale-revision":
				agentApply(t, s, AgentCommand{Action: "message-add", ProfileID: c.Profile, ID: r.Source.SessionID, Text: "New review input"})
			case "expired":
				s.now = func() time.Time { return time.Unix(r.ExpiresAt, 0) }
			case "paused":
				s.db.Exec("UPDATE update_pause SET phase='paused' WHERE id=1")
			}
			before := retainedRevisionState(t, s)
			w := proposalRequest(h, token, r, true)
			if w.Code == 200 || before != retainedRevisionState(t, s) {
				t.Fatal("changed/revoked review saved", w.Code, w.Body.String())
			}
		})
	}
}

func TestWENStrategyConditionalParentAndClearing(t *testing.T) {
	s, h, token, c := conditionalProposalFixture(t)
	review := proposalReview(t, h, token, c)
	lib, e := s.ChangeWENResearch(t.Context(), WENResearchCommand{Profile: c.Profile, Revision: c.LibraryRevision, Action: "save", Raw: review.Candidate.Raw})
	if e != nil {
		t.Fatal(e)
	}
	c.ParentDigest, c.LibraryRevision = review.Candidate.Digest, lib.Revision
	injectRevisionAgents(t, s, func(d *agentDocument) {
		d.AIRequests[0].Cards[0].Text = `{"schema":"wen-strategy-change/2","parentDigest":"` + c.ParentDigest + `","changes":{"conditions":[],"rules":[]}}`
	})
	r := proposalReview(t, h, token, c)
	doc, e := wenstrategy.Decode([]byte(r.Candidate.Raw))
	if e != nil || len(doc.Document.Research.Conditions) != 0 || doc.Document.Allocation.Mode != "fixed" || len(doc.Document.Allocation.Rules) != 0 || r.BranchAmounts == nil || len(*r.BranchAmounts) != 0 {
		t.Fatal("explicit clear semantics", e)
	}
	var fields map[string]json.RawMessage
	b, _ := json.Marshal(r)
	json.Unmarshal(b, &fields)
	if string(fields["branchAmounts"]) != "[]" {
		t.Fatal("missing empty v2 branch list")
	}
	c.Changes = `{"thesis":"trend"}`
	r = proposalReview(t, h, token, c)
	if r.Schema != "wen-strategy-proposal-review/2" {
		t.Fatal("conditional parent review downgraded")
	}
}

func TestWENStrategyConditionalResearchCard(t *testing.T) {
	s, h, token, c := readBatchFixture(t)
	r := loopPrepared(t, s, c, 1, 1)
	library, e := s.WENResearch(t.Context(), c.Profile)
	if e != nil {
		t.Fatal(e)
	}
	parent := library.Versions[0]
	text := `{"schema":"wen-strategy-change/2","parentDigest":"` + parent.Digest + `","changes":{"conditions":[{"metric":"returnBps","lookbackMinutes":30,"operator":"gte","valueBps":"10"}],"rules":[{"when":[{"metric":"returnBps","lookbackMinutes":30,"operator":"gte","valueBps":"25"}],"preset":"wide","weightsBps":[2000,2000,2000,2000,2000]}]}}`
	r = loopAdvance(t, s, r, loopTransport(t, func(f *researchLoopFrame) {
		f.Decision = "complete"
		f.CallRefs = []string{}
		f.Cards = []researchLoopCard{{"paper-strategy-draft", "Conditional proposal", text}}
	}, nil))
	if r.Turn.State != "completed" || len(r.Request.Cards) != 1 {
		t.Fatal("research card missing")
	}
	before, _, _ := readAgents(t.Context(), s.db)
	input := WENStrategyProposalInput{Profile: c.Profile, Revision: r.Revision, LibraryRevision: library.Revision, RequestID: r.Request.ID, CardID: r.Request.Cards[0].ID, ParentDigest: parent.Digest, Name: "Reviewed research conditional"}
	review := proposalReview(t, h, token, input)
	w := proposalRequest(h, token, review, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	after, _, e := readAgents(t.Context(), s.db)
	if e != nil || !reflect.DeepEqual(before.AIRequests, after.AIRequests) || !reflect.DeepEqual(before.ResearchTurns, after.ResearchTurns) || !reflect.DeepEqual(before.Tasks, after.Tasks) {
		t.Fatal("handoff altered completed research or authority", e)
	}
}
