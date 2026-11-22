import { useEffect, useRef, useState, type FormEvent } from "react";
import { previewModel } from "../ui-language-preview";
import { useUI } from "../ui-preferences";
import { useCore } from "../core";
import { useWorkspace, type ChatModelDraft } from "../workspace";
import {
  AIModelPicker,
  ConnectionPicker,
  useAIConnection,
} from "../pages/AIConnection";
import type { AgentView } from "../agents";
import {
  formatApprovalFields,
  formatToolLabel,
  formatToolSummary,
  parseMarkdownBlocks,
  useAgentsV2,
  type AgentApproval,
  type AgentToolCall,
  type AgentV2Message,
  type InlineSegment,
} from "../agents-v2";
import {
  ArrowUp,
  Check,
  Copy,
  History,
  Plug,
  Plus,
  Shield,
  ShieldCheck,
  Square,
  Trash2,
  X,
  Zap,
} from "../icons";
import "./agent-chat-v2.css";

function renderInline(segments: InlineSegment[]) {
  return segments.map((seg, i) => {
    if (seg.kind === "bold") return <strong key={i}>{seg.text}</strong>;
    if (seg.kind === "code")
      return (
        <code key={i} className="v2-inline-code">
          {seg.text}
        </code>
      );
    return <span key={i}>{seg.text}</span>;
  });
}

function CodeBlock({ lang, code }: { lang: string; code: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="v2-code-block">
      <div className="v2-code-bar">
        <span>{lang || "code"}</span>
        <button
          type="button"
          className="text-button v2-copy-btn"
          aria-label="Copy code"
          onClick={() => {
            void navigator.clipboard?.writeText(code);
            setCopied(true);
            setTimeout(() => setCopied(false), 1200);
          }}
        >
          {copied ? <Check size={13} /> : <Copy size={13} />}
        </button>
      </div>
      <pre>
        <code>{code}</code>
      </pre>
    </div>
  );
}

function MarkdownMessage({ text }: { text: string }) {
  const blocks = parseMarkdownBlocks(text);
  return (
    <div className="v2-markdown">
      {blocks.map((block, idx) => {
        if (block.kind === "code") {
          return <CodeBlock key={idx} lang={block.lang} code={block.code} />;
        }
        if (block.kind === "list") {
          return (
            <ul key={idx} className="v2-list">
              {block.items.map((item, j) => (
                <li key={j}>{renderInline(item)}</li>
              ))}
            </ul>
          );
        }
        return <p key={idx}>{renderInline(block.segments)}</p>;
      })}
    </div>
  );
}

function toolStatusLabel(status: string, t: (s: string) => string) {
  switch (status) {
    case "running":
      return t("Running");
    case "completed":
      return t("Done");
    case "awaiting_approval":
      return t("Pending");
    case "rejected":
      return t("Rejected");
    case "error":
      return t("Error");
    default:
      return t(status);
  }
}

function formatPrettyJSON(raw?: string) {
  if (!raw) return "";
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
}

function ToolCallRow({ tc }: { tc: AgentToolCall }) {
  const { t } = useUI();
  const summary =
    tc.status === "completed"
      ? formatToolSummary(tc.toolName, tc.resultJson)
      : "";
  return (
    <details className="v2-tool-call" data-status={tc.status}>
      <summary>
        <span className="v2-tool-head">
          <span className="v2-tool-name">{formatToolLabel(tc.toolName)}</span>
          {summary && <span className="v2-tool-summary">{summary}</span>}
        </span>
        <span className={`v2-pill v2-pill-${tc.status}`}>
          {toolStatusLabel(tc.status, t)}
        </span>
      </summary>
      <div className="v2-tool-body">
        {tc.argsJson && tc.argsJson !== "{}" && (
          <pre className="v2-tool-pre">{formatPrettyJSON(tc.argsJson)}</pre>
        )}
        {tc.resultJson && (
          <pre className="v2-tool-pre">{formatPrettyJSON(tc.resultJson)}</pre>
        )}
        {tc.errorText && <p className="v2-tool-err">{tc.errorText}</p>}
      </div>
    </details>
  );
}

