package tasks

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Store persists universal tasks, DAG edges, runs, logs, and composable libraries in SQLite.
type Store struct {
	mu  sync.RWMutex
	db  *sql.DB
	now func() time.Time
}

func randomHexID(prefix string) string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

// NewStore opens or creates <dataDir>/tasks/tasks.sqlite and seeds built-in personas, skills, and templates.
func NewStore(dataDir string, now func() time.Time) (*Store, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("dataDir required for tasks store")
	}
	if now == nil {
		now = time.Now
	}
	dir := filepath.Join(dataDir, "tasks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create tasks dir: %w", err)
	}
	dbPath := filepath.Join(dir, "tasks.sqlite")
	uri := "file:" + filepath.ToSlash(dbPath) + "?_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, fmt.Errorf("open tasks sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := os.Chmod(dbPath, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = db.Close()
		return nil, fmt.Errorf("chmod tasks sqlite: %w", err)
	}
	if err := migrateTasksSchema(context.Background(), db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := seedBuiltinsTx(context.Background(), db, now().Unix()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("seed task libraries: %w", err)
	}
	if err := os.Chmod(dbPath, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = db.Close()
		return nil, fmt.Errorf("chmod tasks sqlite post-init: %w", err)
	}
	return &Store{db: db, now: now}, nil
}

// Close closes the underlying SQLite connection.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func migrateTasksSchema(ctx context.Context, db *sql.DB) error {
	const ddl = `
CREATE TABLE IF NOT EXISTS tasks (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  goal_domain TEXT NOT NULL,
  goal_objective TEXT NOT NULL DEFAULT '',
  schedule_kind TEXT NOT NULL,
  schedule_spec_json TEXT NOT NULL,
  exec_mode TEXT NOT NULL,
  approval_policy TEXT NOT NULL,
  execution_binding_json TEXT NOT NULL,
  attachments_json TEXT NOT NULL,
  constraints_json TEXT NOT NULL,
  status TEXT NOT NULL,
  tainted INTEGER NOT NULL DEFAULT 0,
  parent_task_id TEXT NOT NULL DEFAULT '',
  swarm_group_id TEXT NOT NULL DEFAULT '',
  template_id TEXT NOT NULL DEFAULT '',
  created_via TEXT NOT NULL DEFAULT 'ui',
  policy_hash TEXT NOT NULL,
  last_run_at INTEGER NOT NULL DEFAULT 0,
  next_run_at INTEGER NOT NULL DEFAULT 0,
  last_hybrid_at INTEGER NOT NULL DEFAULT 0,
  run_count INTEGER NOT NULL DEFAULT 0,
  error_count INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS task_edges (
  id TEXT PRIMARY KEY,
  parent_task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  child_task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  condition TEXT NOT NULL,
  payload_map_json TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  UNIQUE(parent_task_id, child_task_id)
);

CREATE TABLE IF NOT EXISTS task_runs (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  parent_run_id TEXT NOT NULL DEFAULT '',
  trigger_source TEXT NOT NULL,
  exec_mode TEXT NOT NULL,
  status TEXT NOT NULL,
  tainted INTEGER NOT NULL DEFAULT 0,
  started_at INTEGER NOT NULL,
  finished_at INTEGER NOT NULL DEFAULT 0,
  duration_ms INTEGER NOT NULL DEFAULT 0,
  tokens_in INTEGER NOT NULL DEFAULT 0,
  tokens_out INTEGER NOT NULL DEFAULT 0,
  cost_microusd INTEGER NOT NULL DEFAULT 0,
  pnl_microusd INTEGER NOT NULL DEFAULT 0,
  sat_earned_atoms INTEGER NOT NULL DEFAULT 0,
  signal_emitted INTEGER NOT NULL DEFAULT 0,
  threshold_hit INTEGER NOT NULL DEFAULT 0,
  action_taken TEXT NOT NULL DEFAULT '',
  decision_summary TEXT NOT NULL DEFAULT '',
  result_json TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_task_runs_task_started ON task_runs(task_id, started_at DESC);

CREATE TABLE IF NOT EXISTS task_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  run_id TEXT NOT NULL DEFAULT '',
  seq INTEGER NOT NULL DEFAULT 0,
  ts INTEGER NOT NULL,
  level TEXT NOT NULL,
  stage TEXT NOT NULL,
  message TEXT NOT NULL,
  meta_json TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_task_logs_task_id ON task_logs(task_id, id DESC);

CREATE TABLE IF NOT EXISTS task_personas (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  role TEXT NOT NULL,
  goal_domains_json TEXT NOT NULL,
  system_prompt TEXT NOT NULL,
  risk_profile TEXT NOT NULL,
  builtin INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS task_skills (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  goal_domain TEXT NOT NULL,
  instructions TEXT NOT NULL,
  allowed_tools_json TEXT NOT NULL,
  source TEXT NOT NULL DEFAULT 'user',
  sha256 TEXT NOT NULL,
  builtin INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS task_templates (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT NOT NULL,
  goal_domain TEXT NOT NULL,
  exec_mode TEXT NOT NULL,
  default_task_json TEXT NOT NULL,
  builtin INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);`
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("migrate tasks sqlite: %w", err)
	}
	return nil
}

