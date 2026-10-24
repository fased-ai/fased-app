package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
)

func loopJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func loopTransport(t *testing.T, edit func(*researchLoopFrame), rawEdit func([]byte) []byte) http.RoundTripper {
	t.Helper()
	return aiTestTransport(func(req *http.Request) (*http.Response, error) {
		res, e := (fixtureAITransport{}).RoundTrip(req)
		if e != nil || req.URL.Path != "/v1/responses" {
			return res, e
		}
		var body map[string]any
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if json.Unmarshal(b, &body) != nil {
			t.Fatal("fixture body")
		}
		content := body["output"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)
		var f researchLoopFrame
		if json.Unmarshal([]byte(content["text"].(string)), &f) != nil {
			t.Fatal("fixture frame")
		}
		if edit != nil {
			edit(&f)
		}
		b = loopJSON(t, f)
		if rawEdit != nil {
			b = rawEdit(b)
		}
		content["text"] = string(b)
		res.Body = io.NopCloser(bytes.NewReader(loopJSON(t, body)))
		return res, nil
	})
}
func loopAdvance(t *testing.T, s *Store, r WENResearchAIReview, rt http.RoundTripper) WENResearchAIReview {
	t.Helper()
	loopStart(t, s, r)
	if e := s.advanceAgentAI(t.Context(), rt, true); e != nil {
		t.Fatal(e)
	}
	return loopInspect(t, s, r)
}
func loopCancel(t *testing.T, s *Store, r WENResearchAIReview) WENResearchAIReview {
	t.Helper()
	r = loopInspect(t, s, r)
	v, e := s.WENResearchAI(t.Context(), WENResearchAISelection{"wen-research-ai-selection/2", r.Turn.Profile, r.Turn.ID, r.Revision}, true)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func loopNextInput(r WENResearchAIReview) WENResearchAINextInput {
	return WENResearchAINextInput{"wen-research-ai-next/1", r.Turn.Profile, r.Turn.ID, r.Revision, r.Request.ID, r.Turn.Models[len(r.Turn.Models)-1].FrameSHA256}
}

func TestWENResearchLoopScope(t *testing.T) {
	s, h, token, c := readBatchFixture(t)
	r := loopPrepared(t, s, c, 2, 3)
	good := researchLoopFrame{"wen-research-step/1", r.Turn.ID, r.Turn.RootSHA256, 1, "read", []string{"read-1"}, "Review the selected record.", []researchLoopCard{}}
	for _, tc := range []struct {
		name string
		edit func(*researchLoopFrame)
	}{
		{"schema", func(f *researchLoopFrame) { f.Schema = "wen-research-step/2" }},
		{"turn", func(f *researchLoopFrame) { f.TurnID = "other" }},
		{"root", func(f *researchLoopFrame) { f.RootSHA256 = strings.Repeat("0", 64) }},
		{"step", func(f *researchLoopFrame) { f.Step = 2 }},
		{"foreign-call", func(f *researchLoopFrame) { f.CallRefs = []string{"discover-new-source"} }},
		{"duplicate-call", func(f *researchLoopFrame) { f.CallRefs = []string{"read-1", "read-1"} }},
		{"null-calls", func(f *researchLoopFrame) { f.CallRefs = nil }},
		{"null-cards", func(f *researchLoopFrame) { f.Cards = nil }},
		{"empty-read", func(f *researchLoopFrame) { f.CallRefs = []string{} }},
		{"read-with-card", func(f *researchLoopFrame) { f.Cards = []researchLoopCard{{"reminder-draft", "Title", "Text"}} }},
		{"complete-with-calls", func(f *researchLoopFrame) { f.Decision = "complete" }},
		{"answer-2001", func(f *researchLoopFrame) { f.Answer = strings.Repeat("a", 2001) }},
		{"utf8-byte-limit", func(f *researchLoopFrame) { f.Answer = strings.Repeat("λ", 1001) }},
		{"NUL", func(f *researchLoopFrame) { f.Answer = "bad\x00text" }},
		{"tool-output", func(f *researchLoopFrame) { f.Decision = "tool_call" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := good
			tc.edit(&f)
			if _, e := loopFrame(r.Request, loopJSON(t, f)); e == nil {
				t.Fatal("unscoped frame accepted")
			}
		})
	}
	raw := loopJSON(t, good)
	for _, b := range [][]byte{bytes.Replace(raw, []byte(`"schema":`), []byte(`"schema":"duplicate","schema":`), 1), bytes.Replace(raw, []byte(`"step":1`), []byte(`"step":1,"arguments":{}`), 1), bytes.Replace(raw, []byte(`"answer":"Review the selected record."`), []byte(`"answer":null`), 1), bytes.Replace(raw, []byte(`"turnId":`), []byte(`"TurnId":`), 1)} {
		if _, e := loopFrame(r.Request, b); e == nil {
			t.Fatal("ambiguous frame accepted")
		}
	}
	if _, e := loopFrame(r.Request, raw); e != nil {
		t.Fatal("original read refused", e)
	}
	final := good
	final.Decision = "complete"
	final.CallRefs = []string{}
	final.Cards = []researchLoopCard{{"paper-strategy-draft", strings.Repeat("t", 80), strings.Repeat("x", 1000)}}
	if _, e := loopFrame(r.Request, loopJSON(t, final)); e != nil {
		t.Fatal("bounded terminal card", e)
	}
	final.Cards[0].Title += "x"
	if _, e := loopFrame(r.Request, loopJSON(t, final)); e == nil {
		t.Fatal("oversized title")
	}
	wire := r.Request.Disclosure
	for _, v := range []string{`"tools":[]`, `"tool_choice":"none"`, `"store":false`, `"background":false`, `fased_research_step_v1`, researchLoopInstructions} {
		if !strings.Contains(wire, v) {
			t.Fatal("closed wire", v)
		}
	}
	before := retainedRevisionState(t, s)
	for _, args := range []struct{ path, body, auth string }{
		{"ai-prepare-next", string(loopJSON(t, WENResearchAINextInput{"wen-research-ai-next/1", r.Turn.Profile, r.Turn.ID, r.Revision, r.Request.ID, "bad"})), ""},
		{"ai-start", string(loopJSON(t, WENResearchAIStartInput{"wen-research-ai-start/2", r.Turn.Profile, r.Turn.ID, r.Revision, r.Request.ID, r.Request.Digest, 1})), token},
	} {
		w := apiRequest(h, "POST", "/api/v1/wen/research/"+args.path, args.body, args.auth, "http://127.0.0.1:7331", csrfToken(args.auth))
		if w.Code == 200 {
			t.Fatal("auth/ordinary admission bypass")
		}
	}
	if before != retainedRevisionState(t, s) {
		t.Fatal("rejected admission wrote")
	}
}

func TestWENResearchLoopTranscriptBatch(t *testing.T) {
	s, _, _, c := readBatchFixture(t)
	r := loopPrepared(t, s, c, 2, 2)
	sequence := []string{}
	s.researchRead = func(_ context.Context, f researchFactResult) (json.RawMessage, error) {
		sequence = append(sequence, f.ID)
		if f.ID == "read-2" {
			return nil, errors.New("local source temporarily unavailable")
		}
		return f.Data, nil
	}
	r = loopAdvance(t, s, r, loopTransport(t, func(f *researchLoopFrame) { f.CallRefs = []string{"read-1", "read-2"} }, nil))
	if r.Turn.State != "awaiting_review" || !reflect.DeepEqual(sequence, []string{"read-1", "read-2"}) || loopReadCount(r.Turn, 0) != 2 {
		t.Fatal("batch order or cache attempts", r.Turn.State, sequence)
	}
	for i, v := range r.Turn.Tools {
		a := v.Attempt
		if a == nil || a.AttemptID == "" || a.ModelStep != 1 || a.RequestID != r.Request.ID || a.FrameSHA256 != r.Turn.Models[0].FrameSHA256 || a.Result.ResultSHA256 != readResultHash(*a.Result) {
			t.Fatal("durable result identity")
		}
		if i == 1 && a.Result.Error != "source_unavailable" {
			t.Fatal("stable disclosed error")
		}
	}
	original := r
	r = loopNext(t, s, r)
	env, e := frozenLoopContext(r.Request)
	if e != nil || len(env.Research.PriorSteps) != 1 || len(env.Research.PriorSteps[0].Reads) != 2 || env.Research.PriorSteps[0].RequestDigest != original.Request.Digest {
		t.Fatal("error transcript omitted", e)
	}
	// A used ID may not be requested again, and the last model must complete.
	f := researchLoopFrame{"wen-research-step/1", r.Turn.ID, r.Turn.RootSHA256, 2, "read", []string{"read-1"}, "Repeated cache is not new evidence.", []researchLoopCard{}}
	if _, e = loopFrame(r.Request, loopJSON(t, f)); e == nil {
		t.Fatal("repeat/last-attempt read")
	}
	r = loopAdvance(t, s, r, loopTransport(t, func(f *researchLoopFrame) {
		f.Decision = "complete"
		f.CallRefs = []string{}
		f.Cards = []researchLoopCard{{"reminder-draft", "Review", "Review results separately."}}
	}, nil))
	if r.Turn.State != "completed" || len(r.Request.Cards) != 1 || r.Request.Cards[0].Review != "unreviewed" {
		t.Fatal("terminal inert card", r.Turn.State, r.Request.Cards)
	}
	d, _, _ := readAgents(t.Context(), s.db)
	if len(d.Tasks) != 0 {
		t.Fatal("card created task")
	}
}

func TestWENResearchLoopBudget(t *testing.T) {
	t.Run("frame-bytes", func(t *testing.T) {
		s, _, _, c := readBatchFixture(t)
		r := loopPrepared(t, s, c, 1, 1)
		f := fixtureResearchLoop(func() researchLoopEnvelope { v, _ := frozenLoopContext(r.Request); return v }())
		b := loopJSON(t, f)
		b = append(b, bytes.Repeat([]byte(" "), 8192-len(b))...)
		if _, e := loopFrame(r.Request, b); e != nil {
			t.Fatal("8192", e)
		}
		if _, e := loopFrame(r.Request, append(b, ' ')); e == nil {
			t.Fatal("8193")
		}
	})
	t.Run("exact-read-budget", func(t *testing.T) {
		s, _, _, c := readBatchFixture(t)
		r := loopPrepared(t, s, c, 2, 2)
		injectRevisionAgents(t, s, func(d *agentDocument) { // Reprepare normally with the chosen tighter bound.
			d.ResearchTurns = nil
			d.AIRequests = nil
		})
		in := researchInput(t, r.Turn.Selection)
		in.Schema = "wen-research-ai-prepare/2"
		in.Selection.Revision = r.Revision
		in.Selection.Calls = r.Turn.Selection.Calls
		in.Bounds.MaxToolAttempts = 2
		in.Bounds.MaxModelAttempts = 2
		in.Limits.MaxInputTokens = 32000
		in.Limits.BudgetMicrousd = 33000
		in.Bounds.BudgetMicrousd = 66000
		r = preparedResearch(t, s, in)
		r = loopAdvance(t, s, r, loopTransport(t, func(f *researchLoopFrame) { f.CallRefs = []string{"read-1", "read-2"} }, nil))
		if r.Turn.State != "awaiting_review" || loopReadCount(r.Turn, 0) != 2 {
			t.Fatal("last allowed read did not reach review", r.Turn.State)
		}
	})
	for _, mode := range []string{"malformed-known-usage", "last-model-read", "unknown-usage", "context-hold", "expired-review", "clock-backward", "wrong-parent", "duplicate-next"} {
		t.Run(mode, func(t *testing.T) {
			s, _, _, c := readBatchFixture(t)
			r := loopPrepared(t, s, c, 1, 2)
			switch mode {
			case "malformed-known-usage":
				r = loopAdvance(t, s, r, loopTransport(t, nil, func(b []byte) []byte { return append(b, []byte(`{}`)...) }))
				if r.Turn.State != "failed" || r.Request.SpentMicrousd <= 0 || r.Request.HeldMicrousd != 0 || len(r.Turn.Tools) != 0 {
					t.Fatal("malformed usage", r.Turn.State)
				}
				return
			case "last-model-read":
				injectRevisionAgents(t, s, func(d *agentDocument) { d.ResearchTurns = nil; d.AIRequests = nil })
				in := researchInput(t, r.Turn.Selection)
				in.Schema = "wen-research-ai-prepare/2"
				in.Selection.Revision = r.Revision
				in.Bounds.MaxModelAttempts = 1
				r = preparedResearch(t, s, in)
				r = loopAdvance(t, s, r, loopTransport(t, func(f *researchLoopFrame) { f.Decision = "read"; f.CallRefs = []string{"read-1"} }, nil))
				if r.Turn.State != "failed" || len(r.Turn.Tools) != 0 || r.Request.SpentMicrousd <= 0 {
					t.Fatal("last model read admitted")
				}
				return
			case "unknown-usage":
				loopStart(t, s, r)
				if e := s.advanceAgentAI(t.Context(), aiTestTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("uncertain fixture") }), true); e != nil {
					t.Fatal(e)
				}
				r = loopInspect(t, s, r)
				if r.Turn.State != "held" || r.Request.HeldMicrousd != aiRequestReservation(r.Request) || r.Request.SpentMicrousd != 0 {
					t.Fatal("uncertain hold lost")
				}
				before := retainedRevisionState(t, s)
				if _, e := s.PrepareNextWENResearchAI(t.Context(), loopNextInput(r)); e == nil || before != retainedRevisionState(t, s) {
					t.Fatal("unknown auto continue")
				}
				r = loopCancel(t, s, r)
				if r.Request.HeldMicrousd == 0 {
					t.Fatal("cancel erased hold")
				}
				return
			case "context-hold":
				r = loopAdvance(t, s, r, loopTransport(t, func(f *researchLoopFrame) { f.Answer = strings.Repeat("a", 2000) }, nil))
			default:
				r = loopAdvance(t, s, r, fixtureAITransport{})
			}
			if r.Turn.State != "awaiting_review" {
				t.Fatal(r.Turn.State)
			}
			next := loopNextInput(r)
			before := retainedRevisionState(t, s)
			switch mode {
			case "context-hold": // Add legitimate original-message size in a fresh turn, not hidden text.
				// Two full read frames in the three-step fixture exceed the fixed context ceiling.
				s2, _, _, c2 := readBatchFixture(t)
				q := loopPrepared(t, s2, c2, 3, 3)
				q = loopAdvance(t, s2, q, loopTransport(t, func(f *researchLoopFrame) {
					f.Answer = strings.Repeat("a", 2000)
					f.CallRefs = []string{"read-1", "read-2", "read-3"}
				}, nil))
				old := q.Turn
				_, e := s2.PrepareNextWENResearchAI(t.Context(), loopNextInput(q))
				if e == nil || !strings.Contains(e.Error(), "context_too_large") {
					t.Fatal("context ceiling", e)
				}
				q = loopInspect(t, s2, q)
				if q.Turn.State != "held" || !reflect.DeepEqual(q.Turn.Models, old.Models) || !reflect.DeepEqual(q.Turn.Tools, old.Tools) || q.Request.ID != old.CurrentChildID {
					t.Fatal("oversized continuation lost transcript")
				}
				return
			case "expired-review":
				s.now = func() time.Time { return time.Unix(r.Turn.DeadlineAt, 0) }
			case "clock-backward":
				s.now = func() time.Time { return time.Unix(r.Turn.LastObservedAt-1, 0) }
			case "wrong-parent":
				next.FromFrameSHA256 = strings.Repeat("0", 64)
			case "duplicate-next":
				r = loopNext(t, s, r)
				next.Revision = r.Revision
				before = retainedRevisionState(t, s)
			}
			if _, e := s.PrepareNextWENResearchAI(t.Context(), next); e == nil || before != retainedRevisionState(t, s) {
				t.Fatal("next admission bypass", mode, e)
			}
		})
	}
}

