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
import { AgentPlaybooks } from "./AgentPlaybooks";
import { ScheduledAITasks } from "./ScheduledAITasks";
import { SavedReminderDraft } from "./SavedReminderDraft";
import {
  calendarFromInput,
  calendarInput,
  calendarLabel,
} from "../task-calendar";
import { NewsSourcePicker } from "./NewsInputs";

const date = (at: number) => (at ? new Date(at * 1000).toLocaleString() : "—");
export function taskInterval(seconds: number) {
  if (seconds % 86400 === 0)
    return `${seconds / 86400} ${seconds === 86400 ? "day" : "days"}`;
  if (seconds % 3600 === 0)
    return `${seconds / 3600} ${seconds === 3600 ? "hour" : "hours"}`;
  if (seconds % 60 === 0)
    return `${seconds / 60} ${seconds === 60 ? "minute" : "minutes"}`;
  return `${seconds} seconds`;
}
const schedule = (t: {
  calendar?: TaskCalendar;
  intervalSeconds: number;
  maxRuns: number;
}) =>
  t.calendar
    ? "Once · " + calendarLabel(t.calendar)
    : t.maxRuns === 1
      ? `Once, after ${taskInterval(t.intervalSeconds)}`
      : `Every ${taskInterval(t.intervalSeconds)} · ${t.maxRuns} runs`;