// CreateTask validates and persists a new task.
func (s *Store) CreateTask(ctx context.Context, in Task) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.ID == "" {
		in.ID = randomHexID("tsk_")
	}
	nowUnix := s.now().Unix()
	in.CreatedAt = nowUnix
	in.UpdatedAt = nowUnix
	if err := NormalizeAndValidate(&in); err != nil {
		return Task{}, err
	}
	if in.ParentTaskID != "" {
		parent, err := s.getTaskLocked(ctx, in.ParentTaskID)
		if err != nil {
			return Task{}, fmt.Errorf("parent task invalid: %w", err)
		}
		if err := CheckChildPrivilege(parent, in); err != nil {
			return Task{}, err
		}
		if parent.Tainted {
			in.Tainted = true
		}
		in.PolicyHash = ComputeTaskPolicyHash(in)
	}
	if in.Status == StatusActive && in.NextRunAt == 0 {
		in.NextRunAt = computeNextRunAt(in, nowUnix, true)
	}
	if err := s.insertTaskLocked(ctx, in); err != nil {
		return Task{}, err
	}
	return in, nil
}

func (s *Store) insertTaskLocked(ctx context.Context, t Task) error {
	schedB, _ := json.Marshal(t.ScheduleSpec)
	bindB, _ := json.Marshal(t.ExecutionBinding)
	attB, _ := json.Marshal(t.Attachments)
	conB, _ := json.Marshal(t.Constraints)
	taintedInt := 0
	if t.Tainted {
		taintedInt = 1
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO tasks (
  id, name, goal_domain, goal_objective, schedule_kind, schedule_spec_json,
  exec_mode, approval_policy, execution_binding_json, attachments_json, constraints_json,
  status, tainted, parent_task_id, swarm_group_id, template_id, created_via, policy_hash,
  last_run_at, next_run_at, last_hybrid_at, run_count, error_count, last_error, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name, string(t.GoalDomain), t.GoalObjective, string(t.ScheduleKind), string(schedB),
		string(t.ExecMode), string(t.ApprovalPolicy), string(bindB), string(attB), string(conB),
		string(t.Status), taintedInt, t.ParentTaskID, t.SwarmGroupID, t.TemplateID, t.CreatedVia, t.PolicyHash,
		t.LastRunAt, t.NextRunAt, t.LastHybridAt, t.RunCount, t.ErrorCount, t.LastError, t.CreatedAt, t.UpdatedAt)
	return err
}

func scanTaskRow(row interface{ Scan(dest ...any) error }) (Task, error) {
	var t Task
	var goalDomain, schedKind, schedJSON, execMode, appPol, bindJSON, attJSON, conJSON, status string
	var taintedInt int
	if err := row.Scan(
		&t.ID, &t.Name, &goalDomain, &t.GoalObjective, &schedKind, &schedJSON,
		&execMode, &appPol, &bindJSON, &attJSON, &conJSON,
		&status, &taintedInt, &t.ParentTaskID, &t.SwarmGroupID, &t.TemplateID, &t.CreatedVia, &t.PolicyHash,
		&t.LastRunAt, &t.NextRunAt, &t.LastHybridAt, &t.RunCount, &t.ErrorCount, &t.LastError, &t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return Task{}, err
	}
	t.GoalDomain = GoalDomain(goalDomain)
	t.ScheduleKind = ScheduleKind(schedKind)
	t.ExecMode = ExecMode(execMode)
	t.ApprovalPolicy = ApprovalPolicy(appPol)
	t.Status = TaskStatus(status)
	t.Tainted = taintedInt != 0
	_ = json.Unmarshal([]byte(schedJSON), &t.ScheduleSpec)
	_ = json.Unmarshal([]byte(bindJSON), &t.ExecutionBinding)
	_ = json.Unmarshal([]byte(attJSON), &t.Attachments)
	_ = json.Unmarshal([]byte(conJSON), &t.Constraints)
	return t, nil
}

const taskSelectCols = `id, name, goal_domain, goal_objective, schedule_kind, schedule_spec_json,
  exec_mode, approval_policy, execution_binding_json, attachments_json, constraints_json,
  status, tainted, parent_task_id, swarm_group_id, template_id, created_via, policy_hash,
  last_run_at, next_run_at, last_hybrid_at, run_count, error_count, last_error, created_at, updated_at`

func (s *Store) getTaskLocked(ctx context.Context, id string) (Task, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+taskSelectCols+` FROM tasks WHERE id=?`, id)
	return scanTaskRow(row)
}

