import { useState, type FormEvent } from "react";
import { ArrowLeft, ArrowUp, History, Plug, Plus } from "../icons";
import { previewModel } from "../ui-language-preview";
import { Dialog } from "../components/shared";
import { useCore } from "../core";
import { useUI } from "../ui-preferences";
import { useWorkspace, type ChatModelDraft } from "../workspace";
import {
  AIModelPicker,
  ConnectionPicker,
  useAIConnection,
} from "./AIConnection";
import { AgentAIReview } from "./AgentAIReview";
import { ResearchReadPanel, type ResearchSelection } from "./ResearchReadPanel";
import { TaskHistory } from "./TaskHistory";
import type { AgentView } from "../agents";

export function LegacyAgentChat({
  view: v,
  session,
  setSession,
  search,
  setSearch,
  onBackToThreads,
}: {
  view: AgentView;
  session: string;
  setSession: (id: string) => void;
  search: string;
  setSearch: (q: string) => void;
  onBackToThreads?: () => void;
}) {
  const { t: uiText } = useUI();
  const t = uiText;
  const core = useCore();
  const {
    agents,
    setProvidersOpen,
    chatDrafts: messageDrafts,
    setChatDrafts: setMessageDrafts,
    chatModelDrafts,
    setChatModelDrafts,
  } = useWorkspace();
  const { view: connection } = useAIConnection();

  const [archiveOpen, setArchiveOpen] = useState(false);
  const [archiveSearch, setArchiveSearch] = useState("");
  const [archiveSource, setArchiveSource] = useState("");
  const [researchOpen, setResearchOpen] = useState(false);
  const [newConversation, setNewConversation] = useState(false);
  const [railOpen, setRailOpen] = useState(false);
  const [researchSelection, setResearchSelection] =
    useState<ResearchSelection>();
  const [sessionName, setSessionName] = useState("");
  const [firstSend, setFirstSend] = useState<{
    sessionId: string;
    text: string;
    selection?: ChatModelDraft;
  } | null>(null);

  const profile = v.profiles.find((p) => p.id === v.profileId)!;
  const profileDraftKey = `${core.instanceId}:${v.profileId}`;
  const newChatKey = `${profileDraftKey}:new`;
  const newSelection = chatModelDrafts.get(newChatKey);
  const replyConnection =
    newSelection?.connectionRef ??
    (profile.provider === "none"
      ? ""
      : profile.connectionRef || connection?.ref || "");
  const replyModel = newSelection?.model ?? profile.model;

  function setChatSelection(sessionId: string, selection?: ChatModelDraft) {
    const key = `${profileDraftKey}:${sessionId}`;
    setChatModelDrafts((drafts) => {
      const next = new Map(drafts);
      if (selection) next.set(key, selection);
      else next.delete(key);
      return next;
    });
  }

  const query = search.toLowerCase();
  const matching = v.sessions.filter(
    (s) =>
      s.profileId === v.profileId &&
      (!query ||
        s.name.toLowerCase().includes(query) ||
        v.messages.some(
          (m) =>
            m.profileId === v.profileId &&
            m.sessionId === s.id &&
            m.text.toLowerCase().includes(query),
        ) ||
        v.aiRequests.some(
          (q) =>
            q.profileId === v.profileId &&
            q.sessionId === s.id &&
            q.state === "completed" &&
            !q.taskId &&
            (q.prompt.toLowerCase().includes(query) ||
              q.answer.toLowerCase().includes(query)),
        )),
  );
  const activeSession =
    matching.find((s) => s.id === session) ?? matching.at(-1);

  const message = activeSession
    ? (messageDrafts.get(
        `${core.instanceId}:${v.profileId}:${activeSession.id}`,
      ) ?? "")
    : (messageDrafts.get(`${core.instanceId}:${v.profileId}:new`) ?? "");

  function updateMessage(sessionId: string, text: string) {
    setMessageDrafts((drafts) => {
      const next = new Map(drafts);
      if (text)
        next.set(`${core.instanceId}:${v.profileId}:${sessionId}`, text);
      else next.delete(`${core.instanceId}:${v.profileId}:${sessionId}`);
      return next;
    });
  }

  function moveNewChatDrafts(
    sessionId: string,
    text: string,
    selection?: ChatModelDraft,
  ) {
    const target = `${profileDraftKey}:${sessionId}`;
    setMessageDrafts((drafts) => {
      if (!text) return drafts;
      const next = new Map(drafts);
      next.set(target, text);
      if (drafts.get(newChatKey) === text) next.delete(newChatKey);
      return next;
    });
    setChatModelDrafts((drafts) => {
      if (!selection) return drafts;
      const next = new Map(drafts);
      next.set(target, selection);
      if (drafts.get(newChatKey) === selection) next.delete(newChatKey);
      return next;
    });
  }

  function submit(fn: () => Promise<void>) {
    return (e: FormEvent) => {
      e.preventDefault();
      void fn();
    };
  }

  return (
    <>
      {archiveOpen && (
        <Dialog
          title={t("Archived conversations")}
          wide
          onClose={() => setArchiveOpen(false)}
        >
          <TaskHistory
            key={v.profileId}
            view={v}
            kind="conversations"
            initialSearch={archiveSearch}
            initialSource={archiveSource}
          />
        </Dialog>
      )}
      <section
        className={`panel agent-chat has-conversation ${railOpen ? "rail-open" : ""}`}
        aria-label={uiText("Agent chat workspace")}
      >
        <button
          className="text-button mobile-conversation-button"
          aria-expanded={railOpen}
          onClick={() => setRailOpen(!railOpen)}
        >
          {uiText("Conversations")}
        </button>
        <aside
          className="conversation-rail"
          aria-label={uiText("Conversations")}
        >
          {onBackToThreads && (
            <button
              type="button"
              className="secondary"
              onClick={onBackToThreads}
            >
              <ArrowLeft size={14} />
              {t("Back to threads")}
            </button>
          )}
          <div className="rail-heading">
            <h2>{t("Conversations")}</h2>
            <button
              className="icon-button"
              aria-label={t("Archived conversations")}
              title={t("Archived conversations")}
              onClick={() => {
                setArchiveSearch(search);
                setArchiveSource("");
                setArchiveOpen(true);
              }}
            >
              <History size={15} />
            </button>
            <button
              className="text-button"
              onClick={() => setNewConversation(!newConversation)}
              aria-label={t("New conversation")}
            >
              <Plus size={15} />
            </button>
          </div>
          {newConversation && (
            <form
              className="agent-inline"
              onSubmit={submit(async () => {
                const wasEmpty = !activeSession;
                const existing = new Set(v.sessions.map((s) => s.id));
                const draft = message;
                const selection = newSelection;
                await agents.change(
                  {
                    action: "session-create",
                    name: sessionName,
                  },
                  (created) => {
                    const thread = created.sessions.find(
                      (s) => s.profileId === v.profileId && !existing.has(s.id),
                    );
                    if (wasEmpty && thread)
                      moveNewChatDrafts(thread.id, draft, selection);
                    setRailOpen(false);
                    setNewConversation(false);
                    setSessionName("");
                    setSearch("");
                    setSession(thread?.id ?? "");
                  },
                );
              })}
            >
              <label>
                {t("Conversation name")}
                <input
                  required
                  maxLength={80}
                  value={sessionName}
                  onChange={(e) => setSessionName(e.target.value)}
                />
              </label>
              <button className="secondary" disabled={agents.busy}>
                {t("Create conversation")}
              </button>
            </form>
          )}
          <label>
            <span className="sr-only">{t("Search conversations")}</span>
            <input
              type="search"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder={t("Search conversations")}
            />
          </label>
          <div className="agent-session-list">
            {matching.map((s) => (
              <button
                className={
                  s.id === activeSession?.id
                    ? "secondary selected"
                    : "text-button"
                }
                key={s.id}
                onClick={() => {
                  setFirstSend(null);
                  setSession(s.id);
                  setRailOpen(false);
                }}
              >
                {s.name}
              </button>
            ))}
            {matching.length === 0 && (
              <p>
                {uiText(
                  query ? "No matching conversations." : "No conversations",
                )}
              </p>
            )}
          </div>
        </aside>
        <div
          className={`agent-record-body chat-workspace ${researchOpen ? "with-inspector" : ""}`}
        >
          {activeSession ? (
            <>
              <div className="chat-thread">
                <div className="panel-heading chat-thread-heading">
                  <h3>{activeSession.name}</h3>
                  <button
                    className="icon-button"
                    aria-label={t("Archive conversation")}
                    title={t("Archive conversation")}
                    disabled={agents.busy || !!message.trim()}
                    onClick={async () => {
                      if (
                        await agents.change({
                          action: "session-archive",
                          id: activeSession.id,
                        })
                      ) {
                        setSession("");
                        setArchiveSearch("");
                        setArchiveSource("");
                        setArchiveOpen(true);
                      }
                    }}
                  >
                    <History size={15} />
                  </button>
                  <button
                    className="text-button chat-context-button"
                    onClick={() => setResearchOpen(!researchOpen)}
                  >
                    {t("Sources")}
                  </button>
                </div>
                <AgentAIReview
                  view={v}
                  sessionId={activeSession.id}
                  initialSelection={
                    chatModelDrafts.get(
                      `${profileDraftKey}:${activeSession.id}`,
                    ) ??
                    (firstSend?.sessionId === activeSession.id
                      ? firstSend.selection
                      : undefined)
                  }
                  onSelectionChange={(selection) =>
                    setChatSelection(activeSession.id, selection)
                  }
                  composer={{
                    value: message,
                    submitOnMount:
                      firstSend?.sessionId === activeSession.id &&
                      firstSend.text === message,
                    onSubmittedMount: () => setFirstSend(null),
                    onInteract: () => setRailOpen(false),
                    onChange: (text) => {
                      setFirstSend(null);
                      updateMessage(activeSession.id, text);
                    },
                    onSave: async () => {
                      const submitted = message;
                      const thread = activeSession.id;
                      if (
                        await agents.change({
                          action: "message-add",
                          id: activeSession.id,
                          text: submitted,
                        })
                      )
                        setMessageDrafts((current) => {
                          if (
                            current.get(
                              `${core.instanceId}:${v.profileId}:${thread}`,
                            ) !== submitted
                          )
                            return current;
                          const next = new Map(current);
                          next.set(
                            `${core.instanceId}:${v.profileId}:${thread}`,
                            "",
                          );
                          return next;
                        });
                    },
                  }}
                  researchSelection={
                    researchSelection?.profile === v.profileId &&
                    researchSelection?.sessionId === activeSession.id
                      ? researchSelection
                      : undefined
                  }
                  onClearResearch={() => setResearchSelection(undefined)}
                />
              </div>
              {researchOpen && core.mode === "ready" && (
                <aside
                  className="chat-review"
                  aria-label={uiText("Source inspector")}
                >
                  <ResearchReadPanel
                    key={`research:${v.profileId}:${activeSession.id}`}
                    view={v}
                    sessionId={activeSession.id}
                    onClose={() => setResearchOpen(false)}
                    onUseAI={(selection) => {
                      setResearchSelection(selection);
                      setResearchOpen(false);
                    }}
                  />
                </aside>
              )}
            </>
          ) : (
            <div className="chat-thread">
              <form
                className="chat-message-form"
                onSubmit={submit(async () => {
                  if (agents.busy || !message.trim()) return;
                  const submitted = message;
                  const existing = new Set(v.sessions.map((s) => s.id));
                  await agents.change(
                    {
                      action: "session-create",
                      profileId: v.profileId,
                      revision: v.revision,
                      name: t("New conversation"),
                    },
                    (created) => {
                      const thread = created.sessions.find(
                        (s) =>
                          s.profileId === v.profileId && !existing.has(s.id),
                      );
                      if (!thread) return;
                      moveNewChatDrafts(thread.id, submitted, newSelection);
                      setFirstSend({
                        sessionId: thread.id,
                        text: submitted,
                        selection: newSelection,
                      });
                      setSearch("");
                      setSession(thread.id);
                      setRailOpen(false);
                    },
                  );
                })}
              >
                <label className="chat-input">
                  <span>{t("Message")}</span>
                  <textarea
                    aria-label={t("Message")}
                    placeholder={t("Message your agent…")}
                    disabled={agents.busy}
                    required
                    maxLength={2000}
                    value={message}
                    onChange={(e) => updateMessage("new", e.target.value)}
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
                  <details className="reply-model-menu">
                    <summary
                      aria-label={t("Reply model")}
                      title={previewModel(replyModel, t) || t("Provider")}
                    >
                      {previewModel(replyModel, t) || t("Provider")}
                    </summary>
                    <div className="reply-model-panel">
                      <ConnectionPicker
                        value={replyConnection}
                        onChange={(ref) => {
                          const account =
                            connection?.connections?.find(
                              (a) => a.ref === ref,
                            ) ?? (connection?.ref === ref ? connection : null);
                          const recommended = account?.catalog?.models.find(
                            (item) =>
                              item.connectionRef === ref &&
                              item.selectable &&
                              item.recommended,
                          );
                          setChatSelection("new", {
                            connectionRef: ref,
                            model: ref ? (recommended?.id ?? "") : "",
                          });
                        }}
                      />
                      <AIModelPicker
                        value={replyModel}
                        connectionRef={replyConnection}
                        onChange={(next) =>
                          setChatSelection("new", {
                            connectionRef: replyConnection,
                            model: next,
                          })
                        }
                      />
                      <button
                        type="button"
                        className="text-button"
                        onClick={() => setChatSelection("new")}
                      >
                        {t("Use agent default")}
                      </button>
                    </div>
                  </details>
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
                  <button
                    className="primary composer-submit"
                    aria-label={t("Send")}
                    title={t("Send")}
                    disabled={agents.busy || !message.trim()}
                  >
                    <ArrowUp size={20} aria-hidden="true" />
                  </button>
                </div>
              </form>
            </div>
          )}
        </div>
      </section>
    </>
  );
}
