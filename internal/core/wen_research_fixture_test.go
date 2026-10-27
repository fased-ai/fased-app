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

func TestWENResearchFixtureAdmission(t *testing.T) {
	s, h, token, selection := readBatchFixture(t)
	r := preparedResearch(t, s, researchInput(t, selection))
	h.agentFixture = true
	body, _ := json.Marshal(map[string]any{
		"schema": "wen-research-ai-start/1", "profile": selection.Profile,
		"id": r.Turn.ID, "revision": r.Revision,
		"requestId": r.Request.ID, "consentDigest": r.Request.Digest,
	})
	w := apiRequest(h, "POST", "/api/v1/wen/research/ai-start", string(body), token, "http://127.0.0.1:7331", csrfToken(token))
	if w.Code != 200 {
		t.Fatalf("exact-consented one-child fixture admission: HTTP %d: %s", w.Code, w.Body.String())
	}
	var queued WENResearchAIReview
	if json.Unmarshal(w.Body.Bytes(), &queued) != nil || queued.Request.State != "queued" || queued.Turn.StartedAt != 0 || queued.Turn.DeadlineAt != 0 || queued.Turn.Models[0].Attempted || queued.Request.HeldMicrousd != aiRequestReservation(r.Request) {
		t.Fatal("queue/clock/hold did not commit together", w.Body.String())
	}
	count := 0
	rt := aiTestTransport(func(req *http.Request) (*http.Response, error) {
		count++
		d, _, e := readAgents(req.Context(), s.db)
		if e != nil || !d.ResearchTurns[0].Models[0].Attempted || d.AIRequests[0].State != "running" || d.AIRequests[0].DispatchBoot != s.bootID || d.AIRequests[0].HeldMicrousd == 0 || d.ResearchTurns[0].StartedAt != s.now().Unix() || d.ResearchTurns[0].DeadlineAt != d.ResearchTurns[0].StartedAt+90 || taskAIRequestSlots(d) != 3 {
			t.Fatal("operation preceded durable attempt/hold", e)
		}
		return (fixtureAITransport{}).RoundTrip(req)
	})
	if e := s.advanceAgentAI(t.Context(), rt, true); e != nil {
		t.Fatal(e)
	}
	d, _, e := readAgents(t.Context(), s.db)
	q, turn := d.AIRequests[0], d.ResearchTurns[0]
	if e != nil || count != 1 || q.State != "completed" || turn.State != "completed" || q.HeldMicrousd != 0 || q.SpentMicrousd <= 0 || q.SpentMicrousd != aiUsageCost(q, q.InputTokens, q.CachedInputTokens, q.OutputTokens) || taskAIRequestSlots(d) != 1 || len(d.AIRequests) != 1 || len(turn.Tools) != 0 {
		t.Fatal("one child/known usage/unused capacity", count, q, turn.State, e)
	}
	before := retainedRevisionState(t, s)
	if _, e := s.StartWENResearchAI(t.Context(), fixtureResearchConsent(t, s, r), true); e == nil {
		t.Fatal("terminal request replayed")
	}
	if e := s.advanceAgentAI(t.Context(), rt, true); e != nil || count != 1 || before != retainedRevisionState(t, s) {
		t.Fatal("automatic continuation or write", e)
	}

	for _, cause := range []string{"disabled", "digest", "request", "turn", "profile", "revision", "changed-source", "expired", "clock-backward"} {
		t.Run(cause, func(t *testing.T) {
			s, _, _, selection := readBatchFixture(t)
			r := preparedResearch(t, s, researchInput(t, selection))
			c := fixtureResearchConsent(t, s, r)
			switch cause {
			case "digest":
				c.ConsentDigest = strings.Repeat("0", 64)
			case "request":
				c.RequestID = "other-request"
			case "turn":
				c.ID = "other-turn"
			case "profile":
				c.Profile = "news-researcher"
			case "revision":
				c.Revision--
			case "changed-source":
				injectRevisionAgents(t, s, func(d *agentDocument) { d.Messages[0].Text = "Corrected source" })
			case "expired":
				s.now = func() time.Time { return time.Unix(r.Turn.ReviewExpiresAt+1, 0) }
			case "clock-backward":
				s.now = func() time.Time { return time.Unix(r.Turn.CreatedAt-1, 0) }
			}
			before := retainedRevisionState(t, s)
			if _, e := s.StartWENResearchAI(t.Context(), c, cause != "disabled"); e == nil || before != retainedRevisionState(t, s) {
				t.Fatal("invalid consent changed state", e)
			}
		})
	}
}

