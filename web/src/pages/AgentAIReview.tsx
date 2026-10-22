import { ConnectionPicker } from "./AIConnection";
import { useState } from "react";
import type { AgentView } from "../agents";
import { useWorkspace } from "../workspace";

const money = (n: number) => `$${(n / 1_000_000).toFixed(6)}`;
export function AgentAIReview({
  view,
  sessionId,
}: {
  view: AgentView;
  sessionId: string;
}) {
  const { agents } = useWorkspace();
  const [prompt, setPrompt] = useState("");
  const [connectionRef, setConnectionRef] = useState("");
  const [input, setInput] = useState(24000);
  const [output, setOutput] = useState(1024);
  const [deadline, setDeadline] = useState(3);
  const [budget, setBudget] = useState(30000);
  const [consent, setConsent] = useState("");
  const requests = view.aiRequests.filter(
    (q) => q.sessionId === sessionId && !q.taskId,
  );
  return (
    <section className="ai-review" aria-label="Optional AI request review">
      <h3>Optional AI step</h3>
      <p>
        Development fixture only. Optionally select an unlocked test connection.
        No OpenAI account, subscription or charge. Your saved provider
        preference does not enable execution.
      </p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void agents
            .change({
              action: "ai-prepare",
              connectionRef,
              id: sessionId,
              text: prompt,
              limits: {
                maxInputTokens: input,
                maxOutputTokens: output,
                deadlineSeconds: deadline,
                budgetMicrousd: budget,
              },
            })
            .then((ok) => {
              if (ok) {
                setPrompt("");
                setConsent("");
              }
            });
        }}
      >
        <ConnectionPicker value={connectionRef} onChange={setConnectionRef} />
        <label>
          AI request
          <textarea
            required
            maxLength={2000}
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
          />
        </label>
        <div className="agent-form-grid">
          <label>
            Maximum input tokens
            <input
              type="number"
              min={1000}
              max={32000}
              required
              value={input}
              onChange={(e) => setInput(e.target.valueAsNumber)}
            />
          </label>
          <label>
            Maximum output tokens
            <input
              type="number"
              min={128}
              max={1024}
              required
              value={output}
              onChange={(e) => setOutput(e.target.valueAsNumber)}
            />
          </label>
          <label>
            Request deadline (seconds)
            <input
              type="number"
              min={1}
              max={5}
              required
              value={deadline}
              onChange={(e) => setDeadline(e.target.valueAsNumber)}
            />
          </label>
          <label>
            Fixture budget (micro USD)
            <input
              type="number"
              min={1}
              max={1000000}
              required
              value={budget}
              onChange={(e) => setBudget(e.target.valueAsNumber)}
            />
          </label>
        </div>
        <p className="subtle">
          One request, zero tools, no retries or fallback. Example tariff: 1
          micro USD per input token and 4 per output token. These are invented
          fixture prices. Input uses a conservative byte bound including
          instructions and schema; no production cost guarantee.
        </p>
        <button className="secondary" disabled={agents.busy}>
          Prepare disclosure
        </button>
      </form>
      {requests.length === 0 && (
        <p>No prepared requests for this conversation.</p>
      )}
      {requests.map((q) => (
        <article className="ai-request" key={q.id}>
          <h4>{q.prompt}</h4>
          <p>
            <strong>{q.state}</strong> · {q.model} · Held{" "}
            {money(q.heldMicrousd)} · Spent {money(q.spentMicrousd)} (fixture
            only)
          </p>
          <p>
            Limits: {q.limits.maxInputTokens} input / {q.limits.maxOutputTokens}{" "}
            output tokens · {q.limits.deadlineSeconds}s · Budget{" "}
            {money(q.limits.budgetMicrousd)} · No tools
          </p>
          <details>
            <summary>Exact context disclosure</summary>
            <p>Connection: {q.connectionRef || "no credential"}</p>
            <pre>{q.disclosure}</pre>
            <p>
              Also included: fixed instructions treating this context as
              untrusted data, and the review-only output schema. No other
              profiles, expired notes, credentials or wallet records.
            </p>
            <p className="subtle">Disclosure SHA-256: {q.digest}</p>
          </details>
          {q.failure && <p role="status">{q.failure}</p>}
          {q.state === "prepared" && (
            <>
              <label className="ai-consent">
                <input
                  type="checkbox"
                  checked={consent === q.digest}
                  onChange={(e) => setConsent(e.target.checked ? q.digest : "")}
                />
                I reviewed this context and these fixture limits
              </label>
              <button
                className="primary"
                disabled={
                  agents.busy ||
                  consent !== q.digest ||
                  !view.aiFixtureAvailable
                }
                onClick={() => {
                  void agents
                    .change({
                      action: "ai-start",
                      id: q.id,
                      consentDigest: q.digest,
                    })
                    .then((ok) => {
                      if (ok) setConsent("");
                    });
                }}
              >
                Run development fixture
              </button>
              {!view.aiFixtureAvailable && (
                <p className="subtle">
                  Execution is disabled in this core. Only an explicitly started
                  local development fixture can run.
                </p>
              )}
            </>
          )}
          {["prepared", "queued", "running"].includes(q.state) && (
            <button
              className="secondary"
              disabled={agents.busy}
              onClick={() =>
                void agents.change({ action: "ai-cancel", id: q.id })
              }
            >
              Cancel request
            </button>
          )}
          {q.answer && <p className="ai-answer">{q.answer}</p>}
          {q.cards.map((card) => (
            <section
              className="ai-action-card"
              key={card.id}
              aria-label={card.title}
            >
              <small>
                {card.kind} · {card.review} · REVIEW ONLY
              </small>
              <h4>{card.title}</h4>
              <p>{card.text}</p>
              <p className="subtle">
                Keeping this draft records your review. It creates no task, bot,
                permission or trade.
              </p>
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
            </section>
          ))}
        </article>
      ))}
    </section>
  );
}