func TestWENResearchLoopRecovery(t *testing.T) {
	for _, mode := range []string{"read-cancel", "read-source", "read-deadline", "read-pause", "cached-altered", "local-error", "awaiting-cancel", "queued-restart", "prepared-restart", "awaiting-restart", "read-interrupted", "read-queued-restart"} {
		t.Run(mode, func(t *testing.T) {
			s, _, _, c := readBatchFixture(t)
			r := loopPrepared(t, s, c, 2, 2)
			if mode == "queued-restart" {
				loopStart(t, s, r)
			} else if mode != "prepared-restart" {
				s.researchRead = func(ctx context.Context, f researchFactResult) (json.RawMessage, error) {
					switch mode {
					case "read-cancel":
						loopCancel(t, s, r)
					case "read-source":
						injectRevisionAgents(t, s, func(d *agentDocument) { d.Messages[0].Text = "Changed." })
					case "read-deadline":
						s.now = func() time.Time { return time.Unix(r.Turn.CreatedAt+90, 0) }
					case "read-pause":
						if _, e := s.db.Exec("UPDATE update_pause SET phase='paused' WHERE id=1"); e != nil {
							t.Fatal(e)
						}
						return nil, context.Canceled
					case "cached-altered":
						return json.RawMessage(`{"altered":true}`), nil
					case "local-error":
						return nil, errors.New("private internal error")
					}
					return f.Data, nil
				}
				if mode == "read-pause" {
					loopStart(t, s, r)
					e := s.advanceAgentAI(t.Context(), loopTransport(t, func(f *researchLoopFrame) { f.CallRefs = []string{"read-1", "read-2"} }, nil), true)
					if e == nil {
						t.Fatal("paused finish wrote")
					}
				} else {
					r = loopAdvance(t, s, r, loopTransport(t, func(f *researchLoopFrame) { f.CallRefs = []string{"read-1", "read-2"} }, nil))
				}
			}
			if mode == "read-pause" { // A paused commit refuses. Recovery owns the interrupted attempt.
				s.db.Exec("UPDATE update_pause SET phase='idle' WHERE id=1")
			}
			if mode == "awaiting-cancel" {
				r = loopCancel(t, s, r)
				if r.Turn.State != "cancelled" || r.Request.State != "completed" || r.Request.SpentMicrousd <= 0 {
					t.Fatal("awaiting cancel rewrote child")
				}
				return
			}
			if mode == "read-interrupted" || mode == "read-queued-restart" {
				injectRevisionAgents(t, s, func(d *agentDocument) {
					a := d.ResearchTurns[0].Tools[0].Attempt
					d.ResearchTurns[0].Tools = nil
					d.ResearchTurns[0].State = "reads_queued"
					if mode == "read-interrupted" {
						a.Status, a.Result = "running", nil
						d.ResearchTurns[0].Tools = []researchToolRecord{{Attempt: a}}
						d.ResearchTurns[0].State = "reads_running"
					}
				})
			}
			n, e := OpenStore(s.dir)
			if e != nil {
				t.Fatal(e)
			}
			defer n.Close()
			n.now = s.now
			calls := 0
			rt := aiTestTransport(func(req *http.Request) (*http.Response, error) { calls++; return (fixtureAITransport{}).RoundTrip(req) })
			if e = n.advanceAgentAI(t.Context(), rt, true); e != nil {
				t.Fatal(e)
			}
			d, _, e := readAgents(t.Context(), n.db)
			if e != nil {
				t.Fatal(e)
			}
			q, turn := d.AIRequests[0], d.ResearchTurns[0]
			if calls != 0 {
				t.Fatal("restart replayed model")
			}
			switch mode {
			case "queued-restart":
				if q.State != "cancelled" || q.HeldMicrousd != 0 || turn.Models[0].Attempted {
					t.Fatal("queued consent crossed restart")
				}
			case "prepared-restart":
				if turn.State != "prepared" || q.HeldMicrousd != 0 {
					t.Fatal("prepared restarted")
				}
			case "awaiting-restart", "local-error":
				if turn.State != "awaiting_review" {
					t.Fatal("inert review changed", turn.State)
				}
			case "read-cancel":
				if turn.State != "cancelled" || len(turn.Tools) != 2 || turn.Tools[0].Attempt.Result.Error != "cancelled" || turn.Tools[1].NotRun == nil || loopReadCount(turn, 0) != 1 {
					t.Fatal("cancel result/not-run", turn.State)
				}
			case "read-interrupted":
				a := turn.Tools[0].Attempt
				if turn.State != "held" || a.Result.Error != "interrupted" || a.Result.EndedAt != nil || a.Result.RecoveredAt == 0 || turn.Tools[1].NotRun == nil || loopReadCount(turn, 0) != 1 {
					t.Fatal("interrupted timestamps or count")
				}
			case "read-queued-restart":
				if turn.State != "held" || loopReadCount(turn, 0) != 0 || len(turn.Tools) != 2 || turn.Tools[0].NotRun == nil {
					t.Fatal("unstarted batch invented attempts")
				}
			default:
				if turn.State != "held" {
					t.Fatal("invalid source/read not held", mode, turn.State)
				}
			}
			before := retainedRevisionState(t, n)
			if e = n.advanceAgentAI(t.Context(), rt, true); e != nil || before != retainedRevisionState(t, n) || calls != 0 {
				t.Fatal("recovery repeated", e)
			}
		})
	}
}