function ApprovalCard({
  approval,
  busy,
  onDecide,
}: {
  approval: AgentApproval;
  busy: boolean;
  onDecide: (
    id: string,
    decision: "approve" | "reject",
    note: string,
  ) => Promise<boolean>;
}) {
  const { t } = useUI();
  const [note, setNote] = useState("");
  const fields = formatApprovalFields(approval.toolName, approval.payloadJson);
  const pending = approval.status === "pending";

  return (
    <article
      className={`v2-approval-card v2-approval-${approval.status}`}
      aria-label={approval.title}
    >
      <div className="v2-approval-head">
        <span className="v2-approval-title">
          {approval.status === "approved" ? (
            <ShieldCheck size={16} aria-hidden="true" />
          ) : (
            <Shield size={16} aria-hidden="true" />
          )}
          <strong>{approval.title}</strong>
        </span>
        <span className={`v2-pill v2-pill-${approval.status}`}>
          {pending
            ? t("Approval required")
            : approval.status === "approved"
              ? t("Approved")
              : t("Rejected")}
        </span>
      </div>
      {fields.length > 0 && (
        <dl className="v2-approval-grid">
          {fields.map((f) => (
            <div key={f.label} className="v2-approval-row">
              <dt>{t(f.label)}</dt>
              <dd>{f.value}</dd>
            </div>
          ))}
        </dl>
      )}
      {!pending && approval.decisionNote && (
        <p className="v2-approval-note">{approval.decisionNote}</p>
      )}
      {pending && (
        <div className="v2-approval-actions">
          <input
            type="text"
            aria-label={t("Approval note")}
            placeholder={t("Optional note")}
            value={note}
            disabled={busy}
            maxLength={400}
            onChange={(e) => setNote(e.target.value)}
          />
          <button
            type="button"
            className="primary"
            disabled={busy}
            onClick={() => void onDecide(approval.id, "approve", note)}
          >
            {t("Approve")}
          </button>
          <button
            type="button"
            className="secondary"
            disabled={busy}
            onClick={() => void onDecide(approval.id, "reject", note)}
          >
            {t("Reject")}
          </button>
        </div>
      )}
    </article>
  );
}

