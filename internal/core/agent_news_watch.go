package core

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const newsWatchVersion = "news-watch-v1"

// Rule is reviewed policy. Seen is task-owned progress, never a command input.
type NewsWatchRule struct {
	Version  string `json:"version"`
	Contains string `json:"contains"`
}
type TaskNewsWatch struct {
	Rule NewsWatchRule `json:"rule"`
	Seen []string      `json:"seen"`
}
type NewsWatchHeadline struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	PublishedAt int64  `json:"publishedAt"`
}
type NewsWatchResult struct {
	Rule    NewsWatchRule `json:"rule"`
	Matched int           `json:"matched"`
	NewIDs  []string      `json:"newIds"`
}

func validWatchRule(r NewsWatchRule) bool {
	return r.Version == newsWatchVersion && validAgentText(r.Contains, 120, true) && r.Contains == strings.TrimSpace(r.Contains) && !strings.ContainsAny(r.Contains, "\r\n")
}
func watchInput(c AgentCommand, news *TaskNewsInput) error {
	if c.Watch == nil {
		return nil
	}
	if !validWatchRule(*c.Watch) || news == nil || c.Text != "" || c.BotID != "" || c.WEN != nil || c.ConnectionRef != "" || c.Provider != "" || c.Model != "" || c.Limits != nil || c.TotalBudgetMicrousd != 0 || c.AuthorizationSeconds != 0 {
		return errors.New("news watch requires a source and literal headline rule without AI, reminder or financial bindings")
	}
	if c.Action == "task-create" {
		return errors.New("review the news watch before creating it")
	}
	return nil
}
func watchNotice(name string, n int) string {
	return fmt.Sprintf("%s: new matches: %d", name, n)
}
func applyNewsWatch(t *AgentTask, f NewsFetch) *NewsWatchResult {
	out := &NewsWatchResult{Rule: t.Watch.Rule, NewIDs: []string{}}
	seen := map[string]bool{}
	for _, id := range t.Watch.Seen {
		seen[id] = true
	}
	for _, item := range f.Items {
		if !strings.Contains(strings.ToLower(item.Title), strings.ToLower(t.Watch.Rule.Contains)) {
			continue
		}
		out.Matched++
		if !seen[item.ID] {
			out.NewIDs = append(out.NewIDs, item.ID)
			t.Watch.Seen = append(t.Watch.Seen, item.ID)
			seen[item.ID] = true
		}
	}
	sort.Strings(t.Watch.Seen)
	return out
}
func watchStorage(d agentDocument) int {
	n := 0
	for _, t := range d.Tasks {
		if t.Watch != nil && (t.State == "scheduled" || t.State == "paused" || t.State == "waiting-news") {
			n += (t.MaxRuns - t.Runs) * 2048
		}
	}
	return n
}
func watchField(raw json.RawMessage, out any, result bool) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return errors.New("invalid watch fields; preserve records")
	}
	allowed := map[string]bool{"rule": true, "seen": !result, "matched": result, "newIds": result}
	want := 2
	if result {
		want = 3
	}
	if len(fields) != want {
		return errors.New("unsupported watch fields; preserve records")
	}
	for k := range fields {
		if !allowed[k] {
			return errors.New("unsupported watch fields; preserve records")
		}
	}
	var rule map[string]json.RawMessage
	if json.Unmarshal(fields["rule"], &rule) != nil || len(rule) != 2 || rule["version"] == nil || rule["contains"] == nil {
		return errors.New("unsupported watch rule; preserve records")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}
