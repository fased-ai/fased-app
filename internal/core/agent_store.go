package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	agentV2SchemaVersion    = 1
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
	Archived         bool   `json:"archived"`
	CreatedAt        int64  `json:"createdAt"`
	UpdatedAt        int64  `json:"updatedAt"`
	MessageCount     int    `json:"messageCount,omitempty"`
	PendingApprovals int    `json:"pendingApprovals,omitempty"`
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
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
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
	archived INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE INDEX idx_threads_profile ON threads(profile_id, archived, updated_at DESC);

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

PRAGMA user_version=1;
`
		if _, err := tx.ExecContext(ctx, schemaSQL); err != nil {
			return nil, fmt.Errorf("init agents.sqlite schema: %w", err)
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
SELECT t.id, t.profile_id, t.title, t.connection_ref, t.model, t.summary, t.archived, t.created_at, t.updated_at,
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
		if err := rows.Scan(&t.ID, &t.ProfileID, &t.Title, &t.ConnectionRef, &t.Model, &t.Summary, &arch, &t.CreatedAt, &t.UpdatedAt, &t.MessageCount, &t.PendingApprovals); err != nil {
			return nil, err
		}
		t.Archived = arch != 0
		out = append(out, t)
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
		`SELECT id,profile_id,title,connection_ref,model,summary,archived,created_at,updated_at FROM threads WHERE id=?`,
		id,
	).Scan(&t.ID, &t.ProfileID, &t.Title, &t.ConnectionRef, &t.Model, &t.Summary, &arch, &t.CreatedAt, &t.UpdatedAt)
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
	v2, err := s.ensureAgentV2(ctx)
	if err != nil {
		return AgentThreadDetail{}, err
	}
	thread, err := s.getThreadRow(ctx, v2, id)
	if err != nil {
		return AgentThreadDetail{}, err
	}
	msgRows, err := v2.db.QueryContext(ctx,
		`SELECT id,thread_id,seq,role,content,reasoning,status,model,provider,call_id,tool_name,input_tokens,output_tokens,cached_tokens,error_text,created_at
		 FROM messages WHERE thread_id=? ORDER BY seq ASC`,
		id,
	)
	if err != nil {
		return AgentThreadDetail{}, err
	}
	msgs := []AgentV2Message{}
	msgIdx := map[string]int{}
	for msgRows.Next() {
		var m AgentV2Message
		if err := msgRows.Scan(&m.ID, &m.ThreadID, &m.Seq, &m.Role, &m.Content, &m.Reasoning, &m.Status, &m.Model, &m.Provider, &m.CallID, &m.ToolName, &m.InputTokens, &m.OutputTokens, &m.CachedTokens, &m.ErrorText, &m.CreatedAt); err != nil {
			msgRows.Close()
			return AgentThreadDetail{}, err
		}
		msgIdx[m.ID] = len(msgs)
		msgs = append(msgs, m)
	}
	if err := msgRows.Err(); err != nil {
		msgRows.Close()
		return AgentThreadDetail{}, err
	}
	msgRows.Close()

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

	thread.MessageCount = len(msgs)
	thread.PendingApprovals = len(pending)
	return AgentThreadDetail{
		Thread:           thread,
		Messages:         msgs,
		PendingApprovals: pending,
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
			for _, lm := range d.Memories {
				if len(out) >= limit {
					break
				}
				if lm.Expired || (profileID != "" && lm.ProfileID != "" && lm.ProfileID != profileID) {
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

// buildContextWindow returns an updated rolling summary (persisted on the thread if
// compaction occurred) plus the recent suffix of messages for provider submission.
// Historical rows in `messages` are never deleted.
func (s *Store) buildContextWindow(ctx context.Context, detail AgentThreadDetail) (string, []AgentV2Message, error) {
	msgs := detail.Messages
	totalChars := 0
	for _, m := range msgs {
		totalChars += len(m.Content) + len(m.Reasoning)
		for _, tc := range m.ToolCalls {
			totalChars += len(tc.ArgsJSON) + len(tc.ResultJSON)
		}
	}
	summary := detail.Thread.Summary
	if len(msgs) <= agentV2CompactMsgLimit && totalChars <= agentV2CompactCharLimit {
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

	var sb strings.Builder
	if summary != "" {
		sb.WriteString(strings.TrimSpace(summary))
		sb.WriteString("\n---\n")
	}
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
				sb.WriteString(m.Role)
				sb.WriteString(": ")
				sb.WriteString(line)
				sb.WriteByte('\n')
			}
		}
	}
	newSummary := strings.TrimSpace(sb.String())
	if len(newSummary) > agentV2SummaryMaxChars {
		runes := []rune(newSummary)
		if len(runes) > agentV2SummaryMaxChars {
			// Keep the newest tail of the summary when bounding.
			newSummary = "..." + string(runes[len(runes)-agentV2SummaryMaxChars+3:])
		}
	}
	if v2, err := s.ensureAgentV2(ctx); err == nil && newSummary != summary {
		_, _ = v2.db.ExecContext(ctx, `UPDATE threads SET summary=? WHERE id=?`, newSummary, detail.Thread.ID)
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
