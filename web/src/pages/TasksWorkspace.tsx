import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Dialog } from "../components/shared";
import {
  Bot,
  Layers,
  Pause,
  Play,
  Plus,
  RefreshCw,
  Shield,
  SlidersHorizontal,
  Trash2,
  Zap,
} from "../icons";
import {
  activateUniversalTask,
  createTaskSwarm,
  createUniversalTask,
  deleteUniversalTask,
  formatTaskSchedule,
  getTaskDetail,
  instantiateTaskTemplate,
  killAllUniversalTasks,
  listTaskLibraries,
  listUniversalTasks,
  needsStepUpToActivate,
  pauseUniversalTask,
  requestTaskStepUpToken,
  runUniversalTaskNow,
  statusPillTone,
  stopUniversalTask,
  TASK_GOAL_FILTERS,
  type GoalDomain,
  type SwarmRequest,
  type TaskDetail,
  type TaskEdge,
  type TaskLibraries,
  type TaskTemplate,
  type UniversalTask,
} from "../tasks-v2";
import { useUI } from "../ui-preferences";
import { listWalletsV2, type WalletV2 } from "../wallets-v2";
import { useWorkspace } from "../workspace";
import { TaskComposerModal, type ComposerMode } from "./TaskComposerModal";
import { TaskDAGView } from "./TaskDAGView";
import { TaskLibraryDrawer } from "./TaskLibraryDrawer";
import { TaskRunInspector } from "./TaskRunInspector";
import "./TasksWorkspace.css";

type TasksViewMode = "list" | "dag";