// GetTask loads one task by ID.
func (s *Store) GetTask(ctx context.Context, id string) (Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getTaskLocked(ctx, id)
}

// ListTasks lists persisted tasks filtered by GoalDomain, Status, and search query.
func (s *Store) ListTasks(ctx context.Context, f TaskFilter) ([]Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+taskSelectCols+` FROM tasks ORDER BY updated_at DESC, created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Task, 0)
	q := strings.ToLower(strings.TrimSpace(f.Query))
	for rows.Next() {
		t, err := scanTaskRow(rows)
		if err != nil {
			return nil, err
		}
		if f.GoalDomain != "" && t.GoalDomain != f.GoalDomain {
			continue
		}
		if f.Status != "" && t.Status != f.Status {
			continue
		}
		if q != "" {
			hay := strings.ToLower(t.Name + " " + t.GoalObjective + " " + t.ID + " " + string(t.GoalDomain))
			if !strings.Contains(hay, q) {
				continue
			}
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateTask updates mutable definition fields and recomputes PolicyHash.
func (s *Store) UpdateTask(ctx context.Context, in Task) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, err := s.getTaskLocked(ctx, in.ID)
	if err != nil {
		return Task{}, err
	}
	in.CreatedAt = cur.CreatedAt
	in.CreatedVia = cur.CreatedVia
	if in.Status == "" {
		in.Status = cur.Status
	}
	in.RunCount = cur.RunCount
	in.ErrorCount = cur.ErrorCount
	in.LastRunAt = cur.LastRunAt
	in.LastHybridAt = cur.LastHybridAt
	in.UpdatedAt = s.now().Unix()
	if cur.Tainted {
		in.Tainted = true
	}
	if err := NormalizeAndValidate(&in); err != nil {
		return Task{}, err
	}
	if in.ParentTaskID != "" {
		parent, pErr := s.getTaskLocked(ctx, in.ParentTaskID)
		if pErr != nil {
			return Task{}, fmt.Errorf("parent task invalid: %w", pErr)
		}
		if err := CheckChildPrivilege(parent, in); err != nil {
			return Task{}, err
		}
	}
	schedB, _ := json.Marshal(in.ScheduleSpec)
	bindB, _ := json.Marshal(in.ExecutionBinding)
	attB, _ := json.Marshal(in.Attachments)
	conB, _ := json.Marshal(in.Constraints)
	taintedInt := 0
	if in.Tainted {
		taintedInt = 1
	}
	_, err = s.db.ExecContext(ctx, `
UPDATE tasks SET
  name=?, goal_domain=?, goal_objective=?, schedule_kind=?, schedule_spec_json=?,
  exec_mode=?, approval_policy=?, execution_binding_json=?, attachments_json=?, constraints_json=?,
  status=?, tainted=?, parent_task_id=?, swarm_group_id=?, template_id=?, policy_hash=?,
  next_run_at=?, last_error=?, updated_at=?
WHERE id=?`,
		in.Name, string(in.GoalDomain), in.GoalObjective, string(in.ScheduleKind), string(schedB),
		string(in.ExecMode), string(in.ApprovalPolicy), string(bindB), string(attB), string(conB),
		string(in.Status), taintedInt, in.ParentTaskID, in.SwarmGroupID, in.TemplateID, in.PolicyHash,
		in.NextRunAt, in.LastError, in.UpdatedAt, in.ID)
	return in, err
}

// SetTaskStatus transitions a task status cleanly and updates next_run_at.
func (s *Store) SetTaskStatus(ctx context.Context, id string, status TaskStatus, lastErr string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.getTaskLocked(ctx, id)
	if err != nil {
		return Task{}, err
	}
	nowUnix := s.now().Unix()
	t.Status = status
	t.UpdatedAt = nowUnix
	t.LastError = strings.TrimSpace(lastErr)
	if status == StatusActive {
		t.NextRunAt = computeNextRunAt(t, nowUnix, true)
	} else {
		t.NextRunAt = 0
	}
	_, err = s.db.ExecContext(ctx, `UPDATE tasks SET status=?, next_run_at=?, last_error=?, updated_at=? WHERE id=?`,
		string(t.Status), t.NextRunAt, t.LastError, t.UpdatedAt, t.ID)
	return t, err
}

// KillAllTasks marks all active/paused tasks as killed in SQLite.
func (s *Store) KillAllTasks(ctx context.Context, reason string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	nowUnix := s.now().Unix()
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM tasks WHERE status IN ('active', 'paused')`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	_ = rows.Close()
	_, err = s.db.ExecContext(ctx,
		`UPDATE tasks SET status=?, next_run_at=0, last_error=?, updated_at=? WHERE status IN ('active', 'paused')`,
		string(StatusKilled), strings.TrimSpace(reason), nowUnix)
	return ids, err
}

