package core

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// Exercise the v2 root/context branch through the same access/count/create
// guards as v1, with only the compiled provider fixture as transport.
func TestWENResearchLoopProviderSources(t *testing.T) {
	for _, phase := range []string{"before-dispatch", "before-count", "before-create", "in-flight", "completion"} {
		t.Run(phase, func(t *testing.T) {
			s, _, _, c := readBatchFixture(t)
			fixtureNow := time.Unix(openAIModelPolicy(admittedOpenAIModel).ReviewedAt+3600, 0)
			s.now = func() time.Time { return fixtureNow }
			in := researchInput(t, c)
			in.Schema = "wen-research-ai-prepare/2"
			in.Model = admittedOpenAIModel
			in.Limits.MaxInputTokens = 32000
			in.Limits.BudgetMicrousd = 100000
			in.Bounds.BudgetMicrousd = 300000
			connection := saveTestConnection(t, s)
			in.ConnectionRef = connection.Ref
			if _, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: connection.Ref, Passphrase: testVaultPassword}); e != nil {
				t.Fatal(e)
			}
			r := preparedResearch(t, s, in)
			loopStart(t, s, r)
			change := func() {
				injectRevisionAgents(t, s, func(d *agentDocument) { d.Messages[0].Text = "Changed selected source." })
			}
			if phase == "before-dispatch" {
				change()
			}
			calls := 0
			rt := aiTestTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				response, e := (fixtureAITransport{}).RoundTrip(req)
				if calls == 1 && phase == "before-count" || calls == 2 && phase == "before-create" {
					change()
				}
				if req.URL.Path == "/v1/responses" && (phase == "completion" || phase == "in-flight") {
					change()
					if phase == "in-flight" {
						response.Body.Close()
						<-req.Context().Done()
						return nil, context.Canceled
					}
				}
				return response, e
			})
			if e := s.advanceAgentAI(t.Context(), rt, true); e != nil {
				t.Fatal(e)
			}
			v := loopInspect(t, s, r)
			wantCalls := 3
			switch phase {
			case "before-dispatch":
				wantCalls = 0
			case "before-count":
				wantCalls = 1
			case "before-create":
				wantCalls = 2
			}
			if calls != wantCalls || v.Request.Answer != "" || len(v.Request.Cards) != 0 || len(v.Turn.Tools) != 0 || v.Turn.Models[0].Frame != nil {
				t.Fatal("v2 source guard promoted output", phase, calls, v.Request.State)
			}
			if phase == "in-flight" || phase == "before-create" {
				if v.Request.HeldMicrousd != aiRequestReservation(v.Request) || v.Turn.State != "held" {
					t.Fatal("uncertain cost erased")
				}
			} else if v.Request.HeldMicrousd != 0 {
				t.Fatal("known/pre-generation usage not reconciled")
			}
			if phase == "completion" && v.Request.SpentMicrousd <= 0 {
				t.Fatal("verified usage lost")
			}
			before := retainedRevisionState(t, s)
			if e := s.advanceAgentAI(t.Context(), rt, true); e != nil || calls != wantCalls || before != retainedRevisionState(t, s) {
				t.Fatal("source refusal replayed", e)
			}
		})
	}
}
