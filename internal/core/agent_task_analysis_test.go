package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func analysisCommand(v AgentView) AgentCommand {
	return AgentCommand{Action: "task-review", ProfileID: v.ProfileID, Revision: v.Revision, Name: "News analysis", Text: "Summarize the selected evidence without proposing actions", Analysis: &TaskAnalysisPolicy{Version: "task-analysis-v1", Failure: "required"}, IntervalSeconds: 60, MaxRuns: 2, Limits: &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 1, BudgetMicrousd: 30000}, TotalBudgetMicrousd: 60000, AuthorizationSeconds: 3600}
}
func TestTaskAnalysisReviewCreatesNothingAndCompletesWithoutReminder(t *testing.T) {
	s := newsTestStore(t)
	v, err := s.Agents(t.Context(), "trading-analyst", "")
	if err != nil {
		t.Fatal(err)
	}
	c := analysisCommand(v)
	before := retainedRevisionState(t, s)
	reviewed, err := s.changeAgents(t.Context(), c, false)
	if err != nil {
		t.Fatal(err)
	}
	if reviewed.TaskReview == nil || reviewed.TaskReview.AI == nil || retainedRevisionState(t, s) != before || len(reviewed.Tasks) != 0 || len(reviewed.Sessions) != 0 {
		t.Fatal("review wrote a draft or lost frozen analysis")
	}
	c.Action, c.ConsentDigest, c.ExpiresAt = "task-reviewed-create", reviewed.TaskReview.Digest, reviewed.TaskReview.ExpiresAt
	v, err = s.changeAgents(t.Context(), c, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Tasks) != 1 || v.Tasks[0].AI == nil || v.Tasks[0].AI.Analysis == nil || len(v.Sessions) != 1 || len(v.AIRequests) != 0 {
		t.Fatal("creation did not atomically bind analysis context", v.Tasks)
	}
	v, err = s.changeAgents(t.Context(), AgentCommand{Action: "task-run", ProfileID: v.ProfileID, Revision: v.Revision, ID: v.Tasks[0].ID}, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); err != nil {
		t.Fatal(err)
	}
	v, err = s.Agents(t.Context(), v.ProfileID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Runs) != 1 || v.Runs[0].Strategy != "analysis-v1" || v.Runs[0].Analysis == nil || v.Runs[0].Analysis.State != "completed" || len(v.AIRequests) != 1 || !strings.Contains(v.Notices[0].Text, "analysis saved") || strings.Contains(v.Notices[0].Text, c.Text) {
		t.Fatal("analysis was coerced into a reminder", v.Runs, v.Notices, v.AIRequests[0].State, v.AIRequests[0].Failure)
	}
	old, _, _ := readAgents(t.Context(), s.db)
	v = agentApply(t, s, AgentCommand{Action: "task-run-explain", ProfileID: v.ProfileID, ID: v.Runs[0].ID})
	if !strings.Contains(v.Messages[0].Text, "agent-analysis-run-chat-v1") || !strings.Contains(v.Messages[0].Text, v.AIRequests[0].Answer) {
		t.Fatal("result cannot join Chat")
	}
	after, _, _ := readAgents(t.Context(), s.db)
	after.Messages, after.Sessions = old.Messages, old.Sessions
	if !reflect.DeepEqual(old, after) {
		t.Fatal("Discuss changed execution")
	}
	if v.ProviderCallsEnabled || v.FinancialExecution {
		t.Fatal("live authority enabled")
	}
}

