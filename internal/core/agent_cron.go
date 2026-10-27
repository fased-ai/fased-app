package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Five numeric cron fields. Day-of-month and day-of-week use conventional OR
// semantics when both are restricted. No seconds, macros or external scheduler.
type cronField struct {
	bits uint64
	wild bool
}
type cronRule [5]cronField

func parseCron(expression string) (cronRule, error) {
	var rule cronRule
	fields := strings.Fields(expression)
	if len(expression) > 120 || len(fields) != 5 {
		return rule, errors.New("use five cron fields: minute hour day month weekday")
	}
	bounds := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	for i, field := range fields {
		for _, char := range field {
			if (char < '0' || char > '9') && char != '*' && char != '/' && char != ',' && char != '-' {
				return rule, errors.New("cron fields must be numeric")
			}
		}
		lo, hi := bounds[i][0], bounds[i][1]
		rule[i].wild = strings.HasPrefix(field, "*")
		for _, item := range strings.Split(field, ",") {
			parts := strings.Split(item, "/")
			if len(parts) > 2 {
				return rule, errors.New("invalid cron step")
			}
			step := 1
			var e error
			if len(parts) == 2 {
				step, e = strconv.Atoi(parts[1])
				if e != nil || step < 1 || step > hi-lo+1 {
					return rule, errors.New("invalid cron step")
				}
			}
			start, end := lo, hi
			if parts[0] != "*" {
				span := strings.Split(parts[0], "-")
				if len(span) > 2 {
					return rule, errors.New("invalid cron range")
				}
				start, e = strconv.Atoi(span[0])
				if e != nil {
					return rule, errors.New("cron fields must be numeric")
				}
				end = start
				if len(span) == 2 {
					end, e = strconv.Atoi(span[1])
					if e != nil {
						return rule, errors.New("invalid cron range")
					}
				} else if len(parts) == 2 {
					end = hi
				}
			}
			if start < lo || end > hi || start > end {
				return rule, errors.New("cron value outside its field range")
			}
			for v := start; v <= end; v += step {
				bit := v
				if i == 4 && v == 7 {
					bit = 0
				}
				rule[i].bits |= uint64(1) << bit
			}
		}
	}
	return rule, nil
}
func (f cronField) has(v int) bool { return f.bits&(uint64(1)<<v) != 0 }
func (r cronRule) matches(t time.Time) bool {
	if !r[0].has(t.Minute()) || !r[1].has(t.Hour()) || !r[3].has(int(t.Month())) {
		return false
	}
	day, week := r[2].has(t.Day()), r[4].has(int(t.Weekday()))
	if !r[2].wild && !r[4].wild {
		return day || week
	}
	return day && week
}

// A backward offset change may repeat a wall-clock minute. Select only its
// first occurrence, including zones with non-hour transitions.
func firstCronWallMinute(t time.Time) bool {
	start, _ := t.ZoneBounds()
	if start.IsZero() {
		return true
	}
	_, offset := t.Zone()
	_, before := start.Add(-time.Second).Zone()
	if before <= offset {
		return true
	}
	previous := t.Add(-time.Duration(before-offset) * time.Second)
	return previous.Year() != t.Year() || previous.YearDay() != t.YearDay() || previous.Hour() != t.Hour() || previous.Minute() != t.Minute()
}
func nextCron(c *TaskCalendar, after int64) int64 {
	if c == nil || c.Until <= after {
		return 0
	}
	rule, e := parseCron(c.Expression)
	if e != nil {
		return 0
	}
	zone, e := time.LoadLocation(c.TimeZone)
	if e != nil {
		return 0
	}
	// At most one year is examined per admitted schedule. Iterating real instants
	// skips missing wall times and avoids ambiguous local-time construction.
	end := c.Until
	if limit := after + 366*86400; end > limit {
		end = limit
	}
	for at := (after/60 + 1) * 60; at <= end; at += 60 {
		local := time.Unix(at, 0).In(zone)
		if rule.matches(local) && firstCronWallMinute(local) {
			return at
		}
	}
	return 0
}
func normalizedCalendar(c *TaskCalendar, now int64) *TaskCalendar {
	if c == nil {
		return nil
	}
	copy := *c
	if copy.Version == "cron-v1" && copy.At == 0 {
		copy.At = nextCron(&copy, now+59)
	}
	return &copy
}
func validCron(c *TaskCalendar) bool {
	if c.At <= 0 || c.At%60 != 0 || c.Until < c.At || c.Until-c.At > 366*86400 {
		return false
	}
	rule, e := parseCron(c.Expression)
	if e != nil {
		return false
	}
	zone, e := time.LoadLocation(c.TimeZone)
	if e != nil {
		return false
	}
	t := time.Unix(c.At, 0).In(zone)
	return rule.matches(t) && firstCronWallMinute(t)
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
