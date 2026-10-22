package core

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestChatReminderReviewDoesNotSchedule(t *testing.T) {
	s := fixtureStore(t)
	v := aiPrepare(t, s)
	v = aiStart(t, s, v)
	if err := s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	card := v.AIRequests[0].Cards[0]
	revision := v.Revision
	v = agentApply(t, s, AgentCommand{Action: "ai-reminder-review", ProfileID: v.ProfileID, ID: card.ID, Name: "Edited reminder", Text: "Review saved research", IntervalSeconds: 60, MaxRuns: 2})
	if v.Revision != revision || len(v.Tasks) != 0 || v.AIRequests[0].Cards[0].Review != "unreviewed" {
		t.Fatal("review scheduled or changed the draft", v)
	}
}

func reminderFixture(t *testing.T, s *Store) AgentView {
	t.Helper()
	v := aiPrepare(t, s)
	v = aiStart(t, s, v)
	if err := s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); err != nil {
		t.Fatal(err)
	}
	v, err := s.Agents(t.Context(), v.ProfileID, "")
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func reminderProposal(v AgentView) AgentCommand {
	return AgentCommand{Action: "ai-reminder-review", ProfileID: v.ProfileID, Revision: v.Revision, ID: v.AIRequests[0].Cards[0].ID, Name: "Owner edited reminder", Text: "Check my saved research", IntervalSeconds: 60, MaxRuns: 2}
}
func reminderCreation(t *testing.T, s *Store, v AgentView) AgentCommand {
	t.Helper()
	c := reminderProposal(v)
	preview, err := s.ChangeAgents(t.Context(), c)
	if err != nil || preview.ReminderReview == nil {
		t.Fatal("missing read-only review", err)
	}
	c.Action = "ai-reminder-create"
	c.ExpiresAt = preview.ReminderReview.ExpiresAt
	c.ConsentDigest = preview.ReminderReview.Digest
	return c
}
func rewriteReminderFixture(t *testing.T, s *Store, change func(*agentDocument)) {
	t.Helper()
	tx, err := s.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	d, rev, err := readAgents(t.Context(), tx)
	if err != nil {
		t.Fatal(err)
	}
	change(&d)
	if err = saveAgents(t.Context(), tx, d, rev); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func TestChatReminderCreateControlsRestartAndSingleUse(t *testing.T) {
	s := fixtureStore(t)
	now := time.Unix(1800000000, 0)
	s.now = func() time.Time { return now }
	v := reminderFixture(t, s)
	c := reminderCreation(t, s, v)
	created, err := s.ChangeAgents(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Tasks) != 1 || len(created.Runs) != 0 || len(created.Notices) != 0 || len(created.AIRequests) != 1 {
		t.Fatal("unexpected creation", created)
	}
	task := created.Tasks[0]
	if task.ID != "chat-reminder:"+c.ID || task.Name != c.Name || task.Text != c.Text || task.NextAt != now.Unix()+60 || task.MaxRuns != 2 || task.State != "scheduled" || task.AI != nil || task.News != nil || task.WEN != nil || task.BotID != "" || created.AIRequests[0].Cards[0].Review != "kept" {
		t.Fatal("not a plain reviewed reminder", created)
	}
	if created.ReminderReview != nil {
		t.Fatal("review was persisted")
	}
	other, _ := s.Agents(t.Context(), "news-researcher", "")
	if len(other.Tasks) != 0 {
		t.Fatal("task leaked")
	}
	v = agentApply(t, s, AgentCommand{Action: "task-pause", ProfileID: c.ProfileID, ID: task.ID})
	now = now.Add(61 * time.Second)
	if err = s.RunAgentTasks(t.Context()); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Agents(t.Context(), c.ProfileID, "")
	if len(v.Runs) != 0 {
		t.Fatal("paused reminder ran")
	}
	agentApply(t, s, AgentCommand{Action: "task-resume", ProfileID: c.ProfileID, ID: task.ID})
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	n, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	n.now = func() time.Time { return now }
	v, _ = n.Agents(t.Context(), c.ProfileID, "")
	c.Revision = v.Revision
	if _, err = n.ChangeAgents(t.Context(), c); err == nil {
		t.Fatal("draft created twice after restart")
	}
	now = now.Add(61 * time.Second)
	if err = n.RunAgentTasks(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = n.RunAgentTasks(t.Context()); err != nil {
		t.Fatal(err)
	}
	v, _ = n.Agents(t.Context(), c.ProfileID, "")
	if len(v.Runs) != 1 || len(v.Notices) != 1 || v.Tasks[0].Runs != 1 || v.Runs[0].Strategy != "reminder-v1" || v.Runs[0].Outcome != "reminded" || len(v.AIRequests) != 1 {
		t.Fatal("reminder replayed or called AI", v)
	}
	v = agentApply(t, n, AgentCommand{Action: "task-run", ProfileID: c.ProfileID, ID: task.ID})
	if v.Tasks[0].State != "completed" || len(v.Runs) != 2 || len(v.Notices) != 2 {
		t.Fatal(v)
	}
	if _, err = n.ChangeAgents(t.Context(), AgentCommand{Action: "task-run", ProfileID: c.ProfileID, ID: task.ID, Revision: v.Revision}); err == nil {
		t.Fatal("completed reminder replayed")
	}
}
func TestChatReminderReviewRefusals(t *testing.T) {
	cases := []struct {
		name string
		doc  func(*agentDocument)
		cmd  func(*AgentCommand)
	}{
		{"foreign-profile", nil, func(c *AgentCommand) { c.ProfileID = "news-researcher" }},
		{"missing-card", nil, func(c *AgentCommand) { c.ID = "missing" }},
		{"dismissed", func(d *agentDocument) { d.AIRequests[0].Cards[0].Review = "dismissed" }, nil},
		{"not-completed", func(d *agentDocument) { d.AIRequests[0].State = "cancelled" }, nil},
		{"scheduled-AI-source", func(d *agentDocument) { d.AIRequests[0].TaskID = "scheduled-source" }, nil},
		{"strategy-card", func(d *agentDocument) { d.AIRequests[0].Cards[0].Kind = "paper-strategy-draft" }, nil},
		{"duplicate-card", func(d *agentDocument) {
			d.AIRequests[0].Cards = append(d.AIRequests[0].Cards, d.AIRequests[0].Cards[0])
		}, nil},
		{"missing-session", func(d *agentDocument) { d.Sessions = nil }, nil},
		{"task-capacity", func(d *agentDocument) {
			for i := 0; i < 100; i++ {
				d.Tasks = append(d.Tasks, AgentTask{ID: fmt.Sprint(i), MaxRuns: 1})
			}
		}, nil},
		{"evaluation-capacity", func(d *agentDocument) {
			for i := 0; i < 10; i++ {
				d.Tasks = append(d.Tasks, AgentTask{ID: fmt.Sprint(i), MaxRuns: 20})
			}
		}, nil},
		{"interval", nil, func(c *AgentCommand) { c.IntervalSeconds = 59 }},
		{"runs", nil, func(c *AgentCommand) { c.MaxRuns = 21 }},
		{"text", nil, func(c *AgentCommand) { c.Text = "" }},
		{"bot-binding", nil, func(c *AgentCommand) { c.BotID = "bot" }},
		{"AI-binding", nil, func(c *AgentCommand) { c.Limits = &AILimits{} }},
		{"news-binding", nil, func(c *AgentCommand) { c.NewsSourceID = "news" }},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s := fixtureStore(t)
			v := reminderFixture(t, s)
			c := reminderProposal(v)
			if tt.doc != nil {
				rewriteReminderFixture(t, s, tt.doc)
				v, _ = s.Agents(t.Context(), v.ProfileID, "")
				c.Revision = v.Revision
			}
			if tt.cmd != nil {
				tt.cmd(&c)
			}
			before, revision, _ := readAgents(t.Context(), s.db)
			if _, err := s.ChangeAgents(t.Context(), c); err == nil {
				t.Fatal("unsafe reminder review accepted")
			}
			after, next, _ := readAgents(t.Context(), s.db)
			if revision != next || !reflect.DeepEqual(before, after) {
				t.Fatal("refusal changed workspace")
			}
		})
	}
}
func TestChatReminderConsentExpiryPauseAndConcurrentCreation(t *testing.T) {
	for _, mode := range []string{"missing-consent", "changed-text", "changed-interval", "changed-runs", "expired", "stale", "paused", "concurrent"} {
		t.Run(mode, func(t *testing.T) {
			s := fixtureStore(t)
			now := time.Unix(1800000000, 0)
			s.now = func() time.Time { return now }
			v := reminderFixture(t, s)
			c := reminderCreation(t, s, v)
			switch mode {
			case "missing-consent":
				c.ConsentDigest = ""
			case "changed-text":
				c.Text = "different"
			case "changed-interval":
				c.IntervalSeconds = 120
			case "changed-runs":
				c.MaxRuns = 3
			case "expired":
				now = now.Add(600 * time.Second)
			case "stale":
				agentApply(t, s, AgentCommand{Action: "memory-put", Text: "unrelated change", Source: "owner"})
			case "paused":
				if _, err := s.db.Exec("UPDATE update_pause SET phase='paused'"); err != nil {
					t.Fatal(err)
				}
			}
			before, revision, _ := readAgents(t.Context(), s.db)
			if mode == "concurrent" {
				results := make(chan error, 2)
				start := make(chan struct{})
				for i := 0; i < 2; i++ {
					go func() { <-start; _, err := s.ChangeAgents(t.Context(), c); results <- err }()
				}
				close(start)
				successes := 0
				for i := 0; i < 2; i++ {
					if <-results == nil {
						successes++
					}
				}
				after, _, _ := readAgents(t.Context(), s.db)
				if successes != 1 || len(after.Tasks) != 1 {
					t.Fatal("creation not atomic", successes, after.Tasks)
				}
				return
			}
			if _, err := s.ChangeAgents(t.Context(), c); err == nil {
				t.Fatal("unreviewed creation accepted")
			}
			after, next, _ := readAgents(t.Context(), s.db)
			if revision != next || !reflect.DeepEqual(before, after) {
				t.Fatal("failed creation partially committed")
			}
		})
	}
}