func fixtureResearchConsent(t *testing.T, s *Store, r WENResearchAIReview) WENResearchAIStartInput {
	t.Helper()
	_, rev, err := readAgents(t.Context(), s.db)
	if err != nil {
		t.Fatal(err)
	}
	return WENResearchAIStartInput{Schema: "wen-research-ai-start/1", Profile: r.Turn.Profile, ID: r.Turn.ID, Revision: rev, RequestID: r.Request.ID, ConsentDigest: r.Request.Digest}
}
func startFixtureResearch(t *testing.T, s *Store, r WENResearchAIReview) WENResearchAIReview {
	t.Helper()
	out, err := s.StartWENResearchAI(t.Context(), fixtureResearchConsent(t, s, r), true)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestWENResearchFixtureSourceChanges(t *testing.T) {
	for _, phase := range []string{"before-dispatch", "before-count", "before-create", "in-flight", "completion", "library-completion", "deadline-completion", "clock-completion"} {
		t.Run(phase, func(t *testing.T) {
			s, _, _, selection := readBatchFixture(t)
			fixtureNow := time.Unix(openAIModelPolicy(admittedOpenAIModel).ReviewedAt+3600, 0)
			s.now = func() time.Time { return fixtureNow }
			in := researchInput(t, selection)
			in.Model = admittedOpenAIModel
			connection := saveTestConnection(t, s)
			in.ConnectionRef = connection.Ref
			if _, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: connection.Ref, Passphrase: testVaultPassword}); e != nil {
				t.Fatal(e)
			}
			if phase == "library-completion" {
				in.Selection.Sources, in.Selection.Calls = selection.Sources[1:2], selection.Calls[1:2]
			}
			var clock atomic.Int64
			clock.Store(s.now().Unix())
			s.now = func() time.Time { return time.Unix(clock.Load(), 0) }
			r := preparedResearch(t, s, in)
			startFixtureResearch(t, s, r)
			change := func() {
				injectRevisionAgents(t, s, func(d *agentDocument) { d.Messages[0].Text = "Changed selected source" })
			}
			if phase == "before-dispatch" {
				change()
			}
			calls := 0
			rt := aiTestTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				response, err := (fixtureAITransport{}).RoundTrip(req)
				if calls == 1 && phase == "before-count" || calls == 2 && phase == "before-create" {
					change()
				}
				if req.URL.Path == "/v1/responses" {
					switch phase {
					case "in-flight":
						change()
						<-req.Context().Done()
						return nil, req.Context().Err()
					case "completion":
						change()
					case "library-completion":
						if _, e := s.db.Exec("UPDATE wen_research SET document=replace(document,'Fixture session draft','Changed selected strategy'),revision=revision+1 WHERE id=1"); e != nil {
							t.Fatal(e)
						}
					case "deadline-completion":
						clock.Store(r.Turn.CreatedAt + 90)
					case "clock-completion":
						clock.Store(r.Turn.CreatedAt - 1)
					}
				}
				return response, err
			})
			if e := s.advanceAgentAI(t.Context(), rt, true); e != nil {
				t.Fatal(e)
			}
			d, _, err := readAgents(t.Context(), s.db)
			if err != nil {
				t.Fatal(err)
			}
			q := d.AIRequests[0]
			wantCalls, wantState := 3, "failed"
			switch phase {
			case "before-dispatch":
				wantCalls, wantState = 0, "cancelled"
			case "before-count":
				wantCalls = 1
			case "before-create":
				// The shared admission driver records the generation boundary
				// before transport; preserve its conservative uncertain hold.
				wantCalls, wantState = 2, "unknown"
			case "in-flight":
				wantState = "unknown"
			}
			if calls != wantCalls || q.State != wantState || q.Answer != "" || len(q.Cards) != 0 || taskAIRequestSlots(d) != 1 || d.ResearchTurns[0].Models[0].Attempted != (calls > 0) {
				t.Fatal("source/deadline promotion or retry", calls, q.State, q.HeldMicrousd, q.SpentMicrousd, d.ResearchTurns[0].State)
			}
			if phase == "in-flight" || phase == "before-create" {
				if q.HeldMicrousd != aiRequestReservation(q) || q.SpentMicrousd != 0 {
					t.Fatal("uncertain usage erased")
				}
			} else if q.HeldMicrousd != 0 {
				t.Fatal("known/pre-create usage not reconciled")
			}
			if strings.HasSuffix(phase, "completion") && q.SpentMicrousd <= 0 {
				t.Fatal("reported usage lost")
			}
		})
	}
}

