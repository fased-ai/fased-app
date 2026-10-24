package core

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAgentPlaybookSaveIsInert(t *testing.T) {
	s := fixtureStore(t)
	v := agentApply(t, s, AgentCommand{Action: "news-source-add", ProfileID: "news-researcher", Name: "Fixture headlines", Source: fixtureNewsURL, NewsMaxAgeSeconds: 3600})
	_, err := s.ChangeAgents(t.Context(), AgentCommand{Revision: v.Revision, ProfileID: v.ProfileID, Action: "playbook-save", Name: "Morning digest", Text: "Summarize the selected fixture headlines.", NewsSourceID: v.NewsSources[0].ID, NewsMaxAgeSeconds: 3600, Limits: &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 3, BudgetMicrousd: 30000}})
	if err != nil {
		t.Fatal(err)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	if len(v.Tasks) != 0 || len(v.AIRequests) != 0 || len(v.NewsFetches) != 0 {
		t.Fatal("saving a playbook executed work")
	}
}

func playbookSetup(t *testing.T) (*Store, AgentView) {
	t.Helper()
	s := fixtureStore(t)
	p := "news-researcher"
	v := agentApply(t, s, AgentCommand{Action: "news-source-add", ProfileID: p, Name: "Fixture headlines", Source: fixtureNewsURL, NewsMaxAgeSeconds: 3600})
	source := v.NewsSources[0].ID
	v = agentApply(t, s, AgentCommand{Action: "news-fetch", ProfileID: p, ID: source})
	if err := s.advanceAgentNews(t.Context(), fixtureNewsTransport{now: s.now}, false); err != nil {
		t.Fatal(err)
	}
	v = agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: p, Name: "Digest conversation"})
	v = agentApply(t, s, AgentCommand{Action: "playbook-save", ProfileID: p, Name: "Morning digest", Text: "Summarize the selected fixture headlines.", NewsSourceID: source, NewsMaxAgeSeconds: 3600, Limits: &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 3, BudgetMicrousd: 30000}})
	return s, v
}
func playbookReview(t *testing.T, s *Store, v AgentView) PlaybookReview {
	t.Helper()
	p := v.Playbooks[0]
	out, err := s.ChangeAgents(t.Context(), AgentCommand{Action: "playbook-run-review", Revision: v.Revision, ProfileID: v.ProfileID, ID: p.ID, PlaybookVersion: len(p.Versions), SessionID: v.Sessions[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if out.PlaybookReview == nil || out.Revision != v.Revision {
		t.Fatal("review wrote or missing")
	}
	return *out.PlaybookReview
}
func TestAgentPlaybookJourneyVersionsAndRestart(t *testing.T) {
	s, v := playbookSetup(t)
	before := retainedRevisionState(t, s)
	draft, err := s.AgentPlaybookDraft(t.Context(), v.ProfileID, v.Playbooks[0].ID, 1)
	if err != nil || draft.Selected.Version != 1 || draft.Command.Action != "playbook-run-review" {
		t.Fatal(draft, err)
	}
	review := playbookReview(t, s, v)
	if before != retainedRevisionState(t, s) || len(v.Tasks) != 0 || review.Task.MaxRuns != 1 || review.Task.AI.Authorized || review.Task.AI.ConnectionRef != "" || len(review.News.Items) != 1 {
		t.Fatal("prepare wrote or inherited authority")
	}
	if _, err = s.ChangeAgents(t.Context(), *review.Command); err == nil {
		t.Fatal("ordinary core created a run")
	}
	out, err := s.changeAgents(t.Context(), *review.Command, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Tasks) != 1 || out.Tasks[0].State != "waiting-news" || len(out.AIRequests) != 0 || out.Tasks[0].Playbook.Version != 1 {
		t.Fatal("one-run planning", out.Tasks)
	}
	if err = s.advanceAgentNews(t.Context(), fixtureNewsTransport{now: s.now}, true); err != nil {
		t.Fatal(err)
	}
	if err = s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); err != nil {
		t.Fatal(err)
	}
	if err = s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); err != nil {
		t.Fatal(err)
	}
	out, err = s.Agents(t.Context(), v.ProfileID, "")
	if err != nil || out.Tasks[0].State != "completed" || out.Tasks[0].Runs != 1 || len(out.Runs) != 1 || len(out.Notices) != 1 || len(out.AIRequests) != 1 || out.AIRequests[0].Answer == "" || out.Runs[0].News == nil {
		t.Fatal("digest result", out, err)
	}
	if _, err = s.changeAgents(t.Context(), AgentCommand{Action: "task-run", ProfileID: v.ProfileID, Revision: out.Revision, ID: out.Tasks[0].ID}, true); err == nil {
		t.Fatal("second run accepted")
	}
	if _, err = s.changeAgents(t.Context(), *review.Command, true); err == nil {
		t.Fatal("replayed review accepted")
	}
	v = agentApply(t, s, AgentCommand{Action: "playbook-save", ProfileID: v.ProfileID, ID: v.Playbooks[0].ID, Name: "Afternoon digest", Text: "Compare uncertainty in the fixture headlines.", NewsSourceID: v.NewsSources[0].ID, NewsMaxAgeSeconds: 3600, Limits: &draft.Selected.Limits})
	if len(v.Playbooks[0].Versions) != 2 || v.Playbooks[0].Versions[0] != draft.Selected {
		t.Fatal("history overwritten")
	}
	id := v.Playbooks[0].ID
	dir := s.dir
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	n, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	v, err = n.Agents(t.Context(), v.ProfileID, "")
	if err != nil || len(v.Playbooks[0].Versions) != 2 || len(v.Runs) != 1 {
		t.Fatal("restart lost records", err)
	}
	old, err := n.AgentPlaybookDraft(t.Context(), v.ProfileID, id, 1)
	if err != nil || old.Selected != draft.Selected {
		t.Fatal("old version unavailable", err)
	}
	foreign, err := n.Agents(t.Context(), "trading-analyst", "")
	if err != nil || len(foreign.Playbooks) != 0 || len(foreign.Tasks) != 0 {
		t.Fatal("profile isolation", err)
	}
	if _, err = n.AgentPlaybookDraft(t.Context(), foreign.ProfileID, id, 1); err == nil {
		t.Fatal("foreign read accepted")
	}
}
func TestAgentPlaybookReviewFences(t *testing.T) {
	for _, mode := range []string{"digest", "session", "version", "profile", "expiry", "restart", "stale", "rebase", "disabled-source", "failed-news", "missing-news"} {
		t.Run(mode, func(t *testing.T) {
			s, v := playbookSetup(t)
			review := playbookReview(t, s, v)
			c := *review.Command
			switch mode {
			case "digest":
				c.ConsentDigest = "wrong"
			case "session":
				c.SessionID = "missing"
			case "version":
				c.PlaybookVersion = 2
			case "profile":
				c.ProfileID = "trading-analyst"
			case "expiry":
				now := s.now()
				s.now = func() time.Time { return now.Add(601 * time.Second) }
			case "restart":
				s.bootID = "new-disposable-boot"
			case "stale", "rebase":
				v = agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, Text: "Correction", Source: "owner"})
				if mode == "rebase" {
					c.Revision = v.Revision
				}
			case "disabled-source":
				v = agentApply(t, s, AgentCommand{Action: "news-source-disable", ProfileID: v.ProfileID, ID: v.NewsSources[0].ID})
				c.Revision = v.Revision
			case "failed-news", "missing-news":
				if mode == "failed-news" {
					v = agentApply(t, s, AgentCommand{Action: "news-fetch", ProfileID: v.ProfileID, ID: v.NewsSources[0].ID})
				} else {
					_, err := s.db.Exec("UPDATE agent_workspace SET document=json_set(document,'$.newsFetches',json('[]'))")
					if err != nil {
						t.Fatal(err)
					}
				}
				c.Revision = v.Revision
			}
			before := retainedRevisionState(t, s)
			if _, err := s.changeAgents(t.Context(), c, true); err == nil {
				t.Fatal("unsafe consent accepted")
			}
			if before != retainedRevisionState(t, s) {
				t.Fatal("rejection changed records")
			}
		})
	}
}
func TestAgentPlaybookBoundsAndStrictInputs(t *testing.T) {
	s, v := playbookSetup(t)
	l := v.Playbooks[0].Versions[0].Limits
	base := AgentCommand{Action: "playbook-save", Revision: v.Revision, ProfileID: v.ProfileID, ID: v.Playbooks[0].ID, Name: "vNext", Text: "Digest", NewsSourceID: v.NewsSources[0].ID, NewsMaxAgeSeconds: 3600, Limits: &l}
	for _, mode := range []string{"connection", "provider", "schedule", "consent", "memory", "foreign", "source", "tokens", "name"} {
		t.Run(mode, func(t *testing.T) {
			c := base
			limits := l
			c.Limits = &limits
			switch mode {
			case "connection":
				c.ConnectionRef = "forbidden"
			case "provider":
				c.Provider = "openai"
			case "schedule":
				c.MaxRuns = 2
			case "consent":
				c.ConsentDigest = "forbidden"
			case "memory":
				c.MemorySelection = &AIMemorySelection{Mode: "all"}
			case "foreign":
				c.ProfileID = "trading-analyst"
			case "source":
				c.NewsSourceID = "missing"
			case "tokens":
				c.Limits.MaxInputTokens = 1
			case "name":
				c.Name = ""
			}
			before := retainedRevisionState(t, s)
			if _, err := s.ChangeAgents(t.Context(), c); err == nil {
				t.Fatal("invalid settings accepted")
			}
			if before != retainedRevisionState(t, s) {
				t.Fatal("invalid save wrote")
			}
		})
	}
	for i := 1; i < 8; i++ {
		base.Revision = v.Revision
		var err error
		v, err = s.ChangeAgents(t.Context(), base)
		if err != nil {
			t.Fatal(err)
		}
	}
	base.Revision = v.Revision
	if _, err := s.ChangeAgents(t.Context(), base); err == nil {
		t.Fatal("ninth version accepted")
	}
}
func TestAgentPlaybookMigrationAndCompatibility(t *testing.T) {
	s := fixtureStore(t)
	before := retainedRevisionState(t, s)
	if _, err := s.db.Exec("PRAGMA user_version=15"); err != nil {
		t.Fatal(err)
	}
	if err := migratePlaybooks(t.Context(), s.db); err != nil {
		t.Fatal(err)
	}
	if retainedRevisionState(t, s) != before {
		t.Fatal("format migration wrote original records")
	}
	var schema int
	s.db.QueryRow("PRAGMA user_version").Scan(&schema)
	if schema != 16 || StateSchema != 16 {
		t.Fatal("missing newer-reader protection")
	}
	contract := UpdateContract()
	b, _ := json.Marshal(contract)
	if !strings.Contains(string(b), "agents-playbooks-v1") {
		t.Fatal("feature erasure protection missing")
	}
	s2, v := playbookSetup(t)
	if _, err := s2.db.Exec("PRAGMA user_version=15"); err != nil {
		t.Fatal(err)
	}
	if err := migratePlaybooks(t.Context(), s2.db); err == nil {
		t.Fatal("unprotected playbook adoption accepted", v.Playbooks)
	}
}
func TestAgentPlaybookHTTPStrictReadAndAuthentication(t *testing.T) {
	s, v := playbookSetup(t)
	token := claimFixture(t, s)
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/agents?profile=" + v.ProfileID + "&playbook-draft=" + v.Playbooks[0].ID + "&playbook-version=1"
	before := retainedRevisionState(t, s)
	for _, tail := range []string{"", "&q=extra", "&playbook-version=2", "&memory-search=x"} {
		w := apiRequest(h, "GET", path+tail, "", token, "http://127.0.0.1:7331", "")
		want := 200
		if tail != "" {
			want = 400
		}
		if w.Code != want {
			t.Fatalf("read: %d %s", w.Code, w.Body.String())
		}
	}
	w := apiRequest(h, "GET", path, "", "", "http://127.0.0.1:7331", "")
	if w.Code != 401 {
		t.Fatal("unauthenticated draft read", w.Code)
	}
	if retainedRevisionState(t, s) != before {
		t.Fatal("GET wrote")
	}
}

func TestAgentPlaybookPausedMigrationAndStorageRefusal(t *testing.T) {
	s := fixtureStore(t)
	p := pauseCommand(t, s, 1)
	if _, err := s.UpdatePause(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("PRAGMA user_version=15"); err != nil {
		t.Fatal(err)
	}
	if err := migratePlaybooks(t.Context(), s.db); err == nil {
		t.Fatal("paused schema migrated")
	}
	var schema int
	s.db.QueryRow("PRAGMA user_version").Scan(&schema)
	if schema != 15 {
		t.Fatal("schema changed while paused")
	}
	s2, v := playbookSetup(t)
	var raw string
	s2.db.QueryRow("SELECT document FROM agent_workspace WHERE id=1").Scan(&raw)
	raw = strings.Replace(raw, `"kind":"fixture-news-digest-v1"`, `"kind":"fixture-news-digest-v1","futureAuthority":true`, 1)
	if _, err := s2.db.Exec("UPDATE agent_workspace SET document=? WHERE id=1", raw); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Agents(t.Context(), v.ProfileID, ""); err == nil {
		t.Fatal("unknown version fields ignored")
	}
}
