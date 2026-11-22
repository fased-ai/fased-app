import { swapState } from "../onchain";
import { SwapRecoveryActions } from "./OnchainSwap";
import { miningState } from "../wen-mining";
import { WENMiningRecoveryActions } from "./WENMining";
import { useUI } from "../ui-preferences";
import { useEffect, useRef, useState } from "react";
import type { AgentRun, AgentTask, AgentView, TaskCalendar } from "../agents";
import type { ParsedTaskDraft } from "../agents-v2";
import { useCore } from "../core";
import { useWorkspace, type TaskComposerDraft } from "../workspace";
import { RefreshCw } from "../icons";
import { Dialog } from "../components/shared";
import { TaskHistory } from "./TaskHistory";
import { AgentPlaybooks } from "./AgentPlaybooks";
import { ScheduledAITasks } from "./ScheduledAITasks";
import { SavedReminderDraft } from "./SavedReminderDraft";
import { calendarLabel } from "../task-calendar";
import { useAIConnection } from "./AIConnection";
import { AgentStrategies, StrategyFacts } from "./AgentStrategies";
import { AgentTaskDrafts } from "./AgentTaskDrafts";
import { TaskRunCard } from "./TaskRunCard";
import {
  AnalysisTaskAccess,
  TaskComposer,
  formatInterval,
  formatScheduleSummary,
} from "./TaskComposer";

const displayLocale = () =>
  typeof document === "undefined" ? "en" : document.documentElement.lang;
const date = (at: number) =>
  at ? new Date(at * 1000).toLocaleString(displayLocale()) : "—";
export function taskInterval(seconds: number) {
  return formatInterval(seconds);
}
const schedule = (t: {
  calendar?: TaskCalendar;
  intervalSeconds: number;
  maxRuns: number;
}) => formatScheduleSummary(t);
const nextRun = (t: AgentTask) =>
  t.nextAt && t.calendar
    ? calendarLabel({ ...t.calendar, version: "once-at-v1", at: t.nextAt })
    : date(t.nextAt);
const taskStateTitles: Record<string, string> = {
  scheduled: "Scheduled",
  paused: "Paused",
  held: "Held",
  completed: "Completed",
  review: "Review",
  cancelled: "Cancelled",
};
const finished = (t: AgentTask) =>
  t.state === "completed" || t.runs >= t.maxRuns || t.state === "cancelled";

