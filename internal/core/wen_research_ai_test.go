package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func researchPrepareBody(t *testing.T, c WENResearchReadInput) []byte {
	t.Helper()
	c.Sources, c.Calls = c.Sources[:1], c.Calls[:1]
	raw, err := json.Marshal(map[string]any{
		"schema": "wen-research-ai-prepare/1", "selection": c,
		"prompt": "Explain the selected research and its limitations.",
		"model":  "fixture-openai-v1", "memorySelection": map[string]any{"mode": "none"},
		"limits": AILimits{MaxInputTokens: 16000, MaxOutputTokens: 128, DeadlineSeconds: 3, BudgetMicrousd: 20000},
		"bounds": map[string]any{"maxModelAttempts": 3, "maxToolAttempts": 8, "wallSeconds": 90, "budgetMicrousd": 60000},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestWENResearchAIPrepare(t *testing.T) {
	s, h, token, c := readBatchFixture(t)
	w := apiRequest(h, "POST", "/api/v1/wen/research/ai-prepare", string(researchPrepareBody(t, c)), token, "http://127.0.0.1:7331", csrfToken(token))
	if w.Code != 200 {
		t.Fatalf("source-bound inert preparation: HTTP %d: %s", w.Code, w.Body.String())
	}
	var out WENResearchAIReview
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || out.DispatchEnabled || out.Request.State != "prepared" || out.Request.HeldMicrousd != 0 || out.Turn.StartedAt != 0 || len(out.Turn.Tools) != 0 || out.Turn.Models[0].Attempted {
		t.Fatal(w.Body.String())
	}
	d, rev, err := readAgents(t.Context(), s.db)
	if err != nil || taskAIRequestSlots(d) != 3 || len(d.ResearchTurns) != 1 || len(d.AIRequests) != 1 || rev != c.Revision+1 {
		t.Fatal("atomic turn/child/capacity", rev, err)
	}
	if out.Request.Context != researchContext(out.Turn) || out.Request.Digest != aiDigest(out.Request) || len(out.Request.Context) > 8000 || !strings.Contains(out.Request.Disclosure, `manual-ai-research-v1`) || !strings.Contains(out.Request.Context, `2026-10-22T11:59:58Z`) {
		t.Fatal("incomplete frozen disclosure")
	}
	before := retainedRevisionState(t, s)
	for range 2 {
		r, e := s.WENResearchAI(t.Context(), WENResearchAISelection{"wen-research-ai-selection/1", c.Profile, out.Turn.ID, 0}, false)
		if e != nil || r.SourceState != "unchanged" || r.Request.Digest != out.Request.Digest {
			t.Fatal("own child/revision invalidated root", r.SourceState, e)
		}
	}
	if before != retainedRevisionState(t, s) {
		t.Fatal("inspection wrote records")
	}
	if _, e := s.AgentAIRequestDraft(t.Context(), c.Profile, out.Request.ID); e == nil || !strings.Contains(e.Error(), "research") {
		t.Fatal("research stripped into ordinary draft", e)
	}
}

func researchInput(t *testing.T, c WENResearchReadInput) WENResearchAIPrepareInput {
	t.Helper()
	v, e := DecodeWENResearchAIPrepare(researchPrepareBody(t, c))
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func preparedResearch(t *testing.T, s *Store, c WENResearchAIPrepareInput) WENResearchAIReview {
	t.Helper()
	r, e := s.PrepareWENResearchAI(t.Context(), c)
	if e != nil {
		t.Fatal(e)
	}
	return r
}

type researchNoTransport struct{ calls atomic.Int32 }

func (r *researchNoTransport) RoundTrip(*http.Request) (*http.Response, error) {
	r.calls.Add(1)
	return nil, errors.New("unexpected research transport")
}

func TestWENResearchAIContext(t *testing.T) {
	t.Run("semantic-facts-reproducible-and-root-exact", func(t *testing.T) {
		s, _, _, c := readBatchFixture(t)
		in := researchInput(t, c)
		in.Bounds.MaxModelAttempts = 1
		a := preparedResearch(t, s, in)
		s.now = func() time.Time { return time.Unix(a.Turn.CreatedAt+20, 0) }
		_, rev, _ := readAgents(t.Context(), s.db)
		in.Selection.Revision = rev
		b := preparedResearch(t, s, in)
		if a.Turn.RootSHA256 != b.Turn.RootSHA256 || a.Turn.Root.FactsSHA256 != b.Turn.Root.FactsSHA256 || a.Request.Digest == b.Request.Digest || a.Turn.CreatedAt == b.Turn.CreatedAt {
			t.Fatal("invocation time leaked into semantic hash or request identity reused")
		}
	})
	t.Run("history-native-losses-and-gaps-preserved", func(t *testing.T) {
		s, _, _, c := readBatchFixture(t)
		in := researchInput(t, c)
		in.Selection.Sources = []WENReadSource{c.Sources[1], c.Sources[3]}
		in.Selection.Calls = []WENReadCall{c.Calls[2]}
		r := preparedResearch(t, s, in)
		original, e := s.WENResearchRead(t.Context(), WENResearchReadInput{Schema: c.Schema, Profile: c.Profile, SessionID: c.SessionID, Revision: r.Revision, LibraryRevision: c.LibraryRevision, Sources: in.Selection.Sources, Calls: in.Selection.Calls})
		if e != nil {
			t.Fatal(e)
		}
		b, _ := json.Marshal(original.Results[0].Data)
		if string(b) != string(r.Turn.Facts.Results[0].Data) || len(r.Turn.Facts.Results[0].Sources) == 0 {
			t.Fatal("history projection changed")
		}
	})
	for _, change := range []string{"selected-message", "persona", "new-conversation-message", "selected-note", "expired-note", "all-note-set", "deleted-source"} {
		t.Run(change, func(t *testing.T) {
			s, _, _, c := readBatchFixture(t)
			in := researchInput(t, c)
			if strings.Contains(change, "note") {
				injectRevisionAgents(t, s, func(d *agentDocument) {
					d.Memories = append(d.Memories, AgentMemory{ID: "chosen-note", ProfileID: c.Profile, Text: "Retained note", Source: "owner", ExpiresAt: s.now().Unix() + 60})
				})
				_, rev, _ := readAgents(t.Context(), s.db)
				in.Selection.Revision = rev
				in.MemorySelection = AIMemorySelection{Mode: "selected", IDs: []string{"chosen-note"}}
			}
			if change == "all-note-set" {
				in.MemorySelection = AIMemorySelection{Mode: "all"}
			}
			r := preparedResearch(t, s, in)
			injectRevisionAgents(t, s, func(d *agentDocument) {
				switch change {
				case "selected-message":
					d.Messages[0].Text = "Corrected source"
				case "persona":
					d.Profiles[agentProfile(*d, c.Profile)].Persona = "Changed persona"
				case "new-conversation-message":
					d.Messages = append(d.Messages, AgentMessage{ID: "later-user", ProfileID: c.Profile, SessionID: c.SessionID, Role: "user", Text: "New retained question", CreatedAt: s.now().Unix()})
				case "selected-note":
					d.Memories[len(d.Memories)-1].Text = "Corrected note"
				case "expired-note":
					s.now = func() time.Time { return time.Unix(r.Turn.CreatedAt+61, 0) }
				case "all-note-set":
					d.Memories = append(d.Memories, AgentMemory{ID: "later-note", ProfileID: c.Profile, Text: "Added active note", Source: "owner"})
				case "deleted-source":
					d.Messages = d.Messages[1:]
				}
			})
			before := retainedRevisionState(t, s)
			review, e := s.WENResearchAI(t.Context(), WENResearchAISelection{"wen-research-ai-selection/1", c.Profile, r.Turn.ID, 0}, false)
			if e != nil || review.SourceState != "source_changed" || before != retainedRevisionState(t, s) {
				t.Fatal(review.SourceState, e)
			}
		})
	}
	t.Run("unrelated-revisions-do-not-broaden-root", func(t *testing.T) {
		s, _, _, c := readBatchFixture(t)
		r := preparedResearch(t, s, researchInput(t, c))
		injectRevisionAgents(t, s, func(d *agentDocument) {
			d.Messages = append(d.Messages, AgentMessage{ID: "other", ProfileID: "news-researcher", SessionID: "foreign", Role: "user", Text: "Never disclose this"})
		})
		_, e := s.ChangeWENResearch(t.Context(), WENResearchCommand{Profile: c.Profile, Revision: c.LibraryRevision, Action: "save", Raw: strings.Replace(researchRaw(t), "Fixture session draft", "Unselected append", 1)})
		if e != nil {
			t.Fatal(e)
		}
		out, e := s.WENResearchAI(t.Context(), WENResearchAISelection{"wen-research-ai-selection/1", c.Profile, r.Turn.ID, 0}, false)
		if e != nil || out.SourceState != "unchanged" || strings.Contains(out.Request.Disclosure, "Never disclose this") {
			t.Fatal(out.SourceState, e)
		}
	})
}

func TestWENResearchAIRefusals(t *testing.T) {
	for _, name := range []string{"unknown", "duplicate", "missing-model", "null-bounds", "bad-limit", "aggregate-budget", "model-cap", "tool-cap", "wall-cap", "foreign", "stale", "oversized-context", "request-slots", "storage", "cancelled-context"} {
		t.Run(name, func(t *testing.T) {
			s, h, token, c := readBatchFixture(t)
			in := researchInput(t, c)
			raw := researchPrepareBody(t, c)
			switch name {
			case "unknown":
				raw = []byte(strings.Replace(string(raw), `"schema":`, `"unknown":1,"schema":`, 1))
			case "duplicate":
				raw = []byte(strings.Replace(string(raw), `"prompt":`, `"prompt":"duplicate","prompt":`, 1))
			case "missing-model":
				var fields map[string]json.RawMessage
				json.Unmarshal(raw, &fields)
				delete(fields, "model")
				raw, _ = json.Marshal(fields)
			case "null-bounds":
				var fields map[string]json.RawMessage
				json.Unmarshal(raw, &fields)
				fields["bounds"] = json.RawMessage("null")
				raw, _ = json.Marshal(fields)
			case "bad-limit":
				in.Limits.MaxInputTokens = 1000
			case "aggregate-budget":
				in.Bounds.BudgetMicrousd = 1
			case "model-cap":
				in.Bounds.MaxModelAttempts = 4
			case "tool-cap":
				in.Bounds.MaxToolAttempts = 9
			case "wall-cap":
				in.Bounds.WallSeconds = 91
			case "foreign":
				in.Selection.Profile = "news-researcher"
			case "stale":
				in.Selection.Revision++
			case "oversized-context":
				injectRevisionAgents(t, s, func(d *agentDocument) { d.Messages[0].Text = strings.Repeat("\\\"<λ", 350) })
				d, rev, _ := readAgents(t.Context(), s.db)
				in.Selection.Revision = rev
				in.Selection.Sources[0].TextSHA256 = wenHash([]byte(d.Messages[0].Text))
				in.Selection.Calls = append(in.Selection.Calls, WENReadCall{ID: "second", Tool: "wen.research.snapshot", SourceRefs: []string{"snapshot"}})
			case "request-slots":
				for range 8 {
					_, rev, _ := readAgents(t.Context(), s.db)
					in.Selection.Revision = rev
					r := preparedResearch(t, s, in)
					if _, err := s.WENResearchAI(t.Context(), WENResearchAISelection{"wen-research-ai-selection/1", c.Profile, r.Turn.ID, r.Revision}, true); err != nil {
						t.Fatal(err)
					}
				}
				_, rev, _ := readAgents(t.Context(), s.db)
				in.Selection.Revision = rev
			case "storage":
				injectRevisionAgents(t, s, func(d *agentDocument) {
					d.Messages = append(d.Messages, AgentMessage{ID: "unselected", ProfileID: "news-researcher", Role: "user", Text: strings.Repeat("x", 200000)})
				})
				_, rev, _ := readAgents(t.Context(), s.db)
				in.Selection.Revision = rev
			}
			if name != "unknown" && name != "duplicate" && name != "missing-model" && name != "null-bounds" {
				raw, _ = json.Marshal(in)
			}
			before := retainedRevisionState(t, s)
			if name == "cancelled-context" {
				ctx, stop := context.WithCancel(t.Context())
				stop()
				if _, e := s.PrepareWENResearchAI(ctx, in); e == nil {
					t.Fatal("cancelled prepare accepted")
				}
			} else {
				w := apiRequest(h, "POST", "/api/v1/wen/research/ai-prepare", string(raw), token, "http://127.0.0.1:7331", csrfToken(token))
				if w.Code == 200 {
					t.Fatal("refusal accepted", w.Body.String())
				}
			}
			if before != retainedRevisionState(t, s) {
				t.Fatal("failed preparation wrote or evicted records")
			}
		})
	}
}

func TestWENResearchAIRecovery(t *testing.T) {
	t.Run("no-provider-route-including-fixture", func(t *testing.T) {
		s, _, _, c := readBatchFixture(t)
		r := preparedResearch(t, s, researchInput(t, c))
		rt := &researchNoTransport{}
		before := retainedRevisionState(t, s)
		if _, e := s.changeAgents(t.Context(), AgentCommand{Revision: r.Revision, ProfileID: c.Profile, Action: "ai-start", ID: r.Request.ID, ConsentDigest: r.Request.Digest}, true); e == nil {
			t.Fatal("fixture admitted research")
		}
		if _, e := callAgentAIAt(t.Context(), r.Request, rt, s.now); e == nil {
			t.Fatal("direct provider admitted research")
		}
		if e := s.advanceAgentAI(t.Context(), rt, true); e != nil {
			t.Fatal(e)
		}
		if rt.calls.Load() != 0 || before != retainedRevisionState(t, s) {
			t.Fatal("prepared research dispatched or mutated")
		}
		injectRevisionAgents(t, s, func(d *agentDocument) { d.AIRequests[0].State = "running"; d.AIRequests[0].HeldMicrousd = 100 })
		if e := s.advanceAgentAI(t.Context(), rt, true); e != nil {
			t.Fatal(e)
		}
		d, _, e := readAgents(t.Context(), s.db)
		if e != nil || d.AIRequests[0].State != "unknown" || d.AIRequests[0].HeldMicrousd != 100 || d.ResearchTurns[0].State != "held" || rt.calls.Load() != 0 {
			t.Fatal("unknown dispatch replay/release", e)
		}
		_, rev, _ := readAgents(t.Context(), s.db)
		cancel, e := s.WENResearchAI(t.Context(), WENResearchAISelection{"wen-research-ai-selection/1", c.Profile, r.Turn.ID, rev}, true)
		if e != nil || cancel.Request.HeldMicrousd != 100 || cancel.Turn.State != "cancelled" {
			t.Fatal("cancel erased unknown liability", e)
		}
	})
	t.Run("cancel-restart-clock-and-expiry", func(t *testing.T) {
		s, _, _, c := readBatchFixture(t)
		r := preparedResearch(t, s, researchInput(t, c))
		n, e := OpenStore(s.dir)
		if e != nil {
			t.Fatal(e)
		}
		defer n.Close()
		n.now = func() time.Time { return time.Unix(r.Turn.CreatedAt+1, 0) }
		sel := WENResearchAISelection{"wen-research-ai-selection/1", c.Profile, r.Turn.ID, 0}
		out, e := n.WENResearchAI(t.Context(), sel, false)
		if e != nil || out.SourceState != "unchanged" || out.Request.Digest != r.Request.Digest {
			t.Fatal("restart changed roots", e)
		}
		for _, tc := range []struct {
			delta int64
			state string
		}{{-1, "clock_ambiguous"}, {601, "expired"}} {
			n.now = func() time.Time { return time.Unix(r.Turn.CreatedAt+tc.delta, 0) }
			out, e = n.WENResearchAI(t.Context(), sel, false)
			if e != nil || out.SourceState != tc.state {
				t.Fatal(tc.state, out.SourceState, e)
			}
		}
		sel.Revision = r.Revision
		out, e = n.WENResearchAI(t.Context(), sel, true)
		if e != nil || out.Turn.State != "cancelled" || out.Request.HeldMicrousd != 0 {
			t.Fatal("cancel", e)
		}
		d, _, e := readAgents(t.Context(), n.db)
		if e != nil || taskAIRequestSlots(d) != 1 {
			t.Fatal("unused slots not released once", e)
		}
		before := retainedRevisionState(t, n)
		sel.Revision = out.Revision
		if _, e = n.WENResearchAI(t.Context(), sel, true); e == nil || before != retainedRevisionState(t, n) {
			t.Fatal("duplicate cancellation changed records")
		}
	})
	t.Run("unknown-format-refuses-without-erasure", func(t *testing.T) {
		s, _, _, c := readBatchFixture(t)
		preparedResearch(t, s, researchInput(t, c))
		injectRevisionAgents(t, s, func(d *agentDocument) { d.ResearchTurns[0].Schema = "wen-research-turn/99" })
		before := retainedRevisionState(t, s)
		if _, _, e := readAgents(t.Context(), s.db); e == nil {
			t.Fatal("unknown format read")
		}
		if _, e := OpenStore(s.dir); e == nil {
			t.Fatal("unknown format opened")
		}
		if before != retainedRevisionState(t, s) {
			t.Fatal("unknown format erased")
		}
	})
}

func TestWENResearchAIMigration(t *testing.T) {
	t.Run("schema-11-records-preserved", func(t *testing.T) {
		s, _, _, _ := readBatchFixture(t)
		var raw string
		var beforeRevision int64
		if err := s.db.QueryRow("SELECT revision FROM agent_workspace WHERE id=1").Scan(&beforeRevision); err != nil {
			t.Fatal(err)
		}
		s.db.QueryRow("SELECT document FROM agent_workspace WHERE id=1").Scan(&raw)
		var fields map[string]json.RawMessage
		json.Unmarshal([]byte(raw), &fields)
		delete(fields, "researchTurns")
		legacy, _ := json.Marshal(fields)
		s.db.Exec("UPDATE agent_workspace SET document=? WHERE id=1", string(legacy))
		dropEmptyHistoryFixture(t, s)
		s.db.Exec("PRAGMA user_version=11")
		n, e := OpenStore(s.dir)
		if e != nil {
			t.Fatal(e)
		}
		defer n.Close()
		var schema int
		n.db.QueryRow("PRAGMA user_version").Scan(&schema)
		if schema != StateSchema {
			t.Fatal(schema)
		}
		var afterRevision int64
		if err := n.db.QueryRow("SELECT revision FROM agent_workspace WHERE id=1").Scan(&afterRevision); err != nil || afterRevision != beforeRevision {
			t.Fatalf("format-only migration changed workspace revision: before=%d after=%d error=%v", beforeRevision, afterRevision, err)
		}
		var after string
		n.db.QueryRow("SELECT document FROM agent_workspace WHERE id=1").Scan(&after)
		var migrated map[string]json.RawMessage
		json.Unmarshal([]byte(after), &migrated)
		for key, b := range fields {
			if researchHash(json.RawMessage(b)) != researchHash(migrated[key]) {
				t.Fatal("legacy record changed", key)
			}
		}
		if string(migrated["researchTurns"]) != "[]" {
			t.Fatal("migration created research work")
		}
	})
	t.Run("paused-schema-11-refuses", func(t *testing.T) {
		s := fixtureStore(t)
		cmd := pauseCommand(t, s, 1)
		if _, e := s.UpdatePause(t.Context(), cmd); e != nil {
			t.Fatal(e)
		}
		s.db.Exec("PRAGMA user_version=11")
		var raw string
		s.db.QueryRow("SELECT document FROM agent_workspace WHERE id=1").Scan(&raw)
		if _, e := OpenStore(s.dir); !errors.Is(e, ErrCorePaused) {
			t.Fatal("paused migration", e)
		}
		var after string
		var schema int
		s.db.QueryRow("SELECT document FROM agent_workspace WHERE id=1").Scan(&after)
		s.db.QueryRow("PRAGMA user_version").Scan(&schema)
		if raw != after || schema != 11 {
			t.Fatal("paused state changed")
		}
	})
}

func TestWENResearchAIExactDisclosureBoundary(t *testing.T) {
	s, _, _, c := readBatchFixture(t)
	in := researchInput(t, c)
	in.Prompt = "p"
	in.MemorySelection = AIMemorySelection{Mode: "all"}
	in.Bounds.MaxModelAttempts = 1
	injectRevisionAgents(t, s, func(d *agentDocument) {
		d.Messages[0].Text = strings.Repeat("m", 1500)
		d.Memories = append(d.Memories, AgentMemory{ID: "byte-note", ProfileID: c.Profile, Text: "λ<\"&", Source: "owner"})
	})
	measure := func() int {
		d, rev, e := readAgents(t.Context(), s.db)
		if e != nil {
			t.Fatal(e)
		}
		in.Selection.Revision = rev
		in.Selection.Sources[0].TextSHA256 = wenHash([]byte(d.Messages[0].Text))
		facts, e := researchFacts(t.Context(), s.db, in.Selection)
		if e != nil {
			t.Fatal(e)
		}
		root, e := researchBase(d, c.Profile, c.SessionID, in.Prompt, in.MemorySelection, s.now().Unix())
		if e != nil {
			t.Fatal(e)
		}
		root.FactsSHA256 = researchHash(facts)
		return len(researchContext(WENResearchTurn{ID: strings.Repeat("t", 43), Root: root, RootSHA256: researchHash(root), Facts: facts, Bounds: in.Bounds}))
	}
	extra := 6200 - measure()
	if extra < 0 {
		extra = 0
	}
	injectRevisionAgents(t, s, func(d *agentDocument) { d.Memories[len(d.Memories)-1].Text += strings.Repeat("n", extra) })
	padding := 8000 - measure() + 1
	if padding < 1 || padding >= 2000 {
		t.Fatal("boundary fixture", padding)
	}
	in.Prompt = strings.Repeat("p", padding)
	r := preparedResearch(t, s, in)
	if len(r.Request.Context) != 8000 || !strings.Contains(r.Request.Context, `\u003c`) {
		t.Fatal("encoded UTF-8 boundary or escaping", len(r.Request.Context))
	}
	in.Prompt += "p"
	in.Selection.Revision = r.Revision
	before := retainedRevisionState(t, s)
	if _, e := s.PrepareWENResearchAI(t.Context(), in); e == nil || !strings.Contains(e.Error(), "8000 bytes") {
		t.Fatal("8001-byte disclosure accepted or wrong gate", e)
	}
	if before != retainedRevisionState(t, s) {
		t.Fatal("oversized context changed retained state")
	}
}

func TestWENResearchAIHTTPAuthority(t *testing.T) {
	s, h, token, c := readBatchFixture(t)
	for _, action := range []string{"prepare", "inspect", "cancel"} {
		for _, mode := range []string{"auth", "csrf", "origin", "query", "method"} {
			t.Run(action+"/"+mode, func(t *testing.T) {
				method, auth, origin, csrf, query := "POST", token, "http://127.0.0.1:7331", csrfToken(token), ""
				switch mode {
				case "auth":
					auth = ""
				case "csrf":
					csrf = ""
				case "origin":
					origin = "https://foreign.invalid"
				case "query":
					query = "?unexpected=1"
				case "method":
					method = "GET"
				}
				before := retainedRevisionState(t, s)
				w := apiRequest(h, method, "/api/v1/wen/research/ai-"+action+query, string(researchPrepareBody(t, c)), auth, origin, csrf)
				if w.Code == 200 || before != retainedRevisionState(t, s) {
					t.Fatal("authority bypass", w.Code)
				}
			})
		}
	}
	raw := researchPrepareBody(t, c)
	first := apiRequest(h, "POST", "/api/v1/wen/research/ai-prepare", string(raw), token, "http://127.0.0.1:7331", csrfToken(token))
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body.String())
	}
	before := retainedRevisionState(t, s)
	duplicate := apiRequest(h, "POST", "/api/v1/wen/research/ai-prepare", string(raw), token, "http://127.0.0.1:7331", csrfToken(token))
	if duplicate.Code != 409 || before != retainedRevisionState(t, s) {
		t.Fatal("replayed preparation was not atomically refused", duplicate.Code)
	}
}
