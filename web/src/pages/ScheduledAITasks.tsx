import { NewsSourcePicker } from "./NewsInputs";
import { ConnectionPicker } from "./AIConnection";
import { useState } from "react";
import { useWorkspace } from "../workspace";
import type { AgentView } from "../agents";
const money = (n: number) => `$${(n / 1_000_000).toFixed(6)}`;
export function ScheduledAITasks({ view: v }: { view: AgentView }) {
  const { agents } = useWorkspace();
  const [name, setName] = useState("");
  const [prompt, setPrompt] = useState("");
  const [newsSourceId, setNewsSourceId] = useState("");
  const [newsAge, setNewsAge] = useState(3600);
  const [connectionRef, setConnectionRef] = useState("");
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
  const [consent, setConsent] = useState("");
  const sessionID =
    v.sessions.find((s) => s.id === session)?.id ?? v.sessions[0]?.id ?? "";
  return (
    <section className="panel scheduled-ai" aria-label="Scheduled AI steps">
      <div className="panel-heading">
        <h2>Scheduled AI steps</h2>
        <span>DEVELOPMENT FIXTURES</span>
      </div>
      <div className="agent-record-body">
        <p>
          Authorize a fixed context and a bounded schedule. Each successful,
          validated fixture response allows the selected reminder or fixed paper
          evaluation. Cards cannot change inputs or execute trades. Changed
          context, expired consent or provider failure holds this task;
          independent local tasks continue.
        </p>
        {v.tasks
          .filter((t) => t.ai)
          .map((t) => {
            const a = t.ai!;
            const requests = v.aiRequests.filter((q) => q.taskId === t.id);
            const spent = requests.reduce((n, q) => n + q.spentMicrousd, 0);
            const held = requests.reduce((n, q) => n + q.heldMicrousd, 0);
            return (
              <article
                className="ai-request"
                key={t.id}
                aria-label={`AI task ${t.name}`}
              >
                <h3>{t.name}</h3>
                <p>
                  <strong>{t.state}</strong> · {t.runs}/{t.maxRuns} evaluations
                  · Every {t.intervalSeconds}s
                </p>
                <p>
                  Fixture cost: spent {money(spent)} + held {money(held)} /{" "}
                  {money(a.budgetMicrousd)} total. One call per evaluation, zero
                  tools, no retries or provider fallback.
                </p>
                <p>
                  Consent expires{" "}
                  {new Date(a.expiresAt * 1000).toLocaleString()}. The frozen
                  context is rechecked before dispatch and dependent work.
                </p>
                {a.bot ? (
                  <p>
                    Fixed paper inputs: {a.bot.name} · {a.bot.quantity} ×{" "}
                    {a.bot.priceCents} · Shared paper budget {a.bot.budgetCents}{" "}
                    fixture units.
                  </p>
                ) : (
                  <p>Dependent reminder: {t.text}</p>
                )}
                <details>
                  <summary>Review AI task disclosure</summary>
                  <p>Connection: {a.connectionRef || "no credential"}</p>
                  {t.news && (
                    <p>
                      Each run fetches up to five untrusted headlines from{" "}
                      {t.news.url}, with publication/retrieval age at most{" "}
                      {t.news.maxAgeSeconds}s. New evidence is recorded per
                      request. Failure holds only this task.
                    </p>
                  )}
                  <pre>{a.disclosure}</pre>
                  <p>
                    Per call: {a.limits.maxInputTokens} input /{" "}
                    {a.limits.maxOutputTokens} output tokens ·{" "}
                    {a.limits.deadlineSeconds}s ·{" "}
                    {money(a.limits.budgetMicrousd)} cap. Invented rates: 1
                    micro USD per input token, 4 per output token. No provider,
                    API key, subscription or real charge.
                  </p>
                  <p>Task SHA-256: {a.digest}</p>
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
                      Allow this frozen context, schedule and limits
                    </label>
                    <button
                      className="primary"
                      disabled={
                        agents.busy ||
                        !v.aiFixtureAvailable ||
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
                      Authorize fixture schedule
                    </button>
                  </>
                )}
                {t.state === "scheduled" && (
                  <button
                    className="secondary"
                    disabled={agents.busy || !v.aiFixtureAvailable}
                    onClick={() =>
                      void agents.change({ action: "task-run", id: t.id })
                    }
                  >
                    Evaluate now
                  </button>
                )}
                {["scheduled", "paused"].includes(t.state) && (
                  <button
                    className="text-button"
                    disabled={agents.busy || !v.aiFixtureAvailable}
                    onClick={() =>
                      void agents.change({
                        action:
                          t.state === "paused" ? "task-resume" : "task-pause",
                        id: t.id,
                      })
                    }
                  >
                    {t.state === "paused"
                      ? "Resume AI schedule"
                      : "Pause AI schedule"}
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
                      ? "Stop pending AI step"
                      : "Cancel AI schedule"}
                  </button>
                )}
                {t.state === "held" && (
                  <p role="status">
                    Dependent work stopped. Prepare and review a new plan. An
                    uncertain dispatched reservation cannot be reset.
                  </p>
                )}
                {!v.aiFixtureAvailable && (
                  <p className="subtle">
                    Only a standalone core explicitly started with the
                    development fixture can activate or run AI schedules.
                  </p>
                )}
                {requests.map((q) => (
                  <section className="ai-action-card" key={q.id}>
                    <small>
                      Request {q.id} · {q.state}
                    </small>
                    <p>
                      {q.answer ||
                        q.failure ||
                        "Waiting for the fixture result."}
                    </p>
                    {q.cards.map((card) => (
                      <div key={card.id}>
                        <strong>{card.title}</strong>
                        <p>{card.text}</p>
                        <small>{card.review} · REVIEW ONLY</small>
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
                              Keep draft
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
                              Dismiss draft
                            </button>
                          </div>
                        )}
                      </div>
                    ))}
                  </section>
                ))}
              </article>
            );
          })}
        <ConnectionPicker value={connectionRef} onChange={setConnectionRef} />
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
            void agents
              .change({
                action: "task-ai-prepare",
                connectionRef,
                newsSourceId,
                newsMaxAgeSeconds: newsSourceId ? newsAge : undefined,
                name,
                text: prompt,
                id: sessionID,
                botId: bot,
                intervalSeconds: interval,
                maxRuns: runs,
                totalBudgetMicrousd: budget,
                authorizationSeconds: minutes * 60,
                limits: {
                  maxInputTokens: input,
                  maxOutputTokens: output,
                  deadlineSeconds: deadline,
                  budgetMicrousd: perCall,
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
          <h3>Prepare an AI schedule</h3>
          <label>
            AI task title
            <input
              required
              maxLength={80}
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </label>
          <label>
            AI context conversation
            <select
              required
              value={sessionID}
              onChange={(e) => setSession(e.target.value)}
            >
              <option value="">Create a conversation in Chat first</option>
              {v.sessions.map((s) => (
                <option value={s.id} key={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          </label>
          <label>
            AI analysis prompt
            <textarea
              required
              maxLength={1000}
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
            />
          </label>
          <label>
            Step after validated AI response
            <select value={bot} onChange={(e) => setBot(e.target.value)}>
              <option value="">Local reminder using this prompt</option>
              {v.bots.map((b) => (
                <option value={b.id} key={b.id}>
                  Fixed paper evaluation · {b.name}
                </option>
              ))}
            </select>
          </label>
          <div className="agent-grid">
            <label>
              AI cadence (seconds)
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
              AI run count
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
              AI consent lifetime (minutes)
              <input
                type="number"
                required
                min={1}
                max={10080}
                value={minutes}
                onChange={(e) => setMinutes(e.target.valueAsNumber)}
              />
            </label>
            <label>
              AI total cost cap (micro USD)
              <input
                type="number"
                required
                min={1}
                max={1000000}
                value={budget}
                onChange={(e) => setBudget(e.target.valueAsNumber)}
              />
            </label>
            <label>
              Input token cap
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
              Output token cap
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
              AI timeout (seconds)
              <input
                type="number"
                required
                min={1}
                max={5}
                value={deadline}
                onChange={(e) => setDeadline(e.target.valueAsNumber)}
              />
            </label>
            <label>
              Per-call cost cap (micro USD)
              <input
                type="number"
                required
                min={1}
                max={1000000}
                value={perCall}
                onChange={(e) => setPerCall(e.target.valueAsNumber)}
              />
            </label>
          </div>
          <p className="subtle">
            Reserve enough for every evaluation at the reviewed limits. Up to
            ten retained/future requests share local capacity. Prices and token
            counts are fixtures; no real API is connected.
          </p>
          <button className="secondary" disabled={agents.busy || !sessionID}>
            Prepare AI task review
          </button>
        </form>
      </div>
    </section>
  );
}
