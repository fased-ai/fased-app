import { useEffect, useRef, useState, type FormEvent } from "react";
import { previewModel } from "../ui-language-preview";
import { useUI } from "../ui-preferences";
import { useCore } from "../core";
import { useWorkspace, type ChatModelDraft } from "../workspace";
import { useVoiceInput } from "../voice-input";
import {
  AIModelPicker,
  ConnectionPicker,
  useAIConnection,
} from "../pages/AIConnection";
import type { AgentView } from "../agents";
import { parseFasedVisualSpec, parseSafeVisualHTML } from "../agent-visuals";
import {
  createMemoryV2,
  deleteThreadV2,
  extractMessageCitations,
  formatApprovalFields,
  formatToolLabel,
  formatToolSummary,
  parseChatSlashCommand,
  parseDelegatedToolSummary,
  parseMarkdownBlocks,
  useAgentsV2,
  type AgentApproval,
  type AgentThread,
  type AgentToolCall,
  type AgentV2Message,
  type AgentV2SecretSubmission,
  type InlineSegment,
} from "../agents-v2";
import {
  ArrowUp,
  Check,
  Clock3,
  Copy,
  Globe2,
  History,
  KeyRound,
  Mic,
  MicOff,
  Plug,
  Plus,
  RotateCcw,
  Shield,
  ShieldCheck,
  Square,
  Trash2,
  X,
  Zap,
} from "../icons";
import { AgentVisualBlock } from "./AgentVisualBlock";
import { Dialog } from "./shared";
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
    if (seg.kind === "link")
      return (
        <a
          key={i}
          href={seg.url}
          target="_blank"
          rel="noopener noreferrer"
          className="v2-inline-link"
        >
          {seg.text}
        </a>
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
          const lang = block.lang.trim().toLowerCase();
          if (
            lang === "fased-visual" &&
            parseFasedVisualSpec(block.code) !== null
          ) {
            return (
              <AgentVisualBlock
                key={idx}
                lang={lang}
                code={block.code}
                defaultView="preview"
              />
            );
          }
          if (
            lang === "fased-html" &&
            parseSafeVisualHTML(block.code).length > 0
          ) {
            return (
              <AgentVisualBlock
                key={idx}
                lang={lang}
                code={block.code}
                defaultView="preview"
              />
            );
          }
          if (
            (lang === "html" || lang === "svg") &&
            parseSafeVisualHTML(block.code).length > 0
          ) {
            return (
              <AgentVisualBlock
                key={idx}
                lang={lang}
                code={block.code}
                defaultView="code"
              />
            );
          }
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
  const delegated =
    tc.toolName === "delegate_to_agent" && tc.status === "completed"
      ? parseDelegatedToolSummary(tc.resultJson)
      : null;
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
        {delegated && (
          <div className="v2-delegate-summary">
            <strong>
              {t("Delegated to {agent}").replace(
                "{agent}",
                delegated.agentName,
              )}
            </strong>
            <MarkdownMessage text={delegated.summary} />
          </div>
        )}
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

type SecretCardPayload = {
  kind?: string;
  provider?: string;
  accountName?: string;
  rpcEndpoint?: string;
  label?: string;
  reason?: string;
  osVaultAvailable?: boolean;
};

function parseSecretCardPayload(raw: string): SecretCardPayload | null {
  try {
    const parsed = JSON.parse(raw) as unknown;
    return parsed && typeof parsed === "object" && !Array.isArray(parsed)
      ? (parsed as SecretCardPayload)
      : null;
  } catch {
    return null;
  }
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
    secret?: AgentV2SecretSubmission,
  ) => Promise<boolean>;
}) {
  const { t } = useUI();
  const [note, setNote] = useState("");
  const [secretValue, setSecretValue] = useState("");
  const [passphrase, setPassphrase] = useState("");
  const fields = formatApprovalFields(approval.toolName, approval.payloadJson);
  const pending = approval.status === "pending";
  const isSecretRequest = approval.toolName === "request_secret";
  const secretPayload = isSecretRequest
    ? parseSecretCardPayload(approval.payloadJson)
    : null;
  const needsPassphrase =
    isSecretRequest && secretPayload?.osVaultAvailable === false;

  async function submitSecret(e: FormEvent) {
    e.preventDefault();
    const val = secretValue.trim();
    if (!val || busy) return;
    const pass = passphrase;
    setSecretValue("");
    setPassphrase("");
    const ok = await onDecide(approval.id, "approve", "", {
      value: val,
      ...(pass ? { passphrase: pass } : {}),
    });
    if (ok && secretPayload?.kind === "provider_api_key") {
      window.dispatchEvent(new Event("ai-connection-changed"));
    }
  }

  return (
    <article
      className={`v2-approval-card v2-approval-${approval.status}${isSecretRequest ? " v2-secret-card" : ""}`}
      aria-label={approval.title}
    >
      <div className="v2-approval-head">
        <span className="v2-approval-title">
          {isSecretRequest ? (
            <KeyRound size={16} aria-hidden="true" />
          ) : approval.status === "approved" ? (
            <ShieldCheck size={16} aria-hidden="true" />
          ) : (
            <Shield size={16} aria-hidden="true" />
          )}
          <strong>{approval.title}</strong>
        </span>
        <span className={`v2-pill v2-pill-${approval.status}`}>
          {isSecretRequest
            ? pending
              ? t("Secret required")
              : approval.status === "approved"
                ? t("Saved encrypted")
                : t("Rejected")
            : pending
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
      {pending && isSecretRequest && (
        <form
          className="v2-secret-inputs"
          onSubmit={(e) => void submitSecret(e)}
        >
          <div className="v2-secret-fields">
            <input
              type="password"
              autoComplete="new-password"
              aria-label={t("API key or token")}
              placeholder={t("API key or token")}
              value={secretValue}
              disabled={busy}
              maxLength={512}
              onChange={(e) => setSecretValue(e.target.value)}
            />
            {needsPassphrase && (
              <input
                type="password"
                autoComplete="new-password"
                aria-label={t("Vault passphrase")}
                placeholder={t("Vault passphrase")}
                value={passphrase}
                disabled={busy}
                maxLength={256}
                onChange={(e) => setPassphrase(e.target.value)}
              />
            )}
          </div>
          <div className="v2-secret-actions">
            <button
              type="submit"
              className="primary"
              disabled={busy || !secretValue.trim()}
            >
              {t("Save encrypted")}
            </button>
            <button
              type="button"
              className="secondary"
              disabled={busy}
              onClick={() => {
                setSecretValue("");
                setPassphrase("");
                void onDecide(approval.id, "reject", "");
              }}
            >
              {t("Decline")}
            </button>
          </div>
        </form>
      )}
      {pending && !isSecretRequest && (
        <div className="v2-approval-actions">
          <input
            type="text"
            aria-label={t("Approval note")}
            placeholder={t("Optional note")}
            value={note}
            disabled={busy}
            maxLength={400}
            onChange={(e) => setNote(e.target.value)}
            onKeyDown={(e) => {
              if (
                e.key === "Enter" &&
                !e.shiftKey &&
                !e.nativeEvent.isComposing &&
                note.trim() &&
                !busy
              ) {
                e.preventDefault();
                void onDecide(approval.id, "reject", note);
              }
            }}
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

export function AgentChatV2({ view }: { view: AgentView }) {
  const { t, language } = useUI();
  const core = useCore();
  const {
    setProvidersOpen,
    chatDrafts,
    setChatDrafts,
    chatModelDrafts,
    setChatModelDrafts,
    pendingChatPrompt,
    clearChatPrompt,
    pendingChatThread,
    clearChatThread,
  } = useWorkspace();
  const v2 = useAgentsV2(core, view.profileId);
  const { view: connection } = useAIConnection();
  const profile = view.profiles.find((p) => p.id === view.profileId)!;

  function setComposerValueForVoice(nextText: string) {
    setChatDrafts((cur) => {
      const next = new Map(cur);
      const key = `${core.instanceId}:${view.profileId}:v2:${v2.activeThreadId || "new"}`;
      if (nextText) next.set(key, nextText);
      else next.delete(key);
      return next;
    });
  }

  const voice = useVoiceInput({
    language,
    onTranscript: setComposerValueForVoice,
    onError: (key) => v2.setError(t(key)),
  });

  useEffect(() => {
    voice.stop();
  }, [view.profileId, v2.activeThreadId, voice.stop]);

  const consumedPromptIds = useRef(new Set<string>());
  const consumedThreadIds = useRef(new Set<string>());
  const [railOpen, setRailOpen] = useState(false);
  const [search, setSearch] = useState("");
  const [toolsEnabled, setToolsEnabled] = useState(true);
  const [compactBusy, setCompactBusy] = useState(false);
  const [compactNotice, setCompactNotice] = useState("");
  const compactNoticeTimerRef = useRef<number | null>(null);
  const [editingTitle, setEditingTitle] = useState(false);
  const [titleDraft, setTitleDraft] = useState("");
  const [memoryNoteDraft, setMemoryNoteDraft] = useState<{
    text: string;
    error: string;
    busy: boolean;
  } | null>(null);
  const [undoToast, setUndoToast] = useState<{
    kind: "archive" | "delete";
    thread: AgentThread;
    prevActiveId: string;
  } | null>(null);
  const [pendingDeleteIds, setPendingDeleteIds] = useState<Set<string>>(
    () => new Set(),
  );
  const undoTimerRef = useRef<number | null>(null);
  const pendingDeleteRef = useRef<AgentThread | null>(null);
  const undoToastRef = useRef(undoToast);
  undoToastRef.current = undoToast;
  const coreRef = useRef(core);
  coreRef.current = core;
  const [welcomeDismissed, setWelcomeDismissed] = useState(() => {
    try {
      return sessionStorage.getItem("fased.welcome.dismissed") === "1";
    } catch {
      return false;
    }
  });

  function clearCompactNoticeTimer() {
    if (compactNoticeTimerRef.current !== null) {
      window.clearTimeout(compactNoticeTimerRef.current);
      compactNoticeTimerRef.current = null;
    }
  }

  function showCompactNotice(msg: string) {
    clearCompactNoticeTimer();
    setCompactNotice(msg);
    compactNoticeTimerRef.current = window.setTimeout(() => {
      compactNoticeTimerRef.current = null;
      setCompactNotice("");
    }, 4000);
  }

  function clearUndoTimer() {
    if (undoTimerRef.current !== null) {
      window.clearTimeout(undoTimerRef.current);
      undoTimerRef.current = null;
    }
  }

  async function flushPendingDelete() {
    const queued = pendingDeleteRef.current;
    if (!queued) return;
    pendingDeleteRef.current = null;
    clearUndoTimer();
    setPendingDeleteIds((prev) => {
      if (!prev.has(queued.id)) return prev;
      const next = new Set(prev);
      next.delete(queued.id);
      return next;
    });
    await v2.removeThread(queued.id);
  }

  function handleArchiveThread(thread: AgentThread) {
    if (thread.archived) {
      void v2.archiveThread(thread.id, false);
      return;
    }
    void (async () => {
      await flushPendingDelete();
      clearUndoTimer();
      const prevActiveId = v2.activeThreadId;
      await v2.archiveThread(thread.id, true);
      setUndoToast({ kind: "archive", thread, prevActiveId });
      undoTimerRef.current = window.setTimeout(() => {
        undoTimerRef.current = null;
        setUndoToast(null);
      }, 5000);
    })();
  }

  function handleDeleteThread(thread: AgentThread) {
    void (async () => {
      await flushPendingDelete();
      clearUndoTimer();
      const prevActiveId = v2.activeThreadId;
      pendingDeleteRef.current = thread;
      setPendingDeleteIds((prev) => new Set(prev).add(thread.id));
      if (v2.activeThreadId === thread.id) {
        v2.startNewThread();
      }
      setUndoToast({ kind: "delete", thread, prevActiveId });
      undoTimerRef.current = window.setTimeout(() => {
        undoTimerRef.current = null;
        setUndoToast(null);
        const queued = pendingDeleteRef.current;
        if (!queued || queued.id !== thread.id) return;
        pendingDeleteRef.current = null;
        setPendingDeleteIds((prev) => {
          if (!prev.has(queued.id)) return prev;
          const next = new Set(prev);
          next.delete(queued.id);
          return next;
        });
        void v2.removeThread(queued.id);
      }, 5000);
    })();
  }

  async function handleUndo() {
    const active = undoToastRef.current;
    if (!active) return;
    clearUndoTimer();
    setUndoToast(null);
    if (active.kind === "archive") {
      await v2.archiveThread(active.thread.id, false);
      await v2.selectThread(active.prevActiveId || active.thread.id);
      return;
    }
    if (pendingDeleteRef.current?.id === active.thread.id) {
      pendingDeleteRef.current = null;
      setPendingDeleteIds((prev) => {
        if (!prev.has(active.thread.id)) return prev;
        const next = new Set(prev);
        next.delete(active.thread.id);
        return next;
      });
      if (!active.prevActiveId || active.prevActiveId === active.thread.id) {
        await v2.selectThread(active.thread.id);
      }
    }
  }

  useEffect(() => {
    const flushOnLeave = () => {
      clearUndoTimer();
      const queued = pendingDeleteRef.current;
      if (queued) {
        pendingDeleteRef.current = null;
        if (coreRef.current.mode === "ready") {
          void deleteThreadV2(coreRef.current, queued.id);
        }
      }
    };
    window.addEventListener("pagehide", flushOnLeave);
    return () => {
      window.removeEventListener("pagehide", flushOnLeave);
      clearCompactNoticeTimer();
      flushOnLeave();
      setUndoToast(null);
      setCompactNotice("");
      setPendingDeleteIds(new Set());
    };
  }, [core.instanceId, view.profileId]);

  useEffect(() => {
    if (!undoToast) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey) || e.shiftKey || e.altKey) return;
      if (e.key.toLowerCase() !== "z") return;
      const target = e.target as HTMLElement | null;
      if (
        target &&
        (target.tagName === "INPUT" ||
          target.tagName === "TEXTAREA" ||
          target.isContentEditable)
      ) {
        return;
      }
      e.preventDefault();
      void handleUndo();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [undoToast]);

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
    if (next.draftOnly) {
      v2.startNewThread();
      setChatDrafts((cur) => {
        const copy = new Map(cur);
        copy.set(`${core.instanceId}:${view.profileId}:v2:new`, next.prompt);
        return copy;
      });
      return;
    }
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
    core.instanceId,
    v2.streaming,
    v2.startNewThread,
    v2.sendMessage,
    setChatDrafts,
    clearChatPrompt,
    activeConnectionRef,
    activeModel,
  ]);

  useEffect(() => {
    if (
      !pendingChatThread ||
      pendingChatThread.profileId !== view.profileId ||
      v2.streaming ||
      consumedThreadIds.current.has(pendingChatThread.id)
    ) {
      return;
    }
    consumedThreadIds.current.add(pendingChatThread.id);
    const next = pendingChatThread;
    clearChatThread(next.id);
    void v2.selectThread(next.threadId);
    void v2.refreshThreads();
  }, [
    pendingChatThread,
    view.profileId,
    v2.streaming,
    v2.selectThread,
    v2.refreshThreads,
    clearChatThread,
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
    (item) =>
      !pendingDeleteIds.has(item.id) &&
      (!q || item.title.toLowerCase().includes(q)),
  );

  const visibleMessages: AgentV2Message[] = (v2.detail?.messages ?? []).filter(
    (m) => m.role !== "tool",
  );
  const lastAssistantId =
    [...visibleMessages].reverse().find((m) => m.role === "assistant")?.id ??
    "";
  const formatRecalledLabel = (count: number) =>
    count === 1
      ? t("1 rule recalled")
      : t("{count} rules recalled").replace("{count}", String(count));

  async function handleCompact() {
    if (v2.streaming || compactBusy) return;
    const threadId = v2.detail?.thread.id || v2.activeThreadId;
    if (!threadId) {
      showCompactNotice(t("Nothing to compact"));
      return;
    }
    setCompactBusy(true);
    try {
      const res = await v2.compactThread(threadId);
      if (res) {
        showCompactNotice(
          t(res.compacted ? "Context compacted" : "Nothing to compact"),
        );
      }
    } finally {
      setCompactBusy(false);
    }
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    voice.stop();
    if (v2.streaming || !composerValue.trim()) return;
    if (modelMenuRef.current) modelMenuRef.current.open = false;
    const text = composerValue;
    const cmd = parseChatSlashCommand(text);
    if (cmd.kind === "compact") {
      setComposerValue("");
      await handleCompact();
      return;
    }
    if (cmd.kind === "side") {
      const threadId = v2.detail?.thread.id || v2.activeThreadId;
      if (!cmd.question || !threadId) {
        v2.setError(t("Ask a side question"));
        return;
      }
      setComposerValue("");
      const ok = await v2.askSideQuestion({
        threadId,
        content: cmd.question,
        connectionRef: activeConnectionRef || undefined,
        model: activeModel || undefined,
      });
      if (!ok) {
        setComposerValue(text);
      }
      return;
    }
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
                <span className="v2-thread-main">
                  {item.sourceKind === "task" && (
                    <Clock3
                      size={12}
                      className="v2-thread-source-icon"
                      aria-hidden="true"
                    />
                  )}
                  <span className="v2-thread-title">{item.title}</span>
                </span>
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
                  handleDeleteThread(item);
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
                <>
                  <h3
                    onDoubleClick={() => setEditingTitle(true)}
                    title={v2.detail.thread.title}
                  >
                    {v2.detail.thread.title}
                  </h3>
                  {v2.detail.thread.sourceKind === "task" && (
                    <span className="v2-pill v2-thread-source-pill">
                      {t("Task")}
                    </span>
                  )}
                </>
              )}
              <button
                type="button"
                className="secondary v2-compact-btn"
                title={t("Compact conversation")}
                disabled={v2.streaming || compactBusy}
                onClick={() => void handleCompact()}
              >
                {t("Compact")}
              </button>
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
                onClick={() => handleArchiveThread(v2.detail!.thread)}
              >
                <History size={15} />
              </button>
              <button
                type="button"
                className="icon-button"
                aria-label={t("Delete conversation")}
                title={t("Delete conversation")}
                disabled={v2.streaming}
                onClick={() => handleDeleteThread(v2.detail!.thread)}
              >
                <Trash2 size={15} />
              </button>
            </div>
          )}

          {undoToast && (
            <div className="v2-undo-toast" role="status" aria-live="polite">
              <span>
                {t(
                  undoToast.kind === "archive"
                    ? "Conversation archived"
                    : "Conversation deleted",
                )}
              </span>
              <div className="v2-undo-actions">
                <button
                  type="button"
                  className="secondary"
                  onClick={() => void handleUndo()}
                >
                  <RotateCcw size={13} aria-hidden="true" />
                  <span>{t("Undo")}</span>
                </button>
                <button
                  type="button"
                  className="icon-button"
                  aria-label={t("Dismiss")}
                  onClick={() => {
                    clearUndoTimer();
                    if (undoToast.kind === "delete") {
                      void flushPendingDelete();
                    }
                    setUndoToast(null);
                  }}
                >
                  <X size={13} aria-hidden="true" />
                </button>
              </div>
            </div>
          )}

          {compactNotice && (
            <div className="v2-compact-notice" role="status" aria-live="polite">
              <span>{compactNotice}</span>
              <button
                type="button"
                className="icon-button"
                aria-label={t("Dismiss")}
                onClick={() => {
                  clearCompactNoticeTimer();
                  setCompactNotice("");
                }}
              >
                <X size={13} aria-hidden="true" />
              </button>
            </div>
          )}

          {v2.detail?.thread.summary?.trim() && (
            <details className="v2-summary-banner">
              <summary>{t("Compacted context")}</summary>
              <pre>{v2.detail.thread.summary}</pre>
            </details>
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

            {visibleMessages.map((m) => {
              const citations =
                m.role === "assistant"
                  ? extractMessageCitations(m.toolCalls)
                  : [];
              return (
                <article
                  key={m.id}
                  className={`v2-message v2-message-${m.role}`}
                >
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
                      {citations.length > 0 && (
                        <div className="v2-citations" aria-label={t("Sources")}>
                          <Globe2 size={13} aria-hidden="true" />
                          {citations.map((c) => (
                            <a
                              key={c.url}
                              href={c.url}
                              target="_blank"
                              rel="noopener noreferrer"
                              className="v2-citation-chip"
                              title={c.title}
                            >
                              <span>{c.title}</span>
                              <small>{c.domain}</small>
                            </a>
                          ))}
                        </div>
                      )}
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
                      {(() => {
                        const showRecalled =
                          m.id === lastAssistantId &&
                          v2.lastRecalled &&
                          v2.lastRecalled.threadId === v2.detail?.thread.id &&
                          v2.lastRecalled.count > 0;
                        const showHandoff =
                          m.id === lastAssistantId &&
                          v2.lastHandoff &&
                          v2.lastHandoff.threadId === v2.detail?.thread.id &&
                          !!(
                            v2.lastHandoff.fromModel ||
                            v2.lastHandoff.fromProvider
                          );
                        const canRemember =
                          m.role === "assistant" &&
                          m.status !== "streaming" &&
                          m.content.trim() !== "";
                        const showMeta =
                          canRemember ||
                          ((m.content ||
                            m.errorText ||
                            (m.approvals?.length ?? 0) > 0) &&
                            (m.model ||
                              (m.outputTokens ?? 0) > 0 ||
                              !!showRecalled ||
                              !!showHandoff));
                        if (!showMeta) return null;
                        return (
                          <footer className="v2-msg-meta">
                            {m.model && <span>{previewModel(m.model, t)}</span>}
                            {(m.outputTokens ?? 0) > 0 && (
                              <span>
                                {(m.inputTokens ?? 0) + (m.outputTokens ?? 0)}{" "}
                                tok
                              </span>
                            )}
                            {showRecalled && v2.lastRecalled && (
                              <span
                                className="v2-recalled-pill"
                                title={v2.lastRecalled.titles.join(", ")}
                              >
                                {formatRecalledLabel(v2.lastRecalled.count)}
                              </span>
                            )}
                            {showHandoff && v2.lastHandoff && (
                              <span className="v2-handoff-pill">
                                {t("Handoff from {model}").replace(
                                  "{model}",
                                  previewModel(
                                    v2.lastHandoff.fromModel ||
                                      v2.lastHandoff.fromProvider,
                                    t,
                                  ),
                                )}
                              </span>
                            )}
                            {canRemember && (
                              <button
                                type="button"
                                className="text-button v2-msg-remember"
                                onClick={() =>
                                  setMemoryNoteDraft({
                                    text: m.content.trim().slice(0, 280),
                                    error: "",
                                    busy: false,
                                  })
                                }
                              >
                                {t("Remember this")}
                              </button>
                            )}
                          </footer>
                        );
                      })()}
                    </div>
                  )}
                </article>
              );
            })}

            {v2.streaming && (
              <article
                className="v2-message v2-message-assistant v2-message-live"
                role="status"
              >
                <div className="v2-assistant-turn">
                  {v2.lastHandoff &&
                    v2.lastHandoff.threadId === v2.detail?.thread.id &&
                    (v2.lastHandoff.fromModel ||
                      v2.lastHandoff.fromProvider) && (
                      <span className="v2-handoff-pill">
                        {t("Handoff from {model}").replace(
                          "{model}",
                          previewModel(
                            v2.lastHandoff.fromModel ||
                              v2.lastHandoff.fromProvider,
                            t,
                          ),
                        )}
                      </span>
                    )}
                  {(v2.streamDraft?.recalledMemories ?? 0) > 0 && (
                    <span
                      className="v2-recalled-pill"
                      title={(v2.streamDraft?.recalledMemoryTitles ?? []).join(
                        ", ",
                      )}
                    >
                      {formatRecalledLabel(v2.streamDraft!.recalledMemories!)}
                    </span>
                  )}
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

          {v2.sideChat && v2.sideChat.threadId === v2.detail?.thread.id && (
            <aside className="v2-side-card" aria-label={t("Side question")}>
              <div className="v2-side-head">
                <div className="v2-side-title">
                  <strong>{t("Side question")}</strong>
                  {(v2.sideChat.model || v2.sideChat.provider) && (
                    <span className="v2-pill">
                      {previewModel(
                        v2.sideChat.model || v2.sideChat.provider || "",
                        t,
                      )}
                    </span>
                  )}
                </div>
                <button
                  type="button"
                  className="icon-button"
                  aria-label={t("Dismiss")}
                  onClick={() => v2.dismissSideChat()}
                >
                  <X size={14} aria-hidden="true" />
                </button>
              </div>
              <p className="v2-side-question">{v2.sideChat.question}</p>
              {v2.sideChat.error && (
                <p className="v2-tool-err" role="alert">
                  {v2.sideChat.error}
                </p>
              )}
              {v2.sideChat.content ? (
                <div className="v2-side-answer">
                  <MarkdownMessage text={v2.sideChat.content} />
                </div>
              ) : (
                v2.sideChat.loading && (
                  <span className="v2-typing-pulse" aria-label={t("Working")}>
                    ···
                  </span>
                )
              )}
            </aside>
          )}

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
              {voice.supported && !v2.streaming && (
                <button
                  type="button"
                  className={`icon-button v2-voice-btn ${voice.listening ? "is-listening" : ""}`}
                  aria-label={
                    voice.listening ? t("Stop recording") : t("Voice input")
                  }
                  title={
                    voice.listening ? t("Stop recording") : t("Voice input")
                  }
                  aria-pressed={voice.listening}
                  disabled={v2.loading}
                  onClick={() => voice.toggle(composerValue)}
                >
                  {voice.listening ? (
                    <MicOff size={16} aria-hidden="true" />
                  ) : (
                    <Mic size={16} aria-hidden="true" />
                  )}
                </button>
              )}
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

      {memoryNoteDraft && (
        <Dialog
          title={t("Memory")}
          onClose={() => {
            if (!memoryNoteDraft.busy) setMemoryNoteDraft(null);
          }}
        >
          <form
            className="dialog-body v2-memory-form"
            onSubmit={async (e) => {
              e.preventDefault();
              const content = memoryNoteDraft.text.trim();
              if (!content || memoryNoteDraft.busy) return;
              setMemoryNoteDraft({ ...memoryNoteDraft, busy: true, error: "" });
              try {
                await createMemoryV2(core, {
                  profileId: view.profileId,
                  scope: "chat",
                  title: v2.detail?.thread.title || "Chat note",
                  content,
                });
                setMemoryNoteDraft(null);
              } catch (err) {
                setMemoryNoteDraft({
                  ...memoryNoteDraft,
                  busy: false,
                  error:
                    err instanceof Error
                      ? err.message
                      : "Could not save memory",
                });
              }
            }}
          >
            {memoryNoteDraft.error && (
              <p role="alert">{memoryNoteDraft.error}</p>
            )}
            <label>
              {t("Note")}
              <textarea
                aria-label={t("Note")}
                rows={4}
                maxLength={2000}
                required
                autoFocus
                value={memoryNoteDraft.text}
                disabled={memoryNoteDraft.busy}
                onChange={(e) =>
                  setMemoryNoteDraft({
                    ...memoryNoteDraft,
                    text: e.target.value,
                    error: "",
                  })
                }
              />
            </label>
            <div className="agent-actions">
              <button
                type="submit"
                className="primary"
                disabled={memoryNoteDraft.busy || !memoryNoteDraft.text.trim()}
              >
                {t("Save memory")}
              </button>
            </div>
          </form>
        </Dialog>
      )}
    </section>
  );
}
