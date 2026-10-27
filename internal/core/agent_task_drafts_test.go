package core

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestReminderTaskDraftReviewIsInert(t *testing.T) {
	s := fixtureStore(t)
	v := agentRunChatFixture(t, s)
	before, revision, err := readAgents(t.Context(), s.db)
	if err != nil {
		t.Fatal(err)
	}
	v, err = s.ChangeAgents(t.Context(), AgentCommand{
		Action: "task-reminder-review", ProfileID: v.ProfileID, Revision: v.Revision,
		ID: v.Tasks[0].ID, Name: "Reused review", Text: "Check retained research again",
		IntervalSeconds: 120, MaxRuns: 2,
	})
	if err != nil {
		t.Fatal("saved plain reminder cannot become a reviewed draft:", err)
	}
	wire, _ := json.Marshal(v)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(wire, &fields)
	if len(fields["taskReminderReview"]) == 0 {
		t.Fatal("review response missing")
	}
	after, next, err := readAgents(t.Context(), s.db)
	if err != nil || next != revision || !reflect.DeepEqual(before, after) {
		t.Fatal("draft review changed original task, journal or workspace", err)
	}
}

func reminderTaskCommand(v AgentView) AgentCommand {
	return AgentCommand{Action: "task-reminder-review", ProfileID: v.ProfileID, Revision: v.Revision, ID: v.Tasks[0].ID, Name: "Reused reminder", Text: "Review retained results", IntervalSeconds: 120, MaxRuns: 1}
}