func createAnalysisFixture(t *testing.T, s *Store, c AgentCommand) AgentView {
	t.Helper()
	v, err := s.Agents(t.Context(), c.ProfileID, "")
	if err != nil {
		t.Fatal(err)
	}
	c.Revision = v.Revision
	c.Action = "task-review"
	preview, err := s.changeAgents(t.Context(), c, false)
	if err != nil {
		t.Fatal(err)
	}
	c.Action = "task-reviewed-create"
	c.ExpiresAt = preview.TaskReview.ExpiresAt
	c.ConsentDigest = preview.TaskReview.Digest
	v, err = s.changeAgents(t.Context(), c, true)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func analysisWatchFixture(t *testing.T, mode string) (*Store, AgentView) {
	t.Helper()
	s := newsTestStore(t)
	v := newsAdd(t, s, "trading-analyst")
	c := analysisCommand(v)
	c.Watch = &NewsWatchRule{Version: newsWatchVersion, Contains: "paper"}
	c.NewsSourceID = v.NewsSources[0].ID
	c.NewsMaxAgeSeconds = 300
	c.Analysis.Failure = mode
	c.MaxRuns = 4
	c.TotalBudgetMicrousd = 120000
	return s, createAnalysisFixture(t, s, c)
}
func fetchAnalysisWatch(t *testing.T, s *Store, v AgentView, body string, enabled bool) AgentView {
	t.Helper()
	var err error
	v, err = s.changeAgents(t.Context(), AgentCommand{Action: "task-run", ProfileID: v.ProfileID, Revision: v.Revision, ID: v.Tasks[0].ID}, enabled)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.advanceAgentNews(t.Context(), newsHTTPS(t, rssHandler(body, new(atomic.Int32))), enabled); err != nil {
		t.Fatal(err)
	}
	v, err = s.Agents(t.Context(), v.ProfileID, "")
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func TestTaskAnalysisWatchSuccessThenNoNewMatchDoesNotCallAI(t *testing.T) {
	s, v := analysisWatchFixture(t, "required")
	body := watchBody(s.now().Unix())
	v = fetchAnalysisWatch(t, s, v, body, true)
	if v.Tasks[0].State != "waiting-ai" || len(v.Runs) != 0 || len(v.Tasks[0].Watch.Seen) != 0 {
		t.Fatal("match committed before required analysis", v.Tasks)
	}
	if err := s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	if len(v.Runs) != 1 || v.Runs[0].Analysis == nil || v.Runs[0].Analysis.State != "completed" || len(v.Tasks[0].Watch.Seen) != 1 || v.Runs[0].News == nil || v.AIRequests[0].News == nil || *v.Runs[0].News != *v.AIRequests[0].News {
		t.Fatal("analysis and source result diverged", v.Runs)
	}
	v = fetchAnalysisWatch(t, s, v, body, false)
	if len(v.AIRequests) != 1 || len(v.Runs) != 2 || v.Runs[1].Analysis.Reason != "no-new-matches" || len(v.Notices) != 1 {
		t.Fatal("repeat created AI or notification", v.Runs)
	}
	v = agentApply(t, s, AgentCommand{Action: "task-run-explain", ProfileID: v.ProfileID, ID: v.Runs[0].ID})
	if !strings.Contains(v.Messages[0].Text, "news-watch-run-chat-v1") || !strings.Contains(v.Messages[0].Text, v.AIRequests[0].Answer) {
		t.Fatal("watch analysis lost in Chat")
	}
	v = agentApply(t, s, AgentCommand{Action: "memory-chat-save", ProfileID: v.ProfileID, Source: "message", ID: v.Messages[0].ID, Text: "Watch analysis is historical evidence"})
	if len(v.Memories) != 1 {
		t.Fatal("reviewed memory unavailable")
	}
}
func TestTaskAnalysisRequiredVersusOptionalFailureAndUnknownHolds(t *testing.T) {
	for _, policy := range []string{"required", "optional"} {
		for _, failure := range []string{"known", "unknown", "disabled"} {
			t.Run(policy+"/"+failure, func(t *testing.T) {
				s, v := analysisWatchFixture(t, policy)
				v = fetchAnalysisWatch(t, s, v, watchBody(s.now().Unix()), failure != "disabled")
				if failure != "disabled" {
					rt := aiTestTransport(func(r *http.Request) (*http.Response, error) {
						if failure == "unknown" {
							return nil, errors.New("controlled disconnect")
						}
						return &http.Response{StatusCode: 400, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":"controlled rejection"}`))}, nil
					})
					if err := s.advanceAgentAI(t.Context(), rt, true); err != nil {
						t.Fatal(err)
					}
				}
				v, _ = s.Agents(t.Context(), v.ProfileID, "")
				if policy == "required" {
					if v.Tasks[0].State != "held" || len(v.Tasks[0].Watch.Seen) != 0 || v.Runs[0].Analysis != nil {
						t.Fatal("required failure did not hold", v.Tasks, v.Runs)
					}
				} else {
					if v.Tasks[0].State != "scheduled" || len(v.Tasks[0].Watch.Seen) != 1 || v.Runs[0].Analysis == nil || v.Runs[0].Analysis.State != "skipped" {
						t.Fatal("optional failure discarded deterministic result", v.Tasks, v.Runs)
					}
					before := v
					now, dir := s.now(), s.dir
					if err := s.Close(); err != nil {
						t.Fatal(err)
					}
					var err error
					s, err = OpenStore(dir)
					if err != nil {
						t.Fatal(err)
					}
					defer s.Close()
					s.now = func() time.Time { return now }
					v, err = s.Agents(t.Context(), v.ProfileID, "")
					if err != nil || !reflect.DeepEqual(v.Tasks, before.Tasks) || !reflect.DeepEqual(v.Runs, before.Runs) {
						t.Fatal("restart lost fallback result", err)
					}
					if failure == "unknown" {
						if v.AIRequests[0].State != "unknown" || v.AIRequests[0].HeldMicrousd == 0 {
							t.Fatal("unknown use was erased")
						}
						body := strings.ReplaceAll(watchBody(now.Unix()), "Controlled paper-news headline", "Another paper headline")
						v = fetchAnalysisWatch(t, s, v, body, true)
						if len(v.AIRequests) != 1 || v.Runs[1].Analysis.Reason != "ai-profile-held" || v.AIRequests[0].HeldMicrousd != before.AIRequests[0].HeldMicrousd {
							t.Fatal("unknown hold bypassed or repeated", v.Runs)
						}
					}
					v = agentApply(t, s, AgentCommand{Action: "task-run-explain", ProfileID: v.ProfileID, ID: v.Runs[0].ID})
					if !strings.Contains(v.Messages[0].Text, `"state":"skipped"`) {
						t.Fatal("omission missing from Chat")
					}
				}
				if v.ProviderCallsEnabled || v.FinancialExecution {
					t.Fatal("live features activated")
				}
			})
		}
	}
}
func TestTaskAnalysisExplicitPauseDoesNotContinueOptionalWatch(t *testing.T) {
	s, v := analysisWatchFixture(t, "optional")
	v = fetchAnalysisWatch(t, s, v, watchBody(s.now().Unix()), true)
	v = agentApply(t, s, AgentCommand{Action: "task-pause", ProfileID: v.ProfileID, ID: v.Tasks[0].ID})
	if len(v.Tasks[0].Watch.Seen) != 0 || v.Tasks[0].State != "cancelled" || v.AIRequests[0].State != "cancelled" || v.AIRequests[0].HeldMicrousd != 0 {
		t.Fatal("owner stop ran fallback", v.Tasks, v.Runs)
	}
	calls := 0
	rt := aiTestTransport(func(r *http.Request) (*http.Response, error) { calls++; return (fixtureAITransport{}).RoundTrip(r) })
	if err := s.advanceAgentAI(t.Context(), rt, true); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("stopped request replayed")
	}
}
func TestTaskAnalysisInterruptedWatchRecordsOmissionOnce(t *testing.T) {
	s, v := analysisWatchFixture(t, "optional")
	v = fetchAnalysisWatch(t, s, v, watchBody(s.now().Unix()), true)
	rewriteReminderFixture(t, s, func(d *agentDocument) { d.AIRequests[0].State = "running"; d.AIRequests[0].DispatchBoot = s.bootID })
	dir, now := s.dir, s.now()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	n, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	n.now = func() time.Time { return now }
	calls := 0
	rt := aiTestTransport(func(r *http.Request) (*http.Response, error) { calls++; return (fixtureAITransport{}).RoundTrip(r) })
	if err = n.advanceAgentAI(t.Context(), rt, true); err != nil {
		t.Fatal(err)
	}
	v, err = n.Agents(t.Context(), v.ProfileID, "")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || len(v.Runs) != 1 || v.Runs[0].Analysis.Reason != "ai-unknown" || len(v.Tasks[0].Watch.Seen) != 1 || v.AIRequests[0].HeldMicrousd == 0 {
		t.Fatal("interruption replayed or lost usage hold", v.Runs)
	}
	before := retainedRevisionState(t, n)
	if err = n.advanceAgentAI(t.Context(), rt, true); err != nil {
		t.Fatal(err)
	}
	if retainedRevisionState(t, n) != before {
		t.Fatal("omission settled twice")
	}
}
func TestTaskAnalysisReviewBindingAndScope(t *testing.T) {
	s := newsTestStore(t)
	v := newsAdd(t, s, "trading-analyst")
	c := analysisCommand(v)
	preview, err := s.changeAgents(t.Context(), c, false)
	if err != nil {
		t.Fatal(err)
	}
	c.Action, c.ConsentDigest, c.ExpiresAt = "task-reviewed-create", preview.TaskReview.Digest, preview.TaskReview.ExpiresAt
	before := retainedRevisionState(t, s)
	for _, kind := range []string{"policy", "prompt", "source", "profile", "budget", "model", "schedule", "expiry", "raw-prepare"} {
		t.Run(kind, func(t *testing.T) {
			bad := c
			policy := *c.Analysis
			bad.Analysis = &policy
			switch kind {
			case "policy":
				bad.Analysis.Failure = "optional"
			case "prompt":
				bad.Text = "Changed"
			case "source":
				bad.NewsSourceID = v.NewsSources[0].ID
				bad.NewsMaxAgeSeconds = 300
			case "profile":
				bad.ProfileID = "news-researcher"
			case "budget":
				bad.TotalBudgetMicrousd++
			case "model":
				bad.Model = "retired-model"
			case "schedule":
				bad.IntervalSeconds++
			case "expiry":
				bad.ExpiresAt--
			case "raw-prepare":
				bad.Action = "task-ai-prepare"
			}
			if _, err := s.changeAgents(t.Context(), bad, true); err == nil {
				t.Fatal("changed review accepted")
			}
			if retainedRevisionState(t, s) != before {
				t.Fatal("refusal wrote records")
			}
		})
	}
	now := s.now().Add(601 * time.Second)
	s.now = func() time.Time { return now }
	if _, err = s.changeAgents(t.Context(), c, true); err == nil {
		t.Fatal("expired review accepted")
	}
}
func TestTaskAnalysisCalendarAndFreshNewsThroughSharedWorker(t *testing.T) {
	for _, version := range []string{"once-at-v1", "cron-v1"} {
		t.Run(version, func(t *testing.T) {
			s := newsTestStore(t)
			v := newsAdd(t, s, "trading-analyst")
			c := analysisCommand(v)
			c.IntervalSeconds = 0
			c.Calendar = &TaskCalendar{Version: version, At: s.now().Unix() + 120, TimeZone: "UTC"}
			c.NewsSourceID = v.NewsSources[0].ID
			c.NewsMaxAgeSeconds = 300
			if version == "once-at-v1" {
				c.MaxRuns = 1
			} else {
				c.Calendar.Expression = "* * * * *"
				c.Calendar.Until = s.now().Unix() + 600
			}
			v = createAnalysisFixture(t, s, c)
			now := s.now().Add(2 * time.Minute)
			s.now = func() time.Time { return now }
			if err := s.runAgentTasks(t.Context(), true); err != nil {
				t.Fatal(err)
			}
			if err := s.advanceAgentNews(t.Context(), newsHTTPS(t, rssHandler(watchBody(now.Unix()), new(atomic.Int32))), true); err != nil {
				t.Fatal(err)
			}
			if err := s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); err != nil {
				t.Fatal(err)
			}
			v, err := s.Agents(t.Context(), v.ProfileID, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(v.Runs) != 1 || v.Runs[0].Strategy != "analysis-v1" || v.Runs[0].News == nil || v.Runs[0].Analysis == nil {
				t.Fatal("calendar analysis/source did not settle", v.Tasks, v.Runs)
			}
			before := retainedRevisionState(t, s)
			if err = s.runAgentTasks(t.Context(), true); err != nil {
				t.Fatal(err)
			}
			if retainedRevisionState(t, s) != before {
				t.Fatal("calendar repeated")
			}
		})
	}
}

func TestTaskAnalysisNativeGrantAndSourceUseExistingCredentialBoundary(t *testing.T) {
	s := fixtureStore(t)
	a := addNativeAccount(t, s, "anthropic", true)
	rt := nativeHTTPSFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
		res, err := nativeFixtureResponse(r, "anthropic")
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer res.Body.Close()
		w.Header().Set("Content-Type", res.Header.Get("Content-Type"))
		io.Copy(w, res.Body)
	})
	if _, err := s.refreshProviderModels(t.Context(), a.Ref, rt); err != nil {
		t.Fatal(err)
	}
	a, _ = s.AIConnection(a.Ref)
	v, p := nativePermission(t, s, a, registryForProvider("anthropic").Models[0].ID)
	c := analysisCommand(v)
	c.UseAgentDefaults = true
	c.SessionID = p.SessionID
	c.Limits = &p.Limits
	c.TotalBudgetMicrousd = p.Limits.BudgetMicrousd
	c.MaxRuns = 1
	c.AuthorizationSeconds = 7200
	// Creation authorizes the reviewed job, not background credential access.
	v = createAnalysisFixture(t, s, c)
	var err error
	v, err = s.changeAgents(t.Context(), AgentCommand{Action: "task-run", ProfileID: v.ProfileID, Revision: v.Revision, ID: v.Tasks[0].ID}, true)
	if err != nil {
		t.Fatal(err)
	}
	if v.Tasks[0].State != "held" || len(v.AIRequests) != 0 {
		t.Fatal("interactive unlock substituted for finite task grant", v.Tasks)
	}
	v = newsAdd(t, s, v.ProfileID)
	c.NewsSourceID = v.NewsSources[0].ID
	c.NewsMaxAgeSeconds = 300
	v = createAnalysisFixture(t, s, c)
	task := v.Tasks[len(v.Tasks)-1]
	grant, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-review", Ref: a.Ref, ProfileID: v.ProfileID, Revision: v.Revision, TaskIDs: []string{task.ID}, AuthorizationSeconds: 5400})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-allow", Ref: a.Ref, Allowance: grant.AccessReview, ConsentDigest: grant.AccessReview.ID, Passphrase: testVaultPassword}); err != nil {
		t.Fatal(err)
	}
	later := s.now().Add(61 * time.Minute)
	s.now = func() time.Time { return later }
	if err = s.runAgentTasks(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if err = s.advanceAgentNews(t.Context(), newsHTTPS(t, rssHandler(watchBody(later.Unix()), new(atomic.Int32))), false); err != nil {
		t.Fatal(err)
	}
	if err = s.advanceAgentAI(t.Context(), rt, false); err != nil {
		t.Fatal(err)
	}
	v, err = s.Agents(t.Context(), v.ProfileID, "")
	if err != nil {
		t.Fatal(err)
	}
	q := v.AIRequests[len(v.AIRequests)-1]
	if q.State != "completed" || q.TaskID != task.ID || q.ConnectionRef != a.Ref || v.Runs[len(v.Runs)-1].Analysis == nil || v.Runs[len(v.Runs)-1].News == nil {
		t.Fatal("native account/grant/source was disconnected", q.State, q.Failure, v.Tasks)
	}
	status, _ := s.AIConnection(a.Ref)
	if !status.Locked || status.TaskAccess == nil || status.TaskAccess.UsedRequests != 1 || status.LiveCallsEnabled {
		t.Fatal("task grant unlocked Chat or changed live gate", status.Locked)
	}
}

