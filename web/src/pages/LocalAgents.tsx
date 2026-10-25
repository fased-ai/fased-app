import { ResearchReadPanel, type ResearchSelection } from "./ResearchReadPanel";
import { MemoryRecall, memoryEvidenceStatuses } from "./MemoryRecall";
import { useUI } from "../ui-preferences";
import { useCore } from "../core";
import { NewsInputs } from "./NewsInputs";
import { AIConnectionSettings } from "./AIConnection";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { BookOpen, Bot, Plus, Settings2 } from "../icons";
import { Badge, SectionHead } from "../components/shared";
import { useWorkspace } from "../workspace";
import { providerNames, type Provider } from "../state";
import type {
  AgentMemory,
  AgentMemoryRestoreDraft,
  AgentView,
} from "../agents";
import "../agents.css";
import { AgentTasks } from "./AgentTasks";
import { AgentAIReview } from "./AgentAIReview";

const date = (at: number) =>
  at ? new Date(at * 1000).toLocaleString() : "No expiry";
export function LocalAgents() {
  const { t } = useUI();
  const { agents, setProfileOpen, tab, navigate } = useWorkspace();
  const [taskPanel, setTaskPanel] = useState("automations");
  return (
    <>
      <SectionHead
        title={
          agents.view?.profiles.find((p) => p.id === agents.view?.profileId)
            ?.name ?? "Agents"
        }
        action={
          <div className="agent-header-actions">
            {agents.view && (
              <label className="profile-select">
                {t("Agent")}
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
                aria-label="Bots"
                data-tooltip="Bots"
                onClick={() => navigate("agents/bots")}
              >
                <Bot size={17} />
              </button>
            )}
            <button className="secondary" onClick={() => setProfileOpen(true)}>
              <Plus size={16} /> {t("New profile")}
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
            Refresh records
          </button>
        </p>
      )}
      {!agents.view ? (
        <p role="status">Loading local agent records…</p>
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
  const { agents, tab, navigate } = useWorkspace();
  const { t } = useUI();
  const core = useCore();
  const [researchOpen, setResearchOpen] = useState(false);
  const [newConversation, setNewConversation] = useState(false);
  const [railOpen, setRailOpen] = useState(false);
  const [memoryOpen, setMemoryOpen] = useState(false);
  const [researchSelection, setResearchSelection] =
    useState<ResearchSelection>();
  const profile = v.profiles.find((p) => p.id === v.profileId)!;
  const [session, setSession] = useState("");
  const [sessionName, setSessionName] = useState("");
  const [search, setSearch] = useState("");
  const [messageDrafts, setMessageDrafts] = useState(new Map<string, string>());
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
  const memoryRevision = useRef(v.revision);
  function reloadProfile() {
    setName(profile.name);
    setPersona(profile.persona);
    setProvider(profile.provider);
    setModel(profile.model);
    profileRevision.current = v.revision;
  }
  useEffect(() => {
    if (tab === "settings") reloadProfile();
  }, [tab]);
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
    ? (messageDrafts.get(activeSession.id) ?? "")
    : "";
  function updateMessage(sessionId: string, text: string) {
    setMessageDrafts((drafts) => {
      const next = new Map(drafts);
      if (text) next.set(sessionId, text);
      else next.delete(sessionId);
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
      {tab === "chat" && (
        <section
          className={`panel agent-chat ${activeSession ? "has-conversation" : ""} ${railOpen ? "rail-open" : ""}`}
          aria-label="Agent chat workspace"
        >
          <button
            className="text-button mobile-conversation-button"
            aria-expanded={railOpen}
            onClick={() => setRailOpen(!railOpen)}
          >
            Conversations
          </button>
          <aside className="conversation-rail" aria-label="Conversations">
            <div className="rail-heading">
              <h2>Conversations</h2>
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
              {matching.length === 0 && <p>No matching conversations.</p>}
            </div>
          </aside>
          <div
            className={`agent-record-body chat-workspace ${researchOpen ? "with-inspector" : ""}`}
          >
            {activeSession ? (
              <>
                <div className="chat-thread">
                  <h3>{activeSession.name}</h3>
                  <AgentAIReview
                    view={v}
                    sessionId={activeSession.id}
                    composer={{
                      value: message,
                      onChange: (text) => updateMessage(activeSession.id, text),
                      onSave: async () => {
                        if (
                          await agents.change({
                            action: "message-add",
                            id: activeSession.id,
                            text: message,
                          })
                        )
                          updateMessage(activeSession.id, "");
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
                <button
                  className="text-button chat-context-button"
                  onClick={() => setResearchOpen(!researchOpen)}
                >
                  {t("Sources")}
                </button>
                {researchOpen && core.mode === "ready" && (
                  <aside className="chat-review" aria-label="Source inspector">
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
                    ? "No matching conversations"
                    : "Start a conversation"}
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
          <section className="panel" aria-label="Profile settings">
            <div className="panel-heading">
              <h2>Profile settings</h2>
              <Badge>NO AUTHORITY</Badge>
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
                Profile name
                <input
                  required
                  maxLength={80}
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                />
              </label>
              <label>
                Persona
                <textarea
                  maxLength={4000}
                  value={persona}
                  onChange={(e) => setPersona(e.target.value)}
                />
              </label>
              <details className="profile-defaults">
                <summary>Profile defaults</summary>
                <label>
                  Provider preference
                  <select
                    value={provider}
                    onChange={(e) => setProvider(e.target.value as Provider)}
                  >
                    {Object.entries(providerNames).map(([id, title]) => (
                      <option key={id} value={id}>
                        {title}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  Model preference
                  <input
                    maxLength={120}
                    value={model}
                    onChange={(e) => setModel(e.target.value)}
                  />
                </label>
                <p className="subtle">Preferences do not connect a provider.</p>
              </details>
              <button className="primary" disabled={agents.busy}>
                Save profile
              </button>
              <button
                type="button"
                className="text-button"
                onClick={reloadProfile}
              >
                Reload profile fields
              </button>
            </form>
          </section>
          <AIConnectionSettings />
        </div>
      )}
      {tab === "memory" && (
        <section className="panel">
          <div className="panel-heading">
            <h2>Memory</h2>
            <button
              className="secondary"
              onClick={() => {
                resetMemory();
                setMemoryOpen(true);
              }}
            >
              New memory
            </button>
            <Badge>PROFILE ONLY</Badge>
          </div>
          <div className="agent-record-body memory-workspace">
            <div className="memory-library">
              <MemoryRecall
                view={v}
                onOpen={(source) => {
                  setSession(source.sessionId!);
                  setSearch("");
                  navigate("agents/chat");
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
              <form
                aria-label="Memory editor"
                className="memory-editor"
                onSubmit={submit(async () => {
                  if (
                    await agents.change({
                      action: "memory-put",
                      revision: memoryID ? memoryRevision.current : undefined,
                      id: memoryID || undefined,
                      text: memory,
                      source,
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
                <button
                  type="button"
                  className="text-button"
                  onClick={resetMemory}
                >
                  Cancel memory edit
                </button>
                <h3>
                  {memoryRestore
                    ? "Restore a memory"
                    : memoryID
                      ? "Correct a memory"
                      : "Add a memory"}
                </h3>
                {memoryRestore && (
                  <section aria-label="Memory restoration review">
                    <article aria-label="Current memory revision">
                      <h4>Current</h4>
                      <p>{memoryRestore.current.text}</p>
                      <small>
                        {memoryRestore.current.source} · Updated{" "}
                        {date(memoryRestore.current.at)} · Expiry{" "}
                        {date(memoryRestore.current.expiresAt)}
                      </small>
                    </article>
                    <article aria-label="Selected memory revision">
                      <h4>
                        Selected revision {memoryRestore.historyIndex + 1}
                      </h4>
                      <p>{memoryRestore.selected.text}</p>
                      <small>
                        {memoryRestore.selected.source} · Updated{" "}
                        {date(memoryRestore.selected.at)} · Expiry{" "}
                        {date(memoryRestore.selected.expiresAt)}
                      </small>
                    </article>
                    {memoryRestore.selected.expiresAt > 0 &&
                      memoryRestore.selected.expiresAt <= Date.now() / 1000 && (
                        <p role="status">Expired revision</p>
                      )}
                    <section aria-label="Restoration source evidence">
                      <h4>Recorded source</h4>
                      <p>
                        {memoryEvidenceStatuses[memoryRestore.evidence.status]}
                      </p>
                      {memoryRestore.evidence.text && (
                        <blockquote>{memoryRestore.evidence.text}</blockquote>
                      )}
                    </section>
                    {memoryRestore.revision !== v.revision && (
                      <p role="status">
                        Workspace changed. Review the restoration again.
                      </p>
                    )}
                  </section>
                )}
                <label>
                  Memory note
                  <textarea
                    required
                    maxLength={4000}
                    value={memory}
                    onChange={(e) => setMemory(e.target.value)}
                  />
                </label>
                <label>
                  Evidence or source
                  <input
                    required
                    maxLength={300}
                    value={source}
                    onChange={(e) => setSource(e.target.value)}
                    placeholder="e.g. Owner preference, reviewed today"
                  />
                </label>
                <label>
                  Expiry (optional)
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
                <div className="agent-inline">
                  <button
                    className="primary"
                    disabled={
                      agents.busy ||
                      !!(memoryRestore && memoryRestore.revision !== v.revision)
                    }
                  >
                    {memoryRestore
                      ? "Save restored memory"
                      : memoryID
                        ? "Save correction"
                        : "Save memory"}
                  </button>
                  {memoryID && (
                    <button
                      type="button"
                      className="secondary"
                      onClick={resetMemory}
                    >
                      {memoryRestore
                        ? "Cancel restoration"
                        : "Cancel correction"}
                    </button>
                  )}
                </div>
              </form>
            )}
          </div>
        </section>
      )}
      {tab === "bots" && (
        <section className="panel paper-strategies">
          <div className="panel-heading">
            <h2>Paper strategies</h2>
            <Badge>FIXED EXAMPLE INPUTS</Badge>
          </div>
          <div className="agent-split paper-workspace">
            <div className="agent-record-body paper-library">
              <p>
                No exchange, wallet, live price or return calculation is
                connected. These examples exercise budgets and restart handling
                using integer fixture units.
              </p>
              {v.bots.map((b) => (
                <article
                  className="memory-card"
                  key={b.id}
                  aria-label={`Paper strategy ${b.name}`}
                >
                  <strong>{b.name}</strong>
                  <p>
                    {b.strategy} · Quantity {b.quantity} × fixed price{" "}
                    {b.priceCents} · Used {b.spentCents} / {b.budgetCents}{" "}
                    fixture units
                  </p>
                </article>
              ))}
              {!v.bots.length && (
                <p className="subtle">No paper strategies saved.</p>
              )}
            </div>
            <aside
              className="agent-record-body paper-composer"
              aria-label="Create paper strategy"
            >
              <h3>New paper strategy</h3>
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
                  Paper bot name
                  <input
                    required
                    maxLength={80}
                    value={botName}
                    onChange={(e) => setBotName(e.target.value)}
                  />
                </label>
                <div className="agent-grid">
                  <label>
                    Example quantity
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
                    Fixed example price
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
                    Total fixture budget
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
                  Create paper bot
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