func TestReminderTaskDraftCreateRestartAndOriginalCounters(t *testing.T) {
	s := fixtureStore(t)
	now := time.Unix(1800000000, 0)
	s.now = func() time.Time { return now }
	v := agentRunChatFixture(t, s)
	original := v.Tasks[0]
	before, rev, _ := readAgents(t.Context(), s.db)
	draft, err := s.AgentReminderDraft(t.Context(), v.ProfileID, original.ID)
	b, _ := json.Marshal(original)
	if err != nil || draft.Source != original || draft.SourceSHA256 != fmt.Sprintf("%x", sha256.Sum256(b)) || draft.Revision != rev || draft.Name != original.Name || draft.MaxRuns != original.MaxRuns {
		t.Fatal("draft lost source or editable settings", draft, err)
	}
	after, next, _ := readAgents(t.Context(), s.db)
	if next != rev || !reflect.DeepEqual(before, after) {
		t.Fatal("read/abandonment wrote state")
	}
	c := reminderTaskCommand(v)
	reviewed, err := s.ChangeAgents(t.Context(), c)
	if err != nil || reviewed.TaskReminderReview == nil {
		t.Fatal("missing edited review", err)
	}
	c.Action, c.ConsentDigest, c.ExpiresAt = "task-reminder-create", reviewed.TaskReminderReview.Digest, reviewed.TaskReminderReview.ExpiresAt
	for _, edit := range []func(*AgentCommand){
		func(c *AgentCommand) { c.Text = "Changed after review" },
		func(c *AgentCommand) { c.IntervalSeconds++ },
		func(c *AgentCommand) { c.ConsentDigest = "wrong" },
		func(c *AgentCommand) { c.ExpiresAt = now.Unix() },
		func(c *AgentCommand) { c.ExpiresAt += 601 },
	} {
		bad := c
		edit(&bad)
		if _, err = s.ChangeAgents(t.Context(), bad); err == nil {
			t.Fatal("changed/expired review created a task")
		}
	}
	v, err = s.ChangeAgents(t.Context(), c)
	if err != nil || len(v.Tasks) != 2 || v.Tasks[0] != original || len(v.Runs) != 1 || len(v.Notices) != 1 {
		t.Fatal("creation changed original records", err)
	}
	copy := v.Tasks[1]
	if copy.ID == original.ID || copy.Text != c.Text || copy.Runs != 0 || copy.NextAt != now.Unix()+120 || copy.State != "scheduled" || copy.MaxRuns != 1 || copy.AI != nil || copy.News != nil || copy.WEN != nil || copy.BotID != "" {
		t.Fatal("copied execution position or authority", copy)
	}
	if _, err = s.ChangeAgents(t.Context(), c); err == nil {
		t.Fatal("same consent replayed")
	}
	c.Revision = v.Revision
	if _, err = s.ChangeAgents(t.Context(), c); err == nil {
		t.Fatal("old consent replayed with a fresh revision")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	n, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	n.now = func() time.Time { return now }
	v = fixtureProfileProvider(t, n, AgentCommand{Action: "profile-update", ProfileID: v.ProfileID, Name: "Trading Analyst", Provider: "xai"})
	if len(v.Tasks) != 2 || v.Tasks[0] != original || v.Tasks[1] != copy || len(v.Runs) != 1 || len(v.AIRequests) != 0 {
		t.Fatal("restart/provider change ran or modified a task")
	}
	v = agentApply(t, n, AgentCommand{Action: "task-run", ProfileID: v.ProfileID, ID: copy.ID})
	if v.Tasks[0] != original || v.Tasks[1].Runs != 1 || v.Tasks[1].State != "completed" || len(v.Runs) != 2 {
		t.Fatal("new reminder did not retain independent bounds")
	}
	if _, err = n.AgentReminderDraft(t.Context(), "news-researcher", original.ID); err == nil {
		t.Fatal("cross-profile source leaked")
	}
}

func TestReminderTaskDraftSourceAndBindingRefusals(t *testing.T) {
	cases := []struct {
		name string
		doc  func(*agentDocument)
		cmd  func(*AgentCommand)
	}{
		{"missing", nil, func(c *AgentCommand) { c.ID = "missing" }},
		{"foreign", nil, func(c *AgentCommand) { c.ProfileID = "news-researcher" }},
		{"duplicate", func(d *agentDocument) { d.Tasks = append(d.Tasks, d.Tasks[0]) }, nil},
		{"ambiguous-profile", func(d *agentDocument) { d.Profiles = append(d.Profiles, d.Profiles[1]) }, nil},
		{"ai", func(d *agentDocument) { d.Tasks[0].AI = &TaskAIStep{} }, nil},
		{"news", func(d *agentDocument) { d.Tasks[0].News = &TaskNewsInput{} }, nil},
		{"wen", func(d *agentDocument) { d.Tasks[0].WEN = &WENPaperPlan{} }, nil},
		{"bot", func(d *agentDocument) { d.Tasks[0].BotID = "paper" }, nil},
		{"state", func(d *agentDocument) { d.Tasks[0].State = "held" }, nil},
		{"runs", func(d *agentDocument) { d.Tasks[0].Runs++ }, nil},
		{"negative-runs", func(d *agentDocument) { d.Tasks[0].Runs = -1 }, nil},
		{"state-counter", func(d *agentDocument) { d.Tasks[0].State = "scheduled" }, nil},
		{"invalid-text", func(d *agentDocument) { d.Tasks[0].Text = "" }, nil},
		{"invalid-interval", func(d *agentDocument) { d.Tasks[0].IntervalSeconds = 59 }, nil},
		{"attach-bot", nil, func(c *AgentCommand) { c.BotID = "paper" }},
		{"attach-news", nil, func(c *AgentCommand) { c.NewsSourceID = "news" }},
		{"attach-wen", nil, func(c *AgentCommand) { c.WEN = &WENPaperInput{} }},
		{"attach-ai", nil, func(c *AgentCommand) { c.Limits = &AILimits{} }},
		{"attach-connection", nil, func(c *AgentCommand) { c.ConnectionRef = "connection" }},
		{"attach-provider", nil, func(c *AgentCommand) { c.Provider = "openai" }},
		{"attach-evidence", nil, func(c *AgentCommand) { c.EvidenceSelection = &AIEvidenceSelection{} }},
		{"empty-name", nil, func(c *AgentCommand) { c.Name = " " }},
		{"empty-text", nil, func(c *AgentCommand) { c.Text = " " }},
		{"invalid-runs", nil, func(c *AgentCommand) { c.MaxRuns = 21 }},
		{"stale", nil, func(c *AgentCommand) { c.Revision-- }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := fixtureStore(t)
			v := agentRunChatFixture(t, s)
			if tc.doc != nil {
				rewriteReminderFixture(t, s, tc.doc)
				v, _ = s.Agents(t.Context(), v.ProfileID, "")
			}
			c := reminderTaskCommand(v)
			if tc.cmd != nil {
				tc.cmd(&c)
			}
			before, rev, _ := readAgents(t.Context(), s.db)
			if _, err := s.ChangeAgents(t.Context(), c); err == nil {
				t.Fatal("invalid review accepted")
			}
			if tc.doc != nil || tc.name == "foreign" || tc.name == "missing" {
				if _, err := s.AgentReminderDraft(t.Context(), c.ProfileID, c.ID); err == nil {
					t.Fatal("invalid source read accepted")
				}
			}
			after, next, _ := readAgents(t.Context(), s.db)
			if rev != next || !reflect.DeepEqual(before, after) {
				t.Fatal("refusal changed workspace")
			}
		})
	}
}

