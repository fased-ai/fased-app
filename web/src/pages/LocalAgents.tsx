import { previewModel } from "../ui-language-preview";
import { ResearchReadPanel, type ResearchSelection } from "./ResearchReadPanel";
import { MemoryRecall, memoryEvidenceStatuses } from "./MemoryRecall";
import { useUI } from "../ui-preferences";
import { useCore } from "../core";
import { NewsInputs } from "./NewsInputs";
import { modelForProviderChange } from "../agent-defaults";
import {
  AIModelPicker,
  ConnectionPicker,
  useAIConnection,
} from "./AIConnection";
import { useRef, useState, type FormEvent } from "react";
import { BookOpen, Plus, Settings2, History, Plug, ArrowUp } from "../icons";
import { SectionHead, Dialog } from "../components/shared";
import {
  useWorkspace,
  type AgentSettingsDraft,
  type ChatModelDraft,
} from "../workspace";
import { providerNames, type Provider } from "../state";
import type {
  AgentMemory,
  AgentMemoryRestoreDraft,
  AgentView,
} from "../agents";
import "../agents.css";
import { TaskHistory } from "./TaskHistory";
import { AgentTasks } from "./AgentTasks";
import { AgentAIReview } from "./AgentAIReview";

const date = (at: number) =>
  at
    ? new Date(at * 1000).toLocaleString(
        typeof document === "undefined" ? "en" : document.documentElement.lang,
      )
    : "No expiry";
