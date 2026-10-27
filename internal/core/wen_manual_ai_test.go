package core

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Exercise the existing disclosure path with actual retained WEN projections,
// rather than inventing another context selector or a provider integration.
func wenManualAISource(t *testing.T, kind string) (*Store, AgentMessage) {
	t.Helper()
	var s *Store
	var review WENResearchChatReview
	if kind == "research" {
		store, h, token := researchChatFixture(t)
		s = store
		review = readResearchChatReview(t, h, token)
		if w := postResearchChat(h, token, researchChatCommand(review)); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	} else if kind == "revised-strategy" {
		store, h, token, input := revisionFixture(t)
		s = store
		r := revisionReview(t, h, token, input)
		w := revisionRequest(h, token, r, true)
		var saved WENStrategyRevisionResult
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &saved) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		review = readSavedReview(t, h, token, WENSavedChatSelection{Profile: input.Profile, Revision: saved.Library.Revision, StrategyDigest: r.Candidate.Digest})
		if w = postResearchChat(h, token, researchChatCommand(review)); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	} else {
		store, h, token, library := savedChatFixture(t)
		s = store
		selection := savedSelection(library)
		if kind == "strategy" {
			selection.BundleIndex = nil
		}
		if kind == "history" {
			selection.StrategyDigest = ""
		}
		review = readSavedReview(t, h, token, selection)
		if w := postResearchChat(h, token, researchChatCommand(review)); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	v, err := s.Agents(t.Context(), "wen-operator", "")
	if err != nil || len(v.Messages) == 0 {
		t.Fatal(err)
	}
	message := v.Messages[len(v.Messages)-1]
	if message.Role != "user" || message.Text != review.Text {
		t.Fatal("source capture did not retain exact reviewed bytes")
	}
	return s, message
}

func wenManualAICommand(source AgentMessage) AgentCommand {
	return AgentCommand{Action: "ai-prepare", ProfileID: source.ProfileID, ID: source.SessionID, Text: "Review this frozen WEN source and its limits; do not execute.", MemorySelection: &AIMemorySelection{Mode: "none"}, Limits: &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 3, BudgetMicrousd: 30000}}
}

