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

// TaskCalendar is an absolute one-time instant. The named zone is display
// metadata, never the server's local timezone or a repeating wall-clock rule.
type TaskCalendar struct {
	Version  string `json:"version"`
	At       int64  `json:"at"`
	TimeZone string `json:"timeZone"`
}

func validCalendar(c *TaskCalendar) bool {
	if c == nil || c.Version != "once-at-v1" || c.At <= 0 || c.TimeZone == "Local" || !validAgentText(c.TimeZone, 120, true) {
		return false
	}
	_, err := time.LoadLocation(c.TimeZone)
	return err == nil
}

func validateCalendarCommand(c AgentCommand, now int64) error {
	if c.Calendar == nil {
		return nil
	}
	if !validCalendar(c.Calendar) || c.Calendar.At < now+60 || c.Calendar.At > now+366*86400 {
		return errors.New("choose a valid timezone and a date 1 minute–366 days ahead")
	}
	if c.IntervalSeconds != 0 || c.MaxRuns != 1 || c.BotID != "" || c.NewsSourceID != "" || c.NewsMaxAgeSeconds != 0 {
		return errors.New("calendar schedules support one plain reminder, without interval, bot or news inputs")
	}
	return nil
}

func validCalendarTask(t AgentTask) bool {
	return validCalendar(t.Calendar) && t.IntervalSeconds == 0 && t.MaxRuns == 1 && t.Runs >= 0 && t.Runs <= 1 && t.NextAt == t.Calendar.At && t.AI == nil && t.News == nil && t.BotID == "" && t.WEN == nil && t.Playbook == nil && validAgentText(t.Text, 4000, true) &&
		(t.State == "scheduled" || t.State == "paused" || t.State == "completed") && ((t.State == "completed") == (t.Runs == 1))
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
