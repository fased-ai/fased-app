package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func init() {
	taskRunsV2Mirror = func(ctx context.Context, s *Store, d agentDocument, beforeRuns int) {
		s.mirrorNewTaskRunsToV2(ctx, d, beforeRuns)
	}
}

func shouldMirrorTaskRunToV2(run AgentRun) bool {
	if run.Strategy == "news-gate-v1" || run.WEN != nil || run.BotID != "" || run.Swap != nil || run.WENMining != nil {
		return false
	}
	if run.Paper != nil || run.Watch != nil || run.Analysis != nil {
		return true
	}
	switch run.Strategy {
	case "reminder-v1", "ai-step-v1/reminder-v1", "ai-step-v1", "analysis-v1", "news-watch-v1", "paper-limit-buy-v1":
		return true
	default:
		return false
	}
}

func formatScheduledTaskRunMessage(d agentDocument, task AgentTask, run AgentRun) string {
	kindLabel := "Reminder"
	switch {
	case run.Paper != nil || task.StrategyPlan != nil:
		kindLabel = "Paper strategy"
	case run.Watch != nil:
		kindLabel = "News watch"
	case typedAnalysis(task) || run.Analysis != nil:
		kindLabel = "Analysis"
	}

	taskName := sanitizeV2Title(task.Name, "Scheduled task")
	var aiAnswer string
	if run.AIRequestID != "" {
		for _, q := range d.AIRequests {
			if q.ID == run.AIRequestID && q.State == "completed" && strings.TrimSpace(q.Answer) != "" {
				if !typedAnalysis(task) || (run.Analysis != nil && run.Analysis.State == "completed") {
					aiAnswer = strings.TrimSpace(q.Answer)
				}
				break
			}
		}
	}

	var noticeText string
	for _, n := range d.Notices {
		if n.RunID == run.ID && strings.TrimSpace(n.Text) != "" {
			noticeText = strings.TrimSpace(n.Text)
			break
		}
	}

	var sections []string
	sections = append(sections, fmt.Sprintf("**%s** · %s", taskName, kindLabel))

	switch {
	case run.Paper != nil:
		var lines []string
		lines = append(lines, fmt.Sprintf("Paper outcome: **%s**", run.Outcome))
		if run.Paper.Market != "" {
			lines = append(lines, fmt.Sprintf("Market: `%s` · Source: `%s`", run.Paper.Market, run.Paper.Source))
		}
		sections = append(sections, strings.Join(lines, "\n\n"))

	case run.Watch != nil:
		sections = append(sections, fmt.Sprintf("New matches: %d", len(run.Watch.NewIDs)))
		if len(run.Watch.NewIDs) > 0 && run.News != nil {
			titleByID := map[string]string{}
			for _, f := range d.NewsFetches {
				if f.ID == run.News.FetchID && f.ProfileID == run.ProfileID {
					for _, item := range f.Items {
						titleByID[item.ID] = sanitizeV2Title(item.Title, item.ID)
					}
					break
				}
			}
			var bullets []string
			for i, itemID := range run.Watch.NewIDs {
				if i >= 8 {
					bullets = append(bullets, fmt.Sprintf("- +%d more headlines", len(run.Watch.NewIDs)-8))
					break
				}
				headline := titleByID[itemID]
				if headline == "" {
					headline = itemID
				}
				bullets = append(bullets, "- "+headline)
			}
			if len(bullets) > 0 {
				sections = append(sections, strings.Join(bullets, "\n"))
			}
		}
		if aiAnswer != "" {
			sections = append(sections, aiAnswer)
		} else if run.Analysis != nil && run.Analysis.State == "skipped" {
			if run.Analysis.Reason != "" {
				sections = append(sections, fmt.Sprintf("Analysis skipped (%s)", run.Analysis.Reason))
			} else {
				sections = append(sections, "Analysis skipped")
			}
		}

	case typedAnalysis(task) || run.Analysis != nil:
		if aiAnswer != "" {
			sections = append(sections, aiAnswer)
		} else if run.Analysis != nil && run.Analysis.State == "skipped" {
			if run.Analysis.Reason != "" {
				sections = append(sections, fmt.Sprintf("Analysis skipped (%s)", run.Analysis.Reason))
			} else {
				sections = append(sections, "Analysis skipped")
			}
		} else if noticeText != "" {
			sections = append(sections, noticeText)
		} else if strings.TrimSpace(task.Text) != "" {
			sections = append(sections, strings.TrimSpace(task.Text))
		}

	default:
		if strings.TrimSpace(task.Text) != "" {
			sections = append(sections, strings.TrimSpace(task.Text))
		}
		if aiAnswer != "" {
			sections = append(sections, aiAnswer)
		} else if run.Outcome != "reminded" && noticeText != "" {
			sections = append(sections, noticeText)
		}
	}

	sections = append(sections, fmt.Sprintf("Run `%s` · `%s`", run.ID, run.Outcome))
	return strings.Join(sections, "\n\n")
}

