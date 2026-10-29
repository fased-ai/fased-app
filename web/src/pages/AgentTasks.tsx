import { resolveLanguage, translate } from "../ui-language";
import { useUI } from "../ui-preferences";
import { useEffect, useRef, useState } from "react";
import type {
  AgentCommand,
  AgentRun,
  AgentTask,
  AgentView,
  TaskReview,
  TaskCalendar,
} from "../agents";
import { useCore } from "../core";
import { useWorkspace } from "../workspace";
import { RefreshCw } from "../icons";
import { Badge } from "../components/shared";
import { TaskHistory } from "./TaskHistory";
import { AgentPlaybooks } from "./AgentPlaybooks";
import { ScheduledAITasks } from "./ScheduledAITasks";
import { SavedReminderDraft } from "./SavedReminderDraft";
import {
  calendarFromInput,
  calendarInput,
  calendarLabel,
} from "../task-calendar";
import {
  TaskCronFields,
  cronCalendar,
  timeZones,
  type CronSettings,
} from "./TaskCronFields";
import { NewsSourcePicker } from "./NewsInputs";
import { AIModelPicker, useAIConnection } from "./AIConnection";
import { isUnmeteredPolicy } from "../agents";
import { TaskAccessSettings } from "./TaskAccess";

const displayLocale = () =>
  typeof document === "undefined" ? "en" : document.documentElement.lang;
const taskCopy = (key: string, values?: Record<string, string | number>) =>
  translate(resolveLanguage("system", [displayLocale()]), key, values);
const date = (at: number) =>
  at ? new Date(at * 1000).toLocaleString(displayLocale()) : "—";
export function taskInterval(seconds: number) {
  const [value, unit] =
    seconds % 86400 === 0
      ? ([seconds / 86400, "day"] as const)
      : seconds % 3600 === 0
        ? ([seconds / 3600, "hour"] as const)
        : seconds % 60 === 0
          ? ([seconds / 60, "minute"] as const)
          : ([seconds, "second"] as const);
  return new Intl.NumberFormat(displayLocale(), {
    style: "unit",
    unit,
    unitDisplay: "long",
  }).format(value);
}
const schedule = (t: {
  calendar?: TaskCalendar;
  intervalSeconds: number;
  maxRuns: number;
}) =>
  t.calendar
    ? t.calendar.version === "cron-v1"
      ? taskCopy("{schedule} · {count} runs", {
          schedule: calendarLabel(t.calendar),
          count: t.maxRuns,
        })
      : taskCopy("Once · {schedule}", { schedule: calendarLabel(t.calendar) })
    : t.maxRuns === 1
      ? taskCopy("Once, after {duration}", {
          duration: taskInterval(t.intervalSeconds),
        })
      : taskCopy("Every {duration} · {count} runs", {
          duration: taskInterval(t.intervalSeconds),
          count: t.maxRuns,
        });