// DeleteTask removes a task and cascading edges/runs/logs.
func (s *Store) DeleteTask(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx, `DELETE FROM tasks WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// AddEdge validates DAG acyclicity, depth <= 6, fan-out <= 8, and privilege non-escalation before saving.
func (s *Store) AddEdge(ctx context.Context, edge TaskEdge) (TaskEdge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if edge.ParentTaskID == "" || edge.ChildTaskID == "" {
		return TaskEdge{}, errors.New("parentTaskId and childTaskId are required")
	}
	if edge.ParentTaskID == edge.ChildTaskID {
		return TaskEdge{}, errors.New("task cannot link to itself")
	}
	if edge.Condition == "" {
		edge.Condition = EdgeOnSuccess
	}
	if !ValidEdgeCondition(edge.Condition) {
		return TaskEdge{}, fmt.Errorf("invalid edge condition %q", edge.Condition)
	}
	parent, err := s.getTaskLocked(ctx, edge.ParentTaskID)
	if err != nil {
		return TaskEdge{}, fmt.Errorf("parent task not found: %w", err)
	}
	child, err := s.getTaskLocked(ctx, edge.ChildTaskID)
	if err != nil {
		return TaskEdge{}, fmt.Errorf("child task not found: %w", err)
	}
	if err := CheckChildPrivilege(parent, child); err != nil {
		return TaskEdge{}, err
	}
	edges, err := s.listAllEdgesLocked(ctx)
	if err != nil {
		return TaskEdge{}, err
	}
	childrenMap := map[string][]string{}
	parentsMap := map[string][]string{}
	for _, e := range edges {
		childrenMap[e.ParentTaskID] = append(childrenMap[e.ParentTaskID], e.ChildTaskID)
		parentsMap[e.ChildTaskID] = append(parentsMap[e.ChildTaskID], e.ParentTaskID)
	}
	if len(childrenMap[edge.ParentTaskID]) >= MaxDAGFanOut {
		return TaskEdge{}, fmt.Errorf("parent task fan-out exceeds max %d", MaxDAGFanOut)
	}
	// Cycle detection: BFS from ChildTaskID must never reach ParentTaskID.
	if reachesNode(childrenMap, edge.ChildTaskID, edge.ParentTaskID) {
		return TaskEdge{}, errors.New("cycle detected in task DAG")
	}
	// Depth bound: longest path through this edge must not exceed MaxDAGDepth edges/nodes.
	ancDepth := maxDepthBFS(parentsMap, edge.ParentTaskID)
	descDepth := maxDepthBFS(childrenMap, edge.ChildTaskID)
	if ancDepth+1+descDepth > MaxDAGDepth {
		return TaskEdge{}, fmt.Errorf("task DAG chain depth exceeds max %d", MaxDAGDepth)
	}
	if edge.ID == "" {
		edge.ID = randomHexID("edg_")
	}
	edge.CreatedAt = s.now().Unix()
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO task_edges (id, parent_task_id, child_task_id, condition, payload_map_json, created_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		edge.ID, edge.ParentTaskID, edge.ChildTaskID, string(edge.Condition), edge.PayloadMapJSON, edge.CreatedAt); err != nil {
		return TaskEdge{}, err
	}
	if parent.Tainted && !child.Tainted {
		_, _ = s.db.ExecContext(ctx, `UPDATE tasks SET tainted=1 WHERE id=?`, child.ID)
	}
	return edge, nil
}

func reachesNode(adj map[string][]string, start, target string) bool {
	seen := map[string]bool{start: true}
	q := []string{start}
	for len(q) > 0 {
		cur := q[0]
		q = q[1:]
		if cur == target {
			return true
		}
		for _, nxt := range adj[cur] {
			if !seen[nxt] {
				seen[nxt] = true
				q = append(q, nxt)
			}
		}
	}
	return false
}

func maxDepthBFS(adj map[string][]string, start string) int {
	type item struct {
		id    string
		depth int
	}
	best := 0
	seen := map[string]int{start: 0}
	q := []item{{id: start, depth: 0}}
	for len(q) > 0 {
		cur := q[0]
		q = q[1:]
		if cur.depth > best {
			best = cur.depth
		}
		if best >= MaxDAGDepth+2 {
			return best
		}
		for _, nxt := range adj[cur.id] {
			nd := cur.depth + 1
			if prev, ok := seen[nxt]; !ok || nd > prev {
				seen[nxt] = nd
				q = append(q, item{id: nxt, depth: nd})
			}
		}
	}
	return best
}

func (s *Store) listAllEdgesLocked(ctx context.Context) ([]TaskEdge, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, parent_task_id, child_task_id, condition, payload_map_json, created_at FROM task_edges ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskEdge
	for rows.Next() {
		var e TaskEdge
		var cond string
		if err := rows.Scan(&e.ID, &e.ParentTaskID, &e.ChildTaskID, &cond, &e.PayloadMapJSON, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Condition = EdgeCondition(cond)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListEdges returns edges where taskID is either parent or child (or all edges if taskID is empty).
func (s *Store) ListEdges(ctx context.Context, taskID string) ([]TaskEdge, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if strings.TrimSpace(taskID) == "" {
		return s.listAllEdgesLocked(ctx)
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, parent_task_id, child_task_id, condition, payload_map_json, created_at
FROM task_edges WHERE parent_task_id=? OR child_task_id=? ORDER BY created_at ASC`, taskID, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]TaskEdge, 0)
	for rows.Next() {
		var e TaskEdge
		var cond string
		if err := rows.Scan(&e.ID, &e.ParentTaskID, &e.ChildTaskID, &cond, &e.PayloadMapJSON, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Condition = EdgeCondition(cond)
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeleteEdge removes a DAG edge by id.
func (s *Store) DeleteEdge(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `DELETE FROM task_edges WHERE id=?`, id)
	return err
}

// RecordRun persists a run record and updates task counters / next_run_at atomically.
func (s *Store) RecordRun(ctx context.Context, run TaskRun, updatedHybridAt int64) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.getTaskLocked(ctx, run.TaskID)
	if err != nil {
		return Task{}, err
	}
	if run.ID == "" {
		run.ID = randomHexID("run_")
	}
	taintedInt, sigInt, thrInt := 0, 0, 0
	if run.Tainted {
		taintedInt = 1
	}
	if run.SignalEmitted {
		sigInt = 1
	}
	if run.ThresholdHit {
		thrInt = 1
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO task_runs (
  id, task_id, parent_run_id, trigger_source, exec_mode, status, tainted,
  started_at, finished_at, duration_ms, tokens_in, tokens_out, cost_microusd,
  pnl_microusd, sat_earned_atoms, signal_emitted, threshold_hit, action_taken,
  decision_summary, result_json, error
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.TaskID, run.ParentRunID, run.TriggerSource, string(run.ExecMode), run.Status, taintedInt,
		run.StartedAt, run.FinishedAt, run.DurationMs, run.TokensIn, run.TokensOut, run.CostMicroUSD,
		run.PnLMicroUSD, run.SatEarnedAtoms, sigInt, thrInt, run.ActionTaken,
		run.DecisionSummary, run.ResultJSON, run.Error); err != nil {
		return Task{}, err
	}
	t.RunCount++
	t.LastRunAt = run.FinishedAt
	if t.LastRunAt == 0 {
		t.LastRunAt = run.StartedAt
	}
	if updatedHybridAt > 0 {
		t.LastHybridAt = updatedHybridAt
	}
	if run.Status == "failed" {
		t.ErrorCount++
		t.LastError = run.Error
	} else {
		t.LastError = ""
	}
	if t.Status == StatusActive {
		if t.ScheduleKind == ScheduleOnce || (t.Constraints.MaxRuns > 0 && t.RunCount >= t.Constraints.MaxRuns) {
			t.Status = StatusCompleted
			t.NextRunAt = 0
		} else {
			t.NextRunAt = computeNextRunAt(t, s.now().Unix(), false)
		}
	}
	t.UpdatedAt = s.now().Unix()
	if _, err := s.db.ExecContext(ctx, `
UPDATE tasks SET status=?, last_run_at=?, next_run_at=?, last_hybrid_at=?, run_count=?, error_count=?, last_error=?, updated_at=?
WHERE id=?`,
		string(t.Status), t.LastRunAt, t.NextRunAt, t.LastHybridAt, t.RunCount, t.ErrorCount, t.LastError, t.UpdatedAt, t.ID); err != nil {
		return Task{}, err
	}
	return t, nil
}

// ListRuns returns recent runs for a task.
func (s *Store) ListRuns(ctx context.Context, taskID string, limit int) ([]TaskRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, task_id, parent_run_id, trigger_source, exec_mode, status, tainted,
  started_at, finished_at, duration_ms, tokens_in, tokens_out, cost_microusd,
  pnl_microusd, sat_earned_atoms, signal_emitted, threshold_hit, action_taken,
  decision_summary, result_json, error
FROM task_runs WHERE task_id=? ORDER BY started_at DESC LIMIT ?`, taskID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]TaskRun, 0)
	for rows.Next() {
		var r TaskRun
		var em string
		var taintedInt, sigInt, thrInt int
		if err := rows.Scan(
			&r.ID, &r.TaskID, &r.ParentRunID, &r.TriggerSource, &em, &r.Status, &taintedInt,
			&r.StartedAt, &r.FinishedAt, &r.DurationMs, &r.TokensIn, &r.TokensOut, &r.CostMicroUSD,
			&r.PnLMicroUSD, &r.SatEarnedAtoms, &sigInt, &thrInt, &r.ActionTaken,
			&r.DecisionSummary, &r.ResultJSON, &r.Error,
		); err != nil {
			return nil, err
		}
		r.ExecMode = ExecMode(em)
		r.Tainted = taintedInt != 0
		r.SignalEmitted = sigInt != 0
		r.ThresholdHit = thrInt != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// AppendLog writes a structured telemetry line and prunes old entries above maxLogsPerTask.
func (s *Store) AppendLog(ctx context.Context, entry TaskLog) (TaskLog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry.Timestamp == 0 {
		entry.Timestamp = s.now().Unix()
	}
	if entry.Level == "" {
		entry.Level = "info"
	}
	res, err := s.db.ExecContext(ctx, `
INSERT INTO task_logs (task_id, run_id, seq, ts, level, stage, message, meta_json)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.TaskID, entry.RunID, entry.Seq, entry.Timestamp, entry.Level, entry.Stage, entry.Message, entry.MetaJSON)
	if err != nil {
		return TaskLog{}, err
	}
	entry.ID, _ = res.LastInsertId()
	_, _ = s.db.ExecContext(ctx, `
DELETE FROM task_logs WHERE task_id=? AND id NOT IN (
  SELECT id FROM task_logs WHERE task_id=? ORDER BY id DESC LIMIT ?
)`, entry.TaskID, entry.TaskID, maxLogsPerTask)
	return entry, nil
}

