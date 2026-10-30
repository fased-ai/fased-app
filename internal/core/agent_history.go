package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/fased-ai/fased-app/internal/update"
)

const taskHistorySQL = `CREATE TABLE agent_history(id INTEGER PRIMARY KEY AUTOINCREMENT,profile TEXT NOT NULL,source_id TEXT NOT NULL,kind TEXT NOT NULL,search_text TEXT NOT NULL,record TEXT NOT NULL,sha256 TEXT NOT NULL,archived_at INTEGER NOT NULL,UNIQUE(profile,kind,source_id));
CREATE TRIGGER task_history_pause BEFORE INSERT ON agent_history WHEN (SELECT phase FROM update_pause WHERE id=1)='paused' BEGIN SELECT RAISE(ABORT,'core metadata paused for update'); END;
CREATE TRIGGER task_history_immutable_update BEFORE UPDATE ON agent_history BEGIN SELECT RAISE(ABORT,'task history is immutable'); END;
CREATE TRIGGER task_history_immutable_delete BEFORE DELETE ON agent_history BEGIN SELECT RAISE(ABORT,'task history is immutable'); END;`

type TaskHistoryEntry struct {
	Memory     *MemoryHistoryRecord `json:"memory,omitempty"`
	Kind       string               `json:"kind"`
	Session    *AgentSession        `json:"session,omitempty"`
	Messages   []AgentMessage       `json:"messages,omitempty"`
	AIRequests []AgentAIRequest     `json:"aiRequests,omitempty"`
	Version    string               `json:"version"`
	Task       AgentTask            `json:"task"`
	Runs       []AgentRun           `json:"runs"`
	Notices    []AgentNotice        `json:"notices"`
}
type TaskHistorySummary struct {
	ID         int64  `json:"id"`
	TaskID     string `json:"taskId"`
	Kind       string `json:"kind"`
	Messages   int    `json:"messages"`
	Name       string `json:"name"`
	Runs       int    `json:"runs"`
	ArchivedAt int64  `json:"archivedAt"`
	SHA256     string `json:"sha256"`
}
type TaskHistoryView struct {
	ProfileID  string               `json:"profileId"`
	Records    []TaskHistorySummary `json:"records"`
	NextBefore int64                `json:"nextBefore"`
	Entry      *TaskHistoryEntry    `json:"entry,omitempty"`
	SHA256     string               `json:"sha256,omitempty"`
}

