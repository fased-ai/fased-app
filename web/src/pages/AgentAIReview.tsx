import { useUI } from "../ui-preferences";
type ChatComposer = {
  value: string;
  onChange: (value: string) => void;
  onSave: () => Promise<void>;
};
import { AgentEvidenceInspector } from "./AgentEvidence";
import { ChatReply } from "./ChatReply";
import { SlidersHorizontal, BookOpen } from "../icons";
import type { ResearchSelection } from "./ResearchReadPanel";
import { ChatMemoryReview } from "./ChatMemoryReview";
import { NewsSourcePicker } from "./NewsInputs";
import {
  AIModelPicker,
  ConnectionPicker,
  type Connection,
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

export function AgentAIReview({
  view,
  sessionId,
  onReviewStrategy,
  researchSelection,
  onClearResearch,
  composer,
}: {
  view: AgentView;
  sessionId: string;
  onReviewStrategy?: (requestId: string, cardId: string) => void;
  researchSelection?: ResearchSelection;
  onClearResearch?: () => void;
  composer?: ChatComposer;
}) {
  return (
    <AgentAIForm
      key={`${view.profileId}:${sessionId}`}
      view={view}
      sessionId={sessionId}
      onReviewStrategy={onReviewStrategy}
      researchSelection={researchSelection}
      onClearResearch={onClearResearch}
      composer={composer}
    />
  );
}
function AgentAIForm({
  view,
  sessionId,
  onReviewStrategy,
  researchSelection,
  onClearResearch,
  composer,
}: {
  view: AgentView;
  sessionId: string;
  onReviewStrategy?: (requestId: string, cardId: string) => void;
  researchSelection?: ResearchSelection;
  onClearResearch?: () => void;
  composer?: ChatComposer;
}) {
  const { core, agents, navigate } = useWorkspace();
  const { t: tr } = useUI();
  const [optionsOpen, setOptionsOpen] = useState(false);
  const [localPrompt, setLocalPrompt] = useState("");
  const prompt = composer ? composer.value : localPrompt;
  const remembering = /^\s*\/remember(?:\s|$)/.test(prompt);
  function setPrompt(value: string) {
    setLocalPrompt(value);
    composer?.onChange(value);
  }
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
  const [researchMode, setResearchMode] = useState("single");
  const [consent, setConsent] = useState("");
  const [memoryMode, setMemoryMode] = useState<
    AIMemorySelection["mode"] | "legacy"
  >("relevant");
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
  const preparing = useRef(false);
  const currentPrompt = useRef(prompt);
  currentPrompt.current = prompt;
  const messagesRef = useRef<HTMLDivElement>(null);
  const followReplies = useRef(true);
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
    setOptionsOpen(true);
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
  const latest = requests.at(-1);
  useEffect(() => {
    const el = messagesRef.current;
    if (!followReplies.current || !el) return;
    const reply = el.lastElementChild;
    if (latest?.state === "completed" && reply)
      el.scrollTop +=
        reply.getBoundingClientRect().top - el.getBoundingClientRect().top;
    else el.scrollTop = el.scrollHeight;
  }, [view.messages.length, requests.length, latest?.state]);
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
      <details className="chat-evidence">
        <summary>Saved evidence</summary>
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
      </details>
      <section className="ai-review" aria-label="Optional AI request review">
        {draftError && <p role="alert">{draftError}</p>}
        {(draftSource || draftBusy) && (
          <div className="agent-inline">
            <h4>{draftBusy ? "Loading request draft…" : "Request draft"}</h4>
            <button type="button" className="secondary" onClick={cancelDraft}>
              Cancel draft
            </button>
          </div>
        )}
        <div
          className="local-messages"
          aria-live="polite"
          ref={messagesRef}
          onScroll={(e) => {
            const el = e.currentTarget;
            followReplies.current =
              el.scrollHeight - el.scrollTop - el.clientHeight < 80;
          }}
        >
          {[
            ...view.messages
              .filter((m) => m.sessionId === sessionId)
              .map((message) => ({
                id: message.id,
                at: message.createdAt,
                message,
                request: undefined,
              })),
            ...requests.map((request) => ({
              id: request.id,
              at: request.createdAt,
              message: undefined,
              request,
            })),
          ]
            .sort(
              (a, b) => a.at - b.at || Number(!a.message) - Number(!b.message),
            )
            .map((entry) =>
              entry.message ? (
                <article
                  className={`chat-message ${entry.message.role === "system" ? "chat-system" : ""}`}
                  key={entry.id}
                >
                  {entry.message.role === "system" ? (
                    <details>
                      <summary>Saved locally</summary>
                      <p>{entry.message.text}</p>
                    </details>
                  ) : (
                    <>
                      <small>
                        {entry.message.role === "user" ? "You" : "Record"} ·{" "}
                        {new Date(entry.at * 1000).toLocaleString()}
                      </small>
                      <p>{entry.message.text}</p>
                    </>
                  )}
                  {entry.message.role === "user" && (
                    <ChatMemoryReview
                      view={view}
                      sourceKind="message"
                      sourceId={entry.message.id}
                      sourceText={entry.message.text}
                      sessionId={sessionId}
                    />
                  )}
                </article>
              ) : (
                <ChatReply
                  key={entry.id}
                  q={entry.request!}
                  requests={requests}
                  view={view}
                  draftBusy={draftBusy}
                  consent={consent}
                  setConsent={setConsent}
                  useDraft={useDraft}
                  onReviewStrategy={onReviewStrategy}
                />
              ),
            )}
        </div>
        <form
          className="chat-message-form"
          onSubmit={async (e) => {
            e.preventDefault();
            if (agents.busy || draftBusy || preparing.current) return;
            preparing.current = true;
            setDraftError("");
            const epoch = generation.current;
            const submittedPrompt = prompt;
            if (/^\s*\/remember(?:\s|$)/.test(submittedPrompt)) {
              try {
                if (composer) await composer.onSave();
                else if (
                  await agents.change({
                    action: "message-add",
                    id: sessionId,
                    text: submittedPrompt,
                  })
                ) {
                  if (currentPrompt.current === submittedPrompt) setPrompt("");
                }
              } finally {
                preparing.current = false;
              }
              return;
            }
            // Verify the explicitly selected credential immediately before prepare.
            // Never fall back to another connection after lock, rotation or loss.
            if (connectionRef) {
              setDraftBusy(true);
              try {
                const current = await core.request<Connection>("ai/connection");
                if (epoch !== generation.current) {
                  preparing.current = false;
                  return;
                }
                if (current.ref !== connectionRef || current.locked)
                  throw new Error(
                    "Selected connection is locked or unavailable. Unlock it in Providers or select Local fixture.",
                  );
              } catch (err) {
                if (epoch === generation.current)
                  setDraftError(
                    err instanceof Error
                      ? err.message
                      : "Connection unavailable",
                  );
                preparing.current = false;
                return;
              } finally {
                if (epoch === generation.current) setDraftBusy(false);
              }
            }
            if (epoch !== generation.current) {
              preparing.current = false;
              return;
            }
            followReplies.current = true;
            if (researchSelection) {
              if (researchPreparing) {
                preparing.current = false;
                return;
              }
              const epoch = generation.current;
              setResearchPreparing(true);
              setDraftError("");
              void core
                .request("wen/research/ai-prepare", "POST", {
                  schema:
                    researchMode === "loop"
                      ? "wen-research-ai-prepare/2"
                      : "wen-research-ai-prepare/1",
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
                  preparing.current = false;
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
                if (ok && epoch === generation.current) {
                  if (currentPrompt.current === submittedPrompt) setPrompt("");
                  setConsent("");
                  setDraftSource("");
                  setEvidenceSelection(undefined);
                  setEvidencePreview(null);
                }
              })
              .finally(() => {
                preparing.current = false;
              });
          }}
        >
          {researchSelection && (
            <fieldset aria-label="Selected research for AI">
              <legend>Selected research</legend>
              <label>
                Research mode
                <select
                  value={researchMode}
                  disabled={researchPreparing}
                  onChange={(e) => setResearchMode(e.target.value)}
                >
                  <option value="single">Single request</option>
                  <option value="loop">Reviewed continuations</option>
                </select>
              </label>
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
          <label className="chat-input">
            <span>{composer ? tr("Message") : "AI request"}</span>
            <textarea
              ref={questionRef}
              aria-label={composer ? tr("Message") : "AI request"}
              required
              maxLength={composer ? 4000 : 2000}
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              placeholder="Message your agent…"
              autoFocus
              onKeyDown={(e) => {
                if (
                  e.key === "Enter" &&
                  !e.shiftKey &&
                  !e.nativeEvent.isComposing &&
                  !e.repeat
                ) {
                  e.preventDefault();
                  e.currentTarget.form?.requestSubmit();
                }
              }}
            />
          </label>
          <div className="chat-composer-toolbar" aria-label="Reply connection">
            <ConnectionPicker
              value={connectionRef}
              onChange={setConnectionRef}
            />
            <AIModelPicker
              value={model}
              onChange={setModel}
              connectionRef={connectionRef}
            />
            <button
              type="button"
              className="text-button"
              aria-label="Providers"
              title="Providers"
              onClick={() => navigate("agents/settings")}
            >
              <SlidersHorizontal size={16} />
            </button>
          </div>
          <details
            className="message-options"
            open={optionsOpen}
            onToggle={(e) => setOptionsOpen(e.currentTarget.open)}
          >
            <summary>Message options</summary>
            {composer && (
              <button
                type="button"
                className="text-button"
                disabled={agents.busy || !prompt.trim()}
                onClick={() => void composer.onSave()}
              >
                Save message
              </button>
            )}
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
                <option value="relevant">
                  {tr("Relevant notes and recent chat")}
                </option>
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
          </details>
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
          <div className="composer-actions">
            <button
              type="button"
              className="icon-button"
              aria-label={tr("Remember a fact")}
              title={tr("Remember a fact")}
              disabled={agents.busy || draftBusy}
              onClick={() => {
                if (!remembering) setPrompt("/remember " + prompt.trim());
                questionRef.current?.focus();
              }}
            >
              <BookOpen size={16} />
            </button>
            <button
              className="primary"
              disabled={
                agents.busy ||
                !prompt.trim() ||
                prompt.length > 2000 ||
                researchPreparing ||
                draftBusy ||
                (!remembering &&
                  memoryMode === "selected" &&
                  memoryIds.length === 0)
              }
            >
              {remembering
                ? tr("Remember")
                : researchSelection
                  ? "Prepare research disclosure"
                  : tr("Send")}
            </button>
          </div>
        </form>
      </section>
    </>
  );
}