const nextRun = (t: AgentTask) =>
  t.nextAt && t.calendar
    ? calendarLabel({ ...t.calendar, version: "once-at-v1", at: t.nextAt })
    : date(t.nextAt);
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
  const tr = uiText;
  const { agents, focusedTask, setFocusedTask } = useWorkspace();
  const { view: connections } = useAIConnection();
  const [editing, setEditing] = useState(false);
  const [watchMode, setWatchMode] = useState(false);
  const [selected, setSelected] = useState("");
  const [resultId, setResultId] = useState("");
  const [reuse, setReuse] = useState("");
  const [reviewTask, setReviewTask] = useState<AgentTask>();
  const [createdFrom, setCreatedFrom] = useState<string[]>();
  const advanced = useRef<HTMLDetailsElement>(null);
  const runCards = useRef(new Map<string, HTMLElement>());
  const tasks = v.tasks.filter((t) => !t.wen);
  const selectedTask = tasks.find((t) => t.id === selected);
  useEffect(() => {
    if (!focusedTask) return;
    setPanel("automations");
    if (focusedTask === "new") {
      setReviewTask(undefined);
      setWatchMode(false);
      setEditing(true);
    } else if (tasks.some((t) => t.id === focusedTask))
      setSelected(focusedTask);
    setFocusedTask("");
  }, [focusedTask, tasks, setFocusedTask, setPanel]);
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
    if (advanced.current) {
      advanced.current.open = true;
      advanced.current.scrollIntoView({ block: "start" });
    }
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
    const needsReview =
      !!t.ai && ["review", "held", "cancelled"].includes(t.state);
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
            disabled={agents.busy || t.state !== "scheduled" || needsAccess}
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
            disabled={agents.busy || finished(t)}
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
        {!t.calendar && !t.news && !t.botId && !t.ai && (
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
  async function discuss(run: AgentRun) {
    if (await agents.change({ action: "task-run-explain", id: run.id }))
      onDiscuss();
  }
  function result(run: AgentRun, inline = false) {
    const task =
      run.profileId === v.profileId
        ? v.tasks.find(
            (t) => t.id === run.taskId && t.profileId === v.profileId,
          )
        : undefined;
    const notice = v.notices.find((n) => n.runId === run.id);
    const answer = v.aiRequests.find((q) => q.id === run.aiRequestId)?.answer;
    const typedAnalysis =
      run.strategy === "analysis-v1" || !!task?.ai?.analysis;
    const acceptedAnswer =
      typedAnalysis && run.analysis?.state !== "completed" ? undefined : answer;
    return (
      <article
        className="memory-card task-run"
        key={run.id}
        tabIndex={-1}
        aria-label={`Run ${run.id}`}
        ref={(node) => {
          if (node) runCards.current.set(run.id, node);
          else runCards.current.delete(run.id);
        }}
      >
        <header className="task-result-heading">
          <strong>
            {inline
              ? uiText("Latest result")
              : (task?.name ?? uiText("Task unavailable"))}
          </strong>
          <Badge>
            {run.watch
              ? uiText("News watch")
              : run.analysis
                ? uiText("Analysis")
                : run.outcome}
          </Badge>
        </header>
        <time dateTime={new Date(run.at * 1000).toISOString()}>
          {date(run.at)}
        </time>
        <p className="task-result-text">
          {run.watch
            ? uiText("New matches: {count}", { count: run.watch.newIds.length })
            : run.analysis?.state === "skipped"
              ? uiText("Analysis skipped")
              : acceptedAnswer ||
                notice?.text ||
                task?.text ||
                uiText("Saved result")}
        </p>
        {run.watch && run.analysis?.state === "completed" && answer && (
          <p className="ai-answer">{answer}</p>
        )}
        {run.watch && run.analysis?.state === "skipped" && (
          <p role="status">
            {uiText(
              run.analysis.reason === "no-new-matches"
                ? "Nothing new to analyze"
                : "Analysis skipped",
            )}
          </p>
        )}
        {run.watch && (
          <ul className="watch-headlines">
            {run.watch.newIds.map((id) => {
              const item = v.newsFetches
                .find((f) => f.id === run.news?.fetchId)
                ?.items?.find((n) => n.id === id);
              return item ? (
                <li key={id}>
                  <a href={item.url} target="_blank" rel="noreferrer noopener">
                    {item.title}
                  </a>
                </li>
              ) : null;
            })}
          </ul>
        )}
        {!run.wen &&
          !run.botId &&
          [
            "reminder-v1",
            "ai-step-v1/reminder-v1",
            "ai-step-v1",
            "news-watch-v1",
            "analysis-v1",
          ].includes(run.strategy) && (
            <button
              className="secondary"
              disabled={agents.busy}
              onClick={() => void discuss(run)}
            >
              {uiText("Discuss")}
            </button>
          )}
        <details className="task-inspection">
          <summary>{uiText("Run details")}</summary>
          <p>
            {run.strategy}
            {uiText(" · Run ")}
            {run.id}
          </p>
          {run.analysis && (
            <dl className="task-facts">
              <dt>{uiText("Analysis")}</dt>
              <dd>
                {uiText(
                  run.analysis.state === "completed"
                    ? "Completed"
                    : "Analysis skipped",
                )}
              </dd>
              {run.analysis.reason && (
                <>
                  <dt>{uiText("Reason")}</dt>
                  <dd>{run.analysis.reason}</dd>
                </>
              )}
            </dl>
          )}
          {run.botId && (
            <p>
              {uiText("Quantity ")}
              {run.quantity}
              {uiText(" · Fixed price ")}
              {run.priceCents}
              {uiText(" · Cost")} {run.costCents}
              {uiText(" · Budget before ")}
              {run.budgetBefore}
            </p>
          )}
          {run.wen && (
            <p>
              {uiText("Unsigned paper receipt · ")}
              {run.wen.receiptSha256}
            </p>
          )}
          {run.news && (
            <p className="news-provenance">
              {uiText("News snapshot ")}
              {run.news.fetchId} · {run.news.url}
              {uiText(" · retrieved")} {date(run.news.fetchedAt)} ·{" "}
              {run.news.bodySha256}
            </p>
          )}
        </details>
      </article>
    );
  }
  return (
    <div className="tasks-screen">
      <div className="tasks-toolbar">
        <nav className="task-views tabs" aria-label={uiText("Task views")}>
          {["automations", "history", "inbox", "playbooks", "archive"].map(
            (t) => (
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
                      history: "Results",
                      inbox: "Inbox",
                      playbooks: "Templates",
                      archive: "Archive",
                    } as Record<string, string>
                  )[t],
                )}
              </button>
            ),
          )}
        </nav>
        <div className="row-actions">
          <button
            className="text-button"
            aria-label={uiText("Refresh")}
            onClick={() => void agents.refresh().catch(() => {})}
          >
            <RefreshCw size={14} />
          </button>
          <button
            className="primary"
            onClick={() => {
              setPanel("automations");
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
      <div hidden={panel !== "playbooks"}>
        <AgentPlaybooks key={v.profileId} view={v} />
      </div>
      {panel === "archive" && <TaskHistory key={v.profileId} view={v} />}
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
                            setEditing(false);
                            setReuse("");
                          }}
                        >
                          {t.name}
                        </button>
                        <small>{schedule(t)}</small>
                      </td>
                      <td>
                        <span className={`task-state ${t.state}`}>
                          {t.state}
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
              key={`${v.profileId}:${reviewTask?.id || "new"}`}
              view={v}
              seed={reviewTask}
              watchMode={watchMode}
              close={() => setEditing(false)}
              created={() => {
                setCreatedFrom(tasks.map((task) => task.id));
                setEditing(false);
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
                    {selectedTask.watch
                      ? `${uiText("Contains")}: ${selectedTask.watch.rule.contains}`
                      : selectedTask.text || uiText("Fixed paper evaluation")}
                  </p>
                  <dl className="task-facts">
                    <dt>{uiText("Schedule")}</dt>
                    <dd>{schedule(selectedTask)}</dd>
                    <dt>{uiText("Next run")}</dt>
                    <dd>
                      {selectedTask.state === "scheduled"
                        ? nextRun(selectedTask)
                        : "—"}
                    </dd>
                    <dt>{uiText("Status")}</dt>
                    <dd>{selectedTask.state}</dd>
                  </dl>
                  {selectedTask.state === "completed" &&
                    selectedTask.id.length === 43 &&
                    !selectedTask.id.includes(":") &&
                    !selectedTask.ai &&
                    !selectedTask.news &&
                    !selectedTask.wen &&
                    !selectedTask.playbook &&
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
                  {selectedTask.state === "held" && (
                    <p role="status">
                      {v.newsFetches
                        .filter((f) => f.taskId === selectedTask.id)
                        .at(-1)?.failure ||
                        (selectedTask.ai
                          ? uiText("Review a fresh AI plan before continuing.")
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
      {v.tasks.some((task) => task.ai && !task.ai.analysis) && (
        <details className="scheduled-ai-disclosure" ref={advanced}>
          <summary>{uiText("Legacy tasks")}</summary>
          <ScheduledAITasks key={v.profileId} view={v} />
        </details>
      )}
    </div>
  );
}

function AnalysisTaskAccess({
  view,
  task,
}: {
  view: AgentView;
  task: AgentTask;
}) {
  const { t } = useUI();
  const { setProvidersOpen } = useWorkspace();
  const { view: connections, refresh } = useAIConnection();
  const account =
    connections?.connections?.find(
      (item) => item.ref === task.ai?.connectionRef,
    ) ?? (connections?.ref === task.ai?.connectionRef ? connections : null);
  if (!account)
    return (
      <p role="status">
        {t("Provider unavailable")}{" "}
        <button className="text-button" onClick={() => setProvidersOpen(true)}>
          {t("Providers")}
        </button>
      </p>
    );
  return (
    <TaskAccessSettings
      key={`${view.profileId}:${task.id}:${account.ref}`}
      connection={account}
      onChanged={() => {
        void refresh();
        window.dispatchEvent(new Event("ai-connection-changed"));
      }}
    />
  );
}

function TaskComposer({
  view: v,
  watchMode,
  close,
  created,
  seed,
}: {
  view: AgentView;
  watchMode: boolean;
  close: () => void;
  created: () => void;
  seed?: AgentTask;
}) {
  const { t: uiText } = useUI();
  const tr = uiText;
  const core = useCore();
  const { agents, setProvidersOpen } = useWorkspace();
  const localZone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  const freshDraft = () => ({
    name: "",
    text: "",
    prompt: "",
    bot: "",
    news: "",
    age: 3600,
    contains: "",
    seconds: 60,
    unit: 60,
    runs: 1,
    calendarMode: false,
    cronMode: false,
    zone: localZone,
    cron: {
      preset: "daily",
      time: "09:00",
      weekday: "1",
      expression: "0 9 * * 1-5",
      timeZone: localZone,
      until: calendarInput(Date.now() / 1000 + 30 * 86400, localZone),
    } as CronSettings,
    at: calendarInput(Date.now() / 1000 + 1800, localZone),
    analyze: false,
    failure: "required" as "required" | "optional",
    overridden: false,
    connectionRef: "",
    model: "",
    session: "",
    input: 24000,
    output: 1024,
    deadline: undefined as number | undefined,
    perCall: undefined as number | undefined,
    budget: undefined as number | undefined,
    minutes: 60,
  });
  type Draft = ReturnType<typeof freshDraft>;
  const [kind, setKind] = useState<"reminder" | "analysis">(
    seed?.ai?.analysis && !seed.watch ? "analysis" : "reminder",
  );
  const mode = watchMode ? "watch" : kind;
  const [drafts, setDrafts] = useState(() => {
    const initial = {
      reminder: freshDraft(),
      analysis: freshDraft(),
      watch: { ...freshDraft(), name: uiText("News watch") },
    };
    if (seed?.ai?.analysis) {
      const old = seed.ai;
      const copy = {
        ...freshDraft(),
        name: seed.name,
        prompt: seed.text,
        news: seed.news?.sourceId || "",
        age: seed.news?.maxAgeSeconds || 3600,
        contains: seed.watch?.rule.contains || "",
        analyze: true,
        failure: old.analysis!.failure,
        seconds: seed.intervalSeconds || 60,
        runs: Math.max(1, Math.min(10, seed.maxRuns - seed.runs)),
        overridden: true,
        connectionRef: old.connectionRef || "",
        model: old.model || "",
        session: old.sessionId,
        input: old.limits.maxInputTokens,
        output: old.limits.maxOutputTokens,
        deadline: old.limits.deadlineSeconds,
        perCall: old.limits.budgetMicrousd,
        budget: old.budgetMicrousd,
      };
      if (seed.calendar) {
        copy.zone = seed.calendar.timeZone;
        copy.calendarMode = seed.calendar.version === "once-at-v1";
        copy.cronMode = seed.calendar.version === "cron-v1";
        copy.at = calendarInput(seed.calendar.at, seed.calendar.timeZone);
        if (copy.cronMode)
          copy.cron = {
            ...copy.cron,
            preset: "custom",
            expression: seed.calendar.expression || "",
            timeZone: seed.calendar.timeZone,
            until: calendarInput(seed.calendar.until!, seed.calendar.timeZone),
          };
      }
      initial[seed.watch ? "watch" : "analysis"] = copy;
    }
    return initial;
  });
  const draft = drafts[mode];
  const patch = (change: Partial<Draft>) =>
    setDrafts((saved) => ({
      ...saved,
      [mode]: { ...saved[mode], ...change },
    }));
  const {
    name,
    text,
    bot,
    news,
    age,
    contains,
    seconds,
    unit,
    runs,
    calendarMode,
    cronMode,
    zone,
    cron,
    at,
  } = draft;
  const watchName = name,
    watchSource = news,
    watchAge = age;
  const setName = (name: string) => patch({ name });
  const setWatchName = setName;
  const setText = (text: string) => patch({ text });
  const setBot = (bot: string) => patch({ bot });
  const setNews = (news: string) => patch({ news });
  const setWatchSource = setNews;
  const setAge = (age: number) => patch({ age });
  const setWatchAge = setAge;
  const setContains = (contains: string) => patch({ contains });
  const setSeconds = (seconds: number) => patch({ seconds });
  const setUnit = (unit: number) => patch({ unit });
  const setRuns = (runs: number) => patch({ runs });
  const setCalendarMode = (calendarMode: boolean) => patch({ calendarMode });
  const setCronMode = (cronMode: boolean) => patch({ cronMode });
  const setZone = (zone: string) => patch({ zone });
  const setCron = (cron: CronSettings) => patch({ cron });
  const setAt = (at: string) => patch({ at });
  const aiEnabled = mode === "analysis" || (mode === "watch" && draft.analyze);
  const { view: connections } = useAIConnection();
  const accounts =
    connections?.connections ?? (connections?.ref ? [connections] : []);
  const agent = v.profiles.find((item) => item.id === v.profileId);
  const connectionRef = draft.overridden
    ? draft.connectionRef
    : agent?.connectionRef || "";
  const model = draft.overridden ? draft.model : agent?.model || "";
  const account = accounts.find((item) => item.ref === connectionRef);
  const entry = account?.catalog?.models.find(
    (item) => item.id === model && item.connectionRef === connectionRef,
  );
  const policy = entry?.policy;
  const unmetered = isUnmeteredPolicy(policy);
  const interactiveOnly = policy?.provider === "kimi-code";
  const aiReady =
    !!account &&
    !account.locked &&
    !account.planPaused &&
    !!entry?.selectable &&
    !interactiveOnly;
  const local = policy?.provider === "local";
  const deadline = draft.deadline ?? (local ? 60 : 3);
  const ceiling = policy
    ? Math.ceil(
        (draft.input * policy.inputMicrousdPerMillion +
          Math.max(draft.output, policy.billableOutputTokens || 0) *
            policy.outputMicrousdPerMillion) /
          1000000,
      )
    : 30000;
  const perCall = unmetered ? 0 : (draft.perCall ?? Math.max(30000, ceiling));
  const totalBudget = unmetered
    ? 0
    : (draft.budget ??
      Math.min(
        policy?.maxRequestBudgetMicrousd || 1000000,
        perCall * (calendarMode ? 1 : runs),
      ));
  const [review, setReview] = useState<TaskReview>();
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [now, setNow] = useState(Date.now());
  const generation = useRef(0);
  useEffect(
    () => () => {
      generation.current++;
    },
    [],
  );
  useEffect(() => {
    if (!review) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [review]);
  function edit(fn: () => void) {
    generation.current++;
    setReview(undefined);
    setError("");
    fn();
  }
  useEffect(() => {
    generation.current++;
    setReview(undefined);
    setError("");
  }, [mode, connectionRef, model, policy?.id]);
  const stale =
    !!review &&
    (review.revision !== v.revision ||
      review.expiresAt * 1000 <= now ||
      (!!review.ai &&
        (!aiReady ||
          review.ai.connectionRef !== connectionRef ||
          review.ai.model !== model)));
  let calendar: TaskCalendar | undefined;
  let calendarError = "";
  if (calendarMode || cronMode) {
    try {
      calendar = cronMode ? cronCalendar(cron) : calendarFromInput(at, zone);
    } catch (e) {
      calendarError = e instanceof Error ? e.message : "Choose a date and time";
    }
  }
  const command: AgentCommand = {
    action: "task-review",
    profileId: v.profileId,
    revision: v.revision,
    name: (watchMode ? watchName : name).trim(),
    text: aiEnabled ? draft.prompt.trim() : watchMode ? "" : text.trim(),
    botId: watchMode || aiEnabled ? "" : bot,
    newsSourceId: watchMode ? watchSource : news,
    newsMaxAgeSeconds: watchMode
      ? watchSource
        ? watchAge
        : undefined
      : news
        ? age
        : undefined,
    watch: watchMode
      ? { version: "news-watch-v1", contains: contains.trim() }
      : undefined,
    calendar,
    intervalSeconds: calendarMode || cronMode ? 0 : seconds,
    maxRuns: calendarMode ? 1 : runs,
    ...(aiEnabled
      ? {
          analysis: {
            version: "task-analysis-v1",
            failure: watchMode ? draft.failure : "required",
          },
          useAgentDefaults: !draft.overridden,
          connectionRef,
          model,
          sessionId: draft.session || undefined,
          memorySelection: { mode: "relevant" },
          limits: {
            maxInputTokens: draft.input,
            maxOutputTokens: draft.output,
            deadlineSeconds: deadline,
            budgetMicrousd: perCall,
          },
          totalBudgetMicrousd: totalBudget,
          authorizationSeconds: draft.minutes * 60,
        }
      : {}),
  };
  async function prepare() {
    const attempt = ++generation.current;
    setLoading(true);
    setError("");
    setReview(undefined);
    try {
      if (calendarError) throw Error(calendarError);
      if (aiEnabled && !aiReady) throw Error(uiText("Provider unavailable"));
      const next = await core.request<AgentView>("agents", "POST", command);
      if (attempt === generation.current) {
        if (!next.taskReview) throw Error("Task review unavailable");
        setReview(next.taskReview);
        setNow(Date.now());
      }
    } catch (e) {
      if (attempt === generation.current)
        setError(e instanceof Error ? e.message : "Task review unavailable");
    } finally {
      setLoading(false);
    }
  }
  const busy = agents.busy || loading;
  return (
    <aside
      className="task-composer"
      aria-label={review ? uiText("Review task") : uiText("Create a task")}
    >
      <div className="panel-heading">
        <h3>
          {review
            ? tr("Review task")
            : watchMode
              ? uiText("Watch news")
              : tr("New task")}
        </h3>
        <button type="button" className="text-button" onClick={close}>
          {uiText("Cancel task")}
        </button>
      </div>
      <div className="task-detail-body">
        {error && <p role="alert">{error}</p>}
        {review ? (
          <>
            <h3>{review.name}</h3>
            <p className="task-result-text">
              {review.watch
                ? `${uiText("Contains")}: ${review.watch.contains}`
                : review.bot
                  ? `Paper evaluation · ${review.bot.name}`
                  : review.text}
            </p>
            <dl className="task-facts">
              <dt>{uiText("Agent")}</dt>
              <dd>{v.profiles.find((p) => p.id === review.profileId)?.name}</dd>
              <dt>{tr("Schedule")}</dt>
              <dd>
                {review.calendar?.version === "once-at-v1"
                  ? uiText("Once")
                  : schedule(review)}
              </dd>
              <dt>{uiText("Starts")}</dt>
              <dd>
                {review.calendar
                  ? calendarLabel({ ...review.calendar, version: "once-at-v1" })
                  : `${taskInterval(review.intervalSeconds)} after creation`}
              </dd>
              {review.calendar?.version === "cron-v1" && (
                <>
                  <dt>{tr("End date")}</dt>
                  <dd>
                    {calendarLabel({
                      ...review.calendar,
                      version: "once-at-v1",
                      at: review.calendar.until!,
                    })}
                  </dd>
                  <dt>{tr("Clock changes")}</dt>
                  <dd>
                    {tr("Missing times skipped; repeated times run once.")}
                  </dd>
                </>
              )}
              <dt>{uiText("Inputs")}</dt>
              <dd>
                {review.news
                  ? `${v.newsSources.find((s) => s.id === review.news?.sourceId)?.name ?? "Selected source"} · fresh within ${taskInterval(review.news.maxAgeSeconds)}`
                  : review.ai
                    ? uiText("Relevant memory")
                    : uiText("Saved task only")}
              </dd>
              {review.ai && (
                <>
                  <dt>{uiText("Analysis")}</dt>
                  <dd>
                    {uiText(
                      review.ai.analysis?.failure === "optional"
                        ? "Optional"
                        : "Required",
                    )}
                  </dd>
                  <dt>{uiText("If analysis fails")}</dt>
                  <dd>
                    {uiText(
                      review.ai.analysis?.failure === "optional"
                        ? "Keep news result"
                        : "Hold task",
                    )}
                  </dd>
                  {review.watch && (
                    <>
                      <dt>{uiText("Prompt")}</dt>
                      <dd>{review.text}</dd>
                    </>
                  )}
                  <dt>{uiText("Model")}</dt>
                  <dd>{review.ai.model}</dd>
                  <dt>{uiText("Account")}</dt>
                  <dd>{account?.name || account?.provider}</dd>
                  {!isUnmeteredPolicy(review.ai.policy) && (
                    <>
                      <dt>{uiText("Maximum cost")}</dt>
                      <dd>
                        ${(review.ai.budgetMicrousd / 1000000).toFixed(2)}
                      </dd>
                    </>
                  )}
                </>
              )}
            </dl>
            {!review.ai && (
              <p className="task-review-boundary">
                {review.bot
                  ? uiText("Paper only · no market orders")
                  : uiText("No AI or wallet access")}
              </p>
            )}
            {stale && (
              <p role="alert">
                {uiText("Records changed or review expired. Review again.")}
              </p>
            )}
            <div className="task-review-actions">
              <button
                className="text-button"
                disabled={busy}
                onClick={() => edit(() => {})}
              >
                {uiText("Edit task")}
              </button>
              {stale ? (
                <button
                  className="primary"
                  disabled={busy}
                  onClick={() => void prepare()}
                >
                  {uiText("Review again")}
                </button>
              ) : (
                <button
                  className="primary"
                  disabled={busy}
                  onClick={async () => {
                    if (
                      await agents.change({
                        ...command,
                        action: "task-reviewed-create",
                        calendar: review.calendar,
                        revision: review.revision,
                        expiresAt: review.expiresAt,
                        consentDigest: review.digest,
                      })
                    )
                      created();
                  }}
                >
                  {tr("Create task")}
                </button>
              )}
            </div>
            <details className="task-inspection">
              <summary>{uiText("Review details")}</summary>
              <pre>{JSON.stringify(review, null, 2)}</pre>
            </details>
          </>
        ) : (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void prepare();
            }}
          >
            <fieldset disabled={busy}>
              {!watchMode && (
                <label>
                  {uiText("Type")}
                  <select
                    aria-label={uiText("Type")}
                    value={kind}
                    onChange={(e) =>
                      edit(() =>
                        setKind(e.target.value as "reminder" | "analysis"),
                      )
                    }
                  >
                    <option value="reminder">{uiText("Reminder")}</option>
                    <option value="analysis">{uiText("Analysis")}</option>
                  </select>
                </label>
              )}
              <label>
                {tr("Task name")}
                <input
                  required
                  maxLength={80}
                  value={watchMode ? watchName : name}
                  onChange={(e) =>
                    edit(() =>
                      watchMode
                        ? setWatchName(e.target.value)
                        : setName(e.target.value),
                    )
                  }
                />
              </label>
              {watchMode && (
                <>
                  <NewsSourcePicker
                    view={v}
                    value={watchSource}
                    onChange={(value) => edit(() => setWatchSource(value))}
                    age={watchAge}
                    onAge={(value) => edit(() => setWatchAge(value))}
                  />
                  <label>
                    {uiText("Contains")}
                    <input
                      required
                      maxLength={120}
                      value={contains}
                      onChange={(e) => edit(() => setContains(e.target.value))}
                    />
                  </label>
                  <label className="ai-consent">
                    <input
                      type="checkbox"
                      checked={draft.analyze}
                      onChange={(e) =>
                        edit(() => patch({ analyze: e.target.checked }))
                      }
                    />
                    {uiText("Analyze")}
                  </label>
                </>
              )}
              {aiEnabled && (
                <>
                  <label>
                    {uiText("Prompt")}
                    <textarea
                      aria-label={uiText("Prompt")}
                      required
                      rows={3}
                      maxLength={1000}
                      value={draft.prompt}
                      onChange={(e) =>
                        edit(() => patch({ prompt: e.target.value }))
                      }
                    />
                  </label>
                  {mode === "analysis" && (
                    <NewsSourcePicker
                      view={v}
                      value={news}
                      onChange={(news) => edit(() => setNews(news))}
                      age={age}
                      onAge={(age) => edit(() => setAge(age))}
                    />
                  )}
                  {watchMode && (
                    <label>
                      {uiText("If analysis fails")}
                      <select
                        aria-label={uiText("If analysis fails")}
                        value={draft.failure}
                        onChange={(e) =>
                          edit(() =>
                            patch({
                              failure: e.target.value as
                                "required" | "optional",
                            }),
                          )
                        }
                      >
                        <option value="required">{uiText("Hold task")}</option>
                        <option value="optional">
                          {uiText("Keep news result")}
                        </option>
                      </select>
                    </label>
                  )}
                  {!aiReady && (
                    <p role="status">
                      {uiText("Provider unavailable")}{" "}
                      <button
                        type="button"
                        className="text-button"
                        onClick={() => setProvidersOpen(true)}
                      >
                        {uiText("Providers")}
                      </button>
                    </p>
                  )}
                  <details className="task-inspection">
                    <summary>{uiText("Model & limits")}</summary>
                    <label>
                      {uiText("Account")}
                      <select
                        aria-label={uiText("Account")}
                        value={connectionRef}
                        onChange={(e) =>
                          edit(() => {
                            const ref = e.target.value;
                            const next = accounts.find(
                              (item) => item.ref === ref,
                            );
                            patch({
                              overridden: true,
                              connectionRef: ref,
                              model:
                                next?.catalog?.models.find(
                                  (item) => item.selectable && item.recommended,
                                )?.id || "",
                            });
                          })
                        }
                      >
                        <option value="" disabled>
                          {uiText("Select")}
                        </option>
                        {accounts.map((item) => (
                          <option
                            key={item.ref}
                            value={item.ref}
                            disabled={item.locked || item.planPaused}
                          >
                            {item.name || item.provider} ·{" "}
                            {uiText(
                              item.authMethod === "sign-in"
                                ? "ChatGPT"
                                : item.authMethod === "oauth"
                                  ? "Google"
                                  : item.provider === "local"
                                    ? "Local"
                                    : "API key",
                            )}
                          </option>
                        ))}
                        {!!connectionRef && !account && (
                          <option value={connectionRef} disabled>
                            {uiText("Unavailable")}
                          </option>
                        )}
                      </select>
                    </label>
                    <AIModelPicker
                      value={model}
                      onChange={(model) =>
                        edit(() =>
                          patch({ overridden: true, connectionRef, model }),
                        )
                      }
                      connectionRef={connectionRef}
                      allowDemo={false}
                    />
                    <label>
                      {uiText("Context")}
                      <select
                        aria-label={uiText("Context")}
                        value={draft.session}
                        onChange={(e) =>
                          edit(() => patch({ session: e.target.value }))
                        }
                      >
                        <option value="">{uiText("Relevant memory")}</option>
                        {v.sessions.map((session) => (
                          <option key={session.id} value={session.id}>
                            {session.name}
                          </option>
                        ))}
                      </select>
                    </label>
                    <div className="agent-form-grid">
                      <label>
                        {uiText("Input tokens")}
                        <input
                          type="number"
                          required
                          min={1000}
                          max={32000}
                          value={draft.input}
                          onChange={(e) =>
                            edit(() => patch({ input: e.target.valueAsNumber }))
                          }
                        />
                      </label>
                      <label>
                        {uiText("Output tokens")}
                        <input
                          type="number"
                          required
                          min={128}
                          max={1024}
                          value={draft.output}
                          onChange={(e) =>
                            edit(() =>
                              patch({ output: e.target.valueAsNumber }),
                            )
                          }
                        />
                      </label>
                      <label>
                        {uiText("Deadline (seconds)")}
                        <input
                          type="number"
                          required
                          min={1}
                          max={local ? 60 : 5}
                          value={deadline}
                          onChange={(e) =>
                            edit(() =>
                              patch({ deadline: e.target.valueAsNumber }),
                            )
                          }
                        />
                      </label>
                      <label>
                        {uiText("Access (minutes)")}
                        <input
                          type="number"
                          required
                          min={1}
                          max={10080}
                          value={draft.minutes}
                          onChange={(e) =>
                            edit(() =>
                              patch({ minutes: e.target.valueAsNumber }),
                            )
                          }
                        />
                      </label>
                      {!unmetered && (
                        <>
                          <label>
                            {uiText("Per-call cost cap (micro USD)")}
                            <input
                              type="number"
                              required
                              min={1}
                              max={policy?.maxRequestBudgetMicrousd || 1000000}
                              value={perCall}
                              onChange={(e) =>
                                edit(() =>
                                  patch({ perCall: e.target.valueAsNumber }),
                                )
                              }
                            />
                          </label>
                          <label>
                            {uiText("AI total cost cap (micro USD)")}
                            <input
                              type="number"
                              required
                              min={1}
                              max={policy?.maxRequestBudgetMicrousd || 1000000}
                              value={totalBudget}
                              onChange={(e) =>
                                edit(() =>
                                  patch({ budget: e.target.valueAsNumber }),
                                )
                              }
                            />
                          </label>
                        </>
                      )}
                    </div>
                  </details>
                </>
              )}
              {!watchMode && !aiEnabled && !bot && (
                <label>
                  {tr("Reminder text")}
                  <textarea
                    required
                    rows={3}
                    maxLength={4000}
                    aria-label={tr("Reminder text")}
                    value={text}
                    placeholder={uiText(
                      "What should this task remind you to do?",
                    )}
                    onChange={(e) => edit(() => setText(e.target.value))}
                  />
                </label>
              )}
              <label>
                {tr("Schedule")}
                <select
                  aria-label={tr("Schedule")}
                  value={
                    cronMode
                      ? "cron"
                      : calendarMode
                        ? "calendar"
                        : runs === 1
                          ? "once"
                          : "repeat"
                  }
                  onChange={(e) =>
                    edit(() => {
                      const next = e.target.value;
                      setCalendarMode(next === "calendar");
                      setCronMode(next === "cron");
                      setRuns(next === "cron" ? 10 : next === "repeat" ? 3 : 1);
                      if (next === "calendar" || next === "cron") {
                        setBot("");
                        setNews("");
                      }
                    })
                  }
                >
                  <option value="once">{uiText("Run once")}</option>
                  <option value="repeat">{uiText("Repeat")}</option>
                  <option value="calendar">{uiText("At a date & time")}</option>
                  <option value="cron">{tr("Recurring calendar")}</option>
                </select>
              </label>
              {cronMode ? (
                <TaskCronFields
                  value={cron}
                  onChange={(value) => edit(() => setCron(value))}
                />
              ) : calendarMode ? (
                <div className="task-calendar-fields">
                  <label>
                    {uiText("Date & time")}
                    <input
                      type="datetime-local"
                      required
                      aria-label={uiText("Date & time")}
                      value={at}
                      onChange={(e) => edit(() => setAt(e.target.value))}
                    />
                  </label>
                  <label>
                    {uiText("Time zone")}
                    <select
                      value={zone}
                      aria-label={uiText("Time zone")}
                      onChange={(e) =>
                        edit(() => {
                          const next = e.target.value;
                          try {
                            setAt(
                              calendarInput(
                                calendarFromInput(at, zone).at,
                                next,
                              ),
                            );
                          } catch {
                            // A rejected wall time stays visible for correction.
                          }
                          setZone(next);
                        })
                      }
                    >
                      {timeZones(localZone).map((z) => (
                        <option key={z} value={z}>
                          {z}
                        </option>
                      ))}
                    </select>
                  </label>
                </div>
              ) : (
                <div className="task-timing">
                  <label>
                    {runs === 1 ? uiText("First run in") : uiText("Every")}
                    <input
                      type="number"
                      min={60 / unit}
                      max={604800 / unit}
                      step="any"
                      required
                      value={seconds / unit}
                      onChange={(e) =>
                        edit(() =>
                          setSeconds(Math.round(Number(e.target.value) * unit)),
                        )
                      }
                    />
                  </label>
                  <label>
                    {uiText("Unit")}
                    <select
                      value={unit}
                      onChange={(e) => {
                        const next = Number(e.target.value);
                        edit(() => {
                          setSeconds(
                            Math.min(
                              604800,
                              Math.max(60, (seconds / unit) * next),
                            ),
                          );
                          setUnit(next);
                        });
                      }}
                    >
                      <option value={60}>{uiText("Minutes")}</option>
                      <option value={3600}>{uiText("Hours")}</option>
                      <option value={86400}>{uiText("Days")}</option>
                    </select>
                  </label>
                </div>
              )}
              {!calendarMode && (runs > 1 || cronMode) && (
                <label>
                  {uiText("Maximum runs")}
                  <input
                    aria-label={uiText("Maximum evaluations")}
                    type="number"
                    required
                    min={cronMode ? 1 : 2}
                    max={aiEnabled ? 10 : 20}
                    value={runs}
                    onChange={(e) =>
                      edit(() => setRuns(Number(e.target.value)))
                    }
                  />
                </label>
              )}
              {!watchMode && !aiEnabled && !calendarMode && !cronMode && (
                <details className="task-inspection">
                  <summary>{uiText("Options")}</summary>
                  <label>
                    {uiText("Task kind")}
                    <select
                      value={bot}
                      onChange={(e) => edit(() => setBot(e.target.value))}
                    >
                      <option value="">{uiText("Local reminder")}</option>
                      {v.bots.map((b) => (
                        <option key={b.id} value={b.id}>
                          {uiText("Paper evaluation · ")}
                          {b.name}
                        </option>
                      ))}
                    </select>
                  </label>
                  <NewsSourcePicker
                    view={v}
                    value={news}
                    onChange={(value) => edit(() => setNews(value))}
                    age={age}
                    onAge={(value) => edit(() => setAge(value))}
                  />
                  <label>
                    {uiText("Interval in seconds")}
                    <input
                      required
                      type="number"
                      min={60}
                      max={604800}
                      value={seconds}
                      onChange={(e) =>
                        edit(() => setSeconds(Number(e.target.value)))
                      }
                    />
                  </label>
                  {runs === 1 && (
                    <label>
                      {uiText("Maximum evaluations")}
                      <input
                        type="number"
                        required
                        min={1}
                        max={aiEnabled ? 10 : 20}
                        value={runs}
                        onChange={(e) =>
                          edit(() => setRuns(Number(e.target.value)))
                        }
                      />
                    </label>
                  )}
                </details>
              )}
              <button
                className="primary"
                disabled={busy || (aiEnabled && !aiReady)}
              >
                {tr("Review task")}
              </button>
            </fieldset>
          </form>
        )}
      </div>
    </aside>
  );
}