export function AgentChatV2({
  view,
  onOpenLegacySession,
}: {
  view: AgentView;
  onOpenLegacySession: (sessionId: string) => void;
}) {
  const { t } = useUI();
  const core = useCore();
  const {
    setProvidersOpen,
    chatDrafts,
    setChatDrafts,
    chatModelDrafts,
    setChatModelDrafts,
    pendingChatPrompt,
    clearChatPrompt,
  } = useWorkspace();
  const v2 = useAgentsV2(core, view.profileId);
  const { view: connection } = useAIConnection();
  const profile = view.profiles.find((p) => p.id === view.profileId)!;

  const consumedPromptIds = useRef(new Set<string>());
  const [railOpen, setRailOpen] = useState(false);
  const [search, setSearch] = useState("");
  const [toolsEnabled, setToolsEnabled] = useState(true);
  const [editingTitle, setEditingTitle] = useState(false);
  const [titleDraft, setTitleDraft] = useState("");
  const [welcomeDismissed, setWelcomeDismissed] = useState(() => {
    try {
      return sessionStorage.getItem("fased.welcome.dismissed") === "1";
    } catch {
      return false;
    }
  });

  const scrollEndRef = useRef<HTMLDivElement>(null);
  const modelMenuRef = useRef<HTMLDetailsElement>(null);

  const hasConnectedProvider = !!(
    connection?.ref || connection?.connections?.length
  );
  const profileDraftKey = `${core.instanceId}:${view.profileId}`;
  const threadDraftKey = `${profileDraftKey}:v2:${v2.activeThreadId || "new"}`;
  const composerValue = chatDrafts.get(threadDraftKey) ?? "";
  const modelSelection = chatModelDrafts.get(threadDraftKey);

  const allConnections = connection?.connections?.length
    ? connection.connections
    : connection?.ref
      ? [connection]
      : [];
  const defaultUnlocked =
    allConnections.find(
      (a) => a.ref === connection?.ref && !a.locked && !a.planPaused,
    ) ??
    allConnections.find((a) => !a.locked && !a.planPaused) ??
    null;

  const activeConnectionRef =
    modelSelection?.connectionRef ??
    v2.detail?.thread.connectionRef ??
    (profile.provider === "none"
      ? defaultUnlocked?.ref || ""
      : profile.connectionRef || defaultUnlocked?.ref || "");
  const activeAccount = activeConnectionRef
    ? (allConnections.find((a) => a.ref === activeConnectionRef) ?? null)
    : null;
  const activeModel =
    (modelSelection?.model ??
      v2.detail?.thread.model ??
      (profile.provider !== "none" && profile.model ? profile.model : "")) ||
    activeAccount?.catalog?.models.find(
      (m) =>
        m.connectionRef === activeAccount.ref && m.selectable && m.recommended,
    )?.id ||
    activeAccount?.catalog?.models.find(
      (m) => m.connectionRef === activeAccount.ref && m.selectable,
    )?.id ||
    "";

  function setComposerValue(nextText: string) {
    setChatDrafts((cur) => {
      const next = new Map(cur);
      if (nextText) next.set(threadDraftKey, nextText);
      else next.delete(threadDraftKey);
      return next;
    });
  }

  function setModelSelection(sel?: ChatModelDraft) {
    setChatModelDrafts((cur) => {
      const next = new Map(cur);
      if (sel) next.set(threadDraftKey, sel);
      else next.delete(threadDraftKey);
      return next;
    });
  }

  function dismissWelcome() {
    setWelcomeDismissed(true);
    try {
      sessionStorage.setItem("fased.welcome.dismissed", "1");
    } catch {}
  }

  useEffect(() => {
    setEditingTitle(false);
    setTitleDraft(v2.detail?.thread.title ?? "");
  }, [v2.detail?.thread.id, v2.detail?.thread.title]);

  useEffect(() => {
    if (
      !pendingChatPrompt ||
      pendingChatPrompt.profileId !== view.profileId ||
      v2.streaming ||
      consumedPromptIds.current.has(pendingChatPrompt.id)
    ) {
      return;
    }
    consumedPromptIds.current.add(pendingChatPrompt.id);
    const next = pendingChatPrompt;
    clearChatPrompt(next.id);
    void v2.sendMessage({
      content: next.prompt,
      title: next.title,
      forceNewThread: true,
      connectionRef: activeConnectionRef || undefined,
      model: activeModel || undefined,
      toolsEnabled: true,
    });
  }, [
    pendingChatPrompt,
    view.profileId,
    v2.streaming,
    v2.sendMessage,
    clearChatPrompt,
    activeConnectionRef,
    activeModel,
  ]);

  useEffect(() => {
    scrollEndRef.current?.scrollIntoView({ block: "end" });
  }, [
    v2.detail?.messages.length,
    v2.streamDraft?.content,
    v2.streamDraft?.toolCalls.length,
    v2.streamDraft?.approvals.length,
  ]);

  const q = search.trim().toLowerCase();
  const visibleThreads = v2.threads.filter(
    (item) => !q || item.title.toLowerCase().includes(q),
  );
  const legacySessions = view.sessions.filter(
    (s) =>
      s.profileId === view.profileId &&
      (!q || s.name.toLowerCase().includes(q)),
  );

  const visibleMessages: AgentV2Message[] = (v2.detail?.messages ?? []).filter(
    (m) => m.role !== "tool",
  );

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (v2.streaming || !composerValue.trim()) return;
    if (modelMenuRef.current) modelMenuRef.current.open = false;
    const text = composerValue;
    setComposerValue("");
    const ok = await v2.sendMessage({
      content: text,
      connectionRef: activeConnectionRef || undefined,
      model: activeModel || undefined,
      toolsEnabled,
    });
    if (!ok) {
      setComposerValue(text);
    }
  }

  const showConnectCard =
    core.mode === "ready" &&
    connection &&
    !hasConnectedProvider &&
    !view.aiFixtureAvailable &&
    v2.threads.length === 0 &&
    !v2.detail &&
    !welcomeDismissed;

  const activeProviderName = activeAccount?.provider
    ? connection?.catalog?.providers.find(
        (p) => p.id === activeAccount.provider,
      )?.name || activeAccount.provider
    : "";
  const modelSummaryLabel =
    previewModel(activeModel, t) ||
    activeAccount?.name ||
    activeProviderName ||
    t("Auto model");

  return (
    <section
      className={`panel agent-chat has-conversation ${railOpen ? "rail-open" : ""}`}
      aria-label={t("Agent chat workspace")}
    >
      <button
        type="button"
        className="text-button mobile-conversation-button"
        aria-expanded={railOpen}
        onClick={() => setRailOpen(!railOpen)}
      >
        {t("Conversations")}
      </button>

      <aside className="conversation-rail" aria-label={t("Conversations")}>
        <div className="rail-heading">
          <h2>{t("Conversations")}</h2>
          <div className="v2-rail-actions">
            <button
              type="button"
              className={`icon-button ${v2.showArchived ? "is-active" : ""}`}
              aria-label={t("Archived conversations")}
              aria-pressed={v2.showArchived}
              title={t("Archived conversations")}
              onClick={() => v2.setShowArchived(!v2.showArchived)}
            >
              <History size={15} />
            </button>
            <button
              type="button"
              className="icon-button"
              aria-label={t("New thread")}
              title={t("New thread")}
              onClick={() => {
                v2.startNewThread();
                setRailOpen(false);
              }}
            >
              <Plus size={15} />
            </button>
          </div>
        </div>

        <label>
          <span className="sr-only">{t("Search conversations")}</span>
          <input
            name="search"
            type="search"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={t("Search conversations")}
          />
        </label>

        <div className="agent-session-list">
          {visibleThreads.map((item) => (
            <div
              key={item.id}
              className={`v2-thread-row ${item.id === v2.activeThreadId ? "selected" : ""}`}
            >
              <button
                type="button"
                className={
                  item.id === v2.activeThreadId
                    ? "secondary selected"
                    : "text-button"
                }
                onClick={() => {
                  void v2.selectThread(item.id);
                  setRailOpen(false);
                }}
              >
                <span className="v2-thread-title">{item.title}</span>
                {(item.pendingApprovals ?? 0) > 0 && (
                  <span
                    className="v2-pending-dot"
                    aria-label={t("Pending approval")}
                  />
                )}
              </button>
              <button
                type="button"
                className="icon-button v2-thread-delete"
                aria-label={`${t("Delete")} ${item.title}`}
                onClick={(e) => {
                  e.stopPropagation();
                  void v2.removeThread(item.id);
                }}
              >
                <Trash2 size={13} aria-hidden="true" />
              </button>
            </div>
          ))}
          {visibleThreads.length === 0 && (
            <p className="v2-empty-rail">
              {t(q ? "No matching conversations." : "No conversations")}
            </p>
          )}
        </div>

        {legacySessions.length > 0 && (
          <div className="v2-legacy-rail">
            <small>{t("Legacy reviews")}</small>
            <div className="agent-session-list">
              {legacySessions.map((s) => (
                <button
                  key={s.id}
                  type="button"
                  className="text-button"
                  onClick={() => {
                    onOpenLegacySession(s.id);
                    setRailOpen(false);
                  }}
                >
                  {s.name}
                </button>
              ))}
            </div>
          </div>
        )}
      </aside>

      <div className="agent-record-body chat-workspace">
        <div className="chat-thread">
          {v2.detail && (
            <div className="panel-heading chat-thread-heading">
              {editingTitle ? (
                <form
                  className="v2-rename-form"
                  onSubmit={(e) => {
                    e.preventDefault();
                    const trimmed = titleDraft.trim();
                    if (!trimmed || !v2.detail) return;
                    void v2
                      .renameThread(v2.detail.thread.id, trimmed)
                      .then(() => setEditingTitle(false));
                  }}
                >
                  <input
                    aria-label={t("Thread title")}
                    value={titleDraft}
                    maxLength={80}
                    autoFocus
                    onChange={(e) => setTitleDraft(e.target.value)}
                  />
                  <button type="submit" className="icon-button">
                    <Check size={15} />
                  </button>
                  <button
                    type="button"
                    className="icon-button"
                    onClick={() => setEditingTitle(false)}
                  >
                    <X size={15} />
                  </button>
                </form>
              ) : (
                <h3
                  onDoubleClick={() => setEditingTitle(true)}
                  title={v2.detail.thread.title}
                >
                  {v2.detail.thread.title}
                </h3>
              )}
              <button
                type="button"
                className="icon-button"
                aria-label={
                  v2.detail.thread.archived
                    ? t("Unarchive conversation")
                    : t("Archive conversation")
                }
                title={
                  v2.detail.thread.archived
                    ? t("Unarchive conversation")
                    : t("Archive conversation")
                }
                disabled={v2.streaming}
                onClick={() =>
                  void v2.archiveThread(
                    v2.detail!.thread.id,
                    !v2.detail!.thread.archived,
                  )
                }
              >
                <History size={15} />
              </button>
              <button
                type="button"
                className="icon-button"
                aria-label={t("Delete conversation")}
                title={t("Delete conversation")}
                disabled={v2.streaming}
                onClick={() => void v2.removeThread(v2.detail!.thread.id)}
              >
                <Trash2 size={15} />
              </button>
            </div>
          )}

          {v2.error && (
            <div className="storage-warning v2-chat-error" role="alert">
              <span>{v2.error}</span>
              <button
                type="button"
                className="icon-button"
                aria-label={t("Dismiss")}
                onClick={() => v2.setError("")}
              >
                <X size={14} />
              </button>
            </div>
          )}

          <div className="local-messages v2-messages" aria-live="polite">
            {showConnectCard && (
              <section
                className="panel connect-ai-card"
                aria-label={t("Connect AI")}
              >
                <h2>{t("Connect AI")}</h2>
                <div className="connect-ai-actions">
                  <button
                    type="button"
                    className="primary"
                    onClick={() => setProvidersOpen(true)}
                  >
                    {t("Connect provider")}
                  </button>
                  <button
                    type="button"
                    className="text-button"
                    onClick={dismissWelcome}
                  >
                    {t("Skip")}
                  </button>
                </div>
              </section>
            )}

            {visibleMessages.map((m) => (
              <article key={m.id} className={`v2-message v2-message-${m.role}`}>
                {m.role === "user" ? (
                  <div className="v2-user-bubble">{m.content}</div>
                ) : (
                  <div className="v2-assistant-turn">
                    {m.reasoning && (
                      <details className="v2-thinking">
                        <summary>{t("Thinking")}</summary>
                        <pre>{m.reasoning}</pre>
                      </details>
                    )}
                    {(m.toolCalls?.length ?? 0) > 0 && (
                      <div className="v2-tool-list">
                        {m.toolCalls!.map((tc) => (
                          <ToolCallRow key={tc.id} tc={tc} />
                        ))}
                      </div>
                    )}
                    {m.content && <MarkdownMessage text={m.content} />}
                    {m.errorText && (
                      <p className="v2-tool-err">{m.errorText}</p>
                    )}
                    {(m.approvals?.length ?? 0) > 0 && (
                      <div className="v2-approvals-list">
                        {m.approvals!.map((ap) => (
                          <ApprovalCard
                            key={ap.id}
                            approval={ap}
                            busy={
                              v2.streaming || v2.resolvingApprovalId === ap.id
                            }
                            onDecide={v2.decideApproval}
                          />
                        ))}
                      </div>
                    )}
                    {(m.content ||
                      m.errorText ||
                      (m.approvals?.length ?? 0) > 0) &&
                      (m.model || (m.outputTokens ?? 0) > 0) && (
                        <footer className="v2-msg-meta">
                          {m.model && <span>{previewModel(m.model, t)}</span>}
                          {(m.outputTokens ?? 0) > 0 && (
                            <span>
                              {(m.inputTokens ?? 0) + (m.outputTokens ?? 0)} tok
                            </span>
                          )}
                        </footer>
                      )}
                  </div>
                )}
              </article>
            ))}

            {v2.streaming && (
              <article
                className="v2-message v2-message-assistant v2-message-live"
                role="status"
              >
                <div className="v2-assistant-turn">
                  {v2.streamDraft?.reasoning && (
                    <details className="v2-thinking" open>
                      <summary>{t("Thinking")}</summary>
                      <pre>{v2.streamDraft.reasoning}</pre>
                    </details>
                  )}
                  {(v2.streamDraft?.toolCalls.length ?? 0) > 0 && (
                    <div className="v2-tool-list">
                      {v2.streamDraft!.toolCalls.map((tc) => (
                        <ToolCallRow key={tc.id} tc={tc} />
                      ))}
                    </div>
                  )}
                  {v2.streamDraft?.content ? (
                    <MarkdownMessage text={v2.streamDraft.content} />
                  ) : (
                    !v2.streamDraft?.toolCalls.length && (
                      <span
                        className="v2-typing-pulse"
                        aria-label={t("Working")}
                      >
                        ···
                      </span>
                    )
                  )}
                  {(v2.streamDraft?.approvals.length ?? 0) > 0 && (
                    <div className="v2-approvals-list">
                      {v2.streamDraft!.approvals.map((ap) => (
                        <ApprovalCard
                          key={ap.id}
                          approval={ap}
                          busy={true}
                          onDecide={v2.decideApproval}
                        />
                      ))}
                    </div>
                  )}
                </div>
              </article>
            )}

            <div ref={scrollEndRef} />
          </div>

          <form
            className="chat-message-form v2-composer"
            onSubmit={handleSubmit}
          >
            <label className="chat-input">
              <span>{t("Message")}</span>
              <textarea
                name="message"
                aria-label={t("Message")}
                placeholder={t("Message your agent…")}
                disabled={v2.loading}
                required
                maxLength={32000}
                value={composerValue}
                onChange={(e) => setComposerValue(e.target.value)}
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

            <div className="chat-composer-toolbar">
              <details ref={modelMenuRef} className="reply-model-menu">
                <summary
                  aria-label={t("Reply model")}
                  title={modelSummaryLabel}
                >
                  {modelSummaryLabel}
                </summary>
                <div className="reply-model-panel">
                  <ConnectionPicker
                    value={activeConnectionRef}
                    onChange={(ref) => {
                      const account =
                        connection?.connections?.find((a) => a.ref === ref) ??
                        (connection?.ref === ref ? connection : null);
                      const nextModel =
                        account?.catalog?.models.find(
                          (item) =>
                            item.connectionRef === ref &&
                            item.selectable &&
                            item.recommended,
                        ) ??
                        account?.catalog?.models.find(
                          (item) =>
                            item.connectionRef === ref && item.selectable,
                        );
                      setModelSelection({
                        connectionRef: ref,
                        model: ref ? (nextModel?.id ?? "") : "",
                      });
                    }}
                  />
                  <AIModelPicker
                    value={activeModel}
                    connectionRef={activeConnectionRef}
                    onChange={(next) =>
                      setModelSelection({
                        connectionRef: activeConnectionRef,
                        model: next,
                      })
                    }
                  />
                  <button
                    type="button"
                    className="text-button"
                    onClick={() => setModelSelection(undefined)}
                  >
                    {t("Use agent default")}
                  </button>
                </div>
              </details>
              <button
                type="button"
                className={`text-button v2-tools-toggle ${toolsEnabled ? "is-on" : ""}`}
                aria-pressed={toolsEnabled}
                onClick={() => setToolsEnabled(!toolsEnabled)}
              >
                <Zap size={14} aria-hidden="true" />
                <span>{t("Tools")}</span>
              </button>
              <button
                type="button"
                className="icon-button"
                aria-label={t("Providers")}
                title={t("Providers")}
                onClick={() => setProvidersOpen(true)}
              >
                <Plug size={16} aria-hidden="true" />
              </button>
            </div>

            <div className="composer-actions">
              {v2.streaming ? (
                <button
                  type="button"
                  className="primary composer-submit v2-stop-btn"
                  aria-label={t("Stop")}
                  title={t("Stop")}
                  onClick={() => v2.stopStreaming()}
                >
                  <Square size={15} aria-hidden="true" />
                </button>
              ) : (
                <button
                  type="submit"
                  className="primary composer-submit"
                  aria-label={t("Send")}
                  title={t("Send")}
                  disabled={!composerValue.trim()}
                >
                  <ArrowUp size={20} aria-hidden="true" />
                </button>
              )}
            </div>
          </form>
        </div>
      </div>
    </section>
  );
}