// mirrorNewTaskRunsToV2 mirrors newly committed scheduled task runs into
// per-task Agent V2 threads so owners can follow up in AgentChatV2.
// Failures are best-effort so committed core.sqlite task runs never fail.
func (s *Store) mirrorNewTaskRunsToV2(ctx context.Context, d agentDocument, beforeRuns int) {
	if s == nil || s.dir == "" || beforeRuns < 0 || len(d.Runs) <= beforeRuns {
		return
	}
	var pending []AgentRun
	for _, run := range d.Runs[beforeRuns:] {
		if shouldMirrorTaskRunToV2(run) {
			pending = append(pending, run)
		}
	}
	if len(pending) == 0 {
		return
	}

	s.agentV2MirrorMu.Lock()
	defer s.agentV2MirrorMu.Unlock()

	baseCtx := ctx
	if baseCtx == nil || baseCtx.Err() != nil {
		baseCtx = context.Background()
	}
	mCtx, cancel := context.WithTimeout(baseCtx, 3*time.Second)
	defer cancel()

	v2, err := s.ensureAgentV2(mCtx)
	if err != nil {
		return
	}

	type taskKey struct{ profileID, taskID string }
	tasks := make(map[taskKey]AgentTask, len(d.Tasks))
	for _, t := range d.Tasks {
		tasks[taskKey{profileID: t.ProfileID, taskID: t.ID}] = t
	}

	for _, run := range pending {
		task := tasks[taskKey{profileID: run.ProfileID, taskID: run.TaskID}]
		_ = mirrorSingleTaskRunToV2(mCtx, v2, d, task, run, s.now().Unix())
	}
}

func mirrorSingleTaskRunToV2(ctx context.Context, v2 *agentV2Store, d agentDocument, task AgentTask, run AgentRun, now int64) error {
	tx, err := v2.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var already int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM task_run_threads WHERE run_id=?`, run.ID).Scan(&already); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	runAt := run.At
	if runAt <= 0 {
		runAt = now
	}
	profileID := strings.TrimSpace(run.ProfileID)
	if profileID == "" {
		profileID = task.ProfileID
	}
	if profileID == "" {
		profileID = "wen-operator"
	}

	var connRef, taskModel string
	if task.AI != nil {
		if validConnectionRef(task.AI.ConnectionRef) {
			connRef = task.AI.ConnectionRef
		}
		if validProviderModelID(task.AI.Model) {
			taskModel = task.AI.Model
		}
	}

	var threadID string
	err = tx.QueryRowContext(ctx, `
SELECT id FROM threads
WHERE profile_id=? AND source_kind='task' AND source_ref=? AND archived=0
ORDER BY updated_at DESC LIMIT 1`,
		profileID, run.TaskID,
	).Scan(&threadID)
	if errors.Is(err, sql.ErrNoRows) {
		threadID, err = randomToken()
		if err != nil {
			return err
		}
		title := sanitizeV2Title(task.Name, "Scheduled task")
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO threads(id,profile_id,title,connection_ref,model,summary,source_kind,source_ref,archived,created_at,updated_at) VALUES(?,?,?,?,?,'','task',?,0,?,?)`,
			threadID, profileID, title, connRef, taskModel, run.TaskID, runAt, runAt,
		); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	var maxSeq int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM messages WHERE thread_id=?`, threadID).Scan(&maxSeq); err != nil {
		return err
	}
	msgID, err := randomToken()
	if err != nil {
		return err
	}
	content := formatScheduledTaskRunMessage(d, task, run)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO messages(id,thread_id,seq,role,content,reasoning,status,model,provider,call_id,tool_name,input_tokens,output_tokens,cached_tokens,error_text,created_at)
		 VALUES(?,?,?,'assistant',?,'','completed',?,'scheduler','','',0,0,0,'',?)`,
		msgID, threadID, maxSeq+1, content, taskModel, runAt,
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE threads SET updated_at=? WHERE id=?`, runAt, threadID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO task_run_threads(run_id,task_id,thread_id,message_id,created_at) VALUES(?,?,?,?,?)`,
		run.ID, run.TaskID, threadID, msgID, runAt,
	); err != nil {
		return err
	}
	return tx.Commit()
}