func validHistoryDays(days int) bool { return days == 0 || days == 7 || days == 30 || days == 90 }
func archiveEligible(t AgentTask) bool {
	return t.State == "completed" && t.MaxRuns >= 1 && t.MaxRuns <= 20 && t.Runs >= 0 && t.Runs <= t.MaxRuns && len(t.ID) == 43 && !strings.Contains(t.ID, ":") && t.Swap == nil && t.StrategyPlan == nil && t.AI == nil && t.News == nil && t.WEN == nil && t.Playbook == nil && t.BotID == ""
}
func splitTaskHistory(d agentDocument, profile, id string) (agentDocument, TaskHistoryEntry, error) {
	out := d
	out.Tasks = nil
	out.Runs = nil
	out.Notices = nil
	entry := TaskHistoryEntry{Version: "task-history-v1", Kind: "tasks", Runs: []AgentRun{}, Notices: []AgentNotice{}}
	for _, task := range d.Tasks {
		if task.ID == id && task.ProfileID == profile {
			entry.Task = task
		} else {
			out.Tasks = append(out.Tasks, task)
		}
	}
	if entry.Task.ID == "" || !archiveEligible(entry.Task) {
		return d, entry, errors.New("only completed plain reminders can be archived")
	}
	ids := map[string]bool{id: true}
	for _, run := range d.Runs {
		if run.TaskID == id && run.ProfileID == profile {
			entry.Runs = append(entry.Runs, run)
			ids[run.ID] = true
		} else {
			out.Runs = append(out.Runs, run)
		}
	}
	for _, notice := range d.Notices {
		if notice.ProfileID == profile && ids[notice.RunID] && notice.RunID != id {
			entry.Notices = append(entry.Notices, notice)
		} else {
			out.Notices = append(out.Notices, notice)
		}
	}
	if len(entry.Runs) != entry.Task.Runs || len(entry.Notices) != entry.Task.Runs {
		return d, entry, errors.New("task history is incomplete; preserve its active records")
	}
	// Conservative dependency guard covers retained model/research/source snapshots
	// without interpreting untrusted text or moving financial/unknown holds.
	raw, e := json.Marshal(out)
	if e != nil {
		return d, entry, e
	}
	for id := range ids {
		if bytes.Contains(raw, []byte(id)) {
			return d, entry, errors.New("saved evidence references this task; keep its active records")
		}
	}
	return out, entry, nil
}
func archiveTask(ctx context.Context, tx *sql.Tx, d *agentDocument, profile, id string, now int64) error {
	next, entry, e := splitTaskHistory(*d, profile, id)
	if e != nil {
		return e
	}
	raw, e := json.Marshal(entry)
	if e != nil {
		return e
	}
	if len(raw) > agentDocumentLimit {
		return errors.New("task history exceeds the protected record limit")
	}
	digest := sha256.Sum256(raw)
	if _, e = tx.ExecContext(ctx, "INSERT INTO agent_history(profile,source_id,kind,search_text,record,sha256,archived_at) VALUES(?,?,?,?,?,?,?)", profile, id, "tasks", strings.ToLower(entry.Task.Name+" "+entry.Task.Text), string(raw), hex.EncodeToString(digest[:]), now); e != nil {
		return e
	}
	*d = next
	return nil
}
func archiveOldTasks(ctx context.Context, tx *sql.Tx, d *agentDocument, now int64) (bool, error) {
	changed := false
	for i := len(d.Tasks) - 1; i >= 0; i-- {
		task := d.Tasks[i]
		pi := agentProfile(*d, task.ProfileID)
		if pi < 0 || d.Profiles[pi].HistoryDays == 0 || !archiveEligible(task) {
			continue
		}
		last := int64(0)
		read := true
		for _, run := range d.Runs {
			if run.TaskID == task.ID && run.ProfileID == task.ProfileID && run.At > last {
				last = run.At
			}
		}
		if last == 0 && cronTask(task) {
			last = task.Calendar.Until
		}
		for _, notice := range d.Notices {
			if notice.ProfileID == task.ProfileID && strings.HasPrefix(notice.RunID, task.ID+":") && !notice.Read {
				read = false
			}
		}
		if !read || last == 0 || last > now-int64(d.Profiles[pi].HistoryDays)*86400 {
			continue
		}
		if _, _, e := splitTaskHistory(*d, task.ProfileID, task.ID); e != nil {
			continue
		}
		if e := archiveTask(ctx, tx, d, task.ProfileID, task.ID, now); e != nil {
			return false, e
		}
		changed = true
	}
	return changed, nil
}
func parseHistory(raw, hash, profile, id string) (TaskHistoryEntry, error) {
	var entry TaskHistoryEntry
	digest := sha256.Sum256([]byte(raw))
	if len(raw) > agentDocumentLimit || hex.EncodeToString(digest[:]) != hash || update.StrictJSON([]byte(raw), &entry) != nil || entry.Memory != nil || entry.Session != nil || len(entry.Messages) != 0 || len(entry.AIRequests) != 0 || entry.Kind != "tasks" || entry.Version != "task-history-v1" || entry.Task.ProfileID != profile || entry.Task.ID != id || !archiveEligible(entry.Task) || len(entry.Runs) != entry.Task.Runs || len(entry.Notices) != entry.Task.Runs {
		return entry, errors.New("archived history verification failed; preserve records")
	}
	for i, run := range entry.Runs {
		if run.TaskID != id || run.ProfileID != profile || run.ID != fmt.Sprintf("%s:%d", id, i+1) || run.Paper != nil || run.AIRequestID != "" || run.News != nil || run.WEN != nil || run.BotID != "" {
			return entry, errors.New("archived run identity is inconsistent")
		}
	}
	for i, notice := range entry.Notices {
		if notice.ProfileID != profile || notice.RunID != entry.Runs[i].ID {
			return entry, errors.New("archived notice identity is inconsistent")
		}
	}
	return entry, nil
}
func (s *Store) AgentTaskHistory(ctx context.Context, profile string, before int64, id string, options ...string) (TaskHistoryView, error) {
	v := TaskHistoryView{ProfileID: profile, Records: []TaskHistorySummary{}}
	kind, query := "tasks", ""
	if len(options) > 0 {
		kind = options[0]
	}
	if len(options) > 1 {
		query = options[1]
	}
	if (kind != "tasks" && kind != "conversations" && kind != "memories") || !validAgentText(query, 200, false) {
		return v, errors.New("select task or conversation history")
	}
	if !validAgentText(profile, 80, true) || before < 0 || (id != "" && (before != 0 || !validAgentText(id, 200, true))) {
		return v, errors.New("select a profile and history page or one task")
	}
	tx, e := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return v, e
	}
	defer tx.Rollback()
	d, _, e := readAgents(ctx, tx)
	if e != nil {
		return v, e
	}
	if agentProfile(d, profile) < 0 {
		return v, errors.New("profile not found")
	}
	if id != "" {
		var raw, hash string
		if e = tx.QueryRowContext(ctx, "SELECT record,sha256 FROM agent_history WHERE profile=? AND source_id=? AND kind=?", profile, id, kind).Scan(&raw, &hash); e != nil {
			return v, errors.New("profile history task not found")
		}
		entry, e := parseArchivedEntry(raw, hash, profile, id, kind)
		if e != nil {
			return v, e
		}
		v.Entry = &entry
		v.SHA256 = hash
		return v, tx.Commit()
	}
	rows, e := tx.QueryContext(ctx, "SELECT id,source_id,COALESCE(json_extract(record,'$.session.name'),substr(json_extract(record,'$.memory.revision.text'),1,120),json_extract(record,'$.task.name')),COALESCE(json_array_length(json_extract(record,'$.runs')),0),archived_at,sha256,kind,COALESCE(json_array_length(json_extract(record,'$.messages')),0)+2*COALESCE(json_array_length(json_extract(record,'$.aiRequests')),0) FROM agent_history WHERE profile=? AND kind=? AND (?=0 OR id<?) AND instr(search_text,?)>0 ORDER BY id DESC LIMIT 21", profile, kind, before, before, strings.ToLower(strings.TrimSpace(query)))
	if e != nil {
		return v, e
	}
	for rows.Next() {
		var row TaskHistorySummary
		if e = rows.Scan(&row.ID, &row.TaskID, &row.Name, &row.Runs, &row.ArchivedAt, &row.SHA256, &row.Kind, &row.Messages); e != nil {
			rows.Close()
			return v, e
		}
		v.Records = append(v.Records, row)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return v, e
	}
	if len(v.Records) > 20 {
		v.Records = v.Records[:20]
		v.NextBefore = v.Records[19].ID
	}
	return v, tx.Commit()
}
func historySnapshot(ctx context.Context, tx snapshotReader) (string, error) {
	rows, e := tx.QueryContext(ctx, "SELECT id,profile,source_id,record,sha256,archived_at,kind,search_text FROM agent_history ORDER BY id")
	if e != nil {
		return "", e
	}
	defer rows.Close()
	h := sha256.New()
	enc := json.NewEncoder(h)
	for rows.Next() {
		var id, at int64
		var profile, task, raw, hash, kind, search string
		if e = rows.Scan(&id, &profile, &task, &raw, &hash, &at, &kind, &search); e != nil {
			return "", e
		}
		if _, e = parseArchivedEntry(raw, hash, profile, task, kind); e != nil {
			return "", e
		}
		if e = enc.Encode([]any{id, profile, task, hash, at, kind, search}); e != nil {
			return "", e
		}
	}
	if e = rows.Err(); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func finalManualAI(q AgentAIRequest) bool {
	return (q.State == "completed" || q.State == "cancelled" || q.State == "failed") && !aiHasUsageHold(q) && q.TaskID == "" && q.ResearchTurnID == ""
}
func parseArchivedEntry(raw, hash, profile, id, kind string) (TaskHistoryEntry, error) {
	if kind == "tasks" {
		return parseHistory(raw, hash, profile, id)
	}
	if kind == "memories" {
		var entry TaskHistoryEntry
		digest := sha256.Sum256([]byte(raw))
		if len(raw) > agentDocumentLimit || hex.EncodeToString(digest[:]) != hash || update.StrictJSON([]byte(raw), &entry) != nil || entry.Kind != kind || entry.Version != "memory-history-v1" || entry.Memory == nil || entry.Memory.ProfileID != profile || entry.Memory.Index < 0 || id != fmt.Sprintf("%s:%d", entry.Memory.ID, entry.Memory.Index) || entry.Task.ID != "" || entry.Session != nil || len(entry.Messages) != 0 || len(entry.AIRequests) != 0 || len(entry.Runs) != 0 || len(entry.Notices) != 0 || !validAgentText(entry.Memory.Revision.Text, 4000, true) || !validAgentText(entry.Memory.Revision.Source, 300, true) || entry.Memory.Revision.At < 0 || entry.Memory.Revision.ExpiresAt < 0 {
			return entry, errors.New("archived memory verification failed; preserve records")
		}
		return entry, nil
	}

	var entry TaskHistoryEntry
	digest := sha256.Sum256([]byte(raw))
	if kind != "conversations" || len(raw) > agentDocumentLimit || hex.EncodeToString(digest[:]) != hash || update.StrictJSON([]byte(raw), &entry) != nil || entry.Version != "conversation-history-v1" || entry.Kind != kind || entry.Memory != nil || entry.Session == nil || entry.Session.ID != id || entry.Session.ProfileID != profile || entry.Task.ID != "" || len(entry.Runs) != 0 || len(entry.Notices) != 0 {
		return entry, errors.New("archived conversation verification failed; preserve records")
	}
	for _, m := range entry.Messages {
		if m.ProfileID != profile || m.SessionID != id {
			return entry, errors.New("archived message identity is inconsistent")
		}
	}
	for _, q := range entry.AIRequests {
		if q.ProfileID != profile || q.SessionID != id || !finalManualAI(q) {
			return entry, errors.New("archived AI journal is not final")
		}
	}
	return entry, nil
}
func archiveConversation(ctx context.Context, tx *sql.Tx, d *agentDocument, profile, id string, now int64) error {
	out := *d
	out.ArchivedAISpentMicrousd = maps.Clone(d.ArchivedAISpentMicrousd)
	if out.ArchivedAISpentMicrousd == nil {
		out.ArchivedAISpentMicrousd = map[string]int64{}
	}
	out.Sessions = nil
	out.Messages = nil
	out.AIRequests = nil
	entry := TaskHistoryEntry{Version: "conversation-history-v1", Kind: "conversations", Messages: []AgentMessage{}, AIRequests: []AgentAIRequest{}}
	ids := map[string]bool{id: true}
	for _, session := range d.Sessions {
		if session.ID == id && session.ProfileID == profile {
			copy := session
			entry.Session = &copy
		} else {
			out.Sessions = append(out.Sessions, session)
		}
	}
	if entry.Session == nil {
		return errors.New("profile conversation not found")
	}
	for _, m := range d.Messages {
		if m.SessionID == id && m.ProfileID == profile {
			entry.Messages = append(entry.Messages, m)
			ids[m.ID] = true
		} else {
			out.Messages = append(out.Messages, m)
		}
	}
	for _, q := range d.AIRequests {
		if q.SessionID == id && q.ProfileID == profile {
			if !finalManualAI(q) {
				return errors.New("pending work or unsettled journals keep this conversation active")
			}
			if q.SpentMicrousd < 0 || out.ArchivedAISpentMicrousd[profile] > 9223372036854775807-q.SpentMicrousd {
				return errors.New("archived spending is invalid")
			}
			out.ArchivedAISpentMicrousd[profile] += q.SpentMicrousd
			entry.AIRequests = append(entry.AIRequests, q)
			ids[q.ID] = true
		} else {
			out.AIRequests = append(out.AIRequests, q)
		}
	}
	// Verified memory references survive through the read-only archive resolver.
	refs := out
	refs.Memories = append([]AgentMemory(nil), out.Memories...)
	for i, m := range refs.Memories {
		if m.ProfileID != profile {
			continue
		}
		if e := inspectMemorySource(*d, m); e.Status == "matched" && e.SessionID == id {
			refs.Memories[i].Source = ""
			// Explicit /remember captures use the source message in their own
			// identity. This is not a dependency; keep the stored identity and
			// resolve the verified source through read-only archived history.
			if m.ID == "remember:"+e.ID {
				refs.Memories[i].ID = ""
			}
		}
		refs.Memories[i].History = append([]MemoryRevision(nil), m.History...)
		for j, h := range m.History {
			old := m
			old.Source = h.Source
			if e := inspectMemorySource(*d, old); e.Status == "matched" && e.SessionID == id {
				refs.Memories[i].History[j].Source = ""
				if m.ID == "remember:"+e.ID {
					refs.Memories[i].ID = ""
				}
			}
		}
	}
	rest, e := json.Marshal(refs)
	if e != nil {
		return e
	}
	for source := range ids {
		if bytes.Contains(rest, []byte(source)) {
			return errors.New("saved evidence or tasks reference this conversation; preserve it active")
		}
	}
	raw, e := json.Marshal(entry)
	if e != nil {
		return e
	}
	if len(raw) > agentDocumentLimit {
		return errors.New("conversation archive exceeds protected record limit")
	}
	digest := sha256.Sum256(raw)
	search := entry.Session.Name
	for _, m := range entry.Messages {
		search += " " + m.Text
	}
	for _, q := range entry.AIRequests {
		search += " " + q.Prompt + " " + q.Answer
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO agent_history(profile,source_id,kind,search_text,record,sha256,archived_at) VALUES(?,?,?,?,?,?,?)", profile, id, "conversations", strings.ToLower(search), string(raw), hex.EncodeToString(digest[:]), now); e != nil {
		return e
	}
	*d = out
	return nil
}
func hydrateMemorySource(ctx context.Context, r snapshotReader, d *agentDocument, m AgentMemory) error {
	parts := strings.Split(m.Source, " ")
	if len(parts) != 6 || parts[0] != "chat-memory-v1" || parts[2] != "profile="+m.ProfileID || !strings.HasPrefix(parts[3], "session=") {
		return nil
	}
	id := strings.TrimPrefix(parts[3], "session=")
	for _, session := range d.Sessions {
		if session.ID == id {
			return nil
		}
	}
	var raw, hash string
	e := r.QueryRowContext(ctx, "SELECT record,sha256 FROM agent_history WHERE profile=? AND source_id=? AND kind='conversations'", m.ProfileID, id).Scan(&raw, &hash)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	entry, e := parseArchivedEntry(raw, hash, m.ProfileID, id, "conversations")
	if e != nil {
		return e
	}
	d.Sessions = append(d.Sessions, *entry.Session)
	d.Messages = append(d.Messages, entry.Messages...)
	d.AIRequests = append(d.AIRequests, entry.AIRequests...)
	return nil
}

type MemoryHistoryRecord struct {
	ID        string         `json:"id"`
	ProfileID string         `json:"profileId"`
	Index     int64          `json:"index"`
	Revision  MemoryRevision `json:"revision"`
}

func rollMemoryHistory(ctx context.Context, tx *sql.Tx, d *agentDocument, profile, id string, now int64) error {
	for i, m := range d.Memories {
		if m.ID != id || m.ProfileID != profile {
			continue
		}
		if len(m.History) < 8 {
			return nil
		}
		if len(m.History) != 8 || m.HistoryOffset < 0 || m.HistoryOffset == 9223372036854775807 {
			return errors.New("unsupported memory history; preserve records")
		}
		entry := TaskHistoryEntry{Version: "memory-history-v1", Kind: "memories", Memory: &MemoryHistoryRecord{ID: m.ID, ProfileID: profile, Index: m.HistoryOffset, Revision: m.History[0]}}
		raw, e := json.Marshal(entry)
		if e != nil {
			return e
		}
		hash := sha256.Sum256(raw)
		source := fmt.Sprintf("%s:%d", m.ID, m.HistoryOffset)
		if _, e = tx.ExecContext(ctx, "INSERT INTO agent_history(profile,source_id,kind,search_text,record,sha256,archived_at) VALUES(?,?,?,?,?,?,?)", profile, source, "memories", strings.ToLower(m.ID+" "+m.History[0].Text), string(raw), hex.EncodeToString(hash[:]), now); e != nil {
			return e
		}
		d.Memories[i].History = append([]MemoryRevision(nil), m.History[1:]...)
		d.Memories[i].HistoryOffset++
		return nil
	}
	return nil
}