// ListLogs returns bounded telemetry logs for a task ordered oldest-to-newest within recent window.
func (s *Store) ListLogs(ctx context.Context, taskID string, limit int) ([]TaskLog, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > maxLogsPerTask {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, task_id, run_id, seq, ts, level, stage, message, meta_json
FROM task_logs WHERE task_id=? ORDER BY id DESC LIMIT ?`, taskID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rev []TaskLog
	for rows.Next() {
		var l TaskLog
		if err := rows.Scan(&l.ID, &l.TaskID, &l.RunID, &l.Seq, &l.Timestamp, &l.Level, &l.Stage, &l.Message, &l.MetaJSON); err != nil {
			return nil, err
		}
		rev = append(rev, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]TaskLog, len(rev))
	for i := range rev {
		out[i] = rev[len(rev)-1-i]
	}
	return out, nil
}

// ListLibraries returns all seeded + custom personas, skills, and task templates.
func (s *Store) ListLibraries(ctx context.Context) (TaskLibraries, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var libs TaskLibraries
	libs.Personas = []TaskPersona{}
	libs.Skills = []TaskSkill{}
	libs.Templates = []TaskTemplate{}

	pRows, err := s.db.QueryContext(ctx, `SELECT id, name, role, goal_domains_json, system_prompt, risk_profile, builtin, created_at FROM task_personas ORDER BY builtin DESC, name ASC`)
	if err != nil {
		return libs, err
	}
	for pRows.Next() {
		var p TaskPersona
		var domJSON string
		var bi int
		if err := pRows.Scan(&p.ID, &p.Name, &p.Role, &domJSON, &p.SystemPrompt, &p.RiskProfile, &bi, &p.CreatedAt); err != nil {
			_ = pRows.Close()
			return libs, err
		}
		p.BuiltIn = bi != 0
		_ = json.Unmarshal([]byte(domJSON), &p.GoalDomains)
		libs.Personas = append(libs.Personas, p)
	}
	_ = pRows.Close()

	sRows, err := s.db.QueryContext(ctx, `SELECT id, name, goal_domain, instructions, allowed_tools_json, source, sha256, builtin, created_at FROM task_skills ORDER BY builtin DESC, goal_domain ASC, name ASC`)
	if err != nil {
		return libs, err
	}
	for sRows.Next() {
		var sk TaskSkill
		var gd, toolsJSON string
		var bi int
		if err := sRows.Scan(&sk.ID, &sk.Name, &gd, &sk.Instructions, &toolsJSON, &sk.Source, &sk.SHA256, &bi, &sk.CreatedAt); err != nil {
			_ = sRows.Close()
			return libs, err
		}
		sk.GoalDomain = GoalDomain(gd)
		sk.BuiltIn = bi != 0
		_ = json.Unmarshal([]byte(toolsJSON), &sk.AllowedTools)
		libs.Skills = append(libs.Skills, sk)
	}
	_ = sRows.Close()

	tRows, err := s.db.QueryContext(ctx, `SELECT id, name, description, goal_domain, exec_mode, default_task_json, builtin, created_at FROM task_templates ORDER BY builtin DESC, goal_domain ASC, name ASC`)
	if err != nil {
		return libs, err
	}
	for tRows.Next() {
		var tpl TaskTemplate
		var gd, em, defJSON string
		var bi int
		if err := tRows.Scan(&tpl.ID, &tpl.Name, &tpl.Description, &gd, &em, &defJSON, &bi, &tpl.CreatedAt); err != nil {
			_ = tRows.Close()
			return libs, err
		}
		tpl.GoalDomain = GoalDomain(gd)
		tpl.ExecMode = ExecMode(em)
		tpl.BuiltIn = bi != 0
		_ = json.Unmarshal([]byte(defJSON), &tpl.DefaultTask)
		libs.Templates = append(libs.Templates, tpl)
	}
	_ = tRows.Close()
	return libs, nil
}

// GetTemplate loads a template by ID.
func (s *Store) GetTemplate(ctx context.Context, id string) (TaskTemplate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var tpl TaskTemplate
	var gd, em, defJSON string
	var bi int
	err := s.db.QueryRowContext(ctx, `SELECT id, name, description, goal_domain, exec_mode, default_task_json, builtin, created_at FROM task_templates WHERE id=?`, id).
		Scan(&tpl.ID, &tpl.Name, &tpl.Description, &gd, &em, &defJSON, &bi, &tpl.CreatedAt)
	if err != nil {
		return TaskTemplate{}, err
	}
	tpl.GoalDomain = GoalDomain(gd)
	tpl.ExecMode = ExecMode(em)
	tpl.BuiltIn = bi != 0
	_ = json.Unmarshal([]byte(defJSON), &tpl.DefaultTask)
	return tpl, nil
}

func computeNextRunAt(t Task, nowUnix int64, activating bool) int64 {
	switch t.ScheduleKind {
	case ScheduleContinuous:
		sec := t.ScheduleSpec.IntervalSec
		if sec <= 0 {
			sec = 10
		}
		if activating {
			return nowUnix
		}
		return nowUnix + sec
	case ScheduleInterval:
		sec := t.ScheduleSpec.IntervalSec
		if sec <= 0 {
			sec = 60
		}
		if activating {
			return nowUnix
		}
		return nowUnix + sec
	case ScheduleCron:
		if activating && t.LastRunAt == 0 {
			return nowUnix + 60
		}
		return nowUnix + 60
	case ScheduleOnce:
		if t.ScheduleSpec.RunAt > 0 {
			return t.ScheduleSpec.RunAt
		}
		return nowUnix
	case ScheduleEvent:
		return 0
	default:
		return 0
	}
}
