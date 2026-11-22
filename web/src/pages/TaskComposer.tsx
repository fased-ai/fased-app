import { useEffect, useRef, useState } from "react";
import { previewModel } from "../ui-language-preview";
import { resolveLanguage, translate } from "../ui-language";
import { useUI } from "../ui-preferences";
import {
  isUnmeteredPolicy,
  type AgentCommand,
  type AgentTask,
  type AgentView,
  type TaskCalendar,
  type TaskReview,
} from "../agents";
import type { ParsedTaskDraft } from "../agents-v2";
import { useCore } from "../core";
import { useWorkspace, type TaskComposerDraft } from "../workspace";
import {
  intervalWithUnit,
  taskInterval as formatTimingInterval,
} from "../task-timing";
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
import { TaskAccessSettings } from "./TaskAccess";

const displayLocale = () =>
  typeof document === "undefined" ? "en" : document.documentElement.lang;
const taskCopy = (key: string, values?: Record<string, string | number>) =>
  translate(resolveLanguage("system", [displayLocale()]), key, values);

export function formatInterval(seconds: number) {
  return formatTimingInterval(seconds, displayLocale());
}

export function formatScheduleSummary(t: {
  calendar?: TaskCalendar;
  intervalSeconds: number;
  maxRuns: number;
}) {
  return t.calendar
    ? t.calendar.version === "cron-v1"
      ? taskCopy("{schedule} · {count} runs", {
          schedule: calendarLabel(t.calendar),
          count: t.maxRuns,
        })
      : taskCopy("Once · {schedule}", { schedule: calendarLabel(t.calendar) })
    : t.maxRuns === 1
      ? taskCopy("Once, after {duration}", {
          duration: formatInterval(t.intervalSeconds),
        })
      : taskCopy("Every {duration} · {count} runs", {
          duration: formatInterval(t.intervalSeconds),
          count: t.maxRuns,
        });
}

export function AnalysisTaskAccess({
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

export function TaskComposer({
  draftState,
  updateDraft,
  view: v,
  watchMode,
  close,
  created,
  seed,
  draftSeed,
}: {
  view: AgentView;
  draftState: TaskComposerDraft;
  updateDraft: (
    update: (draft: TaskComposerDraft) => TaskComposerDraft,
  ) => void;
  watchMode: boolean;
  close: () => void;
  created: () => void;
  seed?: AgentTask;
  draftSeed?: ParsedTaskDraft;
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
  const kind =
    draftState.kind ??
    (seed?.ai?.analysis && !seed.watch ? "analysis" : "reminder");
  const mode = watchMode ? "watch" : kind;
  const [initialDrafts] = useState(() => {
    const initial = {
      reminder: freshDraft(),
      analysis: freshDraft(),
      watch: { ...freshDraft(), name: uiText("News watch") },
    };
    if (draftSeed) {
      const seedSeconds = Math.max(
        60,
        Math.min(604800, draftSeed.intervalSeconds),
      );
      const seedUnit =
        seedSeconds % 86400 === 0
          ? 86400
          : seedSeconds % 3600 === 0
            ? 3600
            : 60;
      initial.reminder = {
        ...initial.reminder,
        name: draftSeed.name,
        text: draftSeed.description,
        seconds: seedSeconds,
        unit: seedUnit,
      };
      initial.analysis = {
        ...initial.analysis,
        name: draftSeed.name,
        prompt: draftSeed.description,
        seconds: seedSeconds,
        unit: seedUnit,
      };
    }
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
  const drafts = draftState.inputs ?? initialDrafts;
  const draft = drafts[mode];
  const setKind = (nextKind: "reminder" | "analysis") =>
    updateDraft((saved) => ({
      ...saved,
      kind: nextKind,
      inputs: saved.inputs ?? initialDrafts,
    }));
  const patch = (change: Partial<Draft>) =>
    updateDraft((saved) => {
      const inputs = saved.inputs ?? initialDrafts;
      return {
        ...saved,
        inputs: { ...inputs, [mode]: { ...inputs[mode], ...change } },
      };
    });
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
  const setName = (value: string) => patch({ name: value });
  const setWatchName = setName;
  const setText = (value: string) => patch({ text: value });
  const setBot = (value: string) => patch({ bot: value });
  const setNews = (value: string) => patch({ news: value });
  const setWatchSource = setNews;
  const setAge = (value: number) => patch({ age: value });
  const setWatchAge = setAge;
  const setContains = (value: string) => patch({ contains: value });
  const setSeconds = (value: number) => patch({ seconds: value });
  const setUnit = (value: number) => patch({ unit: value });
  const setRuns = (value: number) => patch({ runs: value });
  const setCalendarMode = (value: boolean) => patch({ calendarMode: value });
  const setCronMode = (value: boolean) => patch({ cronMode: value });
  const setZone = (value: string) => patch({ zone: value });
  const setCron = (value: CronSettings) => patch({ cron: value });
  const setAt = (value: string) => patch({ at: value });
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
  const deadline =
    draft.deadline ?? (local || (account && !account.testOnly) ? 60 : 3);
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
                  : formatScheduleSummary(review)}
              </dd>
              <dt>{uiText("Starts")}</dt>
              <dd>
                {review.calendar
                  ? calendarLabel({ ...review.calendar, version: "once-at-v1" })
                  : `${formatInterval(review.intervalSeconds)} after creation`}
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
                  ? `${v.newsSources.find((s) => s.id === review.news?.sourceId)?.name ?? "Selected source"} · fresh within ${formatInterval(review.news.maxAgeSeconds)}`
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
                  <dd>
                    {review.ai.model && previewModel(review.ai.model, uiText)}
                  </dd>
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
                      onChange={(nextNews) => edit(() => setNews(nextNews))}
                      age={age}
                      onAge={(nextAge) => edit(() => setAge(nextAge))}
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
                      onChange={(nextModel) =>
                        edit(() =>
                          patch({
                            overridden: true,
                            connectionRef,
                            model: nextModel,
                          }),
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
                          max={120}
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
              <details className="task-inspection">
                <summary>{tr("Schedule")}</summary>
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
                        setRuns(
                          next === "cron" ? 10 : next === "repeat" ? 3 : 1,
                        );
                        if (next === "calendar" || next === "cron") {
                          setBot("");
                          setNews("");
                        }
                      })
                    }
                  >
                    <option value="once">{uiText("Run once")}</option>
                    <option value="repeat">{uiText("Repeat")}</option>
                    <option value="calendar">
                      {uiText("At a date & time")}
                    </option>
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
                            setSeconds(
                              Math.round(Number(e.target.value) * unit),
                            ),
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
                            setSeconds(intervalWithUnit(seconds, unit, next));
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
              </details>
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
