import { Pause, Play, RefreshCw, Shield, Square, Trash2, Zap } from "../icons";
import {
  formatTaskSchedule,
  needsStepUpToActivate,
  statusPillTone,
  type TaskDetail,
  type UniversalTask,
} from "../tasks-v2";
import { useUI } from "../ui-preferences";

function fmtTime(epochSec?: number): string {
  if (!epochSec) return "—";
  return new Date(epochSec * 1000).toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

export function TaskRunInspector({
  task,
  detail,
  busyId,
  onActivate,
  onPause,
  onStop,
  onRunNow,
  onDelete,
  onRefresh,
  onSelectTask,
}: {
  task: UniversalTask | null;
  detail: TaskDetail | null;
  busyId: string;
  onActivate: (task: UniversalTask) => void;
  onPause: (task: UniversalTask) => void;
  onStop: (task: UniversalTask) => void;
  onRunNow: (task: UniversalTask) => void;
  onDelete: (task: UniversalTask) => void;
  onRefresh: () => void;
  onSelectTask: (id: string) => void;
}) {
  const { t } = useUI();

  if (!task) {
    return (
      <aside
        className="f-panel wt-task-inspector"
        aria-label={t("Task inspector")}
      >
        <div className="wt-tasks-empty">
          {t("Select a task to inspect runs, DAG edges, and telemetry.")}
        </div>
      </aside>
    );
  }

  const busy = busyId === task.id;
  const runs = detail?.runs ?? [];
  const logs = detail?.logs ?? [];
  const edges = detail?.edges ?? [];

  return (
    <aside
      className="f-panel wt-task-inspector"
      aria-label={t("Task inspector")}
    >
      <div className="wt-inspector-head">
        <div className="wt-inspector-title-wrap">
          <h2>{task.name}</h2>
          <div className="wt-inspector-badges">
            <span className={`f-pill ${statusPillTone(task.status)}`}>
              {task.status}
            </span>
            <span className="f-pill">{task.goalDomain}</span>
            <span className="f-pill">{task.execMode}</span>
            <span className="f-pill">{task.approvalPolicy}</span>
            {task.tainted && (
              <span className="f-pill is-warn">{t("Tainted")}</span>
            )}
          </div>
        </div>
        <div className="wt-inspector-actions">
          <button
            type="button"
            className="f-icon-btn"
            aria-label={t("Run now")}
            title={t("Run now")}
            disabled={busy || task.status === "killed"}
            onClick={() => onRunNow(task)}
          >
            <Zap size={14} aria-hidden="true" />
          </button>
          {task.status === "active" ? (
            <button
              type="button"
              className="f-icon-btn"
              aria-label={t("Pause")}
              title={t("Pause")}
              disabled={busy}
              onClick={() => onPause(task)}
            >
              <Pause size={14} aria-hidden="true" />
            </button>
          ) : (
            <button
              type="button"
              className="f-icon-btn"
              aria-label={t("Activate")}
              title={
                needsStepUpToActivate(task)
                  ? t("Activate (requires step-up)")
                  : t("Activate")
              }
              disabled={busy}
              onClick={() => onActivate(task)}
            >
              <Play size={14} aria-hidden="true" />
            </button>
          )}
          <button
            type="button"
            className="f-icon-btn"
            aria-label={t("Stop")}
            title={t("Stop")}
            disabled={busy || task.status === "stopped"}
            onClick={() => onStop(task)}
          >
            <Square size={13} aria-hidden="true" />
          </button>
          <button
            type="button"
            className="f-icon-btn"
            aria-label={t("Refresh")}
            title={t("Refresh")}
            disabled={busy}
            onClick={onRefresh}
          >
            <RefreshCw size={14} aria-hidden="true" />
          </button>
          <button
            type="button"
            className="f-icon-btn"
            aria-label={t("Delete task")}
            title={t("Delete task")}
            disabled={busy}
            onClick={() => onDelete(task)}
          >
            <Trash2 size={14} aria-hidden="true" />
          </button>
        </div>
      </div>

      {task.goalObjective && (
        <p className="wt-inspector-objective">{task.goalObjective}</p>
      )}

      <div className="f-kpi-grid wt-inspector-kpis">
        <div className="wt-kpi-box">
          <span>{t("Schedule")}</span>
          <strong className="f-mono">{formatTaskSchedule(task)}</strong>
        </div>
        <div className="wt-kpi-box">
          <span>{t("Runs / Errors")}</span>
          <strong className="f-mono">
            {task.runCount} / {task.errorCount}
          </strong>
        </div>
        <div className="wt-kpi-box">
          <span>{t("Binding")}</span>
          <strong className="f-mono">
            {task.executionBinding?.walletHandle ||
              [task.executionBinding?.venue, task.executionBinding?.symbol]
                .filter(Boolean)
                .join(" ") ||
              t("Internal")}
          </strong>
        </div>
        <div className="wt-kpi-box">
          <span>{t("Persona / Strategy")}</span>
          <strong className="f-mono">
            {[task.attachments?.personaId, task.attachments?.strategyId]
              .filter(Boolean)
              .join(" · ") || "—"}
          </strong>
        </div>
      </div>

      {needsStepUpToActivate(task) && task.status !== "active" && (
        <div className="wt-stepup-note">
          <Shield size={13} aria-hidden="true" />
          <span>{t("Owner step-up required before live activation")}</span>
        </div>
      )}

      {edges.length > 0 && (
        <div className="wt-inspector-section">
          <h3>{t("DAG Edges")}</h3>
          <div className="f-table">
            {edges.map((edge) => {
              const peer =
                edge.parentTaskId === task.id
                  ? edge.childTaskId
                  : edge.parentTaskId;
              const dir = edge.parentTaskId === task.id ? "→" : "←";
              return (
                <button
                  key={edge.id}
                  type="button"
                  className="f-row wt-edge-row"
                  onClick={() => onSelectTask(peer)}
                >
                  <span className="f-mono">
                    {dir} {peer}
                  </span>
                  <span className="f-pill">{edge.condition}</span>
                </button>
              );
            })}
          </div>
        </div>
      )}

      <div className="wt-inspector-section">
        <h3>
          {t("Recent runs")} ({runs.length})
        </h3>
        {runs.length === 0 ? (
          <div className="wt-tasks-empty-inline">{t("No executions yet.")}</div>
        ) : (
          <div className="f-table wt-runs-table">
            {runs.slice(0, 12).map((run) => (
              <div key={run.id} className="f-row wt-run-row">
                <div className="wt-run-main">
                  <span className={`f-pill ${statusPillTone(run.status)}`}>
                    {run.status}
                  </span>
                  <span className="wt-run-summary">
                    {run.decisionSummary ||
                      run.actionTaken ||
                      run.triggerSource}
                  </span>
                </div>
                <div className="wt-run-meta f-mono">
                  <span>{run.durationMs}ms</span>
                  <span>{fmtTime(run.finishedAt || run.startedAt)}</span>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      <div className="wt-inspector-section">
        <h3>
          {t("Telemetry logs")} ({logs.length})
        </h3>
        {logs.length === 0 ? (
          <div className="wt-tasks-empty-inline">{t("No log lines yet.")}</div>
        ) : (
          <div className="wt-log-stream f-mono">
            {logs.slice(0, 30).map((entry) => (
              <div key={entry.id} className="wt-log-line">
                <span className="wt-log-ts">{fmtTime(entry.timestamp)}</span>
                <span className={`wt-log-lvl is-${entry.level}`}>
                  {entry.level}
                </span>
                <span className="wt-log-stage">{entry.stage}</span>
                <span className="wt-log-msg">{entry.message}</span>
              </div>
            ))}
          </div>
        )}
      </div>
    </aside>
  );
}
