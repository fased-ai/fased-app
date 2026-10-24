package core

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestWENResearchLoopGrammar(t *testing.T) {
	s, _, _, c := readBatchFixture(t)
	in := researchInput(t, c)
	in.Schema = "wen-research-ai-prepare/2"
	r, err := s.PrepareWENResearchAI(t.Context(), in)
	if err != nil || r.Schema != "wen-research-ai-review/2" || r.Turn.Schema != "wen-research-turn/2" || r.Request.State != "prepared" {
		t.Fatal("inert v2 preparation required", err)
	}
}

func loopPrepared(t *testing.T, s *Store, c WENResearchReadInput, calls, models int) WENResearchAIReview {
	t.Helper()
	injectRevisionAgents(t, s, func(d *agentDocument) { d.Messages[0].Text = "Frozen." })
	d, rev, err := readAgents(t.Context(), s.db)
	if err != nil {
		t.Fatal(err)
	}
	c.Revision = rev
	c.Sources[0].TextSHA256 = wenHash([]byte(d.Messages[0].Text))
	in := researchInput(t, c)
	in.Schema = "wen-research-ai-prepare/2"
	in.Prompt = "Review."
	in.Limits.MaxInputTokens = 32000
	in.Limits.BudgetMicrousd = 33000
	in.Bounds.MaxModelAttempts = models
	in.Bounds.BudgetMicrousd = int64(models) * 33000
	in.Selection.Calls = nil
	for i := range calls {
		a := c.Calls[0]
		a.ID = fmt.Sprintf("read-%d", i+1)
		in.Selection.Calls = append(in.Selection.Calls, a)
	}
	r, e := s.PrepareWENResearchAI(t.Context(), in)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func loopInspect(t *testing.T, s *Store, r WENResearchAIReview) WENResearchAIReview {
	t.Helper()
	v, e := s.WENResearchAI(t.Context(), WENResearchAISelection{"wen-research-ai-selection/2", r.Turn.Profile, r.Turn.ID, 0}, false)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func loopStart(t *testing.T, s *Store, r WENResearchAIReview) WENResearchAIReview {
	t.Helper()
	c := fixtureResearchConsent(t, s, r)
	c.Schema = "wen-research-ai-start/2"
	c.Step = r.Request.ResearchStep
	v, e := s.StartWENResearchAI(t.Context(), c, true)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func loopNext(t *testing.T, s *Store, r WENResearchAIReview) WENResearchAIReview {
	t.Helper()
	r = loopInspect(t, s, r)
	m := r.Turn.Models[len(r.Turn.Models)-1]
	v, e := s.PrepareNextWENResearchAI(t.Context(), WENResearchAINextInput{"wen-research-ai-next/1", r.Turn.Profile, r.Turn.ID, r.Revision, r.Request.ID, m.FrameSHA256})
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestWENResearchLoopTranscript(t *testing.T) {
	for _, steps := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("%d-consents", steps), func(t *testing.T) {
			s, _, _, c := readBatchFixture(t)
			r := loopPrepared(t, s, c, max(1, steps-1), steps)
			for step := 1; step <= steps; step++ {
				before := retainedRevisionState(t, s)
				if e := s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); e != nil || before != retainedRevisionState(t, s) {
					t.Fatal("prepared child auto-started", e)
				}
				loopStart(t, s, r)
				if e := s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); e != nil {
					t.Fatal(e)
				}
				r = loopInspect(t, s, r)
				if r.Request.State != "completed" || r.Request.HeldMicrousd != 0 || r.Request.SpentMicrousd <= 0 || len(r.Turn.Models) != step || !r.Turn.Models[step-1].Attempted {
					t.Fatal("child usage/identity", r.Turn.State, r.Request.State)
				}
				if step == steps {
					if r.Turn.State != "completed" {
						t.Fatal("final frame", r.Turn.State)
					}
					break
				}
				if r.Turn.State != "awaiting_review" || len(r.Turn.Tools) != step || r.Turn.Tools[step-1].Attempt.Result.Status != "ok" {
					t.Fatal("read transcript", r.Turn.State, len(r.Turn.Tools))
				}
				originalDeadline := r.Turn.DeadlineAt
				r = loopNext(t, s, r)
				if r.Request.State != "prepared" || r.Request.HeldMicrousd != 0 || r.Turn.DeadlineAt != originalDeadline {
					t.Fatal("next preparation changed authority/clock")
				}
				var env researchLoopEnvelope
				if json.Unmarshal([]byte(r.Request.Context), &env) != nil || len(env.Research.PriorSteps) != step || env.Research.Remaining.ModelAttempts != steps-step {
					t.Fatal("complete prior transcript")
				}
			}
			d, _, e := readAgents(t.Context(), s.db)
			if e != nil || taskAIRequestSlots(d) != steps || len(d.AIRequests) != steps {
				t.Fatal("unused capacity", e)
			}
			before := retainedRevisionState(t, s)
			if e = s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); e != nil || before != retainedRevisionState(t, s) {
				t.Fatal("terminal replay", e)
			}
		})
	}
}
