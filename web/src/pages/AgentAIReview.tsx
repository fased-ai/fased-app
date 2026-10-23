import { AgentEvidenceInspector } from "./AgentEvidence";
import { ResearchTurnReview } from "./ResearchTurnReview";
import type { ResearchSelection } from "./ResearchReadPanel";
import { ChatMemoryReview } from "./ChatMemoryReview";
import { ReminderDraftReview } from "./ReminderDraftReview";
import { NewsSourcePicker } from "./NewsInputs";
import {
  AIModelPicker,
  AIAdmissionReview,
  ConnectionPicker,
} from "./AIConnection";
import { useEffect, useRef, useState } from "react";
import type {
  AgentView,
  AIMemorySelection,
  AgentAIRequestDraft,
  AIEvidenceSelection,
  AgentEvidenceView,
} from "../agents";
import { useWorkspace } from "../workspace";

const money = (n: number) => `$${(n / 1_000_000).toFixed(6)}`;
export function AgentAIReview({
  view,
  sessionId,
  onReviewStrategy,
  researchSelection,
  onClearResearch,
}: {
  view: AgentView;
  sessionId: string;
  onReviewStrategy?: (requestId: string, cardId: string) => void;
  researchSelection?: ResearchSelection;
  onClearResearch?: () => void;
}) {
  return (
    <AgentAIForm
      key={`${view.profileId}:${sessionId}`}
      view={view}
      sessionId={sessionId}
      onReviewStrategy={onReviewStrategy}
      researchSelection={researchSelection}
      onClearResearch={onClearResearch}
    />
  );
}
function AgentAIForm({
  view,
  sessionId,
  onReviewStrategy,
  researchSelection,
  onClearResearch,
}: {
  view: AgentView;
  sessionId: string;
  onReviewStrategy?: (requestId: string, cardId: string) => void;
  researchSelection?: ResearchSelection;
  onClearResearch?: () => void;
}) {
  const { core, agents } = useWorkspace();
  const [prompt, setPrompt] = useState("");
  const [newsSourceId, setNewsSourceId] = useState("");
  const [newsAge, setNewsAge] = useState(3600);
  const [connectionRef, setConnectionRef] = useState("");
  const [model, setModel] = useState("");
  const [input, setInput] = useState(24000);
  const [output, setOutput] = useState(1024);
  const [deadline, setDeadline] = useState(3);
  const [budget, setBudget] = useState(30000);
  const [modelAttempts, setModelAttempts] = useState(3);
  const [toolAttempts, setToolAttempts] = useState(8);
  const [turnSeconds, setTurnSeconds] = useState(90);
  const [turnBudget, setTurnBudget] = useState(90000);
  const [researchPreparing, setResearchPreparing] = useState(false);
  const [consent, setConsent] = useState("");
  const [memoryMode, setMemoryMode] = useState<
    AIMemorySelection["mode"] | "legacy"
  >("all");
  const [memoryIds, setMemoryIds] = useState<string[]>([]);
  const [draftSource, setDraftSource] = useState("");
  const [legacyDraft, setLegacyDraft] = useState(false);
  const [draftBusy, setDraftBusy] = useState(false);
  const [draftError, setDraftError] = useState("");
  const [evidenceSelection, setEvidenceSelection] =
    useState<AIEvidenceSelection>();
  const [evidencePreview, setEvidencePreview] =
    useState<AgentEvidenceView | null>(null);
  const questionRef = useRef<HTMLTextAreaElement>(null);
  const generation = useRef(0);
  useEffect(() => {
    generation.current++;
    setResearchPreparing(false);
    setConsent("");
    if (researchSelection) {
      setNewsSourceId("");
      setEvidenceSelection(undefined);
      setEvidencePreview(null);
      setLegacyDraft(false);
      setMemoryMode("all");
      setDraftSource("");
      questionRef.current?.focus();
    }
  }, [researchSelection]);
  useEffect(
    () => () => {
      generation.current++;
    },
    [],
  );
  function cancelDraft() {
    generation.current++;
    setDraftBusy(false);
    setDraftSource("");
    setDraftError("");
    setLegacyDraft(false);
    setEvidenceSelection(undefined);
    setEvidencePreview(null);
    setPrompt("");
    setNewsSourceId("");
    setNewsAge(3600);
    setConnectionRef("");
    setModel("");
    setInput(24000);
    setOutput(1024);
    setDeadline(3);
    setBudget(30000);
    setMemoryMode("all");
    setMemoryIds([]);
    setConsent("");
  }
  async function useDraft(id: string) {
    onClearResearch?.();
    const epoch = ++generation.current;
    setDraftBusy(true);
    setDraftError("");
    setConsent("");
    try {
      const d = await core.request<AgentAIRequestDraft>(
        `agents?profile=${encodeURIComponent(view.profileId)}&ai-draft=${encodeURIComponent(id)}`,
      );
      if (epoch !== generation.current) return;
      if (d.profileId !== view.profileId || d.sessionId !== sessionId)
        throw new Error("Saved request belongs to another conversation");
      setPrompt(d.prompt);
      setNewsSourceId(d.newsSourceId ?? "");
      setNewsAge(d.newsMaxAgeSeconds ?? 3600);
      setConnectionRef(d.connectionRef ?? "");
      setModel(d.model === "fixture-openai-v1" ? "" : d.model);
      setInput(d.limits.maxInputTokens);
      setOutput(d.limits.maxOutputTokens);
      setDeadline(d.limits.deadlineSeconds);
      setBudget(d.limits.budgetMicrousd);
      setMemoryMode(d.memorySelection?.mode ?? "legacy");
      setMemoryIds(d.memorySelection?.ids ?? []);
      setLegacyDraft(!d.memorySelection);
      setEvidenceSelection(d.evidenceSelection);
      setEvidencePreview(null);
      setDraftSource(id);
      if (d.revision !== view.revision) void agents.refresh().catch(() => {});
    } catch (e) {
      if (epoch === generation.current)
        setDraftError(
          e instanceof Error ? e.message : "Saved request unavailable",
        );
    } finally {
      if (epoch === generation.current) setDraftBusy(false);
    }
  }
  const toggleMemory = (id: string, checked: boolean) =>
    setMemoryIds((ids) =>
      checked ? [...ids, id] : ids.filter((n) => n !== id),
    );
  const requests = view.aiRequests.filter(
    (q) => q.sessionId === sessionId && !q.taskId,
  );
  const evidenceAvailable =
    !evidenceSelection ||
    (evidenceSelection.kind === "memory"
      ? view.memories.some((m) => m.id === evidenceSelection.id && !m.expired)
      : evidenceSelection.kind === "headline"
        ? view.newsFetches.some(
            (f) =>
              f.id === evidenceSelection.id &&
              f.state === "completed" &&
              f.items.some((i) => i.id === evidenceSelection.itemId),
          )
        : view.runs.some(
            (r) =>
              r.id === evidenceSelection.id &&
              r.strategy === "reminder-v1" &&
              !r.news &&
              !r.botId &&
              !r.wen,
          ));
  function removeEvidence() {
    generation.current++;
    setDraftBusy(false);
    setEvidenceSelection(undefined);
    setEvidencePreview(null);
    setConsent("");
  }
  return (
    <>
      {!researchSelection && (
        <AgentEvidenceInspector
          view={view}
          sessionId={sessionId}
          onUse={(e) => {
            generation.current++;
            setDraftBusy(false);
            setDraftError("");
            setDraftSource("");
            setEvidenceSelection({
              kind: e.kind,
              id: e.id,
              ...(e.itemId ? { itemId: e.itemId } : {}),
              sha256: e.sha256,
            });
            setEvidencePreview(e);
            setConsent("");
            questionRef.current?.focus();
          }}
        />
      )}
      <section className="ai-review" aria-label="Optional AI request review">
        <h3>Optional AI step</h3>
        <p>
          Development fixture only. Optionally select an unlocked test
          connection. No OpenAI account, subscription or charge. Your saved
          provider preference does not enable execution.
        </p>
        {draftError && <p role="alert">{draftError}</p>}
        {(draftSource || draftBusy) && (
          <div className="agent-inline">
            <h4>{draftBusy ? "Loading request draft…" : "Request draft"}</h4>
            <button type="button" className="secondary" onClick={cancelDraft}>
              Cancel draft
            </button>
          </div>
        )}
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (researchSelection) {
              if (researchPreparing) return;
              const epoch = generation.current;
              setResearchPreparing(true);
              setDraftError("");
              void core
                .request("wen/research/ai-prepare", "POST", {
                  schema: "wen-research-ai-prepare/1",
                  selection: { ...researchSelection, revision: view.revision },
                  prompt,
                  model: model || "fixture-openai-v1",
                  ...(connectionRef ? { connectionRef } : {}),
                  memorySelection: {
                    mode: memoryMode,
                    ...(memoryMode === "selected" ? { ids: memoryIds } : {}),
                  },
                  limits: {
                    maxInputTokens: input,
                    maxOutputTokens: output,
                    deadlineSeconds: deadline,
                    budgetMicrousd: budget,
                  },
                  bounds: {
                    maxModelAttempts: modelAttempts,
                    maxToolAttempts: toolAttempts,
                    wallSeconds: turnSeconds,
                    budgetMicrousd: turnBudget,
                  },
                })
                .then(() => {
                  if (epoch === generation.current) {
                    onClearResearch?.();
                    setPrompt("");
                  }
                  void agents.refresh().catch(() => {});
                })
                .catch((err: Error) => {
                  if (epoch === generation.current) setDraftError(err.message);
                })
                .finally(() => {
                  if (epoch === generation.current) setResearchPreparing(false);
                });
              return;
            }
            void agents
              .change({
                action: "ai-prepare",
                evidenceSelection,
                ...(memoryMode === "legacy"
                  ? {}
                  : {
                      memorySelection: {
                        mode: memoryMode,
                        ...(memoryMode === "selected"
                          ? { ids: memoryIds }
                          : {}),
                      },
                    }),
                connectionRef,
                model,
                newsSourceId,
                newsMaxAgeSeconds: newsSourceId ? newsAge : undefined,
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
                  setDraftSource("");
                  setEvidenceSelection(undefined);
                  setEvidencePreview(null);
                }
              });
          }}
        >
          {researchSelection && (
            <fieldset aria-label="Selected research for AI">
              <legend>Selected research</legend>
              <pre>{JSON.stringify(researchSelection, null, 2)}</pre>
              <button
                type="button"
                className="secondary"
                disabled={researchPreparing}
                onClick={onClearResearch}
              >
                Remove research
              </button>
            </fieldset>
          )}
          {evidenceSelection && (
            <fieldset aria-label="AI evidence selection">
              <legend>Selected evidence</legend>
              <pre>{JSON.stringify(evidenceSelection, null, 2)}</pre>
              {!evidenceAvailable && (
                <p role="status">
                  Selected evidence unavailable. Inspect again.
                </p>
              )}
              {evidencePreview && (
                <details>
                  <summary>Inspected record</summary>
                  <pre>{JSON.stringify(evidencePreview, null, 2)}</pre>
                </details>
              )}
              <button
                type="button"
                className="secondary"
                onClick={removeEvidence}
              >
                Remove evidence
              </button>
            </fieldset>
          )}
          <ConnectionPicker value={connectionRef} onChange={setConnectionRef} />
          <AIModelPicker
            value={model}
            onChange={setModel}
            connectionRef={connectionRef}
          />
          {!researchSelection && (
            <NewsSourcePicker
              view={view}
              value={newsSourceId}
              onChange={setNewsSourceId}
              age={newsAge}
              onAge={setNewsAge}
            />
          )}
          <label>
            Memory
            <select
              value={memoryMode}
              onChange={(e) =>
                setMemoryMode(
                  e.target.value as AIMemorySelection["mode"] | "legacy",
                )
              }
            >
              {legacyDraft && (
                <option value="legacy">Legacy active notes</option>
              )}
              <option value="all">All active notes</option>
              <option value="none">None</option>
              <option value="selected">Selected notes</option>
            </select>
          </label>
          {memoryMode === "selected" && (
            <fieldset aria-label="AI memory selection">
              <legend>Selected notes</legend>
              {view.memories.map((m) => (
                <label key={m.id}>
                  <input
                    type="checkbox"
                    checked={memoryIds.includes(m.id)}
                    disabled={m.expired && !memoryIds.includes(m.id)}
                    onChange={(e) => toggleMemory(m.id, e.target.checked)}
                  />
                  {m.text}
                  {m.expired ? " (expired)" : ""}
                </label>
              ))}
              {memoryIds
                .filter((id) => !view.memories.some((m) => m.id === id))
                .map((id) => (
                  <label key={id}>
                    <input
                      type="checkbox"
                      checked
                      onChange={() => toggleMemory(id, false)}
                    />
                    Unavailable note: {id}
                  </label>
                ))}
            </fieldset>
          )}
          <label>
            AI request
            <textarea
              ref={questionRef}
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
          {researchSelection && (
            <fieldset
              className="agent-form-grid"
              aria-label="Research turn limits"
            >
              <legend>Research turn limits</legend>
              <label>
                Model attempts
                <input
                  type="number"
                  min={1}
                  max={3}
                  required
                  value={modelAttempts}
                  onChange={(e) => setModelAttempts(e.target.valueAsNumber)}
                />
              </label>
              <label>
                Read attempts
                <input
                  type="number"
                  min={1}
                  max={8}
                  required
                  value={toolAttempts}
                  onChange={(e) => setToolAttempts(e.target.valueAsNumber)}
                />
              </label>
              <label>
                Research deadline (seconds)
                <input
                  type="number"
                  min={1}
                  max={90}
                  required
                  value={turnSeconds}
                  onChange={(e) => setTurnSeconds(e.target.valueAsNumber)}
                />
              </label>
              <label>
                Turn budget (micro USD)
                <input
                  type="number"
                  min={1}
                  max={3000000}
                  required
                  value={turnBudget}
                  onChange={(e) => setTurnBudget(e.target.valueAsNumber)}
                />
              </label>
            </fieldset>
          )}
          <p className="subtle">
            {model ? (
              "Selected snapshot uses its disclosed standard tariff and provider token-count preflight. Development fixtures only."
            ) : (
              <>
                One request, zero tools, no retries or fallback. Example tariff:
                1 micro USD per input token and 4 per output token. These are
                invented fixture prices. Input uses a conservative byte bound
                including instructions and schema; no production cost guarantee.
              </>
            )}
          </p>
          <button
            className="secondary"
            disabled={
              agents.busy ||
              researchPreparing ||
              draftBusy ||
              (memoryMode === "selected" && memoryIds.length === 0)
            }
          >
            {researchSelection
              ? "Prepare research disclosure"
              : "Prepare disclosure"}
          </button>
        </form>
        {requests.length === 0 && (
          <p>No prepared requests for this conversation.</p>
        )}
        {requests.map((q) => (
          <article className="ai-request" key={q.id}>
            <h4>{q.prompt}</h4>
            {!q.researchTurnId && (
              <button
                type="button"
                className="secondary"
                disabled={agents.busy || draftBusy}
                onClick={() => void useDraft(q.id)}
              >
                Use as draft
              </button>
            )}
            {q.researchTurnId && (
              <ResearchTurnReview
                request={q}
                revision={view.revision}
                fixtureAvailable={view.aiFixtureAvailable}
                onChanged={() => agents.refresh()}
              />
            )}
            <AIAdmissionReview policy={q.policy} admission={q.admission} />
            <p>
              <strong>{q.state}</strong> · {q.model} · Held{" "}
              {money(q.heldMicrousd)} · Spent {money(q.spentMicrousd)} (fixture
              only)
            </p>
            <p>
              Limits: {q.limits.maxInputTokens} input /{" "}
              {q.limits.maxOutputTokens} output tokens ·{" "}
              {q.limits.deadlineSeconds}s · Budget{" "}
              {money(q.limits.budgetMicrousd)} · No tools
            </p>
            <details>
              <summary>Exact context disclosure</summary>
              <p>Connection: {q.connectionRef || "no credential"}</p>
              {q.news && (
                <p>
                  News snapshot: {q.news.fetchId} · source {q.news.url} ·
                  freshness {q.news.maxAgeSeconds}s · content hash{" "}
                  {q.news.bodySha256}
                </p>
              )}
              <pre>{q.disclosure}</pre>
              <p>
                Also included: fixed instructions treating this context as
                untrusted data, and the review-only output schema. No other
                profiles, expired notes, credentials or wallet records.
              </p>
              <p className="subtle">Disclosure SHA-256: {q.digest}</p>
            </details>
            {q.failure && <p role="status">{q.failure}</p>}
            {q.state === "prepared" && !q.researchTurnId && (
              <>
                <label className="ai-consent">
                  <input
                    type="checkbox"
                    checked={consent === q.digest}
                    onChange={(e) =>
                      setConsent(e.target.checked ? q.digest : "")
                    }
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
                    Execution is disabled in this core. Only an explicitly
                    started local development fixture can run.
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
            {q.state === "completed" && q.answer && (
              <ChatMemoryReview
                key={`${view.profileId}:${q.id}`}
                view={view}
                sourceId={q.id}
                sourceKind="answer"
                sourceText={q.answer}
                sessionId={q.sessionId}
              />
            )}
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
                  Keeping this draft records your review. It creates no task,
                  bot, permission or trade.
                </p>
                {q.state === "completed" &&
                  card.kind === "paper-strategy-draft" &&
                  card.review !== "dismissed" &&
                  onReviewStrategy && (
                    <button
                      className="secondary"
                      disabled={agents.busy}
                      onClick={() => onReviewStrategy(q.id, card.id)}
                    >
                      Review WEN revision
                    </button>
                  )}
                {card.kind === "reminder-draft" &&
                  card.review !== "dismissed" && (
                    <ReminderDraftReview
                      key={`${view.profileId}:${card.id}`}
                      view={view}
                      card={card}
                    />
                  )}
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
    </>
  );
}
