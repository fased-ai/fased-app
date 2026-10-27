package core

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestCronReviewedWireJourney(t *testing.T) {
	s := fixtureStore(t)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	v, e := s.Agents(t.Context(), "trading-analyst", "")
	if e != nil {
		t.Fatal(e)
	}
	raw := map[string]any{"action": "task-review", "profileId": v.ProfileID, "revision": v.Revision, "name": "Recurring journal", "text": "Review saved results", "maxRuns": 3, "calendar": map[string]any{"version": "cron-v1", "at": 0, "timeZone": "UTC", "expression": "0 13 * * *", "until": now.Add(7 * 24 * time.Hour).Unix()}}
	b, _ := json.Marshal(raw)
	var c AgentCommand
	json.Unmarshal(b, &c)
	reviewed, e := s.ChangeAgents(t.Context(), c)
	if e != nil {
		t.Fatalf("recurring review rejected: %v", e)
	}
	if reviewed.TaskReview == nil || len(reviewed.Tasks) != 0 {
		t.Fatal("review must not create work")
	}
	c.Action = "task-reviewed-create"
	c.Calendar = reviewed.TaskReview.Calendar
	c.ExpiresAt = reviewed.TaskReview.ExpiresAt
	c.ConsentDigest = reviewed.TaskReview.Digest
	created, e := s.ChangeAgents(t.Context(), c)
	if e != nil {
		t.Fatal(e)
	}
	if len(created.Tasks) != 1 || created.Tasks[0].NextAt != now.Add(time.Hour).Unix() {
		t.Fatal("wrong first occurrence", created.Tasks)
	}
	now = now.Add(3*24*time.Hour + 2*time.Hour)
	if e = s.RunAgentTasks(t.Context()); e != nil {
		t.Fatal(e)
	}
	result, e := s.Agents(t.Context(), v.ProfileID, "")
	if e != nil {
		t.Fatal(e)
	}
	if len(result.Runs) != 1 || result.Tasks[0].Runs != 1 || result.Tasks[0].NextAt != time.Date(2026, 10, 29, 13, 0, 0, 0, time.UTC).Unix() {
		t.Fatal("missed dates must coalesce once", result.Tasks)
	}
	if e = s.RunAgentTasks(t.Context()); e != nil {
		t.Fatal(e)
	}
	result, _ = s.Agents(t.Context(), v.ProfileID, "")
	if len(result.Runs) != 1 {
		t.Fatal("occurrence replayed")
	}
}