func TestWENResearchLoopByteBoundaries(t *testing.T) {
	t.Run("complete-context-8000-8001", func(t *testing.T) {
		s, _, _, c := readBatchFixture(t)
		in := researchInput(t, c)
		in.Schema = "wen-research-ai-prepare/2"
		in.Prompt = "p"
		in.MemorySelection = AIMemorySelection{Mode: "all"}
		in.Bounds.MaxModelAttempts = 1
		injectRevisionAgents(t, s, func(d *agentDocument) {
			d.Messages[0].Text = strings.Repeat("m", 1500)
			d.Memories = append(d.Memories, AgentMemory{ID: "loop-byte-note", ProfileID: c.Profile, Text: "λ<\"&", Source: "owner"})
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
			root.Schema = "wen-research-root/2"
			root.FactsSHA256 = researchHash(facts)
			root.CallsSHA256 = researchHash(in.Selection.Calls)
			v := WENResearchTurn{ID: strings.Repeat("t", 43), Root: root, RootSHA256: researchHash(root), Facts: facts, Bounds: in.Bounds, Selection: in.Selection}
			return len(loopContext(v, 1, researchLoopRemaining{s.now().Unix(), 0, 1, in.Bounds.MaxToolAttempts, in.Bounds.BudgetMicrousd}))
		}
		extra := max(0, 6200-measure())
		injectRevisionAgents(t, s, func(d *agentDocument) { d.Memories[len(d.Memories)-1].Text += strings.Repeat("n", extra) })
		padding := 8000 - measure() + 1
		if padding < 1 || padding > 2000 {
			t.Fatal("boundary fixture", padding)
		}
		in.Prompt = strings.Repeat("p", padding)
		r := preparedResearch(t, s, in)
		if len(r.Request.Context) != 8000 || !strings.Contains(r.Request.Context, `\u003c`) {
			t.Fatal("encoded context boundary", len(r.Request.Context))
		}
		in.Prompt += "p"
		in.Selection.Revision = r.Revision
		before := retainedRevisionState(t, s)
		if _, e := s.PrepareWENResearchAI(t.Context(), in); e == nil || before != retainedRevisionState(t, s) {
			t.Fatal("8001 accepted or wrote", e)
		}
	})
	t.Run("result-4000-4001", func(t *testing.T) {
		s, _, _, c := readBatchFixture(t)
		r := loopPrepared(t, s, c, 1, 2)
		fact := r.Turn.Facts.Results[0]
		now := s.now().Unix()
		a := researchReadAttempt{AttemptID: "attempt", CallRef: "read-1", ModelStep: 1, StartedAt: now}
		v := researchReadResult{Schema: "wen-research-call-result/1", AttemptID: a.AttemptID, CallRef: a.CallRef, ModelStep: 1, Status: "ok", Sources: fact.Sources, StartedAt: now, EndedAt: &now, Data: json.RawMessage(`""`)}
		v.ResultSHA256 = readResultHash(v)
		padding := 4000 - len(loopJSON(t, v))
		v.Data = loopJSON(t, strings.Repeat("x", padding))
		v = boundedLoopResult(r.Turn, a, v, now)
		if v.Status != "ok" || len(loopJSON(t, v)) != 4000 {
			t.Fatal("4000 result refused")
		}
		v.Data = loopJSON(t, strings.Repeat("x", padding+1))
		v = boundedLoopResult(r.Turn, a, v, now)
		if v.Status != "error" || v.Error != "too_large" || len(v.Data) != 0 || len(loopJSON(t, v)) > 4000 {
			t.Fatal("4001 clipped/promoted")
		}
	})
}

func TestWENResearchLoopCompatibility(t *testing.T) {
	for _, mode := range []string{"v1-raw-preservation", "paused-12", "unsupported", "v2-record-discriminator", "transcript-tamper"} {
		t.Run(mode, func(t *testing.T) {
			s, _, _, c := readBatchFixture(t)
			if mode == "v1-raw-preservation" || mode == "paused-12" {
				preparedResearch(t, s, researchInput(t, c))
				injectRevisionAgents(t, s, func(d *agentDocument) {
					d.AIRequests[0].State = "unknown"
					d.AIRequests[0].HeldMicrousd = 100
					d.ResearchTurns[0].State = "held"
				})
				s.db.Exec("PRAGMA user_version=12")
				if mode == "paused-12" {
					s.db.Exec("UPDATE update_pause SET phase='paused' WHERE id=1")
				}
				before := retainedRevisionState(t, s)
				n, e := OpenStore(s.dir)
				if mode == "paused-12" {
					if e == nil {
						n.Close()
						t.Fatal("paused migration accepted")
					}
					if before != retainedRevisionState(t, s) {
						t.Fatal("paused mutation")
					}
					return
				}
				if e != nil {
					t.Fatal(e)
				}
				defer n.Close()
				var schema int
				n.db.QueryRow("PRAGMA user_version").Scan(&schema)
				if schema != StateSchema || before != retainedRevisionState(t, n) {
					t.Fatal("12->13 rewrote v1 document/revision/hold")
				}
				return
			}
			r := loopPrepared(t, s, c, 2, 2)
			if mode == "unsupported" {
				if _, e := s.db.Exec(fmt.Sprintf("PRAGMA user_version=%d", StateSchema+1)); e != nil {
					t.Fatal(e)
				}
			} else if mode == "v2-record-discriminator" {
				injectRevisionAgents(t, s, func(d *agentDocument) { d.ResearchTurns[0].Schema = "wen-research-turn/3" })
			} else {
				r = loopAdvance(t, s, r, fixtureAITransport{})
				injectRevisionAgents(t, s, func(d *agentDocument) {
					d.ResearchTurns[0].Tools[0].Attempt.Result.Data = json.RawMessage(`{"forged":true}`)
				})
			}
			before := retainedRevisionState(t, s)
			if n, e := OpenStore(s.dir); e == nil {
				n.Close()
				t.Fatal("unsupported/tampered state opened")
			}
			if before != retainedRevisionState(t, s) {
				t.Fatal("reader erased retained record")
			}
		})
	}
	t.Run("strict-tool-union", func(t *testing.T) {
		for _, raw := range []string{`{"schema":"unknown"}`, `{"schema":"wen-research-call-not-run/1","callRef":"c","modelStep":1,"status":"not_run","reason":"limit","attemptId":"invented"}`, `{"schema":"wen-research-read-attempt/1","attemptId":"a","modelStep":1,"requestId":"q","frameSHA256":"f","callRef":"c","call":{},"startedAt":1,"status":"running","result":null,"extra":true}`} {
			var v researchToolRecord
			if update.StrictJSON([]byte(raw), &v) == nil {
				t.Fatal("unsupported tagged union accepted")
			}
		}
	})
}

func TestWENResearchLoopLaterQueuedRestart(t *testing.T) {
	s, _, _, c := readBatchFixture(t)
	r := loopPrepared(t, s, c, 2, 3)
	r = loopAdvance(t, s, r, fixtureAITransport{})
	r = loopNext(t, s, r)
	queued := loopStart(t, s, r)
	n, e := OpenStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer n.Close()
	n.now = s.now
	no := new(researchNoTransport)
	if e = n.advanceAgentAI(t.Context(), no, true); e != nil {
		t.Fatal(e)
	}
	v := loopInspect(t, n, r)
	if no.calls.Load() != 0 || v.Turn.State != "cancelled" || v.Request.State != "cancelled" || v.Request.HeldMicrousd != 0 || v.Turn.Models[1].Attempted || !v.Turn.Models[0].Attempted || v.Turn.DeadlineAt != queued.Turn.DeadlineAt || len(v.Turn.Tools) != 1 {
		t.Fatal("later consent crossed restart or erased original result")
	}
}
func TestWENResearchLoopStorageLimits(t *testing.T) {
	s, _, _, c := readBatchFixture(t)
	r := loopPrepared(t, s, c, 1, 1)
	in := researchInput(t, r.Turn.Selection)
	in.Schema = "wen-research-ai-prepare/2"
	in.Bounds.MaxModelAttempts = 1
	refused := false
	for range 10 {
		_, rev, e := readAgents(t.Context(), s.db)
		if e != nil {
			t.Fatal(e)
		}
		in.Selection.Revision = rev
		before := retainedRevisionState(t, s)
		_, e = s.PrepareWENResearchAI(t.Context(), in)
		if e != nil {
			if !strings.Contains(e.Error(), "capacity") || before != retainedRevisionState(t, s) {
				t.Fatal("workspace capacity wrote partial work", e)
			}
			refused = true
			break
		}
	}
	if !refused {
		t.Fatal("512 KiB reservations did not limit preparation")
	}
	d, _, e := readAgents(t.Context(), s.db)
	if e != nil {
		t.Fatal(e)
	}
	d.ResearchTurns[0].StopReason = strings.Repeat("x", 65536)
	if validateResearchStorage(loopJSON(t, d), d) == nil {
		t.Fatal("64 KiB turn limit bypassed")
	}
}