export function TasksWorkspace() {
  const { t } = useUI();
  const { core, streamClient } = useWorkspace();

  const [tasks, setTasks] = useState<UniversalTask[]>([]);
  const [edges, setEdges] = useState<TaskEdge[]>([]);
  const [libraries, setLibraries] = useState<TaskLibraries | null>(null);
  const [internalWallets, setInternalWallets] = useState<WalletV2[]>([]);
  const [selectedId, setSelectedId] = useState<string>("");
  const [detail, setDetail] = useState<TaskDetail | null>(null);

  const [goalFilter, setGoalFilter] = useState<"" | GoalDomain>("");
  const [search, setSearch] = useState("");
  const [viewMode, setViewMode] = useState<TasksViewMode>("list");
  const [composerMode, setComposerMode] = useState<ComposerMode | null>(null);
  const [drawerOpen, setDrawerOpen] = useState(false);

  const [busyId, setBusyId] = useState("");
  const [busyTemplateId, setBusyTemplateId] = useState("");
  const [error, setError] = useState("");
  const [stepUpTarget, setStepUpTarget] = useState<UniversalTask | null>(null);
  const [stepUpPassword, setStepUpPassword] = useState("");
  const [stepUpError, setStepUpError] = useState("");

  const loadTasksAndLibraries = useCallback(async () => {
    if (core.mode !== "ready") return;
    setError("");
    try {
      const taskList = await listUniversalTasks(core, {
        goal: goalFilter,
        q: search.trim(),
        limit: 200,
      });
      setTasks(taskList);
      setSelectedId((prev) => {
        if (prev && taskList.some((item) => item.id === prev)) return prev;
        return taskList[0]?.id ?? "";
      });
    } catch (err) {
      setError(err instanceof Error ? err.message : t("Failed to load tasks"));
    }
  }, [core, goalFilter, search, t]);

  useEffect(() => {
    if (core.mode !== "ready") return;
    let cancelled = false;
    void listTaskLibraries(core)
      .then((libs) => {
        if (!cancelled) setLibraries(libs);
      })
      .catch(() => {});
    void listWalletsV2(core)
      .then((res) => {
        if (!cancelled) {
          setInternalWallets(
            (res.wallets ?? []).filter(
              (w) =>
                w.custody === "native_signer" &&
                w.role !== "vault" &&
                w.role !== "watch" &&
                !w.policy.frozen,
            ),
          );
        }
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [core]);

  const loadSelectedDetail = useCallback(async () => {
    if (core.mode !== "ready" || !selectedId) {
      setDetail(null);
      return;
    }
    try {
      const res = await getTaskDetail(core, selectedId);
      setDetail(res);
    } catch {
      // Task may have been deleted; list reload resolves cleanly.
    }
  }, [core, selectedId]);

  const loadAllEdges = useCallback(async () => {
    if (core.mode !== "ready" || tasks.length === 0) {
      setEdges([]);
      return;
    }
    const edgeSets = await Promise.all(
      tasks.slice(0, 40).map((item) =>
        getTaskDetail(core, item.id)
          .then((d) => d.edges ?? [])
          .catch(() => [] as TaskEdge[]),
      ),
    );
    const seen = new Map<string, TaskEdge>();
    for (const set of edgeSets) {
      for (const edge of set) seen.set(edge.id, edge);
    }
    setEdges(Array.from(seen.values()));
  }, [core, tasks]);

  useEffect(() => {
    void loadTasksAndLibraries();
  }, [loadTasksAndLibraries]);

  useEffect(() => {
    void loadSelectedDetail();
  }, [loadSelectedDetail]);

  useEffect(() => {
    if (viewMode === "dag") void loadAllEdges();
  }, [viewMode, loadAllEdges]);

  const reloadRef = useRef({
    loadTasksAndLibraries,
    loadSelectedDetail,
  });
  reloadRef.current = { loadTasksAndLibraries, loadSelectedDetail };

  useEffect(() => {
    if (!streamClient || core.mode !== "ready") return;
    let timer: number | undefined;
    const unsub = streamClient.subscribe("tasks", () => {
      if (timer !== undefined) window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        timer = undefined;
        void reloadRef.current.loadTasksAndLibraries();
        void reloadRef.current.loadSelectedDetail();
      }, 200);
    });
    return () => {
      if (timer !== undefined) window.clearTimeout(timer);
      unsub();
    };
  }, [streamClient, core.mode]);

  const selectedTask = useMemo(
    () => tasks.find((item) => item.id === selectedId) ?? detail?.task ?? null,
    [tasks, selectedId, detail],
  );

  const metrics = useMemo(() => {
    let active = 0;
    let runs = 0;
    let errors = 0;
    for (const item of tasks) {
      if (item.status === "active") active++;
      runs += item.runCount ?? 0;
      errors += item.errorCount ?? 0;
    }
    return { total: tasks.length, active, runs, errors };
  }, [tasks]);

  async function handleCreateTask(input: Partial<UniversalTask>) {
    const created = await createUniversalTask(core, input);
    await loadTasksAndLibraries();
    setSelectedId(created.id);
  }

  async function handleCreateSwarm(req: SwarmRequest) {
    const res = await createTaskSwarm(core, req);
    await loadTasksAndLibraries();
    setSelectedId(res.rootTask.id);
    setViewMode("dag");
  }

  async function handleInstantiateTemplate(tpl: TaskTemplate) {
    setBusyTemplateId(tpl.id);
    setError("");
    try {
      const created = await instantiateTaskTemplate(core, tpl.id, {
        status: "draft",
      });
      await loadTasksAndLibraries();
      setSelectedId(created.id);
      setDrawerOpen(false);
    } catch (err) {
      setError(
        err instanceof Error
          ? err.message
          : t("Failed to instantiate template"),
      );
    } finally {
      setBusyTemplateId("");
    }
  }

  async function handleActivate(task: UniversalTask, token?: string) {
    setBusyId(task.id);
    setError("");
    try {
      await activateUniversalTask(core, task.id, token);
      await loadTasksAndLibraries();
      await loadSelectedDetail();
      setStepUpTarget(null);
      setStepUpPassword("");
    } catch (err) {
      const msg = err instanceof Error ? err.message : t("Activation refused");
      if (
        !token &&
        (needsStepUpToActivate(task) ||
          msg.toLowerCase().includes("step-up") ||
          msg.toLowerCase().includes("step_up"))
      ) {
        setStepUpError("");
        setStepUpTarget(task);
      } else {
        setError(msg);
      }
    } finally {
      setBusyId("");
    }
  }

  async function submitStepUpActivation() {
    if (!stepUpTarget || !stepUpPassword.trim()) {
      setStepUpError(t("Owner password is required."));
      return;
    }
    setStepUpError("");
    try {
      const token = await requestTaskStepUpToken(
        core,
        stepUpPassword,
        stepUpTarget.id,
      );
      await handleActivate(stepUpTarget, token);
    } catch (err) {
      setStepUpError(
        err instanceof Error ? err.message : t("Step-up verification failed"),
      );
    }
  }

  async function handlePause(task: UniversalTask) {
    setBusyId(task.id);
    setError("");
    try {
      await pauseUniversalTask(core, task.id, "Paused by owner");
      await loadTasksAndLibraries();
      await loadSelectedDetail();
    } catch (err) {
      setError(err instanceof Error ? err.message : t("Failed to pause task"));
    } finally {
      setBusyId("");
    }
  }

  async function handleStop(task: UniversalTask) {
    setBusyId(task.id);
    setError("");
    try {
      await stopUniversalTask(core, task.id);
      await loadTasksAndLibraries();
      await loadSelectedDetail();
    } catch (err) {
      setError(err instanceof Error ? err.message : t("Failed to stop task"));
    } finally {
      setBusyId("");
    }
  }

  async function handleRunNow(task: UniversalTask) {
    setBusyId(task.id);
    setError("");
    try {
      await runUniversalTaskNow(core, task.id);
      await loadTasksAndLibraries();
      await loadSelectedDetail();
    } catch (err) {
      setError(err instanceof Error ? err.message : t("Run failed"));
    } finally {
      setBusyId("");
    }
  }

  async function handleDelete(task: UniversalTask) {
    setBusyId(task.id);
    setError("");
    try {
      await deleteUniversalTask(core, task.id);
      if (selectedId === task.id) setSelectedId("");
      await loadTasksAndLibraries();
    } catch (err) {
      setError(err instanceof Error ? err.message : t("Delete failed"));
    } finally {
      setBusyId("");
    }
  }

  async function handleKillAll() {
    setBusyId("__kill_all__");
    setError("");
    try {
      await killAllUniversalTasks(core);
      await loadTasksAndLibraries();
      await loadSelectedDetail();
    } catch (err) {
      setError(err instanceof Error ? err.message : t("Kill switch failed"));
    } finally {
      setBusyId("");
    }
  }

  if (core.mode !== "ready") {
    return (
      <section className="f-panel wt-tasks-shell" aria-label={t("Tasks")}>
        <div className="wt-tasks-topbar">
          <h1>{t("Tasks")}</h1>
        </div>
        <div className="wt-tasks-empty">
          {t(
            "Sign in to manage Universal Task Engine tasks, swarms, and libraries.",
          )}
        </div>
      </section>
    );
  }

  return (
    <section className="wt-tasks-shell" aria-label={t("Tasks")}>
      <div className="f-panel wt-tasks-topbar">
        <div className="wt-tasks-title-group">
          <h1>{t("Tasks")}</h1>
          <div className="wt-tasks-stats f-mono">
            <span>
              {metrics.active}/{metrics.total} {t("Active")}
            </span>
            <span>
              {metrics.runs} {t("Runs")}
            </span>
            {metrics.errors > 0 && (
              <span className="is-danger">
                {metrics.errors} {t("Errors")}
              </span>
            )}
          </div>
        </div>

        <div className="wt-tasks-actions">
          <div
            className="f-seg wt-view-seg"
            role="group"
            aria-label={t("View mode")}
          >
            <button
              type="button"
              aria-pressed={viewMode === "list"}
              onClick={() => setViewMode("list")}
            >
              {t("List")}
            </button>
            <button
              type="button"
              aria-pressed={viewMode === "dag"}
              onClick={() => setViewMode("dag")}
            >
              {t("Swarm DAG")}
            </button>
          </div>

          <button
            type="button"
            className="f-icon-btn"
            aria-label={t("Libraries")}
            title={t("Templates, Personas & Skills")}
            onClick={() => setDrawerOpen(true)}
          >
            <Layers size={14} aria-hidden="true" />
          </button>
          <button
            type="button"
            className="f-icon-btn"
            aria-label={t("Refresh")}
            title={t("Refresh")}
            onClick={() => void loadTasksAndLibraries()}
          >
            <RefreshCw size={14} aria-hidden="true" />
          </button>
          <button
            type="button"
            className="wt-btn-soft wt-kill-btn"
            disabled={busyId === "__kill_all__"}
            onClick={() => void handleKillAll()}
          >
            <Shield size={13} aria-hidden="true" />
            <span>{t("Kill All")}</span>
          </button>
          <button
            type="button"
            className="wt-btn-soft"
            onClick={() => setComposerMode("swarm")}
          >
            <Bot size={13} aria-hidden="true" />
            <span>{t("New Swarm")}</span>
          </button>
          <button
            type="button"
            className="wt-btn-rose-sm"
            onClick={() => setComposerMode("single")}
          >
            <Plus size={14} aria-hidden="true" />
            <span>{t("New Task")}</span>
          </button>
        </div>
      </div>

      <div className="f-panel wt-tasks-filterbar">
        <div
          className="wt-goal-filters"
          role="group"
          aria-label={t("Goal filters")}
        >
          {TASK_GOAL_FILTERS.map((f) => (
            <button
              key={f.id || "all"}
              type="button"
              aria-pressed={goalFilter === f.id}
              className={`wt-filter-chip ${goalFilter === f.id ? "is-selected" : ""}`}
              onClick={() => setGoalFilter(f.id)}
            >
              {t(f.label)}
            </button>
          ))}
        </div>
        <div className="wt-tasks-search-wrap">
          <SlidersHorizontal size={13} aria-hidden="true" />
          <input
            type="search"
            className="wt-tasks-search"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={t("Filter tasks…")}
            aria-label={t("Filter tasks")}
          />
        </div>
      </div>

      {error && (
        <div className="wt-tasks-error" role="alert">
          {error}
        </div>
      )}

      <div className="wt-tasks-grid">
        <div className="f-panel wt-tasks-main">
          {viewMode === "dag" ? (
            <TaskDAGView
              tasks={tasks}
              edges={edges}
              selectedId={selectedId}
              onSelect={setSelectedId}
            />
          ) : tasks.length === 0 ? (
            <div className="wt-tasks-empty">
              <p>{t("No tasks matching this filter.")}</p>
              <div className="wt-empty-actions">
                <button
                  type="button"
                  className="wt-btn-soft"
                  onClick={() => setDrawerOpen(true)}
                >
                  {t("Browse Templates")}
                </button>
                <button
                  type="button"
                  className="wt-btn-rose-sm"
                  onClick={() => setComposerMode("single")}
                >
                  {t("New Task")}
                </button>
              </div>
            </div>
          ) : (
            <div
              className="f-table wt-task-rows"
              role="table"
              aria-label={t("Tasks")}
            >
              {tasks.map((task) => {
                const selected = task.id === selectedId;
                const rowBusy = busyId === task.id;
                return (
                  <div
                    key={task.id}
                    role="row"
                    className={`f-row wt-task-row ${selected ? "is-selected" : ""}`}
                  >
                    <button
                      type="button"
                      className="wt-task-row-select"
                      onClick={() => setSelectedId(task.id)}
                    >
                      <div className="wt-task-row-head">
                        <strong className="wt-task-name">{task.name}</strong>
                        <span
                          className={`f-pill ${statusPillTone(task.status)}`}
                        >
                          {task.status}
                        </span>
                        <span className="f-pill">{task.goalDomain}</span>
                        <span className="f-pill">{task.execMode}</span>
                        {task.swarmGroupId && (
                          <span className="f-pill">{t("Swarm")}</span>
                        )}
                      </div>
                      <div className="wt-task-row-sub f-mono">
                        <span>{formatTaskSchedule(task)}</span>
                        <span>{task.approvalPolicy}</span>
                        <span>
                          {task.runCount} {t("runs")}
                        </span>
                        {task.goalObjective && (
                          <span className="wt-task-obj">
                            {task.goalObjective}
                          </span>
                        )}
                      </div>
                    </button>

                    <div className="wt-task-row-controls">
                      <button
                        type="button"
                        className="f-icon-btn"
                        aria-label={`${t("Run now")}: ${task.name}`}
                        title={t("Run now")}
                        disabled={rowBusy || task.status === "killed"}
                        onClick={() => void handleRunNow(task)}
                      >
                        <Zap size={13} aria-hidden="true" />
                      </button>
                      {task.status === "active" ? (
                        <button
                          type="button"
                          className="f-icon-btn"
                          aria-label={`${t("Pause")}: ${task.name}`}
                          title={t("Pause")}
                          disabled={rowBusy}
                          onClick={() => void handlePause(task)}
                        >
                          <Pause size={13} aria-hidden="true" />
                        </button>
                      ) : (
                        <button
                          type="button"
                          className="f-icon-btn"
                          aria-label={`${t("Activate")}: ${task.name}`}
                          title={t("Activate")}
                          disabled={rowBusy}
                          onClick={() => void handleActivate(task)}
                        >
                          <Play size={13} aria-hidden="true" />
                        </button>
                      )}
                      <button
                        type="button"
                        className="f-icon-btn"
                        aria-label={`${t("Delete")}: ${task.name}`}
                        title={t("Delete")}
                        disabled={rowBusy}
                        onClick={() => void handleDelete(task)}
                      >
                        <Trash2 size={13} aria-hidden="true" />
                      </button>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </div>

        <TaskRunInspector
          task={selectedTask}
          detail={detail}
          busyId={busyId}
          onActivate={(item) => void handleActivate(item)}
          onPause={(item) => void handlePause(item)}
          onStop={(item) => void handleStop(item)}
          onRunNow={(item) => void handleRunNow(item)}
          onDelete={(item) => void handleDelete(item)}
          onRefresh={() => void loadSelectedDetail()}
          onSelectTask={setSelectedId}
        />
      </div>

      {composerMode && (
        <TaskComposerModal
          initialMode={composerMode}
          libraries={libraries}
          internalWallets={internalWallets}
          onClose={() => setComposerMode(null)}
          onCreateTask={handleCreateTask}
          onCreateSwarm={handleCreateSwarm}
        />
      )}

      {drawerOpen && (
        <TaskLibraryDrawer
          libraries={libraries}
          busyTemplateId={busyTemplateId}
          onInstantiate={handleInstantiateTemplate}
          onClose={() => setDrawerOpen(false)}
        />
      )}

      {stepUpTarget && (
        <Dialog
          title={t("Confirm Owner Step-Up")}
          onClose={() => setStepUpTarget(null)}
        >
          <form
            className="wt-task-form"
            onSubmit={(e) => {
              e.preventDefault();
              void submitStepUpActivation();
            }}
          >
            <label className="wt-field">
              <span>
                {t("Owner password to activate")} {stepUpTarget.name}
              </span>
              <input
                type="password"
                value={stepUpPassword}
                onChange={(e) => setStepUpPassword(e.target.value)}
                autoFocus
                required
              />
            </label>
            {stepUpError && (
              <p className="wt-form-error" role="alert">
                {stepUpError}
              </p>
            )}
            <div className="wt-modal-footer">
              <button
                type="button"
                className="wt-btn-soft"
                onClick={() => setStepUpTarget(null)}
              >
                {t("Cancel")}
              </button>
              <button type="submit" className="wt-btn-rose-sm">
                {t("Authorize & Activate")}
              </button>
            </div>
          </form>
        </Dialog>
      )}
    </section>
  );
}