export function LocalAgents() {
  const { t: uiText } = useUI();
  const t = uiText;
  const { agents, setProfileOpen, tab, navigate } = useWorkspace();
  const [taskPanel, setTaskPanel] = useState("automations");
  return (
    <>
      <SectionHead
        title={uiText(
          (
            {
              chat: "Chat",
              tasks: "Tasks",
              news: "News",
              memory: "Memory",
              settings: "Agents",
              bots: "Strategies",
            } as Record<string, string>
          )[tab] || "Agents",
        )}
        action={
          <div className="agent-header-actions">
            {agents.view && (
              <label className="profile-select">
                <span className="sr-only">{t("Agent")}</span>
                <select
                  aria-label={t("Agent")}
                  disabled={agents.busy}
                  value={agents.view.profileId}
                  onChange={(e) => void agents.select(e.target.value)}
                >
                  {agents.view.profiles.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name}
                    </option>
                  ))}
                </select>
              </label>
            )}
            <button
              className="icon-button"
              aria-label={t("Memory")}
              data-tooltip={t("Memory")}
              onClick={() => navigate("agents/memory")}
            >
              <BookOpen size={17} />
            </button>
            <button
              className="icon-button"
              aria-label={t("Agents")}
              data-tooltip={t("Agents")}
              onClick={() => navigate("agents/settings")}
            >
              <Settings2 size={17} />
            </button>
            <button
              className="icon-button"
              aria-label={t("New agent")}
              data-tooltip={t("New agent")}
              onClick={() => setProfileOpen(true)}
            >
              <Plus size={17} aria-hidden="true" />
            </button>
          </div>
        }
      />
      {agents.error && (
        <p className="storage-warning" role="alert">
          {agents.error}{" "}
          <button
            className="text-button"
            onClick={() => void agents.refresh().catch(() => {})}
          >
            {uiText("Refresh records")}
          </button>
        </p>
      )}
      {!agents.view ? (
        <p role="status">{uiText("Loading local agent records…")}</p>
      ) : (
        <>
          <LocalAgentEditor
            key={agents.view.profileId}
            view={agents.view}
            taskPanel={taskPanel}
            setTaskPanel={setTaskPanel}
          />
        </>
      )}
    </>
  );
}
function LocalAgentEditor({
  view: v,
  taskPanel,
  setTaskPanel,
}: {
  view: AgentView;
  taskPanel: string;
  setTaskPanel: (value: string) => void;
}) {
  const { t: uiText } = useUI();
  const {
    agents,
    tab,
    navigate,
    setProvidersOpen,
    agentSettingsDrafts,
    setAgentSettingsDrafts,
    chatModelDrafts,
    setChatModelDrafts,
  } = useWorkspace();
  const t = uiText;
  const core = useCore();
  const [archiveOpen, setArchiveOpen] = useState(false);
  const [archiveSearch, setArchiveSearch] = useState("");
  const [archiveKind, setArchiveKind] = useState<"conversations" | "memories">(
    "conversations",
  );
  const [archiveSource, setArchiveSource] = useState("");
  const [researchOpen, setResearchOpen] = useState(false);
  const [newConversation, setNewConversation] = useState(false);
  const [railOpen, setRailOpen] = useState(false);
  const [memoryOpen, setMemoryOpen] = useState(false);
  const [researchSelection, setResearchSelection] =
    useState<ResearchSelection>();
  const profile = v.profiles.find((p) => p.id === v.profileId)!;
  const { view: connection } = useAIConnection();
  const [session, setSession] = useState("");
  const [sessionName, setSessionName] = useState("");
  const [firstSend, setFirstSend] = useState<{
    sessionId: string;
    text: string;
    selection?: ChatModelDraft;
  } | null>(null);
  const [search, setSearch] = useState("");
  const { chatDrafts: messageDrafts, setChatDrafts: setMessageDrafts } =
    useWorkspace();
  const [memoryID, setMemoryID] = useState("");
  const [memory, setMemory] = useState("");
  const [source, setSource] = useState("");
  const [expiry, setExpiry] = useState("");
  const [memoryRestore, setMemoryRestore] =
    useState<AgentMemoryRestoreDraft | null>(null);
  const [memoryExpiryEdited, setMemoryExpiryEdited] = useState(false);
  const memoryExpiryOriginal = useRef(0);
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
  const initialValues = {
    name: profile.name,
    persona: profile.persona,
    provider: profile.provider,
    model: profile.model,
    accountRef: profile.connectionRef || "",
  };
  const profileDraft = agentSettingsDrafts.get(profileDraftKey) ?? {
    values: initialValues,
    revision: v.revision,
    baseline: JSON.stringify(initialValues),
  };
  const { name, persona, provider, model, accountRef } = profileDraft.values;
  function updateProfileDraft(patch: Partial<AgentSettingsDraft["values"]>) {
    setAgentSettingsDrafts((drafts) => {
      const draft = drafts.get(profileDraftKey) ?? profileDraft;
      const values = { ...draft.values, ...patch };
      const next = new Map(drafts);
      if (JSON.stringify(values) === draft.baseline)
        next.delete(profileDraftKey);
      else next.set(profileDraftKey, { ...draft, values });
      return next;
    });
  }
  const setName = (name: string) => updateProfileDraft({ name });
  const setPersona = (persona: string) => updateProfileDraft({ persona });
  const setModel = (model: string) => updateProfileDraft({ model });
  const setAccountRef = (accountRef: string) =>
    updateProfileDraft({ accountRef });
  function reloadProfile(expected?: AgentSettingsDraft) {
    setAgentSettingsDrafts((drafts) => {
      if (
        !drafts.has(profileDraftKey) ||
        (expected && drafts.get(profileDraftKey) !== expected)
      )
        return drafts;
      const next = new Map(drafts);
      next.delete(profileDraftKey);
      return next;
    });
  }

  const memoryRevision = useRef(v.revision);
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
  function resetMemory() {
    setMemoryOpen(false);
    setMemoryID("");
    setMemory("");
    setSource("");
    setExpiry("");
    setMemoryRestore(null);
    setMemoryExpiryEdited(false);
    memoryExpiryOriginal.current = 0;
  }
  function editMemory(
    m: Pick<AgentMemory, "id" | "text" | "source" | "expiresAt">,
    revision: number,
    restoration: AgentMemoryRestoreDraft | null = null,
  ) {
    setMemoryOpen(true);
    memoryRevision.current = revision;
    setMemoryID(m.id);
    setMemory(m.text);
    setSource(m.source);
    setMemoryRestore(restoration);
    setMemoryExpiryEdited(false);
    memoryExpiryOriginal.current = m.expiresAt;
    setExpiry(
      m.expiresAt
        ? new Date(
            m.expiresAt * 1000 -
              new Date(m.expiresAt * 1000).getTimezoneOffset() * 60000,
          )
            .toISOString()
            .slice(0, 19)
        : "",
    );
  }
  function submit(fn: () => Promise<void>) {
    return (e: FormEvent) => {
      e.preventDefault();
      void fn();
    };
  }
  return (
    <div className="local-agents">
      {tab === "news" && (
        <NewsInputs
          view={v}
          onDiscuss={() => {
            setSession("");
            setSearch("");
            navigate("agents/chat");
          }}
        />
      )}
      {archiveOpen && (
        <Dialog
          title={t(
            archiveKind === "memories"
              ? "Memory history"
              : "Archived conversations",
          )}
          wide
          onClose={() => setArchiveOpen(false)}
        >
          <TaskHistory
            key={v.profileId}
            view={v}
            kind={archiveKind}
            initialSearch={archiveSearch}
            initialSource={archiveSource}
            onMemorySelected={(m) => {
              setArchiveOpen(false);
              editMemory(
                {
                  ...m,
                  id: v.memories.some((note) => note.id === m.id) ? m.id : "",
                },
                v.revision,
              );
            }}
          />
        </Dialog>
      )}
      {tab === "chat" && (
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
            <div className="rail-heading">
              <h2>{t("Conversations")}</h2>
              <button
                className="icon-button"
                aria-label={t("Archived conversations")}
                title={t("Archived conversations")}
                onClick={() => {
                  setArchiveKind("conversations");
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
                        (s) =>
                          s.profileId === v.profileId && !existing.has(s.id),
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
                          setArchiveKind("conversations");
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
                      onSubmittedMount: () => {
                        setFirstSend(null);
                      },
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
                              ) ??
                              (connection?.ref === ref ? connection : null);
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
      )}
      {tab === "settings" && (
        <div className="agent-settings-workspace">
          <nav
            className="panel agent-profile-list"
            aria-label={uiText("Agents")}
          >
            {v.profiles.map((item) => (
              <button
                key={item.id}
                type="button"
                aria-pressed={item.id === v.profileId}
                onClick={() => void agents.select(item.id)}
                disabled={agents.busy}
              >
                <span className="avatar small" aria-hidden="true">
                  {item.name.slice(0, 2).toUpperCase()}
                </span>
                <span>{item.name}</span>
              </button>
            ))}
          </nav>
          <section className="panel" aria-label={uiText("Agent settings")}>
            <div className="panel-heading">
              <h2>{uiText("Agent")}</h2>
              <button
                className="secondary"
                onClick={() => setProvidersOpen(true)}
              >
                <Settings2 size={16} />
                {t("Providers")}
              </button>
            </div>
            <form
              className="agent-record-body"
              onSubmit={submit(async () => {
                if (
                  await agents.change({
                    action: "profile-update",
                    revision: profileDraft.revision,
                    name,
                    text: persona,
                    provider,
                    accountRef,
                    model,
                  })
                ) {
                  reloadProfile(profileDraft);
                }
              })}
            >
              <label>
                {uiText("Name")}
                <input
                  disabled={agents.busy}
                  required
                  maxLength={80}
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                />
              </label>
              <label>
                {uiText("Instructions")}
                <textarea
                  disabled={agents.busy}
                  maxLength={4000}
                  value={persona}
                  onChange={(e) => setPersona(e.target.value)}
                />
              </label>
              <fieldset className="profile-defaults">
                <legend className="sr-only">{uiText("Model")}</legend>
                <label>
                  {uiText("Provider")}
                  <select
                    disabled={agents.busy}
                    value={provider}
                    aria-label={uiText("Provider")}
                    onChange={(e) => {
                      const provider = e.target.value as Provider;
                      const a =
                        connection?.connections?.find(
                          (a) => a.provider === e.target.value,
                        ) ??
                        (connection?.provider === e.target.value
                          ? connection
                          : null);
                      updateProfileDraft({
                        provider,
                        accountRef: a?.ref || "",
                        model: modelForProviderChange(
                          provider,
                          a?.catalog?.models.find((m) => m.recommended)?.id ||
                            connection?.catalog?.models.find(
                              (m) => m.provider === provider && m.recommended,
                            )?.id,
                        ),
                      });
                    }}
                  >
                    <option value="none">{uiText("No AI")}</option>
                    {connection?.catalog?.providers.map((item) => (
                      <option key={item.id} value={item.id}>
                        {item.name}
                      </option>
                    ))}
                    {provider !== "none" &&
                      !connection?.catalog?.providers.some(
                        (item) => item.id === provider,
                      ) && (
                        <option value={provider} disabled>
                          {providerNames[provider]}
                        </option>
                      )}
                  </select>
                </label>
                {provider !== "none" && (
                  <label>
                    {uiText("Account")}
                    <select
                      aria-label={uiText("Account")}
                      value={accountRef}
                      disabled={agents.busy}
                      onChange={(e) => setAccountRef(e.target.value)}
                    >
                      <option value="">
                        {uiText(provider === "openai" ? "Default" : "Select")}
                      </option>
                      {connection?.connections
                        ?.filter((a) => a.provider === provider)
                        .map((a) => (
                          <option key={a.ref} value={a.ref}>
                            {a.name ||
                              connection?.catalog?.providers.find(
                                (p) => p.id === a.provider,
                              )?.name ||
                              "OpenAI"}
                          </option>
                        ))}
                      {accountRef &&
                        !connection?.connections?.some(
                          (a) => a.ref === accountRef,
                        ) && (
                          <option value={accountRef} disabled>
                            {uiText("Unavailable")}
                          </option>
                        )}
                    </select>
                  </label>
                )}
                <div hidden={provider === "none"}>
                  {" "}
                  <AIModelPicker
                    value={model}
                    onChange={setModel}
                    connectionRef={
                      provider !== "none"
                        ? accountRef ||
                          (connection?.provider === provider
                            ? connection.ref
                            : "") ||
                          ""
                        : ""
                    }
                    allowDemo={provider === "none"}
                    disabled={agents.busy}
                  />
                </div>
              </fieldset>
              <div className="row-actions">
                <button className="primary" disabled={agents.busy}>
                  {uiText("Save agent")}
                </button>
                <button
                  type="button"
                  className="text-button"
                  onClick={() => reloadProfile()}
                >
                  {uiText("Reload")}
                </button>
              </div>
            </form>
          </section>
        </div>
      )}
      {tab === "memory" && (
        <section className="panel">
          <div className="panel-heading">
            <h2>{uiText("Memory")}</h2>
            <button
              className="icon-button"
              aria-label={t("Remember")}
              title={t("Remember")}
              onClick={() => {
                resetMemory();
                setMemoryOpen(true);
              }}
            >
              <Plus size={17} />
            </button>
          </div>
          <div className="agent-record-body memory-workspace">
            <div className="memory-library">
              <MemoryRecall
                view={v}
                onOlder={(m) => {
                  setArchiveKind("memories");
                  setArchiveSource("");
                  setArchiveSearch(m.id);
                  setArchiveOpen(true);
                }}
                onOpen={(source) => {
                  if (v.sessions.some((s) => s.id === source.sessionId)) {
                    setSession(source.sessionId!);
                    setSearch("");
                    navigate("agents/chat");
                  } else {
                    setArchiveKind("conversations");
                    setArchiveSource(source.sessionId!);
                    setArchiveSearch("");
                    setArchiveOpen(true);
                  }
                }}
                onCorrect={(m) => editMemory(m, v.revision)}
                onRestore={(draft) =>
                  editMemory(
                    { id: draft.memoryId, ...draft.selected },
                    draft.revision,
                    draft,
                  )
                }
              />
            </div>
            {memoryOpen && (
              <Dialog
                title={
                  memoryRestore
                    ? t("Restore a memory")
                    : memoryID
                      ? t("Edit")
                      : t("Remember")
                }
                onClose={resetMemory}
              >
                <form
                  aria-label={uiText("Memory editor")}
                  className="memory-editor"
                  onSubmit={submit(async () => {
                    if (
                      await agents.change({
                        action: "memory-put",
                        revision: memoryID ? memoryRevision.current : undefined,
                        id: memoryID || undefined,
                        text: memory,
                        source: source.trim() || "Owner note",
                        expiresAt: !memoryExpiryEdited
                          ? memoryExpiryOriginal.current
                          : expiry
                            ? Math.floor(new Date(expiry).getTime() / 1000)
                            : 0,
                      })
                    )
                      resetMemory();
                  })}
                >
                  {agents.error && <p role="alert">{agents.error}</p>}
                  {memoryRestore && (
                    <section aria-label={uiText("Memory restoration review")}>
                      <article aria-label={uiText("Current memory revision")}>
                        <h4>{uiText("Current")}</h4>
                        <p>{memoryRestore.current.text}</p>
                        <small>
                          {memoryRestore.current.source}
                          {uiText(" · Updated")}{" "}
                          {date(memoryRestore.current.at)}
                          {uiText(" · Expiry")}{" "}
                          {uiText(date(memoryRestore.current.expiresAt))}
                        </small>
                      </article>
                      <article aria-label={uiText("Selected memory revision")}>
                        <h4>
                          {uiText("Selected revision ")}
                          {memoryRestore.historyIndex + 1}
                        </h4>
                        <p>{memoryRestore.selected.text}</p>
                        <small>
                          {memoryRestore.selected.source}
                          {uiText(" · Updated")}{" "}
                          {date(memoryRestore.selected.at)}
                          {uiText(" · Expiry")}{" "}
                          {uiText(date(memoryRestore.selected.expiresAt))}
                        </small>
                      </article>
                      {memoryRestore.selected.expiresAt > 0 &&
                        memoryRestore.selected.expiresAt <=
                          Date.now() / 1000 && (
                          <p role="status">{uiText("Expired revision")}</p>
                        )}
                      <section
                        aria-label={uiText("Restoration source evidence")}
                      >
                        <h4>{uiText("Recorded source")}</h4>
                        <p>
                          {uiText(
                            memoryEvidenceStatuses[
                              memoryRestore.evidence.status
                            ],
                          )}
                        </p>
                        {memoryRestore.evidence.text && (
                          <blockquote>{memoryRestore.evidence.text}</blockquote>
                        )}
                      </section>
                      {memoryRestore.revision !== v.revision && (
                        <p role="status">
                          {uiText(
                            "Workspace changed. Review the restoration again.",
                          )}
                        </p>
                      )}
                    </section>
                  )}
                  <label>
                    {t("Note")}
                    <textarea
                      aria-label={t("Note")}
                      required
                      maxLength={4000}
                      value={memory}
                      onChange={(e) => setMemory(e.target.value)}
                    />
                  </label>
                  <details className="task-inspection">
                    <summary>{t("Details")}</summary>
                    <label>
                      {uiText("Evidence or source")}
                      <input
                        maxLength={300}
                        value={source}
                        onChange={(e) => setSource(e.target.value)}
                        placeholder={uiText(
                          "e.g. Owner preference, reviewed today",
                        )}
                      />
                    </label>
                    <label>
                      {uiText("Expiry (optional)")}
                      <input
                        type="datetime-local"
                        step="1"
                        value={expiry}
                        onChange={(e) => {
                          setExpiry(e.target.value);
                          setMemoryExpiryEdited(true);
                        }}
                      />
                    </label>
                  </details>
                  <div className="agent-inline">
                    <button
                      className="primary"
                      disabled={
                        agents.busy ||
                        !!(
                          memoryRestore && memoryRestore.revision !== v.revision
                        )
                      }
                    >
                      {memoryRestore
                        ? t("Save restored memory")
                        : memoryID
                          ? t("Save correction")
                          : t("Save memory")}
                    </button>
                    <button
                      type="button"
                      className="secondary"
                      onClick={resetMemory}
                    >
                      {memoryRestore
                        ? uiText("Cancel restoration")
                        : memoryID
                          ? uiText("Cancel correction")
                          : uiText("Cancel memory edit")}
                    </button>
                  </div>
                </form>
              </Dialog>
            )}
          </div>
        </section>
      )}
      {tab === "tasks" && (
        <AgentTasks
          view={v}
          panel={taskPanel}
          setPanel={setTaskPanel}
          onDiscuss={() => {
            setSession("");
            setSearch("");
            navigate("agents/chat");
          }}
        />
      )}
    </div>
  );
}
