import { agentReplyDefaults } from "../agent-defaults";
import { useUI } from "../ui-preferences";
import { NewsSourcePicker } from "./NewsInputs";
import {
  AIModelPicker,
  useAIConnection,
  AIAdmissionReview,
  ConnectionPicker,
} from "./AIConnection";
import { useState, useEffect, useRef } from "react";
import { useWorkspace } from "../workspace";
import type { AgentView } from "../agents";
import {
  isUnmeteredPolicy,
  planFailureDetails,
  hasReportedProviderUsage,
} from "../agents";
const money = (n: number) => `$${(n / 1_000_000).toFixed(6)}`;
export function ScheduledAITasks({ view: v }: { view: AgentView }) {
  const { t: uiText } = useUI();
  const { agents } = useWorkspace();
  const [name, setName] = useState("");
  const [prompt, setPrompt] = useState("");
  const [newsSourceId, setNewsSourceId] = useState("");
  const [newsAge, setNewsAge] = useState(3600);
  const { view: accounts } = useAIConnection();
  const agent = v.profiles.find((p) => p.id === v.profileId)!;
  const access = agent.connectionRef
    ? (accounts?.connections?.find((a) => a.ref === agent.connectionRef) ??
      null)
    : accounts;
  const inherited = agentReplyDefaults(
    agent,
    access
      ? {
          ref: access.ref,
          provider: access.provider,
          locked: access.locked || !!access.planPaused,
          model: access.catalog?.models.find(
            (m) =>
              m.selectable &&
              m.connectionRef === access.ref &&
              m.id === agent.model,
          )?.id,
        }
      : null,
  );
  const [overridden, setOverridden] = useState(false);
  const [connectionRef, setConnectionRef] = useState("");
  const [model, setModel] = useState("");
  useEffect(() => {
    if (overridden) return;
    setConnectionRef(inherited.connectionRef);
    setModel(inherited.model);
  }, [inherited.connectionRef, inherited.model, overridden]);
  const [session, setSession] = useState("");
  const [bot, setBot] = useState("");
  const [interval, setInterval] = useState(60);
  const [runs, setRuns] = useState(2);
  const [minutes, setMinutes] = useState(60);
  const [input, setInput] = useState(24000);
  const [output, setOutput] = useState(1024);
  const [deadline, setDeadline] = useState(3);
  const [budget, setBudget] = useState(60000);
  const [perCall, setPerCall] = useState(30000);
  const selectedAccount =
    accounts?.connections?.find((item) => item.ref === connectionRef) ??
    (accounts?.ref === connectionRef ? accounts : null);
  const selectedPolicy = selectedAccount?.catalog?.models.find(
    (item) => item.id === model,
  )?.policy;
  const planUsage = isUnmeteredPolicy(selectedPolicy);
  const interactiveOnly = selectedPolicy?.provider === "kimi-code";
  const budgetModel = useRef("");
  useEffect(() => {
    const identity = connectionRef + ":" + model + ":" + selectedPolicy?.id;
    if (budgetModel.current === identity) return;
    const p = selectedPolicy;
    if (!p) return;
    budgetModel.current = identity;
    if (isUnmeteredPolicy(p)) {
      if (p.provider === "local") setDeadline(60);
      return;
    }
    const ceiling = Math.ceil(
      (input * p.inputMicrousdPerMillion +
        Math.max(output, p.billableOutputTokens || 0) *
          p.outputMicrousdPerMillion) /
        1000000,
    );
    setPerCall((b) =>
      Math.min(p.maxRequestBudgetMicrousd || 1000000, Math.max(b, ceiling)),
    );
    setBudget((b) =>
      Math.min(
        p.maxRequestBudgetMicrousd || 1000000,
        Math.max(b, ceiling * runs),
      ),
    );
  }, [model, connectionRef, input, output, runs, accounts, selectedPolicy]);
  const [consent, setConsent] = useState("");
  const sessionID =
    v.sessions.find((s) => s.id === session)?.id ?? v.sessions[0]?.id ?? "";
  return (
    <section
      className="panel scheduled-ai"
      aria-label={uiText("Scheduled AI steps")}
    >
      <div className="panel-heading">
        <h2>{uiText("Scheduled AI steps")}</h2>
        <span>{uiText(planUsage ? "Usage" : "DEVELOPMENT FIXTURES")}</span>
      </div>
      <div className="agent-record-body">
        {!planUsage && (
          <p>
            {uiText(
              "Authorize a fixed context and a bounded schedule. Each successful, validated fixture response allows the selected reminder or fixed paper evaluation. Cards cannot change inputs or execute trades. Changed context, expired consent or provider failure holds this task; independent local tasks continue.",
            )}
          </p>
        )}
        {v.tasks
          .filter((t) => t.ai)
          .map((t) => {
            const a = t.ai!;
            const requests = v.aiRequests.filter((q) => q.taskId === t.id);
            const spent = requests.reduce((n, q) => n + q.spentMicrousd, 0);
            const held = requests.reduce((n, q) => n + q.heldMicrousd, 0);
            const taskPlanUsage = isUnmeteredPolicy(a.policy);
            const reported = requests.filter((q) =>
              hasReportedProviderUsage(q.costBasis),
            );
            return (
              <article
                className="ai-request"
                key={t.id}
                aria-label={`AI task ${t.name}`}
              >
                <h3>{t.name}</h3>
                <p>
                  <strong>{t.state}</strong> · {t.runs}/{t.maxRuns}
                  {uiText(" evaluations · Every ")}
                  {t.intervalSeconds}
                  {uiText("s")}
                </p>
                {taskPlanUsage ? (
                  <p>
                    {uiText("Usage")}
                    {!!reported.length && (
                      <>
                        {" "}
                        · {uiText("Input tokens")}:{" "}
                        {reported.reduce((n, q) => n + q.inputTokens, 0)} ·{" "}
                        {uiText("Output tokens")}:{" "}
                        {reported.reduce((n, q) => n + q.outputTokens, 0)}
                      </>
                    )}
                    {requests.some((q) => q.state === "unknown") && (
                      <> · {uiText("Usage unavailable")}</>
                    )}
                  </p>
                ) : (
                  <p>
                    {uiText("Fixture cost: spent ")}
                    {money(spent)}
                    {uiText(" + held ")}
                    {money(held)} / {money(a.budgetMicrousd)}
                    {uiText(
                      " total. One call per evaluation, zero tools, no retries or provider fallback.",
                    )}
                  </p>
                )}
                <p>
                  {uiText("Consent expires")}{" "}
                  {new Date(a.expiresAt * 1000).toLocaleString(
                    typeof document === "undefined"
                      ? "en"
                      : document.documentElement.lang,
                  )}
                  {uiText(
                    ". The frozen context is rechecked before dispatch and dependent work.",
                  )}
                </p>
                {a.bot ? (
                  <p>
                    {uiText("Fixed paper inputs: ")}
                    {a.bot.name} · {a.bot.quantity} × {a.bot.priceCents}
                    {uiText(" · Shared paper budget ")}
                    {a.bot.budgetCents} {uiText("fixture units.")}
                  </p>
                ) : (
                  <p>
                    {uiText("Dependent reminder: ")}
                    {t.text}
                  </p>
                )}
                <details>
                  <summary>{uiText("Review AI task disclosure")}</summary>
                  <p>
                    {uiText("Connection: ")}
                    {a.connectionRef || uiText("no credential")}
                  </p>
                  {t.news && (
                    <p>
                      {uiText(
                        "Each run fetches up to five untrusted headlines from",
                      )}{" "}
                      {t.news.url}
                      {uiText(", with publication/retrieval age at most")}{" "}
                      {t.news.maxAgeSeconds}
                      {uiText(
                        "s. New evidence is recorded per request. Failure holds only this task.",
                      )}
                    </p>
                  )}
                  <pre>{a.disclosure}</pre>
                  <p>
                    {uiText("Per call: ")}
                    {a.limits.maxInputTokens}
                    {uiText(" input /")} {a.limits.maxOutputTokens}
                    {uiText(" output tokens ·")} {a.limits.deadlineSeconds}
                    {uiText("s")}
                    {!taskPlanUsage && (
                      <>
                        {" · "}
                        {money(a.limits.budgetMicrousd)}
                        {uiText(" cap.")}{" "}
                      </>
                    )}
                    {!taskPlanUsage &&
                      (a.policy ? (
                        uiText(
                          "Use the snapshot tariff disclosed below; all calls remain simulated.",
                        )
                      ) : (
                        <>
                          {uiText(
                            "Invented rates: 1 micro USD per input token, 4 per output token. No provider, API key, subscription or real charge.",
                          )}
                        </>
                      ))}
                  </p>
                  <AIAdmissionReview policy={a.policy} />
                  <p>
                    {uiText("Task SHA-256: ")}
                    {a.digest}
                  </p>
                </details>
                {t.state === "review" && (
                  <>
                    <label className="ai-consent">
                      <input
                        type="checkbox"
                        checked={consent === a.digest}
                        onChange={(e) =>
                          setConsent(e.target.checked ? a.digest : "")
                        }
                      />
                      {uiText("Allow this frozen context, schedule and limits")}
                    </label>
                    <button
                      className="primary"
                      disabled={
                        agents.busy ||
                        !(v.aiExecutionAvailable ?? v.aiFixtureAvailable) ||
                        consent !== a.digest
                      }
                      onClick={() =>
                        void agents
                          .change({
                            action: "task-ai-authorize",
                            id: t.id,
                            consentDigest: a.digest,
                          })
                          .then((ok) => {
                            if (ok) setConsent("");
                          })
                      }
                    >
                      {uiText(
                        taskPlanUsage
                          ? "Authorize task"
                          : "Authorize fixture schedule",
                      )}
                    </button>
                  </>
                )}
                {t.state === "scheduled" && (
                  <button
                    className="secondary"
                    disabled={
                      agents.busy ||
                      !(v.aiExecutionAvailable ?? v.aiFixtureAvailable)
                    }
                    onClick={() =>
                      void agents.change({ action: "task-run", id: t.id })
                    }
                  >
                    {uiText("Evaluate now")}
                  </button>
                )}
                {["scheduled", "paused"].includes(t.state) && (
                  <button
                    className="text-button"
                    disabled={
                      agents.busy ||
                      !(v.aiExecutionAvailable ?? v.aiFixtureAvailable)
                    }
                    onClick={() =>
                      void agents.change({
                        action:
                          t.state === "paused" ? "task-resume" : "task-pause",
                        id: t.id,
                      })
                    }
                  >
                    {t.state === "paused"
                      ? uiText("Resume AI schedule")
                      : uiText("Pause AI schedule")}
                  </button>
                )}
                {["review", "scheduled", "paused", "waiting-ai"].includes(
                  t.state,
                ) && (
                  <button
                    className="text-button"
                    disabled={agents.busy}
                    onClick={() =>
                      void agents.change({ action: "task-ai-cancel", id: t.id })
                    }
                  >
                    {t.state === "waiting-ai"
                      ? uiText("Stop pending AI step")
                      : uiText("Cancel AI schedule")}
                  </button>
                )}
                {t.state === "held" && (
                  <p role="status">
                    {uiText(
                      "Dependent work stopped. Prepare and review a new plan. An uncertain dispatched reservation cannot be reset.",
                    )}
                  </p>
                )}
                {!(v.aiExecutionAvailable ?? v.aiFixtureAvailable) && (
                  <p className="subtle">
                    {uiText(
                      "Only a standalone core explicitly started with the development fixture can activate or run AI schedules.",
                    )}
                  </p>
                )}
                {requests.map((q) => {
                  const planFailure = isUnmeteredPolicy(q.policy)
                    ? planFailureDetails(q.failure)
                    : null;
                  return (
                    <section className="ai-action-card" key={q.id}>
                      <small>
                        {uiText("Request ")}
                        {q.id} · {q.state}
                      </small>
                      <AIAdmissionReview
                        policy={q.policy}
                        admission={q.admission}
                      />
                      <p>
                        {q.answer ||
                          (planFailure
                            ? uiText(planFailure.summary)
                            : q.failure) ||
                          uiText("Waiting for the fixture result.")}
                      </p>
                      {planFailure && (
                        <details>
                          <summary>{uiText("Provider response")}</summary>
                          <pre>{planFailure.details}</pre>
                        </details>
                      )}
                      {q.cards.map((card) => (
                        <div key={card.id}>
                          <strong>{card.title}</strong>
                          <p>{card.text}</p>
                          <small>
                            {card.review}
                            {uiText(" · REVIEW ONLY")}
                          </small>
                          {card.review === "unreviewed" && (
                            <div className="agent-inline">
                              <button
                                className="secondary"
                                disabled={agents.busy}
                                onClick={() =>
                                  void agents.change({
                                    action: "ai-card-keep",
                                    id: card.id,
                                  })
                                }
                              >
                                {uiText("Keep draft")}
                              </button>
                              <button
                                className="text-button"
                                disabled={agents.busy}
                                onClick={() =>
                                  void agents.change({
                                    action: "ai-card-dismiss",
                                    id: card.id,
                                  })
                                }
                              >
                                {uiText("Dismiss draft")}
                              </button>
                            </div>
                          )}
                        </div>
                      ))}
                    </section>
                  );
                })}
              </article>
            );
          })}
        <ConnectionPicker
          value={connectionRef}
          onChange={(ref) => {
            setOverridden(true);
            setConnectionRef(ref);
            const a = accounts?.connections?.find((a) => a.ref === ref);
            setModel(
              ref
                ? a?.catalog?.models.find((m) => m.selectable && m.recommended)
                    ?.id || ""
                : "",
            );
          }}
        />
        <AIModelPicker
          value={model}
          onChange={(value) => {
            setOverridden(true);
            setModel(value);
          }}
          connectionRef={connectionRef}
        />
        {interactiveOnly && (
          <p role="status">
            {uiText("Chat only. Choose an API account for tasks.")}
          </p>
        )}
        <NewsSourcePicker
          view={v}
          value={newsSourceId}
          onChange={setNewsSourceId}
          age={newsAge}
          onAge={setNewsAge}
        />
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (interactiveOnly) return;
            void agents
              .change({
                action: "task-ai-prepare",
                useAgentDefaults: !overridden,
                connectionRef,
                model,
                newsSourceId,
                newsMaxAgeSeconds: newsSourceId ? newsAge : undefined,
                name,
                text: prompt,
                id: sessionID,
                botId: bot,
                intervalSeconds: interval,
                maxRuns: runs,
                totalBudgetMicrousd: planUsage ? 0 : budget,
                authorizationSeconds: minutes * 60,
                limits: {
                  maxInputTokens: input,
                  maxOutputTokens: output,
                  deadlineSeconds: deadline,
                  budgetMicrousd: planUsage ? 0 : perCall,
                },
              })
              .then((ok) => {
                if (ok) {
                  setName("");
                  setPrompt("");
                  setConsent("");
                }
              });
          }}
        >
          <h3>{uiText("Prepare an AI schedule")}</h3>
          <label>
            {uiText("AI task title")}
            <input
              required
              maxLength={80}
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </label>
          <label>
            {uiText("AI context conversation")}
            <select
              required
              value={sessionID}
              onChange={(e) => setSession(e.target.value)}
            >
              <option value="">
                {uiText("Create a conversation in Chat first")}
              </option>
              {v.sessions.map((s) => (
                <option value={s.id} key={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          </label>
          <label>
            {uiText("AI analysis prompt")}
            <textarea
              required
              maxLength={1000}
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
            />
          </label>
          <label>
            {uiText("Step after validated AI response")}
            <select value={bot} onChange={(e) => setBot(e.target.value)}>
              <option value="">
                {uiText("Local reminder using this prompt")}
              </option>
              {v.bots.map((b) => (
                <option value={b.id} key={b.id}>
                  {uiText("Fixed paper evaluation · ")}
                  {b.name}
                </option>
              ))}
            </select>
          </label>
          <div className="agent-grid">
            <label>
              {uiText("AI cadence (seconds)")}
              <input
                type="number"
                required
                min={60}
                max={604800}
                value={interval}
                onChange={(e) => setInterval(e.target.valueAsNumber)}
              />
            </label>
            <label>
              {uiText("AI run count")}
              <input
                type="number"
                required
                min={1}
                max={10}
                value={runs}
                onChange={(e) => setRuns(e.target.valueAsNumber)}
              />
            </label>
            <label>
              {uiText("AI consent lifetime (minutes)")}
              <input
                type="number"
                required
                min={1}
                max={10080}
                value={minutes}
                onChange={(e) => setMinutes(e.target.valueAsNumber)}
              />
            </label>
            {!planUsage && (
              <label>
                {uiText("AI total cost cap (micro USD)")}
                <input
                  type="number"
                  required
                  min={1}
                  max={model.startsWith("grok-") ? 10000000 : 1000000}
                  value={budget}
                  onChange={(e) => setBudget(e.target.valueAsNumber)}
                />
              </label>
            )}
            <label>
              {uiText("Input token cap")}
              <input
                type="number"
                required
                min={1000}
                max={32000}
                value={input}
                onChange={(e) => setInput(e.target.valueAsNumber)}
              />
            </label>
            <label>
              {uiText("Output token cap")}
              <input
                type="number"
                required
                min={128}
                max={1024}
                value={output}
                onChange={(e) => setOutput(e.target.valueAsNumber)}
              />
            </label>
            <label>
              {uiText("AI timeout (seconds)")}
              <input
                type="number"
                required
                min={1}
                max={selectedPolicy?.provider === "local" ? 120 : 5}
                value={deadline}
                onChange={(e) => setDeadline(e.target.valueAsNumber)}
              />
            </label>
            {!planUsage && (
              <label>
                {uiText("Per-call cost cap (micro USD)")}
                <input
                  type="number"
                  required
                  min={1}
                  max={model.startsWith("grok-") ? 10000000 : 1000000}
                  value={perCall}
                  onChange={(e) => setPerCall(e.target.valueAsNumber)}
                />
              </label>
            )}
          </div>
          {!planUsage && (
            <p className="subtle">
              {uiText(
                "Reserve enough for every evaluation at the reviewed limits. Up to ten retained/future requests share local capacity. Prices and token counts are fixtures; no real API is connected.",
              )}
            </p>
          )}
          <button
            className="secondary"
            disabled={agents.busy || !sessionID || interactiveOnly}
          >
            {uiText("Prepare AI task review")}
          </button>
        </form>
      </div>
    </section>
  );
}