func TestWENResearchFixtureUnknownUsage(t *testing.T) {
	for _, mode := range []string{"transport", "cancel-unknown", "cancel-known", "restart-running", "queued-cancel", "disabled-worker", "queued-expiry", "paused-completion"} {
		t.Run(mode, func(t *testing.T) {
			s, _, _, selection := readBatchFixture(t)
			r := preparedResearch(t, s, researchInput(t, selection))
			startFixtureResearch(t, s, r)
			if mode == "queued-cancel" {
				_, rev, _ := readAgents(t.Context(), s.db)
				if _, e := s.WENResearchAI(t.Context(), WENResearchAISelection{"wen-research-ai-selection/1", r.Turn.Profile, r.Turn.ID, rev}, true); e != nil {
					t.Fatal(e)
				}
			}
			if mode == "queued-expiry" {
				s.now = func() time.Time { return time.Unix(r.Turn.ReviewExpiresAt+1, 0) }
			}
			calls := 0
			rt := aiTestTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if strings.HasPrefix(mode, "cancel-") {
					_, rev, _ := readAgents(t.Context(), s.db)
					if _, e := s.WENResearchAI(t.Context(), WENResearchAISelection{"wen-research-ai-selection/1", r.Turn.Profile, r.Turn.ID, rev}, true); e != nil {
						t.Fatal(e)
					}
				}
				if mode == "paused-completion" {
					if _, e := s.db.Exec("UPDATE update_pause SET phase='paused' WHERE id=1"); e != nil {
						t.Fatal(e)
					}
				}
				if mode == "transport" || mode == "cancel-unknown" || mode == "restart-running" {
					return nil, errors.New("fixture response lost")
				}
				return (fixtureAITransport{}).RoundTrip(req)
			})
			err := s.advanceAgentAI(t.Context(), rt, mode != "disabled-worker")
			if mode == "paused-completion" {
				if !errors.Is(err, ErrCorePaused) {
					t.Fatal("pause fence lost", err)
				}
				if _, e := s.db.Exec("UPDATE update_pause SET phase='idle' WHERE id=1"); e != nil {
					t.Fatal(e)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if mode == "restart-running" || mode == "paused-completion" {
				// Restore the durable pre-completion state to model process death.
				injectRevisionAgents(t, s, func(d *agentDocument) { d.AIRequests[0].State = "running"; d.ResearchTurns[0].State = "running" })
			}
			n, e := OpenStore(s.dir)
			if e != nil {
				t.Fatal(e)
			}
			defer n.Close()
			n.now = s.now
			if e := n.advanceAgentAI(t.Context(), rt, true); e != nil {
				t.Fatal(e)
			}
			d, _, e := readAgents(t.Context(), n.db)
			if e != nil {
				t.Fatal(e)
			}
			q := d.AIRequests[0]
			wantCalls := 1
			if mode == "queued-cancel" || mode == "disabled-worker" || mode == "queued-expiry" {
				wantCalls = 0
			}
			if calls != wantCalls || len(d.AIRequests) != 1 || taskAIRequestSlots(d) != 1 {
				t.Fatal("restart replay/capacity", calls, q)
			}
			switch mode {
			case "queued-cancel", "disabled-worker", "queued-expiry":
				if q.State != "cancelled" || q.HeldMicrousd != 0 || q.SpentMicrousd != 0 || d.ResearchTurns[0].Models[0].Attempted {
					t.Fatal(q)
				}
			case "cancel-known":
				if q.State != "cancelled" || q.HeldMicrousd != 0 || q.SpentMicrousd <= 0 || q.Answer != "" {
					t.Fatal("cancelled known usage not reconciled", q)
				}
			default:
				if q.HeldMicrousd != aiRequestReservation(q) || q.SpentMicrousd != 0 || (q.State != "unknown" && q.State != "cancelled") {
					t.Fatal("unknown hold erased", q)
				}
				// A different prepared turn cannot bypass this profile's liability.
				in := researchInput(t, selection)
				_, rev, _ := readAgents(t.Context(), n.db)
				in.Selection.Revision, in.Bounds.MaxModelAttempts = rev, 1
				other := preparedResearch(t, n, in)
				before := retainedRevisionState(t, n)
				if _, e := n.StartWENResearchAI(t.Context(), fixtureResearchConsent(t, n, other), true); e == nil || before != retainedRevisionState(t, n) {
					t.Fatal("profile hold bypass", e)
				}
			}
		})
	}
}