func cronInstant(t *testing.T, value string) int64 {
	t.Helper()
	v, e := time.Parse(time.RFC3339, value)
	if e != nil {
		t.Fatal(e)
	}
	return v.Unix()
}
func TestCronParserDSTAndCalendarSemantics(t *testing.T) {
	for _, expr := range []string{"* * * * *", "*/15 9-17 * * 1-5", "0 9 1 * 1", "0 0 * * 7", "1,31 0/6 * 1,6,12 *"} {
		if _, e := parseCron(expr); e != nil {
			t.Fatal(expr, e)
		}
	}
	for _, expr := range []string{"@daily", "0 0 0 * * *", "60 * * * *", "* 24 * * *", "* * 0 * *", "* * * 13 *", "* * * * 8", "*/0 * * * *", "1,,2 * * * *", "+1 * * * *", "1-0 * * * *", "*/61 * * * *"} {
		if _, e := parseCron(expr); e == nil {
			t.Fatal("invalid cron accepted", expr)
		}
	}
	for _, tc := range []struct{ zone, expr, after, next string }{
		{"UTC", "0 9 1 * 1", "2026-10-25T12:00:00Z", "2026-10-26T09:00:00Z"},
		{"UTC", "0 9 1 * 1", "2026-10-31T12:00:00Z", "2026-11-01T09:00:00Z"},
		{"America/New_York", "30 1 * * *", "2026-11-01T05:00:00Z", "2026-11-01T05:30:00Z"},
		{"America/New_York", "30 1 * * *", "2026-11-01T05:31:00Z", "2026-11-02T06:30:00Z"},
		{"America/Chicago", "30 2 * * *", "2027-03-14T00:00:00Z", "2027-03-15T07:30:00Z"},
		{"Australia/Lord_Howe", "45 1 * * *", "2027-04-03T14:46:00Z", "2027-04-04T15:15:00Z"},
	} {
		t.Run(tc.zone+tc.after, func(t *testing.T) {
			after := cronInstant(t, tc.after)
			c := &TaskCalendar{Version: "cron-v1", Expression: tc.expr, TimeZone: tc.zone, Until: after + 3*86400}
			if got := nextCron(c, after); got != cronInstant(t, tc.next) {
				t.Fatalf("got %s want %s", time.Unix(got, 0), tc.next)
			}
		})
	}
	c := &TaskCalendar{Version: "cron-v1", Expression: "0 0 30 2 *", TimeZone: "UTC", Until: cronInstant(t, "2027-10-25T12:00:00Z")}
	if nextCron(c, cronInstant(t, "2026-10-25T12:00:00Z")) != 0 {
		t.Fatal("impossible date acquired a run")
	}
}
func cronCommand(now time.Time) AgentCommand {
	return AgentCommand{Action: "task-review", ProfileID: "trading-analyst", Name: "Cron journal", Text: "Review retained results", MaxRuns: 3, Calendar: &TaskCalendar{Version: "cron-v1", Expression: "0 13 * * *", TimeZone: "UTC", Until: now.Add(7 * 24 * time.Hour).Unix()}}
}
func TestCronPauseRestartManualExpiryAndConcurrentTick(t *testing.T) {
	s := fixtureStore(t)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	v := createCalendar(t, s, cronCommand(now))
	task := v.Tasks[0]
	v = agentApply(t, s, AgentCommand{Action: "task-pause", ProfileID: v.ProfileID, ID: task.ID})
	now = now.Add(2 * 24 * time.Hour)
	if e := s.RunAgentTasks(t.Context()); e != nil {
		t.Fatal(e)
	}
	dir := s.dir
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	n, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer n.Close()
	n.now = func() time.Time { return now }
	v = agentApply(t, n, AgentCommand{Action: "task-resume", ProfileID: v.ProfileID, ID: task.ID})
	if v.Tasks[0].NextAt != task.NextAt {
		t.Fatal("resume shifted reviewed occurrence")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Go(func() { errs <- n.RunAgentTasks(t.Context()) })
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	v, _ = n.Agents(t.Context(), v.ProfileID, "")
	if len(v.Runs) != 1 || v.Tasks[0].NextAt != time.Date(2026, 10, 27, 13, 0, 0, 0, time.UTC).Unix() {
		t.Fatal("restart/concurrent coalescing changed", v.Tasks)
	}
	// An early manual run consumes the next occurrence; it cannot run again there.
	v = agentApply(t, n, AgentCommand{Action: "task-run", ProfileID: v.ProfileID, ID: task.ID})
	next := v.Tasks[0].NextAt
	if next != time.Date(2026, 10, 28, 13, 0, 0, 0, time.UTC).Unix() {
		t.Fatal("manual run did not consume next occurrence")
	}
	now = time.Unix(next, 0)
	if e = n.RunAgentTasks(t.Context()); e != nil {
		t.Fatal(e)
	}
	v, _ = n.Agents(t.Context(), v.ProfileID, "")
	if v.Tasks[0].State != "completed" || v.Tasks[0].NextAt != 0 || len(v.Runs) != 3 {
		t.Fatal("maximum runs not enforced")
	}
	if e = n.RunAgentTasks(t.Context()); e != nil {
		t.Fatal(e)
	}
	expired := cronCommand(now)
	expired.Calendar.Until = now.Add(25 * time.Hour).Unix()
	v = createCalendar(t, n, expired)
	id := v.Tasks[len(v.Tasks)-1].ID
	now = now.Add(26 * time.Hour)
	if e = n.RunAgentTasks(t.Context()); e != nil {
		t.Fatal(e)
	}
	v, _ = n.Agents(t.Context(), v.ProfileID, "")
	for _, task := range v.Tasks {
		if task.ID == id && (task.State != "completed" || task.Runs != 0) {
			t.Fatal("expired work executed", task)
		}
	}
}
func TestCronReviewRefusalsAndSchemaPreservation(t *testing.T) {
	s := fixtureStore(t)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	v, e := s.Agents(t.Context(), "trading-analyst", "")
	if e != nil {
		t.Fatal(e)
	}
	base := cronCommand(now)
	base.Revision = v.Revision
	r, e := s.ChangeAgents(t.Context(), base)
	if e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*AgentCommand){
		func(c *AgentCommand) { c.Calendar.Expression = "* * * * *" }, func(c *AgentCommand) { c.Calendar.TimeZone = "Asia/Seoul" }, func(c *AgentCommand) { c.Calendar.Until++ }, func(c *AgentCommand) { c.MaxRuns++ }, func(c *AgentCommand) { c.IntervalSeconds = 60 }, func(c *AgentCommand) { c.NewsSourceID = "missing" }, func(c *AgentCommand) { c.BotID = "missing" }, func(c *AgentCommand) { c.Calendar.Version = "future" }, func(c *AgentCommand) { c.Calendar.TimeZone = "Local" }, func(c *AgentCommand) { c.Calendar.Until = now.Add(367 * 24 * time.Hour).Unix() },
	} {
		c := base
		copy := *base.Calendar
		c.Calendar = &copy
		c.Action = "task-reviewed-create"
		c.ConsentDigest = r.TaskReview.Digest
		c.ExpiresAt = r.TaskReview.ExpiresAt
		mutate(&c)
		if _, e = s.ChangeAgents(t.Context(), c); e == nil {
			t.Fatal("changed/unbounded cron admitted")
		}
	}
	v = createCalendar(t, s, base)
	before := retainedRevisionState(t, s)
	s.db.Exec("PRAGMA user_version=17")
	if e = migrateTaskCron(t.Context(), s.db); e == nil {
		t.Fatal("unprotected recurring record adopted")
	}
	if retainedRevisionState(t, s) != before {
		t.Fatal("refused migration mutated original")
	}
	s.db.Exec("PRAGMA user_version=18")
	// An actual 17->18 migration is byte-preserving for protected prior records.
	n := fixtureStore(t)
	agentApply(t, n, AgentCommand{Action: "task-create", ProfileID: v.ProfileID, Name: "Old interval", Text: "Preserve this", IntervalSeconds: 300, MaxRuns: 1})
	before = retainedRevisionState(t, n)
	dropEmptyHistoryFixture(t, n)
	n.db.Exec("PRAGMA user_version=17")
	dir := n.dir
	n.Close()
	opened, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer opened.Close()
	if retainedRevisionState(t, opened) != before {
		t.Fatal("migration changed records")
	}
	pause := pauseCommand(t, opened, 1)
	if _, e = opened.UpdatePause(t.Context(), pause); e != nil {
		t.Fatal(e)
	}
	opened.db.Exec("PRAGMA user_version=17")
	if e = migrateTaskCron(t.Context(), opened.db); e == nil {
		t.Fatal("paused migration accepted")
	}
}

func TestCronMigrationRejectsUnprotectedMemoryArchive(t *testing.T) {
	s := fixtureStore(t)
	agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: "trading-analyst", Text: "Preserve this note", Source: "Owner"})
	dropEmptyHistoryFixture(t, s)
	if _, e := s.db.Exec("UPDATE agent_workspace SET document=json_set(document,'$.memories[0].historyOffset',1)"); e != nil {
		t.Fatal(e)
	}
	before := retainedRevisionState(t, s)
	s.db.Exec("PRAGMA user_version=17")
	if e := migrateTaskCron(t.Context(), s.db); e == nil {
		t.Fatal("unprotected memory archive counter adopted")
	}
	if retainedRevisionState(t, s) != before {
		t.Fatal("refused migration changed note or revision")
	}
}
