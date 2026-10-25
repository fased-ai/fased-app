import { ResearchReadPanel, type ResearchSelection } from "./ResearchReadPanel";
import { MemoryRecall, memoryEvidenceStatuses } from "./MemoryRecall";
import { useUI } from "../ui-preferences";
import { useCore } from "../core";
import { NewsInputs } from "./NewsInputs";
import { modelForProviderChange } from "../agent-defaults";
import { useAIConnection } from "./AIConnection";
import { useLayoutEffect, useRef, useState, type FormEvent } from "react";
import { BookOpen, Bot, Plus, Settings2, History } from "../icons";
import { Badge, SectionHead, Dialog } from "../components/shared";
import { useWorkspace } from "../workspace";
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
            {tab === "settings" && (
              <button
                className="icon-button"
                aria-label={uiText("Bots")}
                data-tooltip={uiText("Bots")}
                onClick={() => navigate("agents/bots")}
              >
                <Bot size={17} />
              </button>
            )}
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
  const { setProvidersOpen } = useWorkspace();
  const { t: uiText } = useUI();
  const { agents, tab, navigate } = useWorkspace();
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
  const [name, setName] = useState(profile.name);
  const [persona, setPersona] = useState(profile.persona);
  const [provider, setProvider] = useState(profile.provider);
  const [model, setModel] = useState(profile.model);
  const [botName, setBotName] = useState("");
  const [quantity, setQuantity] = useState(1);
  const [price, setPrice] = useState(100);
  const [budget, setBudget] = useState(500);
  const profileRevision = useRef(v.revision);
  const profileInitialized = useRef(false);
  const memoryRevision = useRef(v.revision);
  function reloadProfile() {
    setName(profile.name);
    setPersona(profile.persona);
    setProvider(profile.provider);
    setModel(profile.model);
    profileRevision.current = v.revision;
  }
  useLayoutEffect(() => {
    if (tab !== "settings") {
      profileInitialized.current = false;
      return;
    }
    // A preceding save may still be awaiting its response as this tab opens.
    // Capture its resulting revision before fields become interactive, once per
    // entry. Later external changes still require explicit reload/review.
    if (!agents.busy && !profileInitialized.current) {
      reloadProfile();
      profileInitialized.current = true;
    }
  }, [tab, agents.busy]);
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
    : "";
  function updateMessage(sessionId: string, text: string) {
    setMessageDrafts((drafts) => {
      const next = new Map(drafts);
      if (text)
        next.set(`${core.instanceId}:${v.profileId}:${sessionId}`, text);
      else next.delete(`${core.instanceId}:${v.profileId}:${sessionId}`);
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
          className={`panel agent-chat ${activeSession ? "has-conversation" : ""} ${railOpen ? "rail-open" : ""}`}
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
                  if (
                    await agents.change({
                      action: "session-create",
                      name: sessionName,
                    })
                  ) {
                    setRailOpen(false);
                    setNewConversation(false);
                    setSessionName("");
                    setSession("");
                  }
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
              {t("Search conversations")}
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
                    setSession(s.id);
                    setRailOpen(false);
                  }}
                >
                  {s.name}
                </button>
              ))}
              {matching.length === 0 && (
                <p>{uiText("No matching conversations.")}</p>
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
                    composer={{
                      value: message,
                      onInteract: () => setRailOpen(false),
                      onChange: (text) => updateMessage(activeSession.id, text),
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
              <div className="chat-empty">
                <h3>
                  {search
                    ? uiText("No matching conversations")
                    : uiText("Start a conversation")}
                </h3>
                <button
                  className="primary"
                  disabled={agents.busy}
                  onClick={() => {
                    void agents
                      .change({
                        action: "session-create",
                        name: "New conversation",
                      })
                      .then((ok) => {
                        if (ok) {
                          setSearch("");
                          setSession("");
                          setRailOpen(false);
                        }
                      });
                  }}
                >
                  {t("Start chat")}
                </button>
              </div>
            )}
          </div>
        </section>
      )}
      {tab === "settings" && (
        <div className="agent-settings-workspace">
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
                    revision: profileRevision.current,
                    name,
                    text: persona,
                    provider,
                    model,
                  })
                )
                  profileRevision.current++;
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
              <details className="profile-defaults">
                <summary>{uiText("Model")}</summary>
                <label>
                  {uiText("Provider")}
                  <select
                    disabled={agents.busy}
                    value={provider}
                    aria-label={uiText("Provider")}
                    onChange={(e) => {
                      setProvider(e.target.value as Provider);
                      setModel(
                        modelForProviderChange(
                          e.target.value,
                          connection?.admissionPolicy?.model,
                        ),
                      );
                    }}
                  >
                    {Object.entries(providerNames).map(([id, title]) => (
                      <option key={id} value={id}>
                        {title}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  {uiText("Model")}
                  <input
                    list="agent-models"
                    disabled={agents.busy}
                    maxLength={120}
                    value={model}
                    onChange={(e) => setModel(e.target.value)}
                  />
                </label>
                <datalist id="agent-models">
                  {connection?.admissionPolicy && (
                    <option value={connection.admissionPolicy.model} />
                  )}
                </datalist>
              </details>
              <button className="primary" disabled={agents.busy}>
                {uiText("Save agent")}
              </button>
              <button
                type="button"
                className="text-button"
                onClick={reloadProfile}
              >
                {uiText("Reload")}
              </button>
            </form>
          </section>
        </div>
      )}
      {tab === "memory" && (
        <section className="panel">
          <div className="panel-heading">
            <h2>{uiText("Memory")}</h2>
            <button
              className="secondary"
              onClick={() => {
                resetMemory();
                setMemoryOpen(true);
              }}
            >
              {t("Add fact")}
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
                      ? t("Correct a memory")
                      : t("Add fact")
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
                          {
                            memoryEvidenceStatuses[
                              memoryRestore.evidence.status
                            ]
                          }
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
                    {t("Fact")}
                    <textarea
                      aria-label={t("Fact")}
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
      {tab === "bots" && (
        <section className="panel paper-strategies">
          <div className="panel-heading">
            <h2>{uiText("Paper strategies")}</h2>
            <Badge>{uiText("FIXED EXAMPLE INPUTS")}</Badge>
          </div>
          <div className="agent-split paper-workspace">
            <div className="agent-record-body paper-library">
              <p>
                {uiText(
                  "No exchange, wallet, live price or return calculation is connected. These examples exercise budgets and restart handling using integer fixture units.",
                )}
              </p>
              {v.bots.map((b) => (
                <article
                  className="memory-card"
                  key={b.id}
                  aria-label={`Paper strategy ${b.name}`}
                >
                  <strong>{b.name}</strong>
                  <p>
                    {b.strategy}
                    {uiText(" · Quantity ")}
                    {b.quantity}
                    {uiText(" × fixed price")} {b.priceCents}
                    {uiText(" · Used ")}
                    {b.spentCents} / {b.budgetCents} {uiText("fixture units")}
                  </p>
                </article>
              ))}
              {!v.bots.length && (
                <p className="subtle">{uiText("No paper strategies saved.")}</p>
              )}
            </div>
            <aside
              className="agent-record-body paper-composer"
              aria-label={uiText("Create paper strategy")}
            >
              <h3>{uiText("New paper strategy")}</h3>
              <form
                onSubmit={submit(async () => {
                  if (
                    await agents.change({
                      action: "bot-create",
                      name: botName,
                      quantity,
                      priceCents: price,
                      budgetCents: budget,
                    })
                  )
                    setBotName("");
                })}
              >
                <label>
                  {uiText("Paper bot name")}
                  <input
                    required
                    maxLength={80}
                    value={botName}
                    onChange={(e) => setBotName(e.target.value)}
                  />
                </label>
                <div className="agent-grid">
                  <label>
                    {uiText("Example quantity")}
                    <input
                      type="number"
                      min={1}
                      max={1000000}
                      required
                      value={quantity}
                      onChange={(e) => setQuantity(Number(e.target.value))}
                    />
                  </label>
                  <label>
                    {uiText("Fixed example price")}
                    <input
                      type="number"
                      min={1}
                      max={100000000}
                      required
                      value={price}
                      onChange={(e) => setPrice(Number(e.target.value))}
                    />
                  </label>
                  <label>
                    {uiText("Total fixture budget")}
                    <input
                      type="number"
                      min={1}
                      max={1000000000000}
                      required
                      value={budget}
                      onChange={(e) => setBudget(Number(e.target.value))}
                    />
                  </label>
                </div>
                <button className="primary" disabled={agents.busy}>
                  {uiText("Create paper bot")}
                </button>
              </form>{" "}
            </aside>
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