func TestWENManualAIExactSourceDisclosureAndFixture(t *testing.T) {
	for _, kind := range []string{"research", "strategy", "history", "strategy-history", "revised-strategy"} {
		t.Run(kind, func(t *testing.T) {
			s, source := wenManualAISource(t, kind)
			other := agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: source.ProfileID, Name: "Not selected"})
			agentApply(t, s, AgentCommand{Action: "message-add", ProfileID: source.ProfileID, ID: other.Sessions[len(other.Sessions)-1].ID, Text: "other-conversation-private-marker"})
			agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: source.ProfileID, Text: "unselected-note-private-marker", Source: "owner"})
			agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: "news-researcher", Text: "foreign-profile-private-marker", Source: "owner"})
			libraryBefore, err := s.WENResearch(t.Context(), source.ProfileID)
			if err != nil {
				t.Fatal(err)
			}
			v := agentApply(t, s, wenManualAICommand(source))
			q := v.AIRequests[len(v.AIRequests)-1]
			var envelope struct {
				Context struct {
					Messages []struct{ Role, Text string }
					Memories []any
				}
			}
			if json.Unmarshal([]byte(q.Context), &envelope) != nil || len(envelope.Context.Messages) != 1 || envelope.Context.Messages[0].Role != "user" || envelope.Context.Messages[0].Text != source.Text || len(envelope.Context.Memories) != 0 {
				t.Fatal("selected source changed, broadened or truncated", q.Context)
			}
			for _, excluded := range []string{"other-conversation-private-marker", "unselected-note-private-marker", "foreign-profile-private-marker", "wen-strategy-revision-receipt/1"} {
				if strings.Contains(q.Context, excluded) {
					t.Fatal("context leaked", excluded)
				}
			}
			if kind == "strategy-history" && (!strings.Contains(source.Text, `"netSOL":"-100001015"`) || !strings.Contains(source.Text, `"skipped":null`) || !strings.Contains(source.Text, "participation unverified")) {
				t.Fatal("losses, unknowns or evidence limits disappeared")
			}
			wire, err := aiWire(q)
			if err != nil || q.Disclosure != string(wire) || q.Digest != aiDigest(q) || q.State != "prepared" || q.HeldMicrousd != 0 || v.ProviderCallsEnabled || v.FinancialExecution {
				t.Fatal("preparation executed or changed disclosure", err)
			}
			selectionRefused(t, s, AgentCommand{Action: "ai-start", ProfileID: source.ProfileID, ID: q.ID, ConsentDigest: q.Digest}, false)
			selectionRefused(t, s, AgentCommand{Action: "ai-start", ProfileID: source.ProfileID, ID: q.ID, ConsentDigest: "wrong"}, true)
			v = aiStart(t, s, v)
			calls := 0
			rt := aiTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				body, err := io.ReadAll(r.Body)
				var disclosed struct {
					Input, Instructions, ToolChoice string
					Tools                           []any
					Store, Background               bool
				}
				if err != nil || string(body) != q.Disclosure || json.Unmarshal(body, &disclosed) != nil || disclosed.Input != q.Context || disclosed.Instructions != aiInstructions || len(disclosed.Tools) != 0 || disclosed.Store || disclosed.Background || r.Header.Get("Authorization") != "" {
					t.Fatal("wire differs from consented disclosure", err)
				}
				r.Body = io.NopCloser(strings.NewReader(string(body)))
				return (fixtureAITransport{}).RoundTrip(r)
			})
			for range 2 {
				if err := s.advanceAgentAI(t.Context(), rt, true); err != nil {
					t.Fatal(err)
				}
			}
			v, err = s.Agents(t.Context(), source.ProfileID, "")
			if err != nil || calls != 1 || v.AIRequests[len(v.AIRequests)-1].State != "completed" || len(v.Tasks) != 0 || len(v.Bots) != 0 || v.FinancialExecution {
				t.Fatal("fixture failed or created authority", err, calls)
			}
			libraryAfter, _ := s.WENResearch(t.Context(), source.ProfileID)
			if !reflect.DeepEqual(libraryBefore, libraryAfter) {
				t.Fatal("manual review rewrote strategy/history")
			}
		})
	}
}

func TestWENManualAISourceRevalidation(t *testing.T) {
	for _, stage := range []string{"consent", "dispatch"} {
		for _, change := range []string{"source-text", "source-removed", "source-profile", "conversation-addition", "consent-expired", "clock-rollback"} {
			t.Run(stage+"/"+change, func(t *testing.T) {
				s, source := wenManualAISource(t, "strategy-history")
				v := agentApply(t, s, wenManualAICommand(source))
				q := v.AIRequests[0]
				if stage == "dispatch" {
					v = aiStart(t, s, v)
				}
				switch change {
				case "source-text":
					injectRevisionAgents(t, s, func(d *agentDocument) { d.Messages[0].Text += " changed" })
				case "source-removed":
					injectRevisionAgents(t, s, func(d *agentDocument) { d.Messages = nil })
				case "source-profile":
					injectRevisionAgents(t, s, func(d *agentDocument) { d.Messages[0].ProfileID = "news-researcher" })
				case "conversation-addition":
					agentApply(t, s, AgentCommand{Action: "message-add", ProfileID: source.ProfileID, ID: source.SessionID, Text: "New evidence requires new consent"})
				case "consent-expired":
					s.now = func() time.Time { return time.Unix(q.CreatedAt+601, 0) }
				case "clock-rollback":
					s.now = func() time.Time { return time.Unix(q.CreatedAt-1, 0) }
				}
				if stage == "consent" {
					selectionRefused(t, s, AgentCommand{Action: "ai-start", ProfileID: source.ProfileID, ID: q.ID, ConsentDigest: q.Digest}, true)
					return
				}
				calls := 0
				rt := aiTestTransport(func(r *http.Request) (*http.Response, error) { calls++; return (fixtureAITransport{}).RoundTrip(r) })
				if err := s.advanceAgentAI(t.Context(), rt, true); err != nil {
					t.Fatal(err)
				}
				v, _ = s.Agents(t.Context(), source.ProfileID, "")
				if calls != 0 || v.AIRequests[0].State != "cancelled" || v.AIRequests[0].HeldMicrousd != 0 {
					t.Fatal("stale source dispatched or retained a hold", change, calls)
				}
			})
		}
	}
}