// Keep context cancellation covered without any socket, provider or sleep.
func TestWENResearchFixtureCancelledAdmission(t *testing.T) {
	s, _, _, selection := readBatchFixture(t)
	r := preparedResearch(t, s, researchInput(t, selection))
	c := fixtureResearchConsent(t, s, r)
	before := retainedRevisionState(t, s)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, e := s.StartWENResearchAI(ctx, c, true); e == nil || before != retainedRevisionState(t, s) {
		t.Fatal("cancelled admission wrote")
	}
}

func TestWENResearchFixtureHTTPAuthority(t *testing.T) {
	s, h, token, selection := readBatchFixture(t)
	r := preparedResearch(t, s, researchInput(t, selection))
	h.agentFixture = true
	c := fixtureResearchConsent(t, s, r)
	b, _ := json.Marshal(c)
	for _, tc := range []struct {
		name, method, path, token, origin, csrf, body string
		status                                        int
	}{
		{"authentication", "POST", "ai-start", "", "http://127.0.0.1:7331", "", string(b), 401},
		{"csrf", "POST", "ai-start", token, "http://127.0.0.1:7331", "wrong", string(b), 403},
		{"origin", "POST", "ai-start", token, "https://example.org", csrfToken(token), string(b), 403},
		{"method", "GET", "ai-start", token, "http://127.0.0.1:7331", csrfToken(token), string(b), 405},
		{"query", "POST", "ai-start?run=true", token, "http://127.0.0.1:7331", csrfToken(token), string(b), 400},
		{"unknown", "POST", "ai-start", token, "http://127.0.0.1:7331", csrfToken(token), strings.TrimSuffix(string(b), "}") + `,"tools":true}`, 400},
		{"duplicate", "POST", "ai-start", token, "http://127.0.0.1:7331", csrfToken(token), strings.TrimSuffix(string(b), "}") + `,"schema":"wen-research-ai-start/1"}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := retainedRevisionState(t, s)
			w := apiRequest(h, tc.method, "/api/v1/wen/research/"+tc.path, tc.body, tc.token, tc.origin, tc.csrf)
			if w.Code != tc.status || before != retainedRevisionState(t, s) {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}
