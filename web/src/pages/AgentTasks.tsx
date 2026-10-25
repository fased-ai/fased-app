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
  const [editing, setEditing] = useState(false);
  const [selected, setSelected] = useState("");
  const [resultId, setResultId] = useState("");
  const [reuse, setReuse] = useState("");
  const advanced = useRef<HTMLDetailsElement>(null);
  const runCards = useRef(new Map<string, HTMLElement>());
  const tasks = v.tasks.filter((t) => !t.wen);
  const selectedTask = tasks.find((t) => t.id === selected);
  useEffect(() => {
    if (!focusedTask) return;
    setPanel("automations");
    if (focusedTask === "new") setEditing(true);
    else if (tasks.some((t) => t.id === focusedTask)) setSelected(focusedTask);
    setFocusedTask("");
  }, [focusedTask, tasks, setFocusedTask, setPanel]);
  const selectedRun = v.runs.filter((r) => r.taskId === selected).at(-1);
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
    return (
      <div className="row-actions">
        {!t.ai && (
          <button
            className="text-button"
            disabled={agents.busy || t.state !== "scheduled"}
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
          <button className="text-button" onClick={reviewAI}>
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
          <Badge>{run.outcome}</Badge>
        </header>
        <time dateTime={new Date(run.at * 1000).toISOString()}>
          {date(run.at)}
        </time>
        <p className="task-result-text">
          {answer || notice?.text || task?.text || uiText("Saved result")}
        </p>
        {!run.wen &&
          !run.botId &&
          ["reminder-v1", "ai-step-v1/reminder-v1", "ai-step-v1"].includes(
            run.strategy,
          ) && (
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
                {t}
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
              setEditing(true);
              setReuse("");
            }}
          >
            {tr("New task")}
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
              <h2>{uiText("Automations")}</h2>
              <span>{tasks.length}</span>
            </div>
            {tasks.length === 0 ? (
              <div className="task-empty">
                <h3>{uiText("No tasks yet")}</h3>
                <button className="secondary" onClick={() => setEditing(true)}>
                  {uiText("Create your first task")}
                </button>
              </div>
            ) : (
              <table className="task-table" aria-label={uiText("Automations")}>
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
              key={v.profileId}
              view={v}
              close={() => setEditing(false)}
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
                    {selectedTask.text || uiText("Fixed paper evaluation")}
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
            <h2>{uiText("Task inbox")}</h2>
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
        <summary>{uiText("Schedule & storage")}</summary>
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
      <details className="scheduled-ai-disclosure" ref={advanced}>
        <summary>{uiText("AI tasks")}</summary>
        <ScheduledAITasks view={v} />
      </details>
    </div>
  );
}

function TaskComposer({
  view: v,
  close,
}: {
  view: AgentView;
  close: () => void;
}) {
  const { t: uiText } = useUI();
  const tr = uiText;
  const core = useCore();
  const { agents } = useWorkspace();
  const [name, setName] = useState("");
  const [text, setText] = useState("");
  const [bot, setBot] = useState("");
  const [news, setNews] = useState("");
  const [age, setAge] = useState(3600);
  const [seconds, setSeconds] = useState(60);
  const [unit, setUnit] = useState(60);
  const [runs, setRuns] = useState(1);
  const [calendarMode, setCalendarMode] = useState(false);
  const [cronMode, setCronMode] = useState(false);
  const localZone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  const [zone, setZone] = useState(localZone);
  const [cron, setCron] = useState<CronSettings>({
    preset: "daily",
    time: "09:00",
    weekday: "1",
    expression: "0 9 * * 1-5",
    timeZone: localZone,
    until: calendarInput(Date.now() / 1000 + 30 * 86400, localZone),
  });
  const [at, setAt] = useState(() =>
    calendarInput(Date.now() / 1000 + 1800, localZone),
  );
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
  const stale =
    !!review &&
    (review.revision !== v.revision || review.expiresAt * 1000 <= now);
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
    name: name.trim(),
    text: text.trim(),
    botId: bot,
    newsSourceId: news,
    newsMaxAgeSeconds: news ? age : undefined,
    calendar,
    intervalSeconds: calendarMode || cronMode ? 0 : seconds,
    maxRuns: calendarMode ? 1 : runs,
  };
  async function prepare() {
    const attempt = ++generation.current;
    setLoading(true);
    setError("");
    setReview(undefined);
    try {
      if (calendarError) throw Error(calendarError);
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
        <h3>{review ? tr("Review task") : tr("New task")}</h3>
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
              {review.bot
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
                  : uiText("Saved task only")}
              </dd>
            </dl>
            <p className="task-review-boundary">
              {review.bot
                ? uiText("Paper only · no market orders")
                : uiText("No AI or wallet access")}
            </p>
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
                      close();
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
              <label>
                {tr("Task name")}
                <input
                  required
                  maxLength={80}
                  value={name}
                  onChange={(e) => edit(() => setName(e.target.value))}
                />
              </label>
              {!bot && (
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
                    max={20}
                    value={runs}
                    onChange={(e) =>
                      edit(() => setRuns(Number(e.target.value)))
                    }
                  />
                </label>
              )}
              {!calendarMode && !cronMode && (
                <details className="task-inspection">
                  <summary>{uiText("Inputs & advanced timing")}</summary>
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
                        max={20}
                        value={runs}
                        onChange={(e) =>
                          edit(() => setRuns(Number(e.target.value)))
                        }
                      />
                    </label>
                  )}
                </details>
              )}
              <button className="primary" disabled={busy}>
                {tr("Review task")}
              </button>
            </fieldset>
          </form>
        )}
      </div>
    </aside>
  );
}