func TestTaskAnalysisMigrationAndTamperPreserveOriginalRecords(t *testing.T) {
	s := fixtureStore(t)
	a := addNativeAccount(t, s, "anthropic", true)
	v := agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: "trading-analyst", Name: "Old draft context"})
	agentApply(t, s, AgentCommand{Action: "task-create", ProfileID: v.ProfileID, Name: "Old reminder", Text: "Keep this record", IntervalSeconds: 60, MaxRuns: 1})
	before := retainedRevisionState(t, s)
	vault, err := ReadPrivateFile(s.aiVaultPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("PRAGMA user_version=28"); err != nil {
		t.Fatal(err)
	}
	dir := s.dir
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	n, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	var schema int
	if err = n.db.QueryRow("PRAGMA user_version").Scan(&schema); err != nil || schema != StateSchema || schema < 29 {
		t.Fatal("schema floor missing", schema, err)
	}
	nextVault, err := ReadPrivateFile(n.aiVaultPath())
	if err != nil || !bytes.Equal(vault, nextVault) || retainedRevisionState(t, n) != before {
		t.Fatal("migration changed originals/vault", err)
	}
	oldAccount, err := n.AIConnection(a.Ref)
	if err != nil || oldAccount.Ref != a.Ref || !oldAccount.Locked {
		t.Fatal("account identity or lock lost", err)
	}
	v, err = n.Agents(t.Context(), v.ProfileID, "")
	if err != nil {
		t.Fatal(err)
	}
	c := analysisCommand(v)
	v = createAnalysisFixture(t, n, c)
	original := retainedRevisionState(t, n)
	if _, err = n.db.Exec("PRAGMA user_version=28"); err != nil {
		t.Fatal(err)
	}
	if err = migrateTaskAnalysis(t.Context(), n.db); err == nil {
		t.Fatal("unprotected policy adopted")
	}
	if retainedRevisionState(t, n) != original {
		t.Fatal("failed migration rewrote records")
	}
	for _, mode := range []string{"unknown", "alias", "null", "erased", "failure", "digest", "foreign-session"} {
		t.Run(mode, func(t *testing.T) {
			var raw string
			if err := n.db.QueryRow("SELECT document FROM agent_workspace WHERE id=1").Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if err := json.Unmarshal([]byte(raw), &doc); err != nil {
				t.Fatal(err)
			}
			task := doc["tasks"].([]any)[1].(map[string]any)
			ai := task["ai"].(map[string]any)
			policy := ai["analysis"].(map[string]any)
			switch mode {
			case "unknown":
				policy["newAuthority"] = true
			case "alias":
				ai["Analysis"] = ai["analysis"]
				delete(ai, "analysis")
			case "null":
				ai["analysis"] = nil
			case "erased":
				delete(ai, "analysis")
			case "failure":
				policy["failure"] = "optional"
			case "digest":
				ai["digest"] = "changed"
			case "foreign-session":
				ai["sessionId"] = "foreign"
			}
			bad, _ := json.Marshal(doc)
			if _, err := n.db.Exec("UPDATE agent_workspace SET document=? WHERE id=1", string(bad)); err != nil {
				t.Fatal(err)
			}
			if _, _, err := readAgents(t.Context(), n.db); err == nil {
				t.Fatal("changed/erased policy accepted")
			}
			var after string
			n.db.QueryRow("SELECT document FROM agent_workspace WHERE id=1").Scan(&after)
			if after != string(bad) {
				t.Fatal("reader erased changed state")
			}
			if _, err := n.db.Exec("UPDATE agent_workspace SET document=? WHERE id=1", raw); err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, err = n.db.Exec(fmt.Sprintf("PRAGMA user_version=%d", StateSchema)); err != nil {
		t.Fatal(err)
	}
	pause := pauseCommand(t, n, 1)
	if _, err = n.UpdatePause(t.Context(), pause); err != nil {
		t.Fatal(err)
	}
	if _, err = n.db.Exec("PRAGMA user_version=28"); err != nil {
		t.Fatal(err)
	}
	if err = migrateTaskAnalysis(t.Context(), n.db); !errors.Is(err, ErrCorePaused) {
		t.Fatal("paused migration admitted", err)
	}
}
func TestTaskAnalysisProfileAndAccountChangesCannotRewriteSavedWork(t *testing.T) {
	s := newsTestStore(t)
	v, err := s.Agents(t.Context(), "trading-analyst", "")
	if err != nil {
		t.Fatal(err)
	}
	v = createAnalysisFixture(t, s, analysisCommand(v))
	saved := v.Tasks[0]
	foreign, err := s.Agents(t.Context(), "news-researcher", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.changeAgents(t.Context(), AgentCommand{Action: "task-run", ProfileID: foreign.ProfileID, Revision: foreign.Revision, ID: saved.ID}, true); err == nil {
		t.Fatal("foreign task ran")
	}
	v = agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, Text: "Summarize the selected evidence without proposing actions: the owner changed this relevant evidence", Source: "owner correction"})
	v, err = s.changeAgents(t.Context(), AgentCommand{Action: "task-run", ProfileID: v.ProfileID, Revision: v.Revision, ID: saved.ID}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.AIRequests) != 0 || v.Tasks[0].State != "held" || v.Runs[0].Outcome != "ai-consent-invalid" || v.Tasks[0].AI.Digest != saved.AI.Digest {
		t.Fatal("changed memory silently reauthorized AI", v.Tasks)
	}
	v = agentApply(t, s, AgentCommand{Action: "task-run-explain", ProfileID: v.ProfileID, ID: v.Runs[0].ID})
	if !strings.Contains(v.Messages[0].Text, "agent-analysis-run-chat-v1") {
		t.Fatal("held analysis has no typed Chat result")
	}
}

func TestTaskAnalysisResultCannotBeReplayedOrChanged(t *testing.T) {
	s := newsTestStore(t)
	v, _ := s.Agents(t.Context(), "trading-analyst", "")
	v = createAnalysisFixture(t, s, analysisCommand(v))
	v = taskAIRun(t, s, v)
	if err := s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); err != nil {
		t.Fatal(err)
	}
	var original string
	if err := s.db.QueryRow("SELECT document FROM agent_workspace WHERE id=1").Scan(&original); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"request", "replay", "missing", "profile", "financial", "state", "unknown", "alias"} {
		t.Run(mode, func(t *testing.T) {
			var d map[string]any
			json.Unmarshal([]byte(original), &d)
			run := d["runs"].([]any)[0].(map[string]any)
			policy := run["analysis"].(map[string]any)
			switch mode {
			case "request":
				run["aiRequestId"] = "foreign"
			case "replay":
				d["runs"] = append(d["runs"].([]any), run)
				d["tasks"].([]any)[0].(map[string]any)["runs"] = 2
			case "missing":
				d["runs"] = []any{}
			case "profile":
				run["profileId"] = "news-researcher"
			case "financial":
				run["quantity"] = 1
			case "state":
				policy["state"] = "skipped"
			case "unknown":
				policy["newAuthority"] = true
			case "alias":
				run["Analysis"] = run["analysis"]
				delete(run, "analysis")
			}
			b, _ := json.Marshal(d)
			if _, err := s.db.Exec("UPDATE agent_workspace SET document=? WHERE id=1", string(b)); err != nil {
				t.Fatal(err)
			}
			if _, _, err := readAgents(t.Context(), s.db); err == nil {
				t.Fatal("changed result accepted")
			}
			var after string
			s.db.QueryRow("SELECT document FROM agent_workspace WHERE id=1").Scan(&after)
			if after != string(b) {
				t.Fatal("reader erased changed result")
			}
			if _, err := s.db.Exec("UPDATE agent_workspace SET document=? WHERE id=1", original); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTaskAnalysisOptionalExpiryDuringCompletedCallPreservesKnownUsage(t *testing.T) {
	s := newsTestStore(t)
	v := newsAdd(t, s, "trading-analyst")
	c := analysisCommand(v)
	c.Watch = &NewsWatchRule{Version: newsWatchVersion, Contains: "paper"}
	c.NewsSourceID = v.NewsSources[0].ID
	c.NewsMaxAgeSeconds = 300
	c.Analysis.Failure = "optional"
	c.AuthorizationSeconds = 60
	v = createAnalysisFixture(t, s, c)
	v = fetchAnalysisWatch(t, s, v, watchBody(s.now().Unix()), true)
	now := s.now()
	s.now = func() time.Time { return now }
	calls := 0
	rt := aiTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		response, err := (fixtureAITransport{}).RoundTrip(r)
		now = now.Add(61 * time.Second)
		return response, err
	})
	if err := s.advanceAgentAI(t.Context(), rt, true); err != nil {
		t.Fatal("completed usage/result rolled back", err)
	}
	v, err := s.Agents(t.Context(), v.ProfileID, "")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || v.AIRequests[0].State != "completed" || v.AIRequests[0].HeldMicrousd != 0 || v.AIRequests[0].SpentMicrousd == 0 || v.Runs[0].Analysis.State != "skipped" || v.Runs[0].Analysis.Reason != "ai-consent-invalid" || len(v.Tasks[0].Watch.Seen) != 1 {
		t.Fatal("expiry erased known use or admitted output", v.AIRequests[0].State, v.Runs)
	}
	before := retainedRevisionState(t, s)
	if err = s.advanceAgentAI(t.Context(), rt, true); err != nil {
		t.Fatal(err)
	}
	if retainedRevisionState(t, s) != before || calls != 1 {
		t.Fatal("completed request replayed")
	}
}

func TestTaskAnalysisPausedOptionalWatchResumesWithoutAIGrant(t *testing.T) {
	s, v := analysisWatchFixture(t, "optional")
	v = fetchAnalysisWatch(t, s, v, watchBody(s.now().Unix()), false)
	v = agentApply(t, s, AgentCommand{Action: "task-pause", ProfileID: v.ProfileID, ID: v.Tasks[0].ID})
	now := s.now().Add(61 * time.Minute)
	s.now = func() time.Time { return now }
	var err error
	v, err = s.changeAgents(t.Context(), AgentCommand{Action: "task-resume", ProfileID: v.ProfileID, Revision: v.Revision, ID: v.Tasks[0].ID}, false)
	if err != nil {
		t.Fatal("optional deterministic continuation required AI", err)
	}
	body := strings.ReplaceAll(watchBody(now.Unix()), "Controlled paper-news headline", "Another paper headline")
	v = fetchAnalysisWatch(t, s, v, body, false)
	if len(v.AIRequests) != 0 || v.Tasks[0].Runs != 2 || v.Runs[1].Analysis.State != "skipped" || len(v.Tasks[0].Watch.Seen) != 2 {
		t.Fatal("resume lost deterministic scope or called AI", v.Tasks, v.Runs)
	}
}