const nextRun = (t: AgentTask) =>
  t.calendar ? calendarLabel(t.calendar) : date(t.nextAt);
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
  const { agents } = useWorkspace();
  const [editing, setEditing] = useState(false);
  const [selected, setSelected] = useState("");
  const [resultId, setResultId] = useState("");
  const [reuse, setReuse] = useState("");
  const advanced = useRef<HTMLDetailsElement>(null);
  const runCards = useRef(new Map<string, HTMLElement>());
  const tasks = v.tasks.filter((t) => !t.wen);
  const selectedTask = tasks.find((t) => t.id === selected);
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
            aria-label={`Run now: ${t.name}`}
            onClick={async () => {
              if (await agents.change({ action: "task-run", id: t.id })) {
                setSelected(t.id);
                setEditing(false);
              }
            }}
          >
            Run
          </button>
        )}
        {needsReview ? (
          <button className="text-button" onClick={reviewAI}>
            Review
          </button>
        ) : (
          <button
            className="text-button"
            disabled={agents.busy || finished(t)}
            aria-label={`${t.state === "paused" || t.state === "held" ? "Resume" : "Pause"}: ${t.name}`}
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
            {t.state === "paused" || t.state === "held" ? "Resume" : "Pause"}
          </button>
        )}
        <button
          className="text-button"
          aria-label={`Result: ${t.name}`}
          disabled={!v.runs.some((r) => r.taskId === t.id)}
          onClick={() => {
            const r = v.runs.filter((r) => r.taskId === t.id).at(-1);
            if (r) showRun(r.id);
          }}
        >
          Result
        </button>
        {!t.calendar && !t.news && !t.botId && !t.ai && (
          <button
            className="text-button"
            aria-label={`Reuse: ${t.name}`}
            onClick={() => {
              setReuse(t.id);
              setEditing(false);
            }}
          >
            Reuse
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
            {inline ? "Latest result" : (task?.name ?? "Task unavailable")}
          </strong>
          <Badge>{run.outcome}</Badge>
        </header>
        <time dateTime={new Date(run.at * 1000).toISOString()}>
          {date(run.at)}
        </time>
        <p className="task-result-text">
          {answer || notice?.text || task?.text || "Saved result"}
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
              Discuss run
            </button>
          )}
        <details className="task-inspection">
          <summary>Run details</summary>
          <p>
            {run.strategy} · Run {run.id}
          </p>
          {run.botId && (
            <p>
              Quantity {run.quantity} · Fixed price {run.priceCents} · Cost{" "}
              {run.costCents} · Budget before {run.budgetBefore}
            </p>
          )}
          {run.wen && <p>Unsigned paper receipt · {run.wen.receiptSha256}</p>}
          {run.news && (
            <p className="news-provenance">
              News snapshot {run.news.fetchId} · {run.news.url} · retrieved{" "}
              {date(run.news.fetchedAt)} · {run.news.bodySha256}
            </p>
          )}
        </details>
      </article>
    );
  }
  return (
    <div className="tasks-screen">
      <div className="tasks-toolbar">
        <nav className="task-views tabs" aria-label="Task views">
          {["automations", "history", "inbox", "playbooks"].map((t) => (
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
          ))}
        </nav>
        <div className="row-actions">
          <button
            className="text-button"
            aria-label="Refresh"
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
            New task
          </button>
        </div>
      </div>
      <div hidden={panel !== "playbooks"}>
        <AgentPlaybooks key={v.profileId} view={v} />
      </div>
      <section className="panel task-board" hidden={panel !== "automations"}>
        <div
          className={`task-workspace ${editing || selectedTask || reuse ? "with-detail" : ""}`}
        >
          <div className="task-list">
            <div className="panel-heading">
              <h2>Automations</h2>
              <span>{tasks.length}</span>
            </div>
            {tasks.length === 0 ? (
              <div className="task-empty">
                <h3>No tasks yet</h3>
                <button className="secondary" onClick={() => setEditing(true)}>
                  Create your first task
                </button>
              </div>
            ) : (
              <table className="task-table" aria-label="Automations">
                <thead>
                  <tr>
                    <th>Task</th>
                    <th>Status</th>
                    <th>Runs</th>
                    <th>Next run</th>
                    <th>Actions</th>
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
              <aside className="task-detail" aria-label="Task details">
                <div className="panel-heading">
                  <h3>{selectedTask.name}</h3>
                  <button
                    className="text-button"
                    aria-label="Close task details"
                    onClick={() => setSelected("")}
                  >
                    Close
                  </button>
                </div>
                <div className="task-detail-body">
                  <p className="task-result-text">
                    {selectedTask.text || "Fixed paper evaluation"}
                  </p>
                  <dl className="task-facts">
                    <dt>Schedule</dt>
                    <dd>{schedule(selectedTask)}</dd>
                    <dt>Next run</dt>
                    <dd>
                      {selectedTask.state === "scheduled"
                        ? nextRun(selectedTask)
                        : "—"}
                    </dd>
                    <dt>Status</dt>
                    <dd>{selectedTask.state}</dd>
                  </dl>
                  {selectedTask.state === "held" && (
                    <p role="status">
                      {v.newsFetches
                        .filter((f) => f.taskId === selectedTask.id)
                        .at(-1)?.failure ||
                        (selectedTask.ai
                          ? "Review a fresh AI plan before continuing."
                          : "Check the selected news source, then resume.")}
                    </p>
                  )}
                  {selectedRun ? (
                    panel === "automations" ? (
                      result(selectedRun, true)
                    ) : null
                  ) : (
                    <p className="empty-row">No runs yet.</p>
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
          aria-label="Run results"
          hidden={panel !== "history"}
        >
          <div className="panel-heading">
            <h2>Results</h2>
            {resultId && (
              <button className="text-button" onClick={() => setResultId("")}>
                All results
              </button>
            )}
          </div>
          <div className="agent-record-body">
            {v.runs.length === 0 ? (
              <p className="empty-row">No runs yet.</p>
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
            <h2>Task inbox</h2>
          </div>
          <div className="agent-record-body">
            {v.notices.length === 0 ? (
              <p>No task notifications.</p>
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
                          {canView ? "View result" : "Result unavailable"}
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
                            Mark read
                          </button>
                        ) : (
                          <small>Read</small>
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
        <summary>Schedule & storage</summary>
        <p>
          Tasks run while Fased runs, even with the browser closed. Missed
          intervals become one run. Resume starts a new interval. Calendar
          reminders keep their original date; overdue reminders run once after
          resume or restart. Run now completes a calendar reminder early; it
          will not run again on its date.
        </p>
        <p>
          Intervals: 1 minute–7 days, up to 20 runs. Calendar reminders: one
          date within 366 days, in local time or UTC. Cron is not available yet.
        </p>
        {v.taskCapacity && (
          <p>
            Workspace capacity: {v.taskCapacity.tasks}/100 tasks ·{" "}
            {v.taskCapacity.evaluations}/200 allocated runs ·{" "}
            {v.taskCapacity.runs}/200 results · {v.taskCapacity.notices}/200
            notifications · {Math.ceil(v.taskCapacity.bytes / 1024)}/512 KiB.
            Future output also reserves space. History is retained; reaching a
            limit stops further writes. Continuous operation beyond these limits
            is not available yet.
          </p>
        )}
      </details>
      <details className="scheduled-ai-disclosure" ref={advanced}>
        <summary>Advanced AI schedules · fixtures</summary>
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
  const localZone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  const [zone, setZone] = useState(localZone);
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
  if (calendarMode) {
    try {
      calendar = calendarFromInput(at, zone);
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
    intervalSeconds: calendarMode ? 0 : seconds,
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
      aria-label={review ? "Review task" : "Create a task"}
    >
      <div className="panel-heading">
        <h3>{review ? "Review task" : "New task"}</h3>
        <button type="button" className="text-button" onClick={close}>
          Cancel task
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
              <dt>Agent</dt>
              <dd>{v.profiles.find((p) => p.id === review.profileId)?.name}</dd>
              <dt>Schedule</dt>
              <dd>{review.calendar ? "Once" : schedule(review)}</dd>
              <dt>Starts</dt>
              <dd>
                {review.calendar
                  ? calendarLabel(review.calendar)
                  : `${taskInterval(review.intervalSeconds)} after creation`}
              </dd>
              <dt>Inputs</dt>
              <dd>
                {review.news
                  ? `${v.newsSources.find((s) => s.id === review.news?.sourceId)?.name ?? "Selected source"} · fresh within ${taskInterval(review.news.maxAgeSeconds)}`
                  : "Saved task only"}
              </dd>
            </dl>
            <p className="task-review-boundary">
              {review.bot
                ? "Paper only · no market orders"
                : "No AI or wallet access"}
            </p>
            {stale && (
              <p role="alert">
                Records changed or review expired. Review again.
              </p>
            )}
            <div className="task-review-actions">
              <button
                className="text-button"
                disabled={busy}
                onClick={() => edit(() => {})}
              >
                Edit task
              </button>
              {stale ? (
                <button
                  className="primary"
                  disabled={busy}
                  onClick={() => void prepare()}
                >
                  Review again
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
                        revision: review.revision,
                        expiresAt: review.expiresAt,
                        consentDigest: review.digest,
                      })
                    )
                      close();
                  }}
                >
                  Create task
                </button>
              )}
            </div>
            <details className="task-inspection">
              <summary>Review details</summary>
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
                Task name
                <input
                  required
                  maxLength={80}
                  value={name}
                  onChange={(e) => edit(() => setName(e.target.value))}
                />
              </label>
              {!bot && (
                <label>
                  Reminder text
                  <textarea
                    required
                    rows={3}
                    maxLength={4000}
                    aria-label="Reminder text"
                    value={text}
                    placeholder="What should this task remind you to do?"
                    onChange={(e) => edit(() => setText(e.target.value))}
                  />
                </label>
              )}
              <label>
                Schedule
                <select
                  aria-label="Schedule"
                  value={
                    calendarMode ? "calendar" : runs === 1 ? "once" : "repeat"
                  }
                  onChange={(e) =>
                    edit(() => {
                      const next = e.target.value;
                      setCalendarMode(next === "calendar");
                      setRuns(next === "repeat" ? 3 : 1);
                      if (next === "calendar") {
                        setBot("");
                        setNews("");
                      }
                    })
                  }
                >
                  <option value="once">Run once</option>
                  <option value="repeat">Repeat</option>
                  <option value="calendar">At a date & time</option>
                </select>
              </label>
              {calendarMode ? (
                <div className="task-calendar-fields">
                  <label>
                    Date & time
                    <input
                      type="datetime-local"
                      required
                      aria-label="Date & time"
                      value={at}
                      onChange={(e) => edit(() => setAt(e.target.value))}
                    />
                  </label>
                  <label>
                    Time zone
                    <select
                      value={zone}
                      aria-label="Time zone"
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
                      {localZone !== "UTC" && (
                        <option value={localZone}>{localZone}</option>
                      )}
                      <option value="UTC">UTC</option>
                    </select>
                  </label>
                </div>
              ) : (
                <div className="task-timing">
                  <label>
                    {runs === 1 ? "First run in" : "Every"}
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
                    Unit
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
                      <option value={60}>Minutes</option>
                      <option value={3600}>Hours</option>
                      <option value={86400}>Days</option>
                    </select>
                  </label>
                </div>
              )}
              {!calendarMode && runs > 1 && (
                <label>
                  Maximum runs
                  <input
                    aria-label="Maximum evaluations"
                    type="number"
                    required
                    min={2}
                    max={20}
                    value={runs}
                    onChange={(e) =>
                      edit(() => setRuns(Number(e.target.value)))
                    }
                  />
                </label>
              )}
              {!calendarMode && (
                <details className="task-inspection">
                  <summary>Inputs & advanced timing</summary>
                  <label>
                    Task kind
                    <select
                      value={bot}
                      onChange={(e) => edit(() => setBot(e.target.value))}
                    >
                      <option value="">Local reminder</option>
                      {v.bots.map((b) => (
                        <option key={b.id} value={b.id}>
                          Paper evaluation · {b.name}
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
                    Interval in seconds
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
                      Maximum evaluations
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
                Review task
              </button>
            </fieldset>
          </form>
        )}
      </div>
    </aside>
  );
}