export function AgentTasks({
  view: v,
  panel,
  setPanel,
  onDiscuss,
}: {
  view: AgentView;
  panel: string;
  setPanel: (value: string) => void;
  onDiscuss: () => void;
}) {
  const { t: uiText } = useUI();
  const { instanceId } = useCore();
  const tr = uiText;
  const {
    agents,
    focusedTask,
    setFocusedTask,
    focusedRun,
    setFocusedRun,
    taskComposerDrafts,
    setTaskComposerDrafts,
    launchChatPrompt,
  } = useWorkspace();
  const [draftSeed, setDraftSeed] = useState<ParsedTaskDraft>();
  const composerKey = `${instanceId}:${v.profileId}`;
  const composer = taskComposerDrafts.get(composerKey);
  function updateComposer(
    update: (draft: TaskComposerDraft) => TaskComposerDraft,
  ) {
    setTaskComposerDrafts((drafts) => {
      const next = new Map(drafts);
      next.set(
        composerKey,
        update(drafts.get(composerKey) ?? { open: false, watchMode: false }),
      );
      return next;
    });
  }
  function discardComposer(expected?: TaskComposerDraft) {
    setTaskComposerDrafts((drafts) => {
      if (
        !drafts.has(composerKey) ||
        (expected && drafts.get(composerKey) !== expected)
      )
        return drafts;
      const next = new Map(drafts);
      next.delete(composerKey);
      return next;
    });
  }
  const editing = composer?.open ?? false;
  const watchMode = composer?.watchMode ?? false;
  const reviewTask = v.tasks.find((task) => task.id === composer?.seedId);
  const setEditing = (open: boolean) =>
    updateComposer((draft) => ({ ...draft, open }));
  const setWatchMode = (watchMode: boolean) =>
    updateComposer((draft) => ({ ...draft, watchMode }));
  const setReviewTask = (task?: AgentTask) =>
    updateComposer((draft) =>
      draft.seedId === task?.id
        ? draft
        : { ...draft, seedId: task?.id, kind: undefined, inputs: undefined },
    );
  const { view: connections, refresh: refreshConnections } = useAIConnection();
  const [selected, setSelected] = useState("");
  const [resultId, setResultId] = useState("");
  const [reuse, setReuse] = useState("");
  const [createdFrom, setCreatedFrom] = useState<string[]>();
  const [strategyRequest, setStrategyRequest] = useState<string>();
  const [legacyAIReview, setLegacyAIReview] = useState(false);
  const runCards = useRef(new Map<string, HTMLElement>());
  const tasks = v.tasks.filter((t) => !t.wen);
  const selectedTask = tasks.find((t) => t.id === selected);
  useEffect(() => {
    if (!focusedTask) return;
    if (focusedTask === "strategies") {
      setPanel("strategies");
      setFocusedTask("");
      return;
    }
    setPanel("automations");
    if (focusedTask === "new") {
      setReviewTask(undefined);
      setWatchMode(false);
      setEditing(true);
    } else if (tasks.some((t) => t.id === focusedTask))
      setSelected(focusedTask);
    setFocusedTask("");
  }, [focusedTask, tasks, setFocusedTask, setPanel]);
  useEffect(() => {
    if (!focusedRun) return;
    if (
      focusedRun.instanceId === instanceId &&
      focusedRun.profileId === v.profileId &&
      v.runs.some(
        (run) => run.id === focusedRun.id && run.profileId === v.profileId,
      )
    )
      showRun(focusedRun.id);
    setFocusedRun(null);
  }, [focusedRun, instanceId, v.profileId, v.runs, setFocusedRun]);
  const selectedRun = v.runs.filter((r) => r.taskId === selected).at(-1);
  useEffect(() => {
    if (!createdFrom) return;
    const created = tasks.find((task) => !createdFrom.includes(task.id));
    if (created) {
      setSelected(created.id);
      setCreatedFrom(undefined);
    }
  }, [createdFrom, tasks]);
  function reviewAI() {
    setLegacyAIReview(true);
  }
  function showRun(id: string) {
    setResultId(id);
    setPanel("history");
    requestAnimationFrame(() => {
      const card = runCards.current.get(id);
      card?.scrollIntoView({ block: "center" });
      card?.focus({ preventScroll: true });
    });
  }
  function actions(t: AgentTask) {
    if (t.wenMining) {
      const run = v.runs.find((r) => r.taskId === t.id);
      const recovery = v.wenMining?.recovery?.find((r) => r.taskId === t.id);
      return (
        <div className="row-actions">
          <WENMiningRecoveryActions
            recovery={recovery}
            disabled={agents.busy}
            onAction={(action, id) =>
              void agents.change({ action, wenMining: { id } })
            }
          />
          {(recovery?.pause || recovery?.resume) && (
            <button
              className="text-button"
              disabled={agents.busy || !v.wenMining?.available}
              onClick={() =>
                void agents.change({
                  action: recovery.resume ? "task-resume" : "task-pause",
                  id: t.id,
                })
              }
            >
              {uiText(recovery.resume ? "Resume" : "Pause")}
            </button>
          )}
          <button
            className="text-button"
            disabled={!run}
            aria-label={`${uiText("Result")}: ${t.name}`}
            onClick={() => run && showRun(run.id)}
          >
            {uiText("Result")}
          </button>
        </div>
      );
    }
    if (t.swap) {
      const run = v.runs.find((r) => r.taskId === t.id);
      const recovery = v.onchain?.recovery?.find((r) => r.taskId === t.id);
      return (
        <div className="row-actions">
          <SwapRecoveryActions
            recovery={recovery}
            disabled={agents.busy}
            onAction={(action, id) =>
              void agents.change({ action, swap: { id } })
            }
          />
          {(recovery?.pause || recovery?.resume) && (
            <button
              className="text-button"
              disabled={agents.busy || !v.onchain?.available}
              onClick={() =>
                void agents.change({
                  action: recovery.resume ? "task-resume" : "task-pause",
                  id: t.id,
                })
              }
            >
              {uiText(recovery.resume ? "Resume" : "Pause")}
            </button>
          )}
          <button
            className="text-button"
            disabled={!run}
            aria-label={`${uiText("Result")}: ${t.name}`}
            onClick={() => run && showRun(run.id)}
          >
            {uiText("Result")}
          </button>
        </div>
      );
    }
    const latestStrategy = v.strategies
      ?.find((s) => s.id === t.strategyPlan?.strategyId)
      ?.versions.at(-1);
    const needsReview =
      (!!t.strategyPlan &&
        (["review", "held", "cancelled"].includes(t.state) ||
          (t.state === "paused" &&
            latestStrategy?.version !== t.strategyPlan.strategyVersion))) ||
      (!!t.ai && ["review", "held", "cancelled"].includes(t.state));
    const account =
      connections?.connections?.find(
        (item) => item.ref === t.ai?.connectionRef,
      ) ?? (connections?.ref === t.ai?.connectionRef ? connections : null);
    const access = account?.taskAccess;
    const needsAccess =
      !!t.ai?.analysis &&
      t.ai.analysis.failure === "required" &&
      !!t.ai.connectionRef &&
      (!access ||
        account?.planPaused ||
        access.profileId !== v.profileId ||
        access.expiresAt * 1000 <= Date.now() ||
        access.usedRequests >= access.maxRequests ||
        !access.tasks.some(
          (item) => item.id === t.id && item.digest === t.ai?.digest,
        ));
    return (
      <div className="row-actions">
        {(!t.ai || t.ai.analysis) && (
          <button
            className="text-button"
            disabled={
              agents.busy ||
              t.state !== "scheduled" ||
              needsAccess ||
              (!!t.strategyPlan && !v.paperExecutionAvailable)
            }
            title={needsAccess ? uiText("Background access") : undefined}
            aria-label={`${uiText("Run now")}: ${t.name}`}
            onClick={async () => {
              if (await agents.change({ action: "task-run", id: t.id })) {
                setSelected(t.id);
                setEditing(false);
              }
            }}
          >
            {uiText("Run")}
          </button>
        )}
        {needsReview ? (
          <button
            className="text-button"
            onClick={() => {
              if (t.strategyPlan) {
                setStrategyRequest(t.strategyPlan.strategyId);
                setPanel("strategies");
                return;
              }
              if (!t.ai?.analysis) {
                reviewAI();
                return;
              }
              setReviewTask(t);
              setWatchMode(!!t.watch);
              setEditing(true);
            }}
          >
            {uiText("Review")}
          </button>
        ) : (
          <button
            className="text-button"
            disabled={
              agents.busy ||
              finished(t) ||
              (!!t.strategyPlan &&
                t.state === "paused" &&
                !v.paperExecutionAvailable)
            }
            aria-label={`${uiText(t.state === "paused" || t.state === "held" ? "Resume" : "Pause")}: ${t.name}`}
            onClick={() =>
              void agents.change({
                action:
                  t.state === "paused" || t.state === "held"
                    ? "task-resume"
                    : "task-pause",
                id: t.id,
              })
            }
          >
            {t.state === "paused" || t.state === "held"
              ? uiText("Resume")
              : uiText("Pause")}
          </button>
        )}
        <button
          className="text-button"
          aria-label={`${uiText("Result")}: ${t.name}`}
          disabled={!v.runs.some((r) => r.taskId === t.id)}
          onClick={() => {
            const r = v.runs.filter((r) => r.taskId === t.id).at(-1);
            if (r) showRun(r.id);
          }}
        >
          {tr("Result")}
        </button>
        {!t.calendar && !t.news && !t.botId && !t.ai && !t.strategyPlan && (
          <button
            className="text-button"
            aria-label={`${uiText("Reuse")}: ${t.name}`}
            onClick={() => {
              setReuse(t.id);
              setEditing(false);
            }}
          >
            {uiText("Reuse")}
          </button>
        )}
      </div>
    );
  }
  function status(task: AgentTask) {
    if (task.wenMining) {
      return uiText(
        miningState(
          v.wenMining?.recovery?.find((r) => r.taskId === task.id)?.status ??
            v.runs.find((r) => r.taskId === task.id)?.wenMining?.state ??
            "",
        ),
      );
    }
    return task.swap
      ? uiText(
          swapState(
            v.onchain?.recovery?.find((r) => r.taskId === task.id)?.status ??
              v.runs.find((r) => r.taskId === task.id)?.swap?.state ??
              "",
          ),
        )
      : uiText(taskStateTitles[task.state] ?? "Unavailable");
  }
  async function discuss(run: AgentRun) {
    if (await agents.change({ action: "task-run-explain", id: run.id }))
      onDiscuss();
  }
  function runTaskInChat(t: AgentTask) {
    launchChatPrompt({
      profileId: v.profileId,
      title: t.name,
      prompt: t.text || t.name,
    });
  }
  function result(run: AgentRun, inline = false) {
    return (
      <TaskRunCard
        key={run.id}
        run={run}
        view={v}
        inline={inline}
        onRegisterCard={(id, node) => {
          if (node) runCards.current.set(id, node);
          else runCards.current.delete(id);
        }}
        onDiscuss={(r) => void discuss(r)}
        onRunInChat={runTaskInChat}
      />
    );
  }
  return (
    <div className="tasks-screen">
      <div className="tasks-toolbar">
        <nav className="task-views tabs" aria-label={uiText("Task views")}>
          {[
            "automations",
            "strategies",
            "history",
            "inbox",
            "playbooks",
            "archive",
          ]
            .filter((t) => t !== "playbooks" || v.aiFixtureAvailable)
            .map((t) => (
              <button
                key={t}
                aria-pressed={panel === t}
                onClick={() => {
                  setPanel(t);
                  if (t === "history") setResultId("");
                }}
              >
                {uiText(
                  (
                    {
                      automations: "Tasks",
                      strategies: "Strategies",
                      history: "Results",
                      inbox: "Inbox",
                      playbooks: "Templates",
                      archive: "Archive",
                    } as Record<string, string>
                  )[t],
                )}
              </button>
            ))}
        </nav>
        <div className="row-actions">
          <button
            className="text-button"
            aria-label={uiText("Refresh")}
            onClick={() => {
              void agents.refresh().catch(() => {});
              void refreshConnections();
            }}
          >
            <RefreshCw size={14} />
          </button>
          <button
            className="primary"
            onClick={() => {
              setPanel("automations");
              setDraftSeed(undefined);
              setReviewTask(undefined);
              setWatchMode(false);
              setEditing(true);
              setReuse("");
            }}
          >
            {tr("New task")}
          </button>
          <button
            className="secondary"
            onClick={() => {
              setPanel("automations");
              setDraftSeed(undefined);
              setReviewTask(undefined);
              setWatchMode(true);
              setEditing(true);
              setReuse("");
            }}
          >
            {uiText("Watch news")}
          </button>
        </div>
      </div>
      <div hidden={panel !== "strategies"}>
        <AgentStrategies
          key={`${v.profileId}:${instanceId}`}
          view={v}
          requested={strategyRequest}
          handled={() => setStrategyRequest(undefined)}
          created={() => {
            setCreatedFrom(tasks.map((task) => task.id));
            setDraftSeed(undefined);
            setEditing(false);
            setReuse("");
            setPanel("automations");
          }}
        />
      </div>
      {v.aiFixtureAvailable && (
        <div hidden={panel !== "playbooks"}>
          <AgentPlaybooks key={v.profileId} view={v} />
        </div>
      )}
      {panel === "archive" && <TaskHistory key={v.profileId} view={v} />}
      {panel === "automations" && (
        <AgentTaskDrafts
          profileId={v.profileId}
          refreshKey={v.revision}
          onSchedule={(draft) => {
            setPanel("automations");
            setDraftSeed(draft);
            setReviewTask(undefined);
            setWatchMode(false);
            setReuse("");
            updateComposer(() => ({ open: true, watchMode: false }));
          }}
        />
      )}
      <section className="panel task-board" hidden={panel !== "automations"}>
        <div
          className={`task-workspace ${editing || selectedTask || reuse ? "with-detail" : ""}`}
        >
          <div className="task-list">
            <div className="panel-heading">
              <h2>{uiText("Tasks")}</h2>
              <span>{tasks.length}</span>
            </div>
            {tasks.length === 0 ? (
              <div className="task-empty">
                <p>{uiText("No tasks")}</p>
              </div>
            ) : (
              <table className="task-table" aria-label={uiText("Tasks")}>
                <thead>
                  <tr>
                    <th>{uiText("Task")}</th>
                    <th>{uiText("Status")}</th>
                    <th>{uiText("Runs")}</th>
                    <th>{uiText("Next run")}</th>
                    <th>{uiText("Actions")}</th>
                  </tr>
                </thead>
                <tbody>
                  {tasks.map((t) => (
                    <tr key={t.id} data-selected={selected === t.id}>
                      <td>
                        <button
                          className="task-name"
                          onClick={() => {
                            setSelected(t.id);
                            setDraftSeed(undefined);
                            setEditing(false);
                            setReuse("");
                          }}
                        >
                          {t.name}
                        </button>
                        <small>{t.swap ? uiText("Once") : schedule(t)}</small>
                      </td>
                      <td>
                        <span className={`task-state ${t.state}`}>
                          {status(t)}
                        </span>
                      </td>
                      <td>
                        {t.runs} / {t.maxRuns}
                      </td>
                      <td className="task-next">
                        {t.state === "scheduled" ? nextRun(t) : "—"}
                      </td>
                      <td>{actions(t)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
          {editing ? (
            <TaskComposer
              key={`${instanceId}:${v.profileId}:${composer?.seedId || draftSeed?.id || "new"}`}
              draftState={composer!}
              updateDraft={updateComposer}
              view={v}
              seed={reviewTask}
              draftSeed={draftSeed}
              watchMode={watchMode}
              close={() => {
                setDraftSeed(undefined);
                discardComposer();
              }}
              created={() => {
                setCreatedFrom(tasks.map((task) => task.id));
                setDraftSeed(undefined);
                discardComposer(composer);
              }}
            />
          ) : reuse ? (
            <aside className="task-detail">
              <SavedReminderDraft
                key={reuse}
                view={v}
                sourceID={reuse}
                close={() => setReuse("")}
              />
            </aside>
          ) : (
            selectedTask && (
              <aside
                className="task-detail"
                aria-label={uiText("Task details")}
              >
                <div className="panel-heading">
                  <h3>{selectedTask.name}</h3>
                  <button
                    className="text-button"
                    aria-label={uiText("Close task details")}
                    onClick={() => setSelected("")}
                  >
                    {uiText("Close")}
                  </button>
                </div>
                <div className="task-detail-body">
                  <p className="task-result-text">
                    {selectedTask.strategyPlan
                      ? uiText("Paper strategy")
                      : selectedTask.watch
                        ? `${uiText("Contains")}: ${selectedTask.watch.rule.contains}`
                        : selectedTask.text || uiText("Fixed paper evaluation")}
                  </p>
                  {selectedTask.strategyPlan && (
                    <StrategyFacts
                      plan={selectedTask.strategyPlan.plan}
                      version={selectedTask.strategyPlan.strategyVersion}
                    />
                  )}
                  <dl className="task-facts">
                    <dt>{uiText("Schedule")}</dt>
                    <dd>
                      {selectedTask.swap || selectedTask.wenMining
                        ? uiText("Once")
                        : schedule(selectedTask)}
                    </dd>
                    <dt>{uiText("Next run")}</dt>
                    <dd>
                      {selectedTask.state === "scheduled"
                        ? nextRun(selectedTask)
                        : "—"}
                    </dd>
                    <dt>{uiText("Status")}</dt>
                    <dd>{status(selectedTask)}</dd>
                  </dl>
                  {!selectedTask.swap &&
                    !selectedTask.wenMining &&
                    !selectedTask.wen &&
                    !selectedTask.botId && (
                      <div className="row-actions">
                        <button
                          type="button"
                          className="secondary"
                          aria-label={`${uiText("Run in Chat")}: ${selectedTask.name}`}
                          onClick={() => runTaskInChat(selectedTask)}
                        >
                          {uiText("Run in Chat")}
                        </button>
                      </div>
                    )}
                  {selectedTask.state === "completed" &&
                    selectedTask.id.length === 43 &&
                    !selectedTask.id.includes(":") &&
                    !selectedTask.ai &&
                    !selectedTask.news &&
                    !selectedTask.wen &&
                    !selectedTask.playbook &&
                    !selectedTask.strategyPlan &&
                    !selectedTask.swap &&
                    !selectedTask.wenMining &&
                    !selectedTask.botId && (
                      <button
                        className="text-button"
                        disabled={agents.busy}
                        onClick={async () => {
                          if (
                            await agents.change({
                              action: "task-archive",
                              id: selectedTask.id,
                            })
                          ) {
                            setSelected("");
                            setPanel("archive");
                          }
                        }}
                      >
                        {tr("Archive task")}
                      </button>
                    )}
                  {selectedTask.state === "held" &&
                    !selectedTask.swap &&
                    !selectedTask.wenMining && (
                      <p role="status">
                        {v.newsFetches
                          .filter((f) => f.taskId === selectedTask.id)
                          .at(-1)?.failure ||
                          (selectedTask.strategyPlan
                            ? uiText("Review again")
                            : selectedTask.ai
                              ? uiText(
                                  "Review a fresh AI plan before continuing.",
                                )
                              : uiText(
                                  "Check the selected news source, then resume.",
                                ))}
                      </p>
                    )}
                  {selectedTask.ai?.analysis && (
                    <AnalysisTaskAccess view={v} task={selectedTask} />
                  )}
                  {selectedRun ? (
                    panel === "automations" ? (
                      result(selectedRun, true)
                    ) : null
                  ) : (
                    <p className="empty-row">{uiText("No runs yet.")}</p>
                  )}
                </div>
              </aside>
            )
          )}
        </div>
      </section>
      <div className="task-results">
        <section
          className="panel"
          aria-label={uiText("Run results")}
          hidden={panel !== "history"}
        >
          <div className="panel-heading">
            <h2>{uiText("Results")}</h2>
            {resultId && (
              <button className="text-button" onClick={() => setResultId("")}>
                {uiText("All results")}
              </button>
            )}
          </div>
          <div className="agent-record-body">
            {v.runs.length === 0 ? (
              <p className="empty-row">{uiText("No runs yet.")}</p>
            ) : (
              panel === "history" &&
              v.runs
                .slice()
                .reverse()
                .filter((r) => !resultId || r.id === resultId)
                .map((r) => result(r))
            )}
          </div>
        </section>
        <section className="panel" hidden={panel !== "inbox"}>
          <div className="panel-heading">
            <h2>{uiText("Inbox")}</h2>
          </div>
          <div className="agent-record-body">
            {v.notices.length === 0 ? (
              <p>{uiText("No task notifications.")}</p>
            ) : (
              v.notices
                .slice()
                .reverse()
                .map((n) => {
                  const canView =
                    n.profileId === v.profileId &&
                    v.runs.some(
                      (r) => r.id === n.runId && r.profileId === v.profileId,
                    );
                  return (
                    <article
                      className="task-notice"
                      key={n.id}
                      aria-label={`Task notification ${n.id}`}
                    >
                      <p>{n.text}</p>
                      <time>{date(n.at)}</time>
                      <div className="row-actions">
                        <button
                          className="secondary"
                          disabled={agents.busy || !canView}
                          onClick={() => {
                            if (canView) showRun(n.runId);
                          }}
                        >
                          {canView
                            ? uiText("View result")
                            : uiText("Result unavailable")}
                        </button>
                        {!n.read ? (
                          <button
                            className="text-button"
                            disabled={agents.busy}
                            onClick={() =>
                              void agents.change({
                                action: "notice-read",
                                id: n.id,
                              })
                            }
                          >
                            {uiText("Mark read")}
                          </button>
                        ) : (
                          <small>{uiText("Read")}</small>
                        )}
                      </div>
                    </article>
                  );
                })
            )}
          </div>
        </section>
      </div>
      {v.aiFixtureAvailable && (
        <div className="fixture-regression-tools">
          {" "}
          <details className="task-inspection schedule-behavior">
            <summary>{uiText("Timing")}</summary>
            <p>
              {uiText(
                "Tasks run while Fased runs, even with the browser closed. Missed intervals become one run. Resume starts a new interval. Calendar reminders keep their original date; overdue reminders run once after resume or restart. Run now completes a calendar reminder early; it will not run again on its date. For a recurring calendar, Run now consumes the upcoming occurrence.",
              )}
            </p>
            <p>
              {uiText(
                "Intervals: 1 minute–7 days, up to 20 runs. Calendar reminders: one date within 366 days. Recurring calendars use a named time zone, an end date within one year and up to 20 runs. Missing local times are skipped; repeated times run once.",
              )}
            </p>
            {v.taskCapacity && (
              <p>
                {uiText("Workspace capacity: ")}
                {v.taskCapacity.tasks}
                {uiText("/100 tasks ·")} {v.taskCapacity.evaluations}
                {uiText("/200 allocated runs ·")} {v.taskCapacity.runs}
                {uiText("/200 results · ")}
                {v.taskCapacity.notices}
                {uiText("/200 notifications · ")}
                {Math.ceil(v.taskCapacity.bytes / 1024)}
                {uiText(
                  "/512 KiB. Future output also reserves space. History is retained; reaching a limit stops further writes. Continuous operation beyond these limits is not available yet.",
                )}
              </p>
            )}
          </details>
          <details className="scheduled-ai-disclosure">
            <summary>{uiText("Advanced")}</summary>
            <ScheduledAITasks key={v.profileId} view={v} />
          </details>
        </div>
      )}
      {legacyAIReview && (
        <Dialog
          title={uiText("Review task")}
          onClose={() => setLegacyAIReview(false)}
        >
          <ScheduledAITasks key={v.profileId} view={v} />
        </Dialog>
      )}
    </div>
  );
}
