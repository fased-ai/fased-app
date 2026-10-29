import { agentReplyDefaults } from "../agent-defaults";
import { Plug } from "../icons";
import { useUI } from "../ui-preferences";
type ChatComposer = {
  value: string;
  onChange: (value: string) => void;
  onSave: () => Promise<void>;
  onInteract?: () => void;
};
import { AgentEvidenceInspector } from "./AgentEvidence";
import { ChatPermissionReview } from "./ChatPermission";
import { ChatReply } from "./ChatReply";
import { NewsWatchMessage } from "./NewsWatchMessage";
import { SlidersHorizontal, BookOpen } from "../icons";
import type { ResearchSelection } from "./ResearchReadPanel";
import { ChatMemoryReview } from "./ChatMemoryReview";
import { NewsSourcePicker } from "./NewsInputs";
import {
  AIModelPicker,
  useAIConnection,
  ConnectionPicker,
  type Connection,
} from "./AIConnection";
import { useEffect, useRef, useState } from "react";
import type {
  AgentView,
  ChatPermission,
  AIMemorySelection,
  AgentAIRequestDraft,
  AIEvidenceSelection,
  AgentEvidenceView,
} from "../agents";
import { isUnmeteredPolicy } from "../agents";
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
  const { setProvidersOpen } = useWorkspace();
  const { t: uiText } = useUI();
  const { core, agents, navigate } = useWorkspace();
  const tr = uiText;
  const [optionsOpen, setOptionsOpen] = useState(false);
  const [permissionReview, setPermissionReview] = useState<ChatPermission>();
  const [permissionPrompt, setPermissionPrompt] = useState("");
  const [reviewEach, setReviewEach] = useState(false);
  const [localPrompt, setLocalPrompt] = useState("");
  const prompt = composer ? composer.value : localPrompt;
  const remembering = /^\s*\/remember(?:\s|$)/.test(prompt);
  function setPrompt(value: string) {
    setLocalPrompt(value);
    composer?.onChange(value);
  }
  const [newsSourceId, setNewsSourceId] = useState("");
  const [newsAge, setNewsAge] = useState(3600);
  const { view: accounts, refresh: refreshAccess } = useAIConnection();
  const agent = view.profiles.find((p) => p.id === view.profileId)!;
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
  useEffect(() => {
    if (overridden || draftSource) return;
    setConnectionRef(inherited.connectionRef);
    setModel(inherited.model);
  }, [inherited.connectionRef, inherited.model, overridden, draftSource]);
  const selectedAccess =
    accounts?.connections?.find((a) => a.ref === connectionRef) ??
    (accounts?.ref === connectionRef ? accounts : null);
  const selectedPolicy = selectedAccess?.catalog?.models.find(
    (item) => item.id === model,
  )?.policy;
  const planUsage = isUnmeteredPolicy(selectedPolicy);
  const requestBudget = planUsage ? 0 : budget;
  const replyReady =
    overridden || draftSource
      ? (!connectionRef && !model) ||
        (!!connectionRef &&
          selectedAccess?.ref === connectionRef &&
          !selectedAccess.locked &&
          !selectedAccess.planPaused &&
          ((!model && selectedAccess.testOnly) ||
            selectedAccess.catalog?.models.some(
              (m) =>
                m.selectable &&
                m.connectionRef === connectionRef &&
                m.id === model,
            )))
      : inherited.ready;

  const budgetModel = useRef("");
  useEffect(() => {
    if (draftSource) return;
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
    setBudget((b) =>
      Math.min(p.maxRequestBudgetMicrousd || 1000000, Math.max(b, ceiling)),
    );
  }, [
    model,
    connectionRef,
    draftSource,
    input,
    output,
    accounts,
    selectedPolicy,
  ]);

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
    setOptionsOpen(false);
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
  const currentPermission = view.sessions.find((s) => s.id === sessionId)?.chat;
  const normalChat =
    !reviewEach &&
    !researchSelection &&
    !evidenceSelection &&
    !newsSourceId &&
    !draftSource &&
    (memoryMode === "relevant" || memoryMode === "none");
  function matchingPermission(p: ChatPermission) {
    return (
      p.sessionId === sessionId &&
      p.profileId === view.profileId &&
      p.connectionRef === connectionRef &&
      p.model === (model || "fixture-openai-v1") &&
      p.memoryMode === memoryMode &&
      p.limits.maxInputTokens === input &&
      p.limits.maxOutputTokens === output &&
      p.limits.deadlineSeconds === deadline &&
      p.limits.budgetMicrousd === requestBudget
    );
  }
  useEffect(() => {
    setPermissionReview(undefined);
  }, [
    connectionRef,
    model,
    memoryMode,
    input,
    output,
    deadline,
    requestBudget,
    newsSourceId,
    evidenceSelection,
    reviewEach,
  ]);
  async function sendNormal(p: ChatPermission, text: string, allow = false) {
    if (!matchingPermission(p)) return;
    setDraftBusy(true);
    try {
      if (
        allow &&
        !(await agents.change({
          action: "chat-allow",
          id: sessionId,
          chatPermission: p,
          consentDigest: p.id,
        }))
      )
        return;
      setPermissionReview(undefined);
      const ok = await agents.change({
        action: "chat-send",
        id: sessionId,
        text,
        consentDigest: p.id,
      });
      if (ok && currentPrompt.current === text) setPrompt("");
    } finally {
      setDraftBusy(false);
    }
  }
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
  }, [
    view.messages.length,
    requests.length,
    latest?.state,
    latest?.partialAnswer,
  ]);
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
  const permissionAccount =
    accounts?.connections?.find(
      (a) => a.ref === permissionReview?.connectionRef,
    ) ?? (accounts?.ref === permissionReview?.connectionRef ? accounts : null);
  const permissionPolicy = permissionAccount?.catalog?.models.find(
    (m) => m.id === permissionReview?.model,
  )?.policy;
  const permissionProvider =
    accounts?.catalog?.providers.find(
      (p) => p.id === permissionAccount?.provider,
    )?.name || tr("Unavailable");
  return (
    <>
      {permissionReview && (
        <ChatPermissionReview
          permission={permissionReview}
          providerName={
            isUnmeteredPolicy(permissionPolicy)
              ? tr("Usage")
              : permissionReview.connectionRef
                ? permissionProvider
                : tr("Demo")
          }
          policy={permissionPolicy}
          busy={agents.busy || draftBusy}
          onClose={() => setPermissionReview(undefined)}
          onAllow={() =>
            void sendNormal(permissionReview, permissionPrompt, true)
          }
        />
      )}

      <section
        className="ai-review"
        aria-label={uiText("Optional AI request review")}
      >
        {draftError && <p role="alert">{uiText(draftError)}</p>}
        {(draftSource || draftBusy) && (
          <div className="agent-inline">
            <h4>
              {draftBusy
                ? uiText("Loading request draft…")
                : uiText("Request draft")}
            </h4>
            <button type="button" className="secondary" onClick={cancelDraft}>
              {uiText("Cancel draft")}
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
                      <summary>{uiText("Saved locally")}</summary>
                      <NewsWatchMessage text={entry.message.text} />
                    </details>
                  ) : (
                    <>
                      <small>
                        {entry.message.role === "user"
                          ? uiText("You")
                          : uiText("Record")}{" "}
                        · {new Date(entry.at * 1000).toLocaleString()}
                      </small>
                      <NewsWatchMessage text={entry.message.text} />
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
          onClickCapture={() => composer?.onInteract?.()}
          onKeyDownCapture={() => composer?.onInteract?.()}
          onSubmit={async (e) => {
            e.preventDefault();
            setOptionsOpen(false);
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
                const selected =
                  current.connections?.find((a) => a.ref === connectionRef) ??
                  (current.ref === connectionRef ? current : null);
                if (!selected || selected.locked || selected.planPaused)
                  throw new Error(tr("Provider unavailable"));
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
            if (normalChat) {
              try {
                if (
                  currentPermission &&
                  matchingPermission(currentPermission) &&
                  view.chatReadiness?.[sessionId] === "ready"
                ) {
                  await sendNormal(currentPermission, submittedPrompt);
                } else {
                  setDraftBusy(true);
                  const reviewed = await core.request<AgentView>(
                    "agents",
                    "POST",
                    {
                      action: "chat-review",
                      useAgentDefaults: !overridden && !draftSource,
                      profileId: view.profileId,
                      revision: view.revision,
                      id: sessionId,
                      connectionRef,
                      model,
                      memorySelection: { mode: memoryMode },
                      limits: {
                        maxInputTokens: input,
                        maxOutputTokens: output,
                        deadlineSeconds: deadline,
                        budgetMicrousd: requestBudget,
                      },
                      maxRuns: 20,
                      authorizationSeconds: 900,
                      totalBudgetMicrousd: planUsage
                        ? 0
                        : Math.max(600000, budget),
                    },
                  );
                  if (epoch === generation.current && reviewed.chatReview) {
                    setPermissionPrompt(submittedPrompt);
                    setPermissionReview(reviewed.chatReview);
                  }
                }
              } catch (err) {
                setDraftError(
                  err instanceof Error
                    ? err.message
                    : "Chat permission unavailable",
                );
              } finally {
                preparing.current = false;
                setDraftBusy(false);
              }
              return;
            }
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
                    budgetMicrousd: requestBudget,
                  },
                  bounds: {
                    maxModelAttempts: modelAttempts,
                    maxToolAttempts: toolAttempts,
                    wallSeconds: turnSeconds,
                    budgetMicrousd: planUsage ? 0 : turnBudget,
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
                useAgentDefaults: !overridden && !draftSource,
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
                  budgetMicrousd: requestBudget,
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
            <fieldset aria-label={uiText("Selected research for AI")}>
              <legend>{uiText("Selected research")}</legend>
              <label>
                {uiText("Research mode")}
                <select
                  value={researchMode}
                  disabled={researchPreparing}
                  onChange={(e) => setResearchMode(e.target.value)}
                >
                  <option value="single">{uiText("Single request")}</option>
                  <option value="loop">
                    {uiText("Reviewed continuations")}
                  </option>
                </select>
              </label>
              <pre>{JSON.stringify(researchSelection, null, 2)}</pre>
              <button
                type="button"
                className="secondary"
                disabled={researchPreparing}
                onClick={onClearResearch}
              >
                {uiText("Remove research")}
              </button>
            </fieldset>
          )}
          {evidenceSelection && (
            <fieldset aria-label={uiText("AI evidence selection")}>
              <legend>{uiText("Selected evidence")}</legend>
              <pre>{JSON.stringify(evidenceSelection, null, 2)}</pre>
              {!evidenceAvailable && (
                <p role="status">
                  {uiText("Selected evidence unavailable. Inspect again.")}
                </p>
              )}
              {evidencePreview && (
                <details>
                  <summary>{uiText("Inspected record")}</summary>
                  <pre>{JSON.stringify(evidencePreview, null, 2)}</pre>
                </details>
              )}
              <button
                type="button"
                className="secondary"
                onClick={removeEvidence}
              >
                {uiText("Remove evidence")}
              </button>
            </fieldset>
          )}
          <label className="chat-input">
            <span>{composer ? tr("Message") : uiText("AI request")}</span>
            <textarea
              ref={questionRef}
              aria-label={composer ? tr("Message") : uiText("AI request")}
              required
              maxLength={composer ? 4000 : 2000}
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              placeholder={uiText("Message your agent…")}
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
          <div
            className="chat-composer-toolbar"
            aria-label={uiText("Reply connection")}
          >
            <details className="reply-model-menu">
              <summary
                aria-label={tr("Reply model")}
                title={model || tr("No AI")}
              >
                {model.replace(/-\d{4}-\d{2}-\d{2}$/, "") ||
                  tr(replyReady ? "No AI" : "Provider")}
              </summary>
              <div className="reply-model-panel">
                <ConnectionPicker
                  value={connectionRef}
                  onChange={(ref) => {
                    setOverridden(true);
                    setConnectionRef(ref);
                    const a = accounts?.connections?.find((a) => a.ref === ref);
                    setModel(
                      ref
                        ? a?.catalog?.models.find(
                            (m) => m.selectable && m.recommended,
                          )?.id || ""
                        : "",
                    );
                    if (!ref) setModel("");
                  }}
                />
                <AIModelPicker
                  value={model}
                  onChange={(next) => {
                    setOverridden(true);
                    setModel(next);
                  }}
                  connectionRef={connectionRef}
                />
                <button
                  type="button"
                  className="text-button"
                  onClick={() => setOverridden(false)}
                >
                  {tr("Use agent default")}
                </button>
              </div>
            </details>
            <button
              type="button"
              className="text-button"
              aria-label={uiText("Providers")}
              title={uiText("Providers")}
              onClick={() => setProvidersOpen(true)}
            >
              <Plug size={16} />
            </button>
            <details
              className="message-options"
              open={optionsOpen}
              onToggle={(e) => {
                setOptionsOpen(e.currentTarget.open);
                if (e.currentTarget.open) void refreshAccess();
              }}
            >
              <summary aria-label={tr("Options")} title={tr("Options")}>
                <SlidersHorizontal size={16} />
              </summary>
              <details className="chat-evidence">
                <summary>{uiText("Sources")}</summary>
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
                      setOptionsOpen(false);
                      questionRef.current?.focus();
                    }}
                  />
                )}
              </details>
              <label className="ai-consent">
                <input
                  type="checkbox"
                  checked={reviewEach}
                  onChange={(e) => setReviewEach(e.target.checked)}
                />
                {tr("Review each reply")}
              </label>
              {currentPermission &&
                view.chatReadiness?.[sessionId] === "ready" && (
                  <div className="agent-inline">
                    <span>
                      {tr("{count} replies left", {
                        count:
                          currentPermission.maxRequests -
                          currentPermission.usedRequests,
                      })}
                    </span>
                    <button
                      type="button"
                      className="text-button"
                      disabled={agents.busy}
                      onClick={() =>
                        void agents.change({
                          action: "chat-revoke",
                          id: sessionId,
                          consentDigest: currentPermission.id,
                        })
                      }
                    >
                      {tr("Revoke")}
                    </button>
                  </div>
                )}
              {composer && (
                <button
                  type="button"
                  className="text-button"
                  disabled={agents.busy || !prompt.trim()}
                  onClick={async () => {
                    setOptionsOpen(false);
                    await composer.onSave();
                  }}
                >
                  {uiText("Save message")}
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
                {uiText("Memory")}
                <select
                  value={memoryMode}
                  onChange={(e) =>
                    setMemoryMode(
                      e.target.value as AIMemorySelection["mode"] | "legacy",
                    )
                  }
                >
                  {legacyDraft && (
                    <option value="legacy">
                      {uiText("Legacy active notes")}
                    </option>
                  )}
                  <option value="relevant">
                    {tr("Relevant notes and recent chat")}
                  </option>
                  <option value="all">{uiText("All active notes")}</option>
                  <option value="none">{uiText("None")}</option>
                  <option value="selected">{uiText("Selected notes")}</option>
                </select>
              </label>
              {memoryMode === "selected" && (
                <fieldset aria-label={uiText("AI memory selection")}>
                  <legend>{uiText("Selected notes")}</legend>
                  {view.memories.map((m) => (
                    <label key={m.id}>
                      <input
                        type="checkbox"
                        checked={memoryIds.includes(m.id)}
                        disabled={m.expired && !memoryIds.includes(m.id)}
                        onChange={(e) => toggleMemory(m.id, e.target.checked)}
                      />
                      {m.text}
                      {m.expired ? uiText(" (expired)") : ""}
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
                        {uiText("Unavailable note: ")}
                        {id}
                      </label>
                    ))}
                </fieldset>
              )}
              <div className="agent-form-grid">
                <label>
                  {uiText("Maximum input tokens")}
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
                  {uiText("Maximum output tokens")}
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
                  {uiText("Request deadline (seconds)")}
                  <input
                    type="number"
                    min={1}
                    max={selectedPolicy?.provider === "local" ? 120 : 5}
                    required
                    value={deadline}
                    onChange={(e) => setDeadline(e.target.valueAsNumber)}
                  />
                </label>
                {!planUsage && (
                  <label>
                    {uiText("Spending limit ($)")}
                    <input
                      type="number"
                      min={0.000001}
                      step="0.000001"
                      max={
                        (selectedAccess?.catalog?.models.find(
                          (m) => m.id === model,
                        )?.policy?.maxRequestBudgetMicrousd || 1000000) /
                        1000000
                      }
                      required
                      value={budget / 1_000_000}
                      onChange={(e) =>
                        setBudget(
                          Math.round(e.target.valueAsNumber * 1_000_000),
                        )
                      }
                    />
                  </label>
                )}
              </div>
            </details>
          </div>
          {!replyReady && (
            <div className="reply-unavailable" role="status">
              {tr("Provider unavailable")}
              <button
                type="button"
                className="text-button"
                onClick={() => navigate("agents/settings")}
              >
                {tr("Configure")}
              </button>
            </div>
          )}
          {researchSelection && (
            <fieldset
              className="agent-form-grid"
              aria-label={uiText("Research turn limits")}
            >
              <legend>{uiText("Research turn limits")}</legend>
              <label>
                {uiText("Model attempts")}
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
                {uiText("Read attempts")}
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
                {uiText("Research deadline (seconds)")}
                <input
                  type="number"
                  min={1}
                  max={90}
                  required
                  value={turnSeconds}
                  onChange={(e) => setTurnSeconds(e.target.valueAsNumber)}
                />
              </label>
              {!planUsage && (
                <label>
                  {uiText("Turn budget (micro USD)")}
                  <input
                    type="number"
                    min={1}
                    max={3000000}
                    required
                    value={turnBudget}
                    onChange={(e) => setTurnBudget(e.target.valueAsNumber)}
                  />
                </label>
              )}
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
            {latest?.chatPermissionId &&
            ["queued", "running"].includes(latest.state) ? (
              <button
                type="button"
                className="primary"
                disabled={agents.busy}
                onClick={() =>
                  void agents.change({ action: "ai-cancel", id: latest.id })
                }
              >
                {tr("Stop")}
              </button>
            ) : (
              <button
                className="primary"
                disabled={
                  agents.busy ||
                  !prompt.trim() ||
                  (!remembering && !replyReady) ||
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
                    ? uiText("Prepare research disclosure")
                    : tr("Send")}
              </button>
            )}
          </div>
        </form>
      </section>
    </>
  );
}
