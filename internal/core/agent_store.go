package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	agentV2SchemaVersion    = 3
	agentV2MaxPromptBytes   = 32 * 1024
	agentV2CompactMsgLimit  = 24
	agentV2CompactCharLimit = 48000
	agentV2KeepRecentMsgs   = 12
	agentV2SummaryMaxChars  = 4000
)

type AgentThread struct {
	ID               string `json:"id"`
	ProfileID        string `json:"profileId"`
	Title            string `json:"title"`
	ConnectionRef    string `json:"connectionRef,omitempty"`
	Model            string `json:"model,omitempty"`
	Summary          string `json:"summary,omitempty"`
	SourceKind       string `json:"sourceKind,omitempty"`
	SourceRef        string `json:"sourceRef,omitempty"`
	Archived         bool   `json:"archived"`
	CreatedAt        int64  `json:"createdAt"`
	UpdatedAt        int64  `json:"updatedAt"`
	MessageCount     int    `json:"messageCount,omitempty"`
	PendingApprovals int    `json:"pendingApprovals,omitempty"`
}

type TaskThreadLinks struct {
	ByTask map[string]string `json:"byTask"`
	ByRun  map[string]string `json:"byRun"`
}

type AgentV2Message struct {
	ID           string          `json:"id"`
	ThreadID     string          `json:"threadId"`
	Seq          int             `json:"seq"`
	Role         string          `json:"role"`
	Content      string          `json:"content"`
	Reasoning    string          `json:"reasoning,omitempty"`
	Status       string          `json:"status"`
	Model        string          `json:"model,omitempty"`
	Provider     string          `json:"provider,omitempty"`
	CallID       string          `json:"callId,omitempty"`
	ToolName     string          `json:"toolName,omitempty"`
	InputTokens  int64           `json:"inputTokens,omitempty"`
	OutputTokens int64           `json:"outputTokens,omitempty"`
	CachedTokens int64           `json:"cachedTokens,omitempty"`
	ErrorText    string          `json:"errorText,omitempty"`
	CreatedAt    int64           `json:"createdAt"`
	ToolCalls    []AgentToolCall `json:"toolCalls,omitempty"`
	Approvals    []AgentApproval `json:"approvals,omitempty"`
}

