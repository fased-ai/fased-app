package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/fased-ai/fased-app/internal/scheduler"
)

// Five numeric cron fields. Day-of-month and day-of-week use conventional OR
// semantics when both are restricted. No seconds, macros or external scheduler.
type cronRule = scheduler.CronRule

func parseCron(expression string) (cronRule, error) {
	return scheduler.ParseCron(expression)
}
func firstCronWallMinute(t time.Time) bool {
	return scheduler.FirstCronWallMinute(t)
}
func nextCron(c *TaskCalendar, after int64) int64 {
	return scheduler.NextCron(c, after)
}
func normalizedCalendar(c *TaskCalendar, now int64) *TaskCalendar {
	return scheduler.NormalizedCalendar(c, now)
}
func validCron(c *TaskCalendar) bool {
	return scheduler.ValidCron(c)
}
func cronTask(t AgentTask) bool { return t.Calendar != nil && t.Calendar.Version == "cron-v1" }
func validCronTask(t AgentTask) bool {
	if !validCalendar(t.Calendar) || t.MaxRuns < 1 || t.MaxRuns > 20 || t.Runs < 0 || t.Runs > t.MaxRuns {
		return false
	}
	if t.State == "completed" {
		return t.NextAt == 0
	}
	if (t.State != "scheduled" && t.State != "paused") || t.Runs >= t.MaxRuns || t.NextAt < t.Calendar.At || t.NextAt > t.Calendar.Until {
		return false
	}
	copy := *t.Calendar
	copy.At = t.NextAt
	return validCron(&copy) && (t.Runs != 0 || t.NextAt == t.Calendar.At)
}
func advanceCron(t *AgentTask, now int64) {
	if t.Runs >= t.MaxRuns {
		t.State = "completed"
		t.NextAt = 0
		return
	}
	after := now
	if t.NextAt > after {
		after = t.NextAt
	} // Run now consumes the upcoming occurrence.
	t.NextAt = nextCron(t.Calendar, after)
	if t.NextAt == 0 {
		t.State = "completed"
	}
}

// Format-only: keep every document, revision and consent byte unchanged. A
// schema-17 reader must refuse the new recurring policy instead of erasing it.
func migrateTaskCron(ctx context.Context, db *sql.DB) error {
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	phase, _, e := readPause(ctx, tx)
	if e != nil {
		return e
	}
	if phase == "paused" {
		return ErrCorePaused
	}
	var raw string
	if e = tx.QueryRowContext(ctx, "SELECT document FROM agent_workspace WHERE id=1").Scan(&raw); e != nil {
		return e
	}
	var d agentDocument
	if json.Unmarshal([]byte(raw), &d) != nil {
		return errors.New("invalid prior task records; preserve state")
	}
	if e = validateCalendarStorage([]byte(raw), d); e != nil {
		return e
	}
	for _, t := range d.Tasks {
		if t.Calendar != nil && t.Calendar.Version != "once-at-v1" {
			return errors.New("recurring policy lacks schema protection; preserve state")
		}
	}
	if len(d.ArchivedAISpentMicrousd) != 0 {
		return errors.New("archived journals lack schema protection; preserve records")
	}
	for _, m := range d.Memories {
		if m.HistoryOffset != 0 {
			return errors.New("archived memory lacks schema protection; preserve records")
		}
	}
	for _, p := range d.Profiles {
		if p.HistoryDays != 0 {
			return errors.New("history policy lacks schema protection; preserve records")
		}
	}
	if _, e = tx.ExecContext(ctx, taskHistorySQL); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "PRAGMA user_version=18"); e != nil {
		return e
	}
	return tx.Commit()
}
