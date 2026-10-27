package core

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	_ "time/tzdata"
)

// TaskCalendar is a versioned reviewed schedule. Once-at keeps its absolute
// instant; cron evaluates named-zone wall time within a fixed bounded horizon.
type TaskCalendar struct {
	Expression string `json:"expression,omitempty"`
	Until      int64  `json:"until,omitempty"`
	Version    string `json:"version"`
	At         int64  `json:"at"`
	TimeZone   string `json:"timeZone"`
}

func validCalendar(c *TaskCalendar) bool {
	if c == nil || c.TimeZone == "Local" || !validAgentText(c.TimeZone, 120, true) {
		return false
	}
	_, err := time.LoadLocation(c.TimeZone)
	if err != nil {
		return false
	}
	if c.Version == "cron-v1" {
		return validCron(c)
	}
	return c.Version == "once-at-v1" && c.At > 0 && c.Expression == "" && c.Until == 0
}

func validateCalendarCommand(c AgentCommand, now int64) error {
	if c.Calendar == nil {
		return nil
	}
	if c.Calendar.Version == "cron-v1" {
		if _, e := parseCron(c.Calendar.Expression); e != nil {
			return e
		}
	}
	calendar := normalizedCalendar(c.Calendar, now)
	if !validCalendar(calendar) || calendar.At < now+60 || calendar.At > now+366*86400 || (calendar.Version == "cron-v1" && calendar.Until > now+366*86400) {
		return errors.New("choose a valid timezone and a date 1 minute–366 days ahead")
	}
	if c.IntervalSeconds != 0 || (calendar.Version == "once-at-v1" && c.MaxRuns != 1) || c.BotID != "" || c.NewsSourceID != "" || c.NewsMaxAgeSeconds != 0 {
		return errors.New("calendar schedules support plain reminders without interval, bot or news inputs")
	}
	return nil
}

func validCalendarTask(t AgentTask) bool {
	policy := (cronTask(t) && validCronTask(t)) || (!cronTask(t) && validCalendar(t.Calendar) && t.MaxRuns == 1 && t.Runs >= 0 && t.Runs <= 1 && t.NextAt == t.Calendar.At && ((t.State == "completed") == (t.Runs == 1)))
	return policy && t.IntervalSeconds == 0 && t.AI == nil && t.News == nil && t.BotID == "" && t.WEN == nil && t.Playbook == nil && validAgentText(t.Text, 4000, true) &&
		(t.State == "scheduled" || t.State == "paused" || t.State == "completed")
}

func validateCalendarTasks(d agentDocument) error {
	for _, t := range d.Tasks {
		if t.Calendar != nil && !validCalendarTask(t) {
			return errors.New("unsupported calendar task; preserve its records")
		}
	}
	return nil
}

func validateCalendarStorage(raw []byte, d agentDocument) error {
	var root struct {
		Tasks []map[string]json.RawMessage `json:"tasks"`
	}
	if json.Unmarshal(raw, &root) != nil {
		return errors.New("invalid calendar document; preserve state")
	}
	for _, t := range root.Tasks {
		for key := range t {
			if strings.EqualFold(key, "calendar") && key != "calendar" {
				return errors.New("noncanonical calendar fields; preserve state")
			}
		}
		if b, ok := t["calendar"]; ok {
			var fields map[string]json.RawMessage
			if json.Unmarshal(b, &fields) != nil {
				return errors.New("invalid calendar fields; preserve state")
			}
			for key := range fields {
				if key != "version" && key != "at" && key != "timeZone" && key != "expression" && key != "until" {
					return errors.New("unsupported calendar fields; preserve state")
				}
			}
			var c *TaskCalendar
			dec := json.NewDecoder(bytes.NewReader(b))
			dec.DisallowUnknownFields()
			if dec.Decode(&c) != nil || c == nil {
				return errors.New("unsupported calendar fields; preserve state")
			}
		}
	}
	return validateCalendarTasks(d)
}

// This format-only migration changes no document, revision, grant or history.
// A schema-16 reader would discard calendar fields and must refuse schema 17.
func migrateTaskCalendar(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	phase, _, err := readPause(ctx, tx)
	if err != nil {
		return err
	}
	if phase == "paused" {
		return ErrCorePaused
	}
	var raw string
	if err = tx.QueryRowContext(ctx, "SELECT document FROM agent_workspace WHERE id=1").Scan(&raw); err != nil {
		return err
	}
	var root struct {
		Tasks []map[string]json.RawMessage `json:"tasks"`
	}
	if json.Unmarshal([]byte(raw), &root) != nil {
		return errors.New("invalid original tasks; preserve state")
	}
	for _, t := range root.Tasks {
		for key := range t {
			if strings.EqualFold(key, "calendar") {
				return errors.New("calendar tasks without schema protection; preserve state")
			}
		}
	}
	if _, err = tx.ExecContext(ctx, "PRAGMA user_version=17"); err != nil {
		return err
	}
	return tx.Commit()
}