type AgentToolCall struct {
	ID          string `json:"id"`
	ThreadID    string `json:"threadId"`
	MessageID   string `json:"messageId"`
	CallID      string `json:"callId"`
	ToolName    string `json:"toolName"`
	Tier        string `json:"tier"`
	ArgsJSON    string `json:"argsJson"`
	Status      string `json:"status"`
	ResultJSON  string `json:"resultJson,omitempty"`
	ErrorText   string `json:"errorText,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
	CompletedAt int64  `json:"completedAt,omitempty"`
}

type AgentApproval struct {
	ID           string `json:"id"`
	ThreadID     string `json:"threadId"`
	MessageID    string `json:"messageId"`
	ToolCallID   string `json:"toolCallId"`
	ToolName     string `json:"toolName"`
	Title        string `json:"title"`
	Summary      string `json:"summary"`
	PayloadJSON  string `json:"payloadJson"`
	Status       string `json:"status"`
	DecisionNote string `json:"decisionNote,omitempty"`
	CreatedAt    int64  `json:"createdAt"`
	DecidedAt    int64  `json:"decidedAt,omitempty"`
}

type AgentV2Memory struct {
	ID        string `json:"id"`
	ProfileID string `json:"profileId,omitempty"`
	Scope     string `json:"scope"`
	Title     string `json:"title,omitempty"`
	Content   string `json:"content"`
	Tags      string `json:"tags,omitempty"`
	Pinned    bool   `json:"pinned"`
	Source    string `json:"source"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

type AgentThreadDetail struct {
	Thread           AgentThread      `json:"thread"`
	Messages         []AgentV2Message `json:"messages"`
	PendingApprovals []AgentApproval  `json:"pendingApprovals"`
	HasMore          bool             `json:"hasMore,omitempty"`
	NextBeforeSeq    int              `json:"nextBeforeSeq,omitempty"`
}

type agentV2Store struct {
	db *sql.DB
}

func (a *agentV2Store) close() error {
	if a == nil || a.db == nil {
		return nil
	}
	return a.db.Close()
}

func (s *Store) ensureAgentV2(ctx context.Context) (*agentV2Store, error) {
	s.agentV2Mu.Lock()
	defer s.agentV2Mu.Unlock()
	if s.agentV2 != nil {
		return s.agentV2, nil
	}
	if s.dir == "" {
		return nil, errors.New("agent store requires an initialized state directory")
	}
	if err := privateDir(s.dir); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(s.dir, "agents.sqlite")
	if err := privateFile(dbPath); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	cleanup := true
	defer func() {
		if cleanup {
			_ = db.Close()
		}
	}()
	if _, err := db.ExecContext(ctx, `PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA synchronous=FULL;`); err != nil {
		return nil, err
	}
	var version int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return nil, err
	}
	if version > agentV2SchemaVersion {
		return nil, errors.New("agents.sqlite schema is newer than this binary; refusing downgrade")
	}
	if version > 0 {
		if err := sqliteQuickCheck(ctx, db, "agents.sqlite"); err != nil {
			return nil, err
		}
	}
	if version == 0 {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		schemaSQL := `
CREATE TABLE threads(
	id TEXT PRIMARY KEY,
	profile_id TEXT NOT NULL,
	title TEXT NOT NULL,
	connection_ref TEXT NOT NULL DEFAULT '',
	model TEXT NOT NULL DEFAULT '',
	summary TEXT NOT NULL DEFAULT '',
	source_kind TEXT NOT NULL DEFAULT '',
	source_ref TEXT NOT NULL DEFAULT '',
	archived INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE INDEX idx_threads_profile ON threads(profile_id, archived, updated_at DESC);
CREATE INDEX idx_threads_source ON threads(profile_id, source_kind, source_ref, archived, updated_at DESC);

CREATE TABLE task_run_threads(
	run_id TEXT PRIMARY KEY,
	task_id TEXT NOT NULL,
	thread_id TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
	message_id TEXT NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE INDEX idx_task_run_threads_task ON task_run_threads(task_id);

CREATE TABLE messages(
	id TEXT PRIMARY KEY,
	thread_id TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
	seq INTEGER NOT NULL,
	role TEXT NOT NULL CHECK(role IN ('user','assistant','system','tool')),
	content TEXT NOT NULL DEFAULT '',
	reasoning TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'completed',
	model TEXT NOT NULL DEFAULT '',
	provider TEXT NOT NULL DEFAULT '',
	call_id TEXT NOT NULL DEFAULT '',
	tool_name TEXT NOT NULL DEFAULT '',
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	cached_tokens INTEGER NOT NULL DEFAULT 0,
	error_text TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX idx_messages_thread_seq ON messages(thread_id, seq);

CREATE TABLE tool_calls(
	id TEXT PRIMARY KEY,
	thread_id TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
	message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
	call_id TEXT NOT NULL,
	tool_name TEXT NOT NULL,
	tier TEXT NOT NULL CHECK(tier IN ('read','action')),
	args_json TEXT NOT NULL,
	status TEXT NOT NULL CHECK(status IN ('pending','awaiting_approval','running','completed','rejected','error')),
	result_json TEXT NOT NULL DEFAULT '',
	error_text TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	completed_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_tool_calls_thread ON tool_calls(thread_id, created_at);
CREATE INDEX idx_tool_calls_message ON tool_calls(message_id);

CREATE TABLE approvals(
	id TEXT PRIMARY KEY,
	thread_id TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
	message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
	tool_call_id TEXT NOT NULL REFERENCES tool_calls(id) ON DELETE CASCADE,
	tool_name TEXT NOT NULL,
	title TEXT NOT NULL,
	summary TEXT NOT NULL,
	payload_json TEXT NOT NULL,
	status TEXT NOT NULL CHECK(status IN ('pending','approved','rejected','expired')),
	decision_note TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	decided_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_approvals_thread_status ON approvals(thread_id, status);

CREATE TABLE memories(
	id TEXT PRIMARY KEY,
	profile_id TEXT NOT NULL DEFAULT '',
	scope TEXT NOT NULL DEFAULT 'workspace',
	title TEXT NOT NULL DEFAULT '',
	content TEXT NOT NULL,
	tags TEXT NOT NULL DEFAULT '',
	pinned INTEGER NOT NULL DEFAULT 0,
	source TEXT NOT NULL DEFAULT 'user',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE INDEX idx_memories_profile ON memories(profile_id, pinned DESC, updated_at DESC);

CREATE VIRTUAL TABLE memories_fts USING fts5(
	memory_id UNINDEXED,
	profile_id UNINDEXED,
	title,
	content,
	tags,
	tokenize='porter unicode61'
);

CREATE TABLE IF NOT EXISTS provider_model_cache (
	ref TEXT PRIMARY KEY,
	provider TEXT NOT NULL,
	state TEXT NOT NULL,
	fetched_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL,
	sha256 TEXT NOT NULL,
	source TEXT NOT NULL,
	models_json TEXT NOT NULL,
	updated_at INTEGER NOT NULL
);

PRAGMA user_version=3;
`
		if _, err := tx.ExecContext(ctx, schemaSQL); err != nil {
			return nil, fmt.Errorf("init agents.sqlite schema: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		version = 3
	}
	if version == 1 {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		upgradeSQL := `
CREATE TABLE IF NOT EXISTS provider_model_cache (
	ref TEXT PRIMARY KEY,
	provider TEXT NOT NULL,
	state TEXT NOT NULL,
	fetched_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL,
	sha256 TEXT NOT NULL,
	source TEXT NOT NULL,
	models_json TEXT NOT NULL,
	updated_at INTEGER NOT NULL
);

PRAGMA user_version=2;
`
		if _, err := tx.ExecContext(ctx, upgradeSQL); err != nil {
			return nil, fmt.Errorf("upgrade agents.sqlite schema v1->v2: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		version = 2
	}
	if version == 2 {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		upgradeV3SQL := `
ALTER TABLE threads ADD COLUMN source_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE threads ADD COLUMN source_ref TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_threads_source ON threads(profile_id, source_kind, source_ref, archived, updated_at DESC);
CREATE TABLE IF NOT EXISTS task_run_threads(
	run_id TEXT PRIMARY KEY,
	task_id TEXT NOT NULL,
	thread_id TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
	message_id TEXT NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_task_run_threads_task ON task_run_threads(task_id);

PRAGMA user_version=3;
`
		if _, err := tx.ExecContext(ctx, upgradeV3SQL); err != nil {
			return nil, fmt.Errorf("upgrade agents.sqlite schema v2->v3: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
	}
	v2 := &agentV2Store{db: db}
	s.agentV2 = v2
	cleanup = false
	return v2, nil
}

func sanitizeV2Title(title, fallback string) string {
	t := strings.TrimSpace(title)
	t = strings.Join(strings.Fields(t), " ")
	if t == "" {
		t = fallback
	}
	if len(t) > 80 {
		// Trim cleanly on UTF-8 rune boundary.
		runes := []rune(t)
		if len(runes) > 80 {
			t = strings.TrimSpace(string(runes[:77])) + "..."
		}
	}
	return t
}

func isV2DraftScope(scope string) bool {
	return strings.HasSuffix(strings.TrimSpace(scope), "_draft")
}

func sanitizeV2MemorySnippet(content string, maxRunes int) string {
	t := strings.Join(strings.Fields(strings.TrimSpace(content)), " ")
	if t == "" || maxRunes <= 0 {
		return t
	}
	runes := []rune(t)
	if len(runes) > maxRunes {
		if maxRunes <= 3 {
			return string(runes[:maxRunes])
		}
		return strings.TrimSpace(string(runes[:maxRunes-3])) + "..."
	}
	return t
}

func validV2ID(id string) bool {
	if len(id) < 6 || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func (s *Store) validProfileOrDefault(ctx context.Context, profileID string) string {
	profileID = strings.TrimSpace(profileID)
	d, _, err := readAgents(ctx, s.db)
	if err == nil {
		for _, p := range d.Profiles {
			if p.ID == profileID {
				return p.ID
			}
		}
		if profileID == "" && len(d.Profiles) > 0 {
			return d.Profiles[0].ID
		}
	}
	if profileID != "" && validV2ID(profileID) {
		return profileID
	}
	return "wen-operator"
}

func (s *Store) CreateThread(ctx context.Context, profileID, title, connectionRef, model string) (AgentThread, error) {
	v2, err := s.ensureAgentV2(ctx)
	if err != nil {
		return AgentThread{}, err
	}
	profileID = s.validProfileOrDefault(ctx, profileID)
	title = sanitizeV2Title(title, "New chat")
	if connectionRef != "" && !validConnectionRef(connectionRef) {
		return AgentThread{}, errors.New("invalid AI connection reference")
	}
	if model != "" && !validProviderModelID(model) {
		return AgentThread{}, errors.New("invalid model identifier")
	}
	id, err := randomToken()
	if err != nil {
		return AgentThread{}, err
	}
	now := s.now().Unix()
	t := AgentThread{
		ID:            id,
		ProfileID:     profileID,
		Title:         title,
		ConnectionRef: connectionRef,
		Model:         model,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	_, err = v2.db.ExecContext(ctx,
		`INSERT INTO threads(id,profile_id,title,connection_ref,model,summary,archived,created_at,updated_at) VALUES(?,?,?,?,?,'',0,?,?)`,
		t.ID, t.ProfileID, t.Title, t.ConnectionRef, t.Model, t.CreatedAt, t.UpdatedAt,
	)
	if err != nil {
		return AgentThread{}, err
	}
	return t, nil
}

func (s *Store) ListThreads(ctx context.Context, profileID string, includeArchived bool) ([]AgentThread, error) {
	v2, err := s.ensureAgentV2(ctx)
	if err != nil {
		return nil, err
	}
	profileID = strings.TrimSpace(profileID)
	query := `
SELECT t.id, t.profile_id, t.title, t.connection_ref, t.model, t.summary, t.source_kind, t.source_ref, t.archived, t.created_at, t.updated_at,
	(SELECT COUNT(*) FROM messages m WHERE m.thread_id = t.id),
	(SELECT COUNT(*) FROM approvals a WHERE a.thread_id = t.id AND a.status = 'pending')
FROM threads t
WHERE (? = '' OR t.profile_id = ?)
  AND (? = 1 OR t.archived = 0)
ORDER BY t.updated_at DESC, t.created_at DESC
LIMIT 200`
	archivedFlag := 0
	if includeArchived {
		archivedFlag = 1
	}
	rows, err := v2.db.QueryContext(ctx, query, profileID, profileID, archivedFlag)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentThread{}
	for rows.Next() {
		var t AgentThread
		var arch int
		if err := rows.Scan(&t.ID, &t.ProfileID, &t.Title, &t.ConnectionRef, &t.Model, &t.Summary, &t.SourceKind, &t.SourceRef, &arch, &t.CreatedAt, &t.UpdatedAt, &t.MessageCount, &t.PendingApprovals); err != nil {
			return nil, err
		}
		t.Archived = arch != 0
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) ListTaskThreadLinks(ctx context.Context, profileID string) (TaskThreadLinks, error) {
	out := TaskThreadLinks{
		ByTask: map[string]string{},
		ByRun:  map[string]string{},
	}
	if s == nil {
		return out, nil
	}
	s.agentV2Mu.Lock()
	v2 := s.agentV2
	s.agentV2Mu.Unlock()
	if v2 == nil {
		if s.dir == "" {
			return out, nil
		}
		if _, err := os.Stat(filepath.Join(s.dir, "agents.sqlite")); errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		var err error
		v2, err = s.ensureAgentV2(ctx)
		if err != nil {
			return out, err
		}
	}
	profileID = strings.TrimSpace(profileID)
	rows, err := v2.db.QueryContext(ctx, `
SELECT r.run_id, r.task_id, r.thread_id
FROM task_run_threads r
JOIN threads t ON t.id = r.thread_id
WHERE (? = '' OR t.profile_id = ?)
  AND t.archived = 0
ORDER BY r.created_at DESC, r.run_id DESC
LIMIT 500`, profileID, profileID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var runID, taskID, threadID string
		if err := rows.Scan(&runID, &taskID, &threadID); err != nil {
			return out, err
		}
		out.ByRun[runID] = threadID
		if _, exists := out.ByTask[taskID]; !exists {
			out.ByTask[taskID] = threadID
		}
	}
	return out, rows.Err()
}

func (s *Store) getThreadRow(ctx context.Context, v2 *agentV2Store, id string) (AgentThread, error) {
	if !validV2ID(id) {
		return AgentThread{}, ErrThreadNotFound
	}
	var t AgentThread
	var arch int
	err := v2.db.QueryRowContext(ctx,
		`SELECT id,profile_id,title,connection_ref,model,summary,source_kind,source_ref,archived,created_at,updated_at FROM threads WHERE id=?`,
		id,
	).Scan(&t.ID, &t.ProfileID, &t.Title, &t.ConnectionRef, &t.Model, &t.Summary, &t.SourceKind, &t.SourceRef, &arch, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentThread{}, ErrThreadNotFound
	}
	if err != nil {
		return AgentThread{}, err
	}
	t.Archived = arch != 0
	return t, nil
}

var (
	ErrThreadNotFound   = errors.New("thread not found")
	ErrApprovalNotFound = errors.New("approval not found")
	ErrMemoryNotFound   = errors.New("memory not found")
)

func (s *Store) GetThread(ctx context.Context, id string) (AgentThreadDetail, error) {
	return s.GetThreadPage(ctx, id, 0, 0)
}

func (s *Store) GetThreadPage(ctx context.Context, id string, limit, beforeSeq int) (AgentThreadDetail, error) {
	v2, err := s.ensureAgentV2(ctx)
	if err != nil {
		return AgentThreadDetail{}, err
	}
	thread, err := s.getThreadRow(ctx, v2, id)
	if err != nil {
		return AgentThreadDetail{}, err
	}
	if limit < 0 {
		limit = 0
	}
	if limit > 500 {
		limit = 500
	}
	var totalMsgs int
	if err := v2.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE thread_id=?`, id).Scan(&totalMsgs); err != nil {
		return AgentThreadDetail{}, err
	}
	var msgRows *sql.Rows
	if limit > 0 {
		if beforeSeq > 0 {
			msgRows, err = v2.db.QueryContext(ctx,
				`SELECT id,thread_id,seq,role,content,reasoning,status,model,provider,call_id,tool_name,input_tokens,output_tokens,cached_tokens,error_text,created_at
				 FROM messages WHERE thread_id=? AND seq<? ORDER BY seq DESC LIMIT ?`,
				id, beforeSeq, limit+1,
			)
		} else {
			msgRows, err = v2.db.QueryContext(ctx,
				`SELECT id,thread_id,seq,role,content,reasoning,status,model,provider,call_id,tool_name,input_tokens,output_tokens,cached_tokens,error_text,created_at
				 FROM messages WHERE thread_id=? ORDER BY seq DESC LIMIT ?`,
				id, limit+1,
			)
		}
	} else {
		msgRows, err = v2.db.QueryContext(ctx,
			`SELECT id,thread_id,seq,role,content,reasoning,status,model,provider,call_id,tool_name,input_tokens,output_tokens,cached_tokens,error_text,created_at
			 FROM messages WHERE thread_id=? ORDER BY seq ASC`,
			id,
		)
	}
	if err != nil {
		return AgentThreadDetail{}, err
	}
	msgs := []AgentV2Message{}
	for msgRows.Next() {
		var m AgentV2Message
		if err := msgRows.Scan(&m.ID, &m.ThreadID, &m.Seq, &m.Role, &m.Content, &m.Reasoning, &m.Status, &m.Model, &m.Provider, &m.CallID, &m.ToolName, &m.InputTokens, &m.OutputTokens, &m.CachedTokens, &m.ErrorText, &m.CreatedAt); err != nil {
			msgRows.Close()
			return AgentThreadDetail{}, err
		}
		msgs = append(msgs, m)
	}
	if err := msgRows.Err(); err != nil {
		msgRows.Close()
		return AgentThreadDetail{}, err
	}
	msgRows.Close()

	hasMore := false
	nextBeforeSeq := 0
	if limit > 0 {
		if len(msgs) > limit {
			hasMore = true
			msgs = msgs[:limit]
		}
		// Reverse DESC page back into chronological ASC order.
		for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
			msgs[i], msgs[j] = msgs[j], msgs[i]
		}
		if hasMore && len(msgs) > 0 {
			nextBeforeSeq = msgs[0].Seq
		}
	}
	msgIdx := make(map[string]int, len(msgs))
	for i, m := range msgs {
		msgIdx[m.ID] = i
	}

	tcRows, err := v2.db.QueryContext(ctx,
		`SELECT id,thread_id,message_id,call_id,tool_name,tier,args_json,status,result_json,error_text,created_at,completed_at
		 FROM tool_calls WHERE thread_id=? ORDER BY created_at ASC, id ASC`,
		id,
	)
	if err != nil {
		return AgentThreadDetail{}, err
	}
	for tcRows.Next() {
		var tc AgentToolCall
		if err := tcRows.Scan(&tc.ID, &tc.ThreadID, &tc.MessageID, &tc.CallID, &tc.ToolName, &tc.Tier, &tc.ArgsJSON, &tc.Status, &tc.ResultJSON, &tc.ErrorText, &tc.CreatedAt, &tc.CompletedAt); err != nil {
			tcRows.Close()
			return AgentThreadDetail{}, err
		}
		if idx, ok := msgIdx[tc.MessageID]; ok {
			msgs[idx].ToolCalls = append(msgs[idx].ToolCalls, tc)
		}
	}
	if err := tcRows.Err(); err != nil {
		tcRows.Close()
		return AgentThreadDetail{}, err
	}
	tcRows.Close()

	apRows, err := v2.db.QueryContext(ctx,
		`SELECT id,thread_id,message_id,tool_call_id,tool_name,title,summary,payload_json,status,decision_note,created_at,decided_at
		 FROM approvals WHERE thread_id=? ORDER BY created_at ASC, id ASC`,
		id,
	)
	if err != nil {
		return AgentThreadDetail{}, err
	}
	pending := []AgentApproval{}
	for apRows.Next() {
		var ap AgentApproval
		if err := apRows.Scan(&ap.ID, &ap.ThreadID, &ap.MessageID, &ap.ToolCallID, &ap.ToolName, &ap.Title, &ap.Summary, &ap.PayloadJSON, &ap.Status, &ap.DecisionNote, &ap.CreatedAt, &ap.DecidedAt); err != nil {
			apRows.Close()
			return AgentThreadDetail{}, err
		}
		if idx, ok := msgIdx[ap.MessageID]; ok {
			msgs[idx].Approvals = append(msgs[idx].Approvals, ap)
		}
		if ap.Status == "pending" {
			pending = append(pending, ap)
		}
	}
	if err := apRows.Err(); err != nil {
		apRows.Close()
		return AgentThreadDetail{}, err
	}
	apRows.Close()

	thread.MessageCount = totalMsgs
	thread.PendingApprovals = len(pending)
	return AgentThreadDetail{
		Thread:           thread,
		Messages:         msgs,
		PendingApprovals: pending,
		HasMore:          hasMore,
		NextBeforeSeq:    nextBeforeSeq,
	}, nil
}

type ThreadUpdateInput struct {
	Title         *string `json:"title,omitempty"`
	Archived      *bool   `json:"archived,omitempty"`
	ConnectionRef *string `json:"connectionRef,omitempty"`
	Model         *string `json:"model,omitempty"`
}

func (s *Store) UpdateThread(ctx context.Context, id string, patch ThreadUpdateInput) (AgentThread, error) {
	v2, err := s.ensureAgentV2(ctx)
	if err != nil {
		return AgentThread{}, err
	}
	t, err := s.getThreadRow(ctx, v2, id)
	if err != nil {
		return AgentThread{}, err
	}
	if patch.Title != nil {
		title := sanitizeV2Title(*patch.Title, "")
		if title == "" {
			return AgentThread{}, errors.New("thread title cannot be empty")
		}
		t.Title = title
	}
	if patch.Archived != nil {
		t.Archived = *patch.Archived
	}
	if patch.ConnectionRef != nil {
		if *patch.ConnectionRef != "" && !validConnectionRef(*patch.ConnectionRef) {
			return AgentThread{}, errors.New("invalid AI connection reference")
		}
		t.ConnectionRef = *patch.ConnectionRef
	}
	if patch.Model != nil {
		if *patch.Model != "" && !validProviderModelID(*patch.Model) {
			return AgentThread{}, errors.New("invalid model identifier")
		}
		t.Model = *patch.Model
	}
	t.UpdatedAt = s.now().Unix()
	arch := 0
	if t.Archived {
		arch = 1
	}
	res, err := v2.db.ExecContext(ctx,
		`UPDATE threads SET title=?, archived=?, connection_ref=?, model=?, updated_at=? WHERE id=?`,
		t.Title, arch, t.ConnectionRef, t.Model, t.UpdatedAt, t.ID,
	)
	if err != nil {
		return AgentThread{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return AgentThread{}, ErrThreadNotFound
	}
	return t, nil
}

func (s *Store) DeleteThread(ctx context.Context, id string) error {
	if !validV2ID(id) {
		return ErrThreadNotFound
	}
	v2, err := s.ensureAgentV2(ctx)
	if err != nil {
		return err
	}
	res, err := v2.db.ExecContext(ctx, `DELETE FROM threads WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrThreadNotFound
	}
	return nil
}

func (s *Store) AppendMessage(ctx context.Context, m AgentV2Message) (AgentV2Message, error) {
	v2, err := s.ensureAgentV2(ctx)
	if err != nil {
		return AgentV2Message{}, err
	}
	if m.ID == "" {
		id, err := randomToken()
		if err != nil {
			return AgentV2Message{}, err
		}
		m.ID = id
	}
	if m.Status == "" {
		m.Status = "completed"
	}
	if m.CreatedAt == 0 {
		m.CreatedAt = s.now().Unix()
	}
	tx, err := v2.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentV2Message{}, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM threads WHERE id=?`, m.ThreadID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentV2Message{}, ErrThreadNotFound
		}
		return AgentV2Message{}, err
	}
	var maxSeq int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM messages WHERE thread_id=?`, m.ThreadID).Scan(&maxSeq); err != nil {
		return AgentV2Message{}, err
	}
	m.Seq = maxSeq + 1
	_, err = tx.ExecContext(ctx,
		`INSERT INTO messages(id,thread_id,seq,role,content,reasoning,status,model,provider,call_id,tool_name,input_tokens,output_tokens,cached_tokens,error_text,created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.ThreadID, m.Seq, m.Role, m.Content, m.Reasoning, m.Status, m.Model, m.Provider, m.CallID, m.ToolName, m.InputTokens, m.OutputTokens, m.CachedTokens, m.ErrorText, m.CreatedAt,
	)
	if err != nil {
		return AgentV2Message{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE threads SET updated_at=? WHERE id=?`, m.CreatedAt, m.ThreadID); err != nil {
		return AgentV2Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentV2Message{}, err
	}
	return m, nil
}

func (s *Store) UpdateMessage(ctx context.Context, m AgentV2Message) error {
	v2, err := s.ensureAgentV2(ctx)
	if err != nil {
		return err
	}
	res, err := v2.db.ExecContext(ctx,
		`UPDATE messages SET content=?, reasoning=?, status=?, model=?, provider=?, input_tokens=?, output_tokens=?, cached_tokens=?, error_text=? WHERE id=?`,
		m.Content, m.Reasoning, m.Status, m.Model, m.Provider, m.InputTokens, m.OutputTokens, m.CachedTokens, m.ErrorText, m.ID,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errors.New("message not found")
	}
	_, err = v2.db.ExecContext(ctx, `UPDATE threads SET updated_at=? WHERE id=?`, s.now().Unix(), m.ThreadID)
	return err
}

func (s *Store) CreateToolCall(ctx context.Context, tc AgentToolCall) (AgentToolCall, error) {
	v2, err := s.ensureAgentV2(ctx)
	if err != nil {
		return AgentToolCall{}, err
	}
	if tc.ID == "" {
		id, err := randomToken()
		if err != nil {
			return AgentToolCall{}, err
		}
		tc.ID = id
	}
	if tc.CreatedAt == 0 {
		tc.CreatedAt = s.now().Unix()
	}
	_, err = v2.db.ExecContext(ctx,
		`INSERT INTO tool_calls(id,thread_id,message_id,call_id,tool_name,tier,args_json,status,result_json,error_text,created_at,completed_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		tc.ID, tc.ThreadID, tc.MessageID, tc.CallID, tc.ToolName, tc.Tier, tc.ArgsJSON, tc.Status, tc.ResultJSON, tc.ErrorText, tc.CreatedAt, tc.CompletedAt,
	)
	if err != nil {
		return AgentToolCall{}, err
	}
	return tc, nil
}

func (s *Store) UpdateToolCall(ctx context.Context, tc AgentToolCall) error {
	v2, err := s.ensureAgentV2(ctx)
	if err != nil {
		return err
	}
	_, err = v2.db.ExecContext(ctx,
		`UPDATE tool_calls SET status=?, result_json=?, error_text=?, completed_at=? WHERE id=?`,
		tc.Status, tc.ResultJSON, tc.ErrorText, tc.CompletedAt, tc.ID,
	)
	return err
}

func (s *Store) getToolCall(ctx context.Context, v2 *agentV2Store, id string) (AgentToolCall, error) {
	var tc AgentToolCall
	err := v2.db.QueryRowContext(ctx,
		`SELECT id,thread_id,message_id,call_id,tool_name,tier,args_json,status,result_json,error_text,created_at,completed_at
		 FROM tool_calls WHERE id=?`, id,
	).Scan(&tc.ID, &tc.ThreadID, &tc.MessageID, &tc.CallID, &tc.ToolName, &tc.Tier, &tc.ArgsJSON, &tc.Status, &tc.ResultJSON, &tc.ErrorText, &tc.CreatedAt, &tc.CompletedAt)
	return tc, err
}

func (s *Store) CreateApproval(ctx context.Context, ap AgentApproval) (AgentApproval, error) {
	v2, err := s.ensureAgentV2(ctx)
	if err != nil {
		return AgentApproval{}, err
	}
	if ap.ID == "" {
		id, err := randomToken()
		if err != nil {
			return AgentApproval{}, err
		}
		ap.ID = id
	}
	if ap.Status == "" {
		ap.Status = "pending"
	}
	if ap.CreatedAt == 0 {
		ap.CreatedAt = s.now().Unix()
	}
	_, err = v2.db.ExecContext(ctx,
		`INSERT INTO approvals(id,thread_id,message_id,tool_call_id,tool_name,title,summary,payload_json,status,decision_note,created_at,decided_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		ap.ID, ap.ThreadID, ap.MessageID, ap.ToolCallID, ap.ToolName, ap.Title, ap.Summary, ap.PayloadJSON, ap.Status, ap.DecisionNote, ap.CreatedAt, ap.DecidedAt,
	)
	if err != nil {
		return AgentApproval{}, err
	}
	return ap, nil
}

func (s *Store) GetApproval(ctx context.Context, id string) (AgentApproval, error) {
	if !validV2ID(id) {
		return AgentApproval{}, ErrApprovalNotFound
	}
	v2, err := s.ensureAgentV2(ctx)
	if err != nil {
		return AgentApproval{}, err
	}
	var ap AgentApproval
	err = v2.db.QueryRowContext(ctx,
		`SELECT id,thread_id,message_id,tool_call_id,tool_name,title,summary,payload_json,status,decision_note,created_at,decided_at
		 FROM approvals WHERE id=?`, id,
	).Scan(&ap.ID, &ap.ThreadID, &ap.MessageID, &ap.ToolCallID, &ap.ToolName, &ap.Title, &ap.Summary, &ap.PayloadJSON, &ap.Status, &ap.DecisionNote, &ap.CreatedAt, &ap.DecidedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentApproval{}, ErrApprovalNotFound
	}
	return ap, err
}

func (s *Store) DecideApproval(ctx context.Context, id, newStatus, note string) (AgentApproval, error) {
	if newStatus != "approved" && newStatus != "rejected" {
		return AgentApproval{}, errors.New("invalid approval status")
	}
	if !validV2ID(id) {
		return AgentApproval{}, ErrApprovalNotFound
	}
	if len(note) > 1000 || !utf8.ValidString(note) {
		return AgentApproval{}, errors.New("invalid approval note")
	}
	v2, err := s.ensureAgentV2(ctx)
	if err != nil {
		return AgentApproval{}, err
	}
	now := s.now().Unix()
	res, err := v2.db.ExecContext(ctx,
		`UPDATE approvals SET status=?, decision_note=?, decided_at=? WHERE id=? AND status='pending'`,
		newStatus, strings.TrimSpace(note), now, id,
	)
	if err != nil {
		return AgentApproval{}, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		if _, gerr := s.GetApproval(ctx, id); errors.Is(gerr, ErrApprovalNotFound) {
			return AgentApproval{}, ErrApprovalNotFound
		}
		return AgentApproval{}, ErrConflict
	}
	return s.GetApproval(ctx, id)
}

func (s *Store) PendingApprovalsCountForMessage(ctx context.Context, messageID string) (int, error) {
	v2, err := s.ensureAgentV2(ctx)
	if err != nil {
		return 0, err
	}
	var n int
	err = v2.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM approvals WHERE message_id=? AND status='pending'`, messageID,
	).Scan(&n)
	return n, err
}

type MemoryInput struct {
	ProfileID string `json:"profileId,omitempty"`
	Scope     string `json:"scope,omitempty"`
	Title     string `json:"title,omitempty"`
	Content   string `json:"content"`
	Tags      string `json:"tags,omitempty"`
	Pinned    bool   `json:"pinned,omitempty"`
	Source    string `json:"source,omitempty"`
}

type MemoryUpdateInput struct {
	Title   *string `json:"title,omitempty"`
	Content *string `json:"content,omitempty"`
	Tags    *string `json:"tags,omitempty"`
	Pinned  *bool   `json:"pinned,omitempty"`
	Scope   *string `json:"scope,omitempty"`
}

func (s *Store) CreateMemory(ctx context.Context, in MemoryInput) (AgentV2Memory, error) {
	content := strings.TrimSpace(in.Content)
	if content == "" || len(content) > 16384 || !utf8.ValidString(content) {
		return AgentV2Memory{}, errors.New("memory content must be 1 to 16384 UTF-8 bytes")
	}
	title := sanitizeV2Title(in.Title, sanitizeV2Title(content, "Memory"))
	scope := strings.TrimSpace(in.Scope)
	if scope == "" {
		scope = "workspace"
	}
	if len(scope) > 40 || !validAgentText(scope, 40, true) {
		return AgentV2Memory{}, errors.New("invalid memory scope")
	}
	tags := strings.TrimSpace(in.Tags)
	if len(tags) > 256 || !utf8.ValidString(tags) {
		return AgentV2Memory{}, errors.New("invalid memory tags")
	}
	source := strings.TrimSpace(in.Source)
	if source == "" {
		source = "user"
	}
	if !oneOf(source, "user", "agent", "system", "legacy") {
		source = "user"
	}
	profileID := strings.TrimSpace(in.ProfileID)
	if profileID != "" && !validV2ID(profileID) {
		return AgentV2Memory{}, errors.New("invalid profile id")
	}
	v2, err := s.ensureAgentV2(ctx)
	if err != nil {
		return AgentV2Memory{}, err
	}
	id, err := randomToken()
	if err != nil {
		return AgentV2Memory{}, err
	}
	now := s.now().Unix()
	m := AgentV2Memory{
		ID:        id,
		ProfileID: profileID,
		Scope:     scope,
		Title:     title,
		Content:   content,
		Tags:      tags,
		Pinned:    in.Pinned,
		Source:    source,
		CreatedAt: now,
		UpdatedAt: now,
	}
	pinnedInt := 0
	if m.Pinned {
		pinnedInt = 1
	}
	tx, err := v2.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentV2Memory{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO memories(id,profile_id,scope,title,content,tags,pinned,source,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.ProfileID, m.Scope, m.Title, m.Content, m.Tags, pinnedInt, m.Source, m.CreatedAt, m.UpdatedAt,
	)
	if err != nil {
		return AgentV2Memory{}, err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO memories_fts(memory_id,profile_id,title,content,tags) VALUES(?,?,?,?,?)`,
		m.ID, m.ProfileID, m.Title, m.Content, m.Tags,
	)
	if err != nil {
		return AgentV2Memory{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentV2Memory{}, err
	}
	return m, nil
}

func (s *Store) DeleteMemory(ctx context.Context, id string) error {
	if !validV2ID(id) {
		return ErrMemoryNotFound
	}
	v2, err := s.ensureAgentV2(ctx)
	if err != nil {
		return err
	}
	tx, err := v2.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM memories WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrMemoryNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM memories_fts WHERE memory_id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpdateMemory(ctx context.Context, id string, patch MemoryUpdateInput) (AgentV2Memory, error) {
	if !validV2ID(id) {
		return AgentV2Memory{}, ErrMemoryNotFound
	}
	v2, err := s.ensureAgentV2(ctx)
	if err != nil {
		return AgentV2Memory{}, err
	}
	var m AgentV2Memory
	var pinnedInt int
	err = v2.db.QueryRowContext(ctx,
		`SELECT id,profile_id,scope,title,content,tags,pinned,source,created_at,updated_at
		 FROM memories WHERE id=?`, id,
	).Scan(&m.ID, &m.ProfileID, &m.Scope, &m.Title, &m.Content, &m.Tags, &pinnedInt, &m.Source, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentV2Memory{}, ErrMemoryNotFound
	}
	if err != nil {
		return AgentV2Memory{}, err
	}
	m.Pinned = pinnedInt != 0

	if patch.Content != nil {
		content := strings.TrimSpace(*patch.Content)
		if content == "" || len(content) > 16384 || !utf8.ValidString(content) {
			return AgentV2Memory{}, errors.New("memory content must be 1 to 16384 UTF-8 bytes")
		}
		m.Content = content
	}
	if patch.Title != nil {
		m.Title = sanitizeV2Title(*patch.Title, m.Title)
	}
	if patch.Scope != nil {
		scope := strings.TrimSpace(*patch.Scope)
		if scope == "" || len(scope) > 40 || !validAgentText(scope, 40, true) {
			return AgentV2Memory{}, errors.New("invalid memory scope")
		}
		m.Scope = scope
	}
	if patch.Tags != nil {
		tags := strings.TrimSpace(*patch.Tags)
		if len(tags) > 256 || !utf8.ValidString(tags) {
			return AgentV2Memory{}, errors.New("invalid memory tags")
		}
		m.Tags = tags
	}
	if patch.Pinned != nil {
		m.Pinned = *patch.Pinned
	}
	m.UpdatedAt = s.now().Unix()
	nextPinned := 0
	if m.Pinned {
		nextPinned = 1
	}

	tx, err := v2.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentV2Memory{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`UPDATE memories SET scope=?, title=?, content=?, tags=?, pinned=?, updated_at=? WHERE id=?`,
		m.Scope, m.Title, m.Content, m.Tags, nextPinned, m.UpdatedAt, m.ID,
	); err != nil {
		return AgentV2Memory{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM memories_fts WHERE memory_id=?`, m.ID); err != nil {
		return AgentV2Memory{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO memories_fts(memory_id,profile_id,title,content,tags) VALUES(?,?,?,?,?)`,
		m.ID, m.ProfileID, m.Title, m.Content, m.Tags,
	); err != nil {
		return AgentV2Memory{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentV2Memory{}, err
	}
	return m, nil
}

func (s *Store) ListPinnedMemories(ctx context.Context, profileID string, excludeDrafts bool, limit int) ([]AgentV2Memory, error) {
	v2, err := s.ensureAgentV2(ctx)
	if err != nil {
		return nil, err
	}
	profileID = strings.TrimSpace(profileID)
	if limit <= 0 {
		limit = 6
	}
	if limit > 50 {
		limit = 50
	}
	draftFilter := ""
	if excludeDrafts {
		draftFilter = " AND NOT (length(scope) >= 6 AND substr(scope, length(scope) - 5) = '_draft')"
	}
	rows, err := v2.db.QueryContext(ctx,
		`SELECT id,profile_id,scope,title,content,tags,pinned,source,created_at,updated_at
		 FROM memories
		 WHERE pinned = 1 AND (? = '' OR profile_id = '' OR profile_id = ?)`+draftFilter+`
		 ORDER BY updated_at DESC LIMIT ?`,
		profileID, profileID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []AgentV2Memory{}
	for rows.Next() {
		var m AgentV2Memory
		var p int
		if err := rows.Scan(&m.ID, &m.ProfileID, &m.Scope, &m.Title, &m.Content, &m.Tags, &p, &m.Source, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		m.Pinned = p != 0
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func sanitizeFTSQuery(raw string) string {
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, raw)
	fields := strings.Fields(cleaned)
	if len(fields) == 0 {
		return ""
	}
	seen := map[string]bool{}
	parts := make([]string, 0, min(len(fields), 8))
	for _, tok := range fields {
		if seen[tok] {
			continue
		}
		seen[tok] = true
		parts = append(parts, `"`+tok+`"*`)
		if len(parts) >= 8 {
			break
		}
	}
	return strings.Join(parts, " ")
}

func (s *Store) ListMemoriesByScope(ctx context.Context, profileID, scope string, limit int) ([]AgentV2Memory, error) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return s.SearchMemories(ctx, profileID, "", limit)
	}
	v2, err := s.ensureAgentV2(ctx)
	if err != nil {
		return nil, err
	}
	profileID = strings.TrimSpace(profileID)
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := v2.db.QueryContext(ctx,
		`SELECT id,profile_id,scope,title,content,tags,pinned,source,created_at,updated_at
		 FROM memories
		 WHERE scope = ? AND (? = '' OR profile_id = '' OR profile_id = ?)
		 ORDER BY pinned DESC, updated_at DESC LIMIT ?`,
		scope, profileID, profileID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []AgentV2Memory{}
	for rows.Next() {
		var m AgentV2Memory
		var p int
		if err := rows.Scan(&m.ID, &m.ProfileID, &m.Scope, &m.Title, &m.Content, &m.Tags, &p, &m.Source, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		m.Pinned = p != 0
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) SearchMemories(ctx context.Context, profileID, query string, limit int) ([]AgentV2Memory, error) {
	v2, err := s.ensureAgentV2(ctx)
	if err != nil {
		return nil, err
	}
	profileID = strings.TrimSpace(profileID)
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	qTrim := strings.TrimSpace(query)
	out := []AgentV2Memory{}
	seenID := map[string]bool{}
	appendRow := func(m AgentV2Memory) {
		if seenID[m.ID] || len(out) >= limit {
			return
		}
		seenID[m.ID] = true
		out = append(out, m)
	}

	if qTrim == "" {
		rows, err := v2.db.QueryContext(ctx,
			`SELECT id,profile_id,scope,title,content,tags,pinned,source,created_at,updated_at
			 FROM memories WHERE (? = '' OR profile_id = '' OR profile_id = ?)
			 ORDER BY pinned DESC, updated_at DESC LIMIT ?`,
			profileID, profileID, limit,
		)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var m AgentV2Memory
			var p int
			if err := rows.Scan(&m.ID, &m.ProfileID, &m.Scope, &m.Title, &m.Content, &m.Tags, &p, &m.Source, &m.CreatedAt, &m.UpdatedAt); err != nil {
				rows.Close()
				return nil, err
			}
			m.Pinned = p != 0
			appendRow(m)
		}
		rows.Close()
	} else if fts := sanitizeFTSQuery(qTrim); fts != "" {
		rows, err := v2.db.QueryContext(ctx,
			`SELECT m.id,m.profile_id,m.scope,m.title,m.content,m.tags,m.pinned,m.source,m.created_at,m.updated_at
			 FROM memories_fts f
			 JOIN memories m ON m.id = f.memory_id
			 WHERE memories_fts MATCH ? AND (? = '' OR m.profile_id = '' OR m.profile_id = ?)
			 ORDER BY m.pinned DESC, bm25(memories_fts) ASC, m.updated_at DESC
			 LIMIT ?`,
			fts, profileID, profileID, limit,
		)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var m AgentV2Memory
			var p int
			if err := rows.Scan(&m.ID, &m.ProfileID, &m.Scope, &m.Title, &m.Content, &m.Tags, &p, &m.Source, &m.CreatedAt, &m.UpdatedAt); err != nil {
				rows.Close()
				return nil, err
			}
			m.Pinned = p != 0
			appendRow(m)
		}
		rows.Close()
	} else {
		likePattern := "%" + qTrim + "%"
		rows, err := v2.db.QueryContext(ctx,
			`SELECT id,profile_id,scope,title,content,tags,pinned,source,created_at,updated_at
			 FROM memories
			 WHERE (? = '' OR profile_id = '' OR profile_id = ?)
			   AND (title LIKE ? OR content LIKE ? OR tags LIKE ?)
			 ORDER BY pinned DESC, updated_at DESC LIMIT ?`,
			profileID, profileID, likePattern, likePattern, likePattern, limit,
		)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var m AgentV2Memory
			var p int
			if err := rows.Scan(&m.ID, &m.ProfileID, &m.Scope, &m.Title, &m.Content, &m.Tags, &p, &m.Source, &m.CreatedAt, &m.UpdatedAt); err != nil {
				rows.Close()
				return nil, err
			}
			m.Pinned = p != 0
			appendRow(m)
		}
		rows.Close()
	}

	// Merge legacy agent_workspace memories read-only so existing facts remain searchable.
	if len(out) < limit && s.db != nil {
		d, _, err := readAgents(ctx, s.db)
		if err == nil {
			lowerQ := strings.ToLower(qTrim)
			tokens := strings.Fields(strings.ToLower(strings.Map(func(r rune) rune {
				if unicode.IsLetter(r) || unicode.IsDigit(r) {
					return r
				}
				return ' '
			}, qTrim)))
			now := s.now().Unix()
			for _, lm := range d.Memories {
				if len(out) >= limit {
					break
				}
				if lm.Expired || (lm.ExpiresAt > 0 && lm.ExpiresAt <= now) || (profileID != "" && lm.ProfileID != "" && lm.ProfileID != profileID) {
					continue
				}
				if qTrim != "" {
					hay := strings.ToLower(lm.Text + " " + lm.Source)
					matched := false
					if len(tokens) == 0 {
						matched = strings.Contains(hay, lowerQ)
					} else {
						matched = true
						for _, tok := range tokens {
							if !strings.Contains(hay, tok) {
								matched = false
								break
							}
						}
					}
					if !matched {
						continue
					}
				}
				appendRow(AgentV2Memory{
					ID:        "legacy-" + lm.ID,
					ProfileID: lm.ProfileID,
					Scope:     "legacy",
					Title:     sanitizeV2Title(lm.Text, "Saved fact"),
					Content:   lm.Text,
					Source:    "legacy",
					UpdatedAt: lm.UpdatedAt,
					CreatedAt: lm.UpdatedAt,
				})
			}
		}
	}
	return out, nil
}

type AgentCompactResult struct {
	Thread        AgentThread `json:"thread"`
	Compacted     bool        `json:"compacted"`
	CompactedMsgs int         `json:"compactedMessages"`
	RemainingMsgs int         `json:"remainingMessages"`
	SummaryChars  int         `json:"summaryChars"`
}

func summarizeV2Messages(existingSummary string, older []AgentV2Message, maxChars int) string {
	if maxChars <= 0 {
		maxChars = agentV2SummaryMaxChars
	}
	var olderSB strings.Builder
	for _, m := range older {
		if m.Role == "user" || m.Role == "assistant" {
			line := sanitizeV2Title(m.Content, "")
			if line == "" && len(m.ToolCalls) > 0 {
				var names []string
				for _, tc := range m.ToolCalls {
					names = append(names, tc.ToolName+"("+tc.Status+")")
				}
				line = "tools: " + strings.Join(names, ", ")
			}
			if line != "" {
				olderSB.WriteString(m.Role)
				olderSB.WriteString(": ")
				olderSB.WriteString(line)
				olderSB.WriteByte('\n')
			}
		}
	}
	olderBlock := strings.TrimSpace(olderSB.String())
	trimmedExisting := strings.TrimSpace(existingSummary)
	if olderBlock == "" {
		return trimmedExisting
	}
	if trimmedExisting != "" {
		existingTail := strings.TrimPrefix(trimmedExisting, "...")
		if trimmedExisting == olderBlock || strings.Contains(trimmedExisting, olderBlock) || (existingTail != "" && strings.HasSuffix(olderBlock, existingTail)) {
			return trimmedExisting
		}
	}
	var newSummary string
	if trimmedExisting != "" && (strings.HasPrefix(olderBlock, trimmedExisting) || (strings.TrimPrefix(trimmedExisting, "...") != "" && strings.Contains(olderBlock, strings.TrimPrefix(trimmedExisting, "...")))) {
		newSummary = olderBlock
	} else {
		var sb strings.Builder
		if trimmedExisting != "" {
			sb.WriteString(trimmedExisting)
			sb.WriteString("\n---\n")
		}
		sb.WriteString(olderBlock)
		newSummary = strings.TrimSpace(sb.String())
	}
	runes := []rune(newSummary)
	if len(runes) > maxChars {
		if maxChars > 3 {
			// Keep the newest tail of the summary when bounding.
			newSummary = "..." + string(runes[len(runes)-maxChars+3:])
		} else {
			newSummary = string(runes[len(runes)-maxChars:])
		}
	}
	return newSummary
}

// CompactThread explicitly rolls completed conversation turns into the persistent
// thread summary without deleting historical rows from `messages`.
func (s *Store) CompactThread(ctx context.Context, threadID string, keepRecent int) (AgentCompactResult, error) {
	detail, err := s.GetThread(ctx, threadID)
	if err != nil {
		return AgentCompactResult{}, err
	}
	msgs := detail.Messages
	existing := strings.TrimSpace(detail.Thread.Summary)
	if len(msgs) == 0 {
		return AgentCompactResult{
			Thread:       detail.Thread,
			SummaryChars: len([]rune(existing)),
		}, nil
	}

	var cut int
	if keepRecent > 0 {
		if len(msgs) <= keepRecent {
			return AgentCompactResult{
				Thread:        detail.Thread,
				RemainingMsgs: len(msgs),
				SummaryChars:  len([]rune(existing)),
			}, nil
		}
		cut = len(msgs) - keepRecent
		// Never split an assistant tool-call message from its immediately following tool messages.
		for cut > 0 && msgs[cut].Role == "tool" {
			cut--
		}
		if cut <= 0 {
			return AgentCompactResult{
				Thread:        detail.Thread,
				RemainingMsgs: len(msgs),
				SummaryChars:  len([]rune(existing)),
			}, nil
		}
	} else if len(msgs) > 8 {
		cut = len(msgs) - 6
		for cut > 0 && msgs[cut].Role == "tool" {
			cut--
		}
		if cut <= 0 {
			cut = len(msgs)
		}
	} else {
		cut = len(msgs)
	}

	older := msgs[:cut]
	recent := msgs[cut:]
	newSummary := summarizeV2Messages(existing, older, agentV2SummaryMaxChars)
	if newSummary == "" || newSummary == existing {
		return AgentCompactResult{
			Thread:        detail.Thread,
			RemainingMsgs: len(msgs),
			SummaryChars:  len([]rune(existing)),
		}, nil
	}

	v2, err := s.ensureAgentV2(ctx)
	if err != nil {
		return AgentCompactResult{}, err
	}
	if _, err := v2.db.ExecContext(ctx, `UPDATE threads SET summary=? WHERE id=?`, newSummary, detail.Thread.ID); err != nil {
		return AgentCompactResult{}, err
	}
	detail.Thread.Summary = newSummary
	return AgentCompactResult{
		Thread:        detail.Thread,
		Compacted:     true,
		CompactedMsgs: len(older),
		RemainingMsgs: len(recent),
		SummaryChars:  len([]rune(newSummary)),
	}, nil
}

// buildContextWindow returns an updated rolling summary (persisted on the thread if
// compaction occurred) plus the recent suffix of messages for provider submission.
// Historical rows in `messages` are never deleted.
func (s *Store) buildContextWindow(ctx context.Context, detail AgentThreadDetail) (string, []AgentV2Message, error) {
	return s.contextWindowForModel(ctx, detail, detail.Thread.Model, true)
}

func (s *Store) contextWindowForModel(ctx context.Context, detail AgentThreadDetail, modelID string, persist bool) (string, []AgentV2Message, error) {
	msgs := detail.Messages
	totalChars := 0
	for _, m := range msgs {
		totalChars += len(m.Content) + len(m.Reasoning)
		for _, tc := range m.ToolCalls {
			totalChars += len(tc.ArgsJSON) + len(tc.ResultJSON)
		}
	}
	charLimit := agentV2CompactCharLimit
	trimmedModel := strings.TrimSpace(modelID)
	modelMeta, modelOK := s.resolveManifestModel("", trimmedModel)
	if !modelOK {
		modelMeta, modelOK = currentProviderModel(trimmedModel)
	}
	if modelOK && modelMeta.ContextTokens > 0 {
		budget := int(modelMeta.ContextTokens * 2)
		if budget < 8000 {
			budget = 8000
		}
		if budget < charLimit {
			charLimit = budget
		}
	}
	summary := detail.Thread.Summary
	if len(msgs) <= agentV2CompactMsgLimit && totalChars <= charLimit {
		return summary, msgs, nil
	}
	if len(msgs) <= agentV2KeepRecentMsgs {
		return summary, msgs, nil
	}
	cut := len(msgs) - agentV2KeepRecentMsgs
	// Never split an assistant tool-call message from its immediately following tool messages.
	for cut > 0 && msgs[cut].Role == "tool" {
		cut--
	}
	if cut <= 0 {
		return summary, msgs, nil
	}
	older := msgs[:cut]
	recent := msgs[cut:]

	newSummary := summarizeV2Messages(summary, older, agentV2SummaryMaxChars)
	if persist && newSummary != summary {
		if v2, err := s.ensureAgentV2(ctx); err == nil {
			_, _ = v2.db.ExecContext(ctx, `UPDATE threads SET summary=? WHERE id=?`, newSummary, detail.Thread.ID)
		}
	}
	return newSummary, recent, nil
}

// buildToolResultJSON encodes a tool response within the bounded output limit.
func encodeToolResultJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"error":"result encoding failed"}`
	}
	const maxBytes = 12 * 1024
	if len(b) <= maxBytes {
		return string(b)
	}
	truncated, _ := json.Marshal(map[string]any{
		"truncated": true,
		"preview":   string(b[:maxBytes-256]),
	})
	return string(truncated)
}
