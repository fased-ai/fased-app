import { previewModel } from "../ui-language-preview";
import { agentReplyDefaults } from "../agent-defaults";
import { Plug, ArrowUp, Square, BookOpen } from "../icons";
import { useUI } from "../ui-preferences";
type ChatComposer = {
  value: string;
  onChange: (value: string) => void;
  onSave: () => Promise<void>;
  onInteract?: () => void;
  submitOnMount?: boolean;
  onSubmittedMount?: () => void;
};
import { ChatPermissionReview } from "./ChatPermission";
import type { ResearchSelection } from "./ResearchReadPanel";
import {
  AIModelPicker,
  useAIConnection,
  ConnectionPicker,
  type Connection,
} from "./AIConnection";
import { AgentAIHistoryList } from "./AgentAIHistoryList";
import { AgentAIContextFields } from "./AgentAIContextFields";
import { AgentAIOptionsMenu } from "./AgentAIOptionsMenu";
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
import { useWorkspace, type ChatModelDraft } from "../workspace";

export function AgentAIReview({
  view,
  sessionId,
  onReviewStrategy,
  researchSelection,
  onClearResearch,
  composer,
  initialSelection,
  onSelectionChange,
}: {
  view: AgentView;
  sessionId: string;
  onReviewStrategy?: (requestId: string, cardId: string) => void;
  researchSelection?: ResearchSelection;
  onClearResearch?: () => void;
  composer?: ChatComposer;
  initialSelection?: ChatModelDraft;
  onSelectionChange?: (selection?: ChatModelDraft) => void;
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
      initialSelection={initialSelection}
      onSelectionChange={onSelectionChange}
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
  initialSelection,
  onSelectionChange,
}: {
  view: AgentView;
  sessionId: string;
  onReviewStrategy?: (requestId: string, cardId: string) => void;
  researchSelection?: ResearchSelection;
  onClearResearch?: () => void;
  composer?: ChatComposer;
  initialSelection?: ChatModelDraft;
  onSelectionChange?: (selection?: ChatModelDraft) => void;
}) {
  const { setProvidersOpen } = useWorkspace();
  const { t: uiText } = useUI();
  const { core, agents, navigate } = useWorkspace();
  const tr = uiText;
  const [optionsOpen, setOptionsOpen] = useState(false);
  const optionsMenu = useRef<HTMLDetailsElement>(null);
  function closeOptions() {
    // Native details opens before its queued toggle event updates React state.
    // Close the actual disclosure as well, even when state is already false.
    if (optionsMenu.current) optionsMenu.current.open = false;
    setOptionsOpen(false);
  }
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
  const {
    view: accounts,
    refresh: refreshAccess,
    error: accountError,
  } = useAIConnection();
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
  const [overridden, setOverridden] = useState(initialSelection !== undefined);
  const [connectionRef, setConnectionRef] = useState(
    initialSelection?.connectionRef ?? "",
  );
  const [model, setModel] = useState(initialSelection?.model ?? "");
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
  const [limitsIdentity, setLimitsIdentity] = useState("");
  useEffect(() => {
    if (draftSource) return;
    const identity = connectionRef + ":" + model + ":" + selectedPolicy?.id;
    if (budgetModel.current === identity) return;
    const p = selectedPolicy;
    if (!p) return;
    budgetModel.current = identity;
    setLimitsIdentity(identity);
    if (p.provider === "local" || !selectedAccess?.testOnly) {
      setDeadline((d) => (d <= 5 ? 60 : d));
    }
    if (isUnmeteredPolicy(p)) return;
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
    closeOptions();
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
  const submittedMount = useRef(false);
  useEffect(() => {
    if (!composer?.submitOnMount || submittedMount.current || agents.busy)
      return;
    if (
      (agent.provider !== "none" || connectionRef) &&
      !accounts &&
      !accountError
    )
      return;
    if (
      !overridden &&
      (connectionRef !== inherited.connectionRef || model !== inherited.model)
    )
      return;
    if (
      selectedPolicy &&
      limitsIdentity !== `${connectionRef}:${model}:${selectedPolicy.id}`
    )
      return;
    // Let provider-derived limits settle before the existing form handles Send.
    const timer = window.setTimeout(() => {
      submittedMount.current = true;
      composer.onSubmittedMount?.();
      if (!remembering && !replyReady) {
        setDraftError(tr("Provider unavailable"));
        return;
      }
      questionRef.current?.form?.requestSubmit();
    }, 0);
    return () => window.clearTimeout(timer);
  }, [
    composer,
    agents.busy,
    accounts,
    accountError,
    budget,
    deadline,
    selectedPolicy,
    limitsIdentity,
    agent.provider,
    overridden,
    connectionRef,
    model,
    inherited.connectionRef,
    inherited.model,
    remembering,
    replyReady,
    tr,
  ]);
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
    permissionPolicy?.provider === "ollama-cloud" ||
    permissionPolicy?.model === "gemma4:cloud"
      ? "Ollama Cloud"
      : accounts?.catalog?.providers.find(
          (p) => p.id === permissionAccount?.provider,
        )?.name || tr("Unavailable");
  return (
    <>
      {permissionReview && (
        <ChatPermissionReview
          permission={permissionReview}
          providerName={
            permissionReview.connectionRef ? permissionProvider : tr("Demo")
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
        <AgentAIHistoryList
          view={view}
          sessionId={sessionId}
          requests={requests}
          messagesRef={messagesRef}
          followReplies={followReplies}
          draftBusy={draftBusy}
          consent={consent}
          setConsent={setConsent}
          useDraft={useDraft}
          onReviewStrategy={onReviewStrategy}
        />
        <form
          className="chat-message-form"
          onClickCapture={() => composer?.onInteract?.()}
          onKeyDownCapture={() => composer?.onInteract?.()}
          onKeyDown={(e) => {
            if (e.key !== "Escape") return;
            const modelMenu = e.currentTarget.querySelector<HTMLDetailsElement>(
              ".reply-model-menu[open]",
            );
            if (optionsMenu.current?.open || optionsOpen) {
              e.preventDefault();
              closeOptions();
              e.currentTarget
                .querySelector<HTMLElement>(".message-options > summary")
                ?.focus();
            } else if (modelMenu) {
              e.preventDefault();
              modelMenu.open = false;
              modelMenu.querySelector<HTMLElement>("summary")?.focus();
            }
          }}
          onSubmit={async (e) => {
            e.preventDefault();
            closeOptions();
            const modelMenu = e.currentTarget.querySelector<HTMLDetailsElement>(
              ".reply-model-menu[open]",
            );
            if (modelMenu) modelMenu.open = false;
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
          <AgentAIContextFields
            researchSelection={researchSelection}
            researchMode={researchMode}
            setResearchMode={setResearchMode}
            researchPreparing={researchPreparing}
            onClearResearch={onClearResearch}
            evidenceSelection={evidenceSelection}
            evidenceAvailable={evidenceAvailable}
            evidencePreview={evidencePreview}
            removeEvidence={removeEvidence}
            modelAttempts={modelAttempts}
            setModelAttempts={setModelAttempts}
            toolAttempts={toolAttempts}
            setToolAttempts={setToolAttempts}
            turnSeconds={turnSeconds}
            setTurnSeconds={setTurnSeconds}
            planUsage={planUsage}
            turnBudget={turnBudget}
            setTurnBudget={setTurnBudget}
          />
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
                title={previewModel(model, tr) || tr("No AI")}
              >
                {previewModel(model, tr).replace(/-\d{4}-\d{2}-\d{2}$/, "") ||
                  tr(replyReady ? "No AI" : "Provider")}
              </summary>
              <div className="reply-model-panel">
                <ConnectionPicker
                  value={connectionRef}
                  onChange={(ref) => {
                    setOverridden(true);
                    setConnectionRef(ref);
                    const a = accounts?.connections?.find((a) => a.ref === ref);
                    const selectedModel = ref
                      ? a?.catalog?.models.find(
                          (m) =>
                            m.connectionRef === ref &&
                            m.selectable &&
                            m.recommended,
                        )?.id || ""
                      : "";
                    setModel(selectedModel);
                    onSelectionChange?.({
                      connectionRef: ref,
                      model: selectedModel,
                    });
                  }}
                />
                <AIModelPicker
                  value={model}
                  onChange={(next) => {
                    setOverridden(true);
                    setModel(next);
                    onSelectionChange?.({ connectionRef, model: next });
                  }}
                  connectionRef={connectionRef}
                />
                <button
                  type="button"
                  className="text-button"
                  onClick={() => {
                    setOverridden(false);
                    onSelectionChange?.();
                  }}
                >
                  {tr("Use agent default")}
                </button>
              </div>
            </details>
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
              type="button"
              className="text-button"
              aria-label={uiText("Providers")}
              title={uiText("Providers")}
              onClick={() => setProvidersOpen(true)}
            >
              <Plug size={16} />
            </button>
            <AgentAIOptionsMenu
              optionsMenu={optionsMenu}
              optionsOpen={optionsOpen}
              setOptionsOpen={setOptionsOpen}
              refreshAccess={refreshAccess}
              closeOptions={closeOptions}
              view={view}
              sessionId={sessionId}
              researchSelection={researchSelection}
              generation={generation}
              setDraftBusy={setDraftBusy}
              setDraftError={setDraftError}
              setDraftSource={setDraftSource}
              setEvidenceSelection={setEvidenceSelection}
              setEvidencePreview={setEvidencePreview}
              setConsent={setConsent}
              questionRef={questionRef}
              reviewEach={reviewEach}
              setReviewEach={setReviewEach}
              currentPermission={currentPermission}
              hasComposer={!!composer}
              onSaveComposer={composer?.onSave}
              prompt={prompt}
              newsSourceId={newsSourceId}
              setNewsSourceId={setNewsSourceId}
              newsAge={newsAge}
              setNewsAge={setNewsAge}
              memoryMode={memoryMode}
              setMemoryMode={setMemoryMode}
              legacyDraft={legacyDraft}
              memoryIds={memoryIds}
              toggleMemory={toggleMemory}
              input={input}
              setInput={setInput}
              output={output}
              setOutput={setOutput}
              deadline={deadline}
              setDeadline={setDeadline}
              planUsage={planUsage}
              budget={budget}
              setBudget={setBudget}
              selectedAccess={selectedAccess}
              model={model}
            />
          </div>
          <div className="composer-actions">
            {latest?.chatPermissionId &&
            ["queued", "running"].includes(latest.state) ? (
              <button
                type="button"
                className="primary composer-submit"
                aria-label={tr("Stop")}
                title={tr("Stop")}
                disabled={agents.busy}
                onClick={() =>
                  void agents.change({ action: "ai-cancel", id: latest.id })
                }
              >
                <Square size={16} fill="currentColor" aria-hidden="true" />
              </button>
            ) : (
              <button
                className="primary composer-submit"
                aria-label={
                  remembering
                    ? tr("Remember")
                    : researchSelection
                      ? uiText("Prepare research disclosure")
                      : tr("Send")
                }
                title={
                  remembering
                    ? tr("Remember")
                    : researchSelection
                      ? uiText("Prepare research disclosure")
                      : tr("Send")
                }
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
                {remembering ? (
                  <BookOpen size={18} aria-hidden="true" />
                ) : (
                  <ArrowUp size={20} aria-hidden="true" />
                )}
              </button>
            )}
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
        </form>
      </section>
    </>
  );
}
