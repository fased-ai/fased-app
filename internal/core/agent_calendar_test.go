package core

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// The wire test fails on the interval-only reader instead of requiring the new
// Go type, so it exercises the actual shared command contract.
func TestCalendarReviewedRestartExactlyOnce(t *testing.T) {
	s := fixtureStore(t)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	v, err := s.Agents(t.Context(), "trading-analyst", "")
	if err != nil {
		t.Fatal(err)
	}
	raw := map[string]any{"action": "task-review", "profileId": v.ProfileID, "revision": v.Revision, "name": "Calendar journal", "text": "Review saved fixture results", "maxRuns": 1, "calendar": map[string]any{"version": "once-at-v1", "at": now.Unix() + 3600, "timeZone": "America/Chicago"}}
	b, _ := json.Marshal(raw)
	var c AgentCommand
	if err = json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	reviewed, err := s.ChangeAgents(t.Context(), c)
	if err != nil {
		t.Fatal("calendar review not supported:", err)
	}
	after, _ := s.Agents(t.Context(), v.ProfileID, "")
	if !reflect.DeepEqual(v, after) {
		t.Fatal("review wrote records")
	}
	c.Action, c.ConsentDigest, c.ExpiresAt = "task-reviewed-create", reviewed.TaskReview.Digest, reviewed.TaskReview.ExpiresAt
	v, err = s.ChangeAgents(t.Context(), c)
	if err != nil || len(v.Tasks) != 1 || len(v.Runs) != 0 || v.Tasks[0].NextAt != now.Unix()+3600 {
		t.Fatal("create", v, err)
	}
	if err = s.RunAgentTasks(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now = now.Add(3 * time.Hour)
	s.now = func() time.Time { return now }
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Go(func() { errs <- s.RunAgentTasks(t.Context()) })
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	v, err = s.Agents(t.Context(), v.ProfileID, "")
	if err != nil || len(v.Runs) != 1 || len(v.Notices) != 1 || v.Tasks[0].Runs != 1 || v.Tasks[0].State != "completed" {
		t.Fatal("one durable run", v, err)
	}
	before := retainedRevisionState(t, s)
	if err = s.RunAgentTasks(t.Context()); err != nil {
		t.Fatal(err)
	}
	if retainedRevisionState(t, s) != before {
		t.Fatal("replayed completed calendar task")
	}
}

func calendarCommand(now time.Time) AgentCommand {
	return AgentCommand{Action: "task-review", ProfileID: "trading-analyst", Name: "Calendar reminder", Text: "Review the fixture journal", MaxRuns: 1, Calendar: &TaskCalendar{Version: "once-at-v1", At: now.Unix() + 3600, TimeZone: "America/Chicago"}}
}
func createCalendar(t *testing.T, s *Store, c AgentCommand) AgentView {
	t.Helper()
	v := agentApply(t, s, c)
	c.Action, c.ConsentDigest, c.ExpiresAt = "task-reviewed-create", v.TaskReview.Digest, v.TaskReview.ExpiresAt
	return agentApply(t, s, c)
}
func TestCalendarReviewBindingsAndAdmission(t *testing.T) {
	s := fixtureStore(t)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	c := calendarCommand(now)
	v := agentApply(t, s, c)
	c.Action, c.Revision, c.ConsentDigest, c.ExpiresAt = "task-reviewed-create", v.Revision, v.TaskReview.Digest, v.TaskReview.ExpiresAt
	before := retainedRevisionState(t, s)
	for _, alter := range []func(*AgentCommand){
		func(c *AgentCommand) { c.Calendar.At++ },
		func(c *AgentCommand) { c.Calendar.TimeZone = "UTC" },
		func(c *AgentCommand) { c.Calendar.Version = "future" },
		func(c *AgentCommand) { c.Calendar.TimeZone = "Local" },
		func(c *AgentCommand) { c.Calendar.TimeZone = "missing/zone" },
		func(c *AgentCommand) { c.Calendar.At = now.Unix() + 59 },
		func(c *AgentCommand) { c.Calendar.At = now.Unix() + 366*86400 + 1 },
		func(c *AgentCommand) { c.IntervalSeconds = 60 },
		func(c *AgentCommand) { c.MaxRuns = 2 },
		func(c *AgentCommand) { c.ProfileID = "news-researcher" },
		func(c *AgentCommand) { c.BotID = "paper" },
		func(c *AgentCommand) { c.NewsSourceID = "news" },
		func(c *AgentCommand) { c.Limits = &AILimits{} },
		func(c *AgentCommand) { c.Action = "task-create" },
		func(c *AgentCommand) { c.Action = "ai-prepare" },
		func(c *AgentCommand) { c.Action = "session-create" },
		func(c *AgentCommand) { c.ExpiresAt = now.Unix() },
	} {
		bad := c
		copy := *c.Calendar
		bad.Calendar = &copy
		alter(&bad)
		if _, err := s.ChangeAgents(t.Context(), bad); err == nil {
			t.Fatal("accepted changed/unsupported calendar policy", bad)
		}
		if retainedRevisionState(t, s) != before {
			t.Fatal("rejection wrote records")
		}
	}
	v, err := s.ChangeAgents(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	if v.Tasks[0].Calendar.At != c.Calendar.At || v.Tasks[0].Calendar.TimeZone != c.Calendar.TimeZone {
		t.Fatal("reviewed schedule lost")
	}
	if _, err = s.ChangeAgents(t.Context(), c); err == nil {
		t.Fatal("replayed review")
	}
	foreign, _ := s.Agents(t.Context(), "news-researcher", "")
	if len(foreign.Tasks) != 0 {
		t.Fatal("profile leak")
	}
}

func TestCalendarPauseResumeKeepsInstantAndManualRunCompletes(t *testing.T) {
	s := fixtureStore(t)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	c := calendarCommand(now)
	v := createCalendar(t, s, c)
	id := v.Tasks[0].ID
	v = agentApply(t, s, AgentCommand{Action: "task-pause", ProfileID: v.ProfileID, ID: id})
	now = now.Add(2 * time.Hour)
	if err := s.RunAgentTasks(t.Context()); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	if len(v.Runs) != 0 || v.Tasks[0].NextAt != c.Calendar.At {
		t.Fatal("paused calendar ran or moved")
	}
	v = agentApply(t, s, AgentCommand{Action: "task-resume", ProfileID: v.ProfileID, ID: id})
	if v.Tasks[0].NextAt != c.Calendar.At {
		t.Fatal("resume moved calendar date")
	}
	if err := s.RunAgentTasks(t.Context()); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	if len(v.Runs) != 1 || v.Tasks[0].State != "completed" {
		t.Fatal("overdue reminder not completed")
	}
	early := createCalendar(t, s, calendarCommand(now))
	id = early.Tasks[1].ID
	v = agentApply(t, s, AgentCommand{Action: "task-run", ProfileID: v.ProfileID, ID: id})
	if v.Tasks[1].State != "completed" || v.Tasks[1].NextAt != now.Unix()+3600 {
		t.Fatal("early run changed date or left task pending")
	}
	v = agentApply(t, s, AgentCommand{Action: "task-run-explain", ProfileID: v.ProfileID, ID: v.Runs[1].ID})
	if len(v.Messages) != 1 || !strings.Contains(v.Messages[0].Text, "once-at-v1") {
		t.Fatal("calendar missing in discuss-run source")
	}
	now = now.Add(48 * time.Hour)
	if err := s.RunAgentTasks(t.Context()); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	if len(v.Runs) != 2 {
		t.Fatal("manual completion replayed on its date")
	}
}

func TestCalendarMigrationPreservesOriginalsAndRefusesUnprotectedFields(t *testing.T) {
	s := fixtureStore(t)
	agentApply(t, s, AgentCommand{Action: "task-create", ProfileID: "trading-analyst", Name: "Old interval", Text: "Retain this schedule", IntervalSeconds: 300, MaxRuns: 2})
	before := retainedRevisionState(t, s)
	dropEmptyHistoryFixture(t, s)
	if _, err := s.db.Exec("PRAGMA user_version=16"); err != nil {
		t.Fatal(err)
	}
	dir := s.dir
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	n, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	if retainedRevisionState(t, n) != before {
		t.Fatal("migration rewrote original records/revision")
	}
	var schema int
	if err = n.db.QueryRow("PRAGMA user_version").Scan(&schema); err != nil || schema != 18 || StateSchema != 18 {
		t.Fatal("schema protection missing", schema, err)
	}
	b, _ := json.Marshal(UpdateContract())
	if !strings.Contains(string(b), "agents-calendar-v1") {
		t.Fatal("feature protection missing")
	}
	now := time.Now()
	n.now = func() time.Time { return now }
	createCalendar(t, n, calendarCommand(now))
	if _, err = n.db.Exec("PRAGMA user_version=16"); err != nil {
		t.Fatal(err)
	}
	original := retainedRevisionState(t, n)
	if err = migrateTaskCalendar(t.Context(), n.db); err == nil {
		t.Fatal("unprotected calendar adopted")
	}
	if retainedRevisionState(t, n) != original {
		t.Fatal("failed migration changed originals")
	}
	n.db.QueryRow("PRAGMA user_version").Scan(&schema)
	if schema != 16 {
		t.Fatal("failed migration advanced schema")
	}
}

func TestCalendarPausedMigrationAndUnknownStorageRefused(t *testing.T) {
	s := fixtureStore(t)
	p := pauseCommand(t, s, 1)
	if _, err := s.UpdatePause(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	s.db.Exec("PRAGMA user_version=16")
	if err := migrateTaskCalendar(t.Context(), s.db); err == nil {
		t.Fatal("paused migration accepted")
	}
	var schema int
	s.db.QueryRow("PRAGMA user_version").Scan(&schema)
	if schema != 16 {
		t.Fatal("paused schema changed")
	}
	n := fixtureStore(t)
	now := time.Now()
	n.now = func() time.Time { return now }
	createCalendar(t, n, calendarCommand(now))
	var raw string
	n.db.QueryRow("SELECT document FROM agent_workspace WHERE id=1").Scan(&raw)
	for _, mutate := range []func(string) string{
		func(s string) string {
			return strings.Replace(s, `"version":"once-at-v1"`, `"version":"once-at-v1","futureAuthority":true`, 1)
		},
		func(s string) string { return strings.Replace(s, `"version":"once-at-v1"`, `"version":"future"`, 1) },
		func(s string) string { return strings.Replace(s, `"intervalSeconds":0`, `"intervalSeconds":60`, 1) },
		func(s string) string { return strings.Replace(s, `"calendar":`, `"Calendar":`, 1) },
	} {
		bad := mutate(raw)
		if bad == raw {
			t.Fatal("fixture not mutated")
		}
		if _, err := n.db.Exec("UPDATE agent_workspace SET document=? WHERE id=1", bad); err != nil {
			t.Fatal(err)
		}
		if _, err := n.Agents(t.Context(), "trading-analyst", ""); err == nil {
			t.Fatal("unknown/inconsistent calendar discarded")
		}
	}
}