func TestWENManualAIBoundsAndFrozenRecovery(t *testing.T) {
	for _, kind := range []string{"context-capacity", "input-budget", "request-capacity", "document-capacity", "paused", "stale-revision", "restart-frozen-source"} {
		t.Run(kind, func(t *testing.T) {
			s, source := wenManualAISource(t, "strategy-history")
			command := wenManualAICommand(source)
			switch kind {
			case "context-capacity":
				for range 2 {
					agentApply(t, s, AgentCommand{Action: "message-add", ProfileID: source.ProfileID, ID: source.SessionID, Text: strings.Repeat("x", 3500)})
				}
				selectionRefused(t, s, command, false)
			case "input-budget":
				command.Limits.MaxInputTokens = 1000
				selectionRefused(t, s, command, false)
			case "request-capacity":
				for range 10 {
					v := agentApply(t, s, command)
					q := v.AIRequests[len(v.AIRequests)-1]
					agentApply(t, s, AgentCommand{Action: "ai-cancel", ProfileID: source.ProfileID, ID: q.ID})
				}
				selectionRefused(t, s, command, false)
			case "document-capacity":
				refused := false
				for range 10 {
					v, _ := s.Agents(t.Context(), source.ProfileID, "")
					command.Revision = v.Revision
					before := retainedRevisionState(t, s)
					if _, err := s.ChangeAgents(t.Context(), command); err != nil {
						if !strings.Contains(err.Error(), "capacity") || retainedRevisionState(t, s) != before || len(v.AIRequests) == 0 {
							t.Fatal("capacity did not preserve disclosure/results reservations", err)
						}
						refused = true
						break
					}
				}
				if !refused {
					t.Fatal("pending result reservations did not enforce document capacity")
				}
			case "paused":
				if _, err := s.db.Exec("UPDATE update_pause SET phase='paused' WHERE id=1"); err != nil {
					t.Fatal(err)
				}
				selectionRefused(t, s, command, false)
			case "stale-revision":
				v, _ := s.Agents(t.Context(), source.ProfileID, "")
				command.Revision = v.Revision
				agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: source.ProfileID, Text: "Not selected", Source: "owner"})
				before := retainedRevisionState(t, s)
				if _, err := s.ChangeAgents(t.Context(), command); err == nil || retainedRevisionState(t, s) != before {
					t.Fatal("stale revision accepted or mutated state")
				}
			case "restart-frozen-source":
				v := agentApply(t, s, command)
				q := v.AIRequests[0]
				library, _ := s.WENResearch(t.Context(), source.ProfileID)
				_, err := s.ChangeWENResearch(t.Context(), WENResearchCommand{Profile: source.ProfileID, Revision: library.Revision, Action: "save", Raw: strings.Replace(researchRaw(t), "Fixture session draft", "Later version not disclosed", 1)})
				if err != nil {
					t.Fatal(err)
				}
				if err = s.Close(); err != nil {
					t.Fatal(err)
				}
				n, err := OpenStore(s.dir)
				if err != nil {
					t.Fatal(err)
				}
				defer n.Close()
				n.now = func() time.Time { return time.Unix(q.CreatedAt+60, 0) }
				d, _, _ := readAgents(t.Context(), n.db)
				context, err := aiRequestContext(d, q, n.now().Unix())
				if err != nil || context != q.Context || strings.Contains(context, "Later version not disclosed") {
					t.Fatal("restart refreshed immutable historical source", err)
				}
				v, _ = n.Agents(t.Context(), source.ProfileID, "")
				if v.AIRequests[0].Disclosure != q.Disclosure || v.AIRequests[0].State != "prepared" {
					t.Fatal("prepared review changed or auto-started on restart")
				}
				v = aiStart(t, n, v)
				if err = n.advanceAgentAI(t.Context(), fixtureAITransport{}, true); err != nil {
					t.Fatal(err)
				}
				v, _ = n.Agents(t.Context(), source.ProfileID, "")
				if v.AIRequests[0].State != "completed" || v.Messages[0].Text != source.Text {
					t.Fatal("explicit post-restart fixture lost source")
				}
			}
		})
	}
}