func TestReminderTaskDraftStaleAndConcurrentCreation(t *testing.T) {
	s := fixtureStore(t)
	v := agentRunChatFixture(t, s)
	c := reminderTaskCommand(v)
	reviewed, err := s.ChangeAgents(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	c.Action, c.ConsentDigest, c.ExpiresAt = "task-reminder-create", reviewed.TaskReminderReview.Digest, reviewed.TaskReminderReview.ExpiresAt
	v = fixtureProfileProvider(t, s, AgentCommand{Action: "profile-update", ProfileID: v.ProfileID, Name: "Trading Analyst", Provider: "xai"})
	if _, err = s.ChangeAgents(t.Context(), c); err == nil {
		t.Fatal("stale preview accepted after provider/source revision change")
	}
	c = reminderTaskCommand(v)
	reviewed, _ = s.ChangeAgents(t.Context(), c)
	c.Action, c.ConsentDigest, c.ExpiresAt = "task-reminder-create", reviewed.TaskReminderReview.Digest, reviewed.TaskReminderReview.ExpiresAt
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() { _, err := s.ChangeAgents(t.Context(), c); results <- err })
	}
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		}
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	if accepted != 1 || len(v.Tasks) != 2 || len(v.Runs) != 1 {
		t.Fatal("concurrent confirmation duplicated creation", accepted)
	}
}

func TestReminderTaskDraftHTTPStrictAndAuthenticated(t *testing.T) {
	s := fixtureStore(t)
	token := claimFixture(t, s)
	v := agentRunChatFixture(t, s)
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	base := "http://127.0.0.1:7331/api/v1/agents?profile=" + v.ProfileID + "&reminder-draft=" + v.Tasks[0].ID
	for _, tc := range []struct {
		query, token string
		status       int
	}{
		{base, "", 401}, {base, token, 200},
		{base + "&ai-draft=id", token, 400}, {base + "&evidence=memory", token, 400},
		{base + "&q=", token, 400}, {base + "&reminder-draft=other", token, 400},
		{base + "&profile=news-researcher", token, 400},
		{"http://127.0.0.1:7331/api/v1/agents?reminder-draft=" + v.Tasks[0].ID, token, 400},
	} {
		r := httptest.NewRequest("GET", tc.query, nil)
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: got %d want %d: %s", tc.query, w.Code, tc.status, w.Body)
		}
	}
	current, _ := s.Agents(t.Context(), v.ProfileID, "")
	if !reflect.DeepEqual(current, v) {
		t.Fatal("HTTP inspection changed records")
	}
}