func validateNewsWatchStorage(raw []byte, d agentDocument) error {
	var root struct {
		Tasks []map[string]json.RawMessage
		Runs  []map[string]json.RawMessage
	}
	if json.Unmarshal(raw, &root) != nil {
		return errors.New("invalid watch storage")
	}
	for i, rows := range [][]map[string]json.RawMessage{root.Tasks, root.Runs} {
		for _, row := range rows {
			for key, b := range row {
				if !strings.EqualFold(key, "watch") {
					continue
				}
				if key != "watch" {
					return errors.New("noncanonical watch field; preserve records")
				}
				if i == 0 {
					var w *TaskNewsWatch
					if watchField(b, &w, false) != nil || w == nil {
						return errors.New("invalid watch policy; preserve records")
					}
				} else {
					var w *NewsWatchResult
					if watchField(b, &w, true) != nil || w == nil {
						return errors.New("invalid watch result; preserve records")
					}
				}
			}
		}
	}
	for _, t := range d.Tasks {
		if t.Watch == nil {
			continue
		}
		if !validWatchRule(t.Watch.Rule) || t.News == nil || t.AI != nil || t.BotID != "" || t.WEN != nil || t.Playbook != nil || t.Text != "" || t.Watch.Seen == nil || t.MaxRuns < 1 || t.MaxRuns > 20 || t.Runs < 0 || t.Runs > t.MaxRuns || len(t.Watch.Seen) > t.Runs*5 || !sort.StringsAreSorted(t.Watch.Seen) {
			return errors.New("unsupported watch task; preserve records")
		}
		switch t.State {
		case "scheduled", "paused", "waiting-news", "held", "completed":
		default:
			return errors.New("unsupported watch state")
		}
		union := map[string]bool{}
		for _, run := range d.Runs {
			if run.TaskID != t.ID {
				continue
			}
			if run.Watch == nil {
				if run.Strategy == newsWatchVersion {
					return errors.New("watch result missing")
				}
				continue
			}
			w := run.Watch
			if run.ProfileID != t.ProfileID || run.Strategy != newsWatchVersion || run.AIRequestID != "" || run.AIDigest != "" || run.News == nil || w.Rule != t.Watch.Rule || w.NewIDs == nil || len(w.NewIDs) > 5 || w.Matched < 0 || w.Matched > 5 {
				return errors.New("unsupported watch result; preserve records")
			}
			var f *NewsFetch
			for i := range d.NewsFetches {
				if d.NewsFetches[i].ID == run.News.FetchID {
					if f != nil {
						return errors.New("ambiguous watch snapshot")
					}
					f = &d.NewsFetches[i]
				}
			}
			if f == nil || f.State != "completed" || f.ProfileID != t.ProfileID || f.SourceID != t.News.SourceID || f.URL != t.News.URL || *newsEvidence(*f) != *run.News {
				return errors.New("watch snapshot changed; preserve records")
			}
			matched := map[string]bool{}
			for _, item := range f.Items {
				if _, _, _, err := retainedNewsItem(d, t.ProfileID, f.ID, item.ID, f.FetchedAt); err != nil {
					return errors.New("watch headline changed; preserve records")
				}
				if strings.Contains(strings.ToLower(item.Title), strings.ToLower(w.Rule.Contains)) {
					matched[item.ID] = true
				}
			}
			if len(matched) != w.Matched || (len(w.NewIDs) > 0 && run.Outcome != "matched") || (len(w.NewIDs) == 0 && run.Outcome != "no-new-matches") {
				return errors.New("watch result changed; preserve records")
			}
			expected := []string{}
			for _, item := range f.Items {
				if matched[item.ID] && !union[item.ID] {
					expected = append(expected, item.ID)
				}
			}
			if len(expected) != len(w.NewIDs) {
				return errors.New("watch result omitted or replayed an item")
			}
			for j := range expected {
				if expected[j] != w.NewIDs[j] {
					return errors.New("watch result ordering changed")
				}
			}
			for _, id := range w.NewIDs {
				if !matched[id] || union[id] {
					return errors.New("duplicate/foreign watch result")
				}
				union[id] = true
			}
		}
		if len(union) != len(t.Watch.Seen) {
			return errors.New("watch cursor changed; preserve records")
		}
		for _, id := range t.Watch.Seen {
			if !union[id] {
				return errors.New("watch cursor changed; preserve records")
			}
		}
	}
	for _, run := range d.Runs {
		if run.Watch == nil {
			continue
		}
		found := false
		for _, t := range d.Tasks {
			if t.ID == run.TaskID && t.ProfileID == run.ProfileID && t.Watch != nil {
				found = true
			}
		}
		if !found {
			return errors.New("orphan watch result; preserve records")
		}
	}
	return nil
}

// Only the schema floor changes. Existing documents, revisions and vault bytes
// remain untouched, and unfinished recovery cannot be migrated.
func migrateNewsWatch(ctx context.Context, db *sql.DB) error {
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
		Tasks []map[string]json.RawMessage
		Runs  []map[string]json.RawMessage
	}
	if json.Unmarshal([]byte(raw), &root) != nil {
		return errors.New("invalid original watch state")
	}
	for _, rows := range [][]map[string]json.RawMessage{root.Tasks, root.Runs} {
		for _, row := range rows {
			for k := range row {
				if strings.EqualFold(k, "watch") {
					return errors.New("watch fields without schema protection; preserve records")
				}
			}
		}
	}
	if _, _, err = readAgents(ctx, tx); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "PRAGMA user_version=28"); err != nil {
		return err
	}
	return tx.Commit()
}

func validWatchCalendar(t AgentTask) bool {
	if !validCalendar(t.Calendar) || t.IntervalSeconds != 0 || t.MaxRuns < 1 || t.MaxRuns > 20 || t.Runs < 0 || t.Runs > t.MaxRuns {
		return false
	}
	if t.State == "held" {
		return t.NextAt == 0 && t.Runs > 0
	}
	if cronTask(t) {
		if t.State == "waiting-news" {
			t.State = "scheduled"
		}
		return validCronTask(t)
	}
	if t.MaxRuns != 1 || t.NextAt != t.Calendar.At {
		return false
	}
	return (t.Runs == 0 && (t.State == "scheduled" || t.State == "paused" || t.State == "waiting-news")) || (t.Runs == 1 && t.State == "completed")
}
