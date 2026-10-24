import { ResearchReadPanel, type ResearchSelection } from "./ResearchReadPanel";
import { MemoryRecall, memoryEvidenceStatuses } from "./MemoryRecall";
import { useCore } from "../core";
import { NewsInputs, NewsSourcePicker } from "./NewsInputs";
import { AIConnectionSettings } from "./AIConnection";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { Plus, RefreshCw } from "../icons";
import { Badge, SectionHead } from "../components/shared";
import { useWorkspace } from "../workspace";
import { providerNames, type Provider } from "../state";
import type {
  AgentMemory,
  AgentMemoryRestoreDraft,
  AgentView,
} from "../agents";
import "../agents.css";
import { AgentPlaybooks } from "./AgentPlaybooks";
import { ScheduledAITasks } from "./ScheduledAITasks";
import { SavedReminderDraft } from "./SavedReminderDraft";
import { AgentAIReview } from "./AgentAIReview";

const date = (at: number) =>
  at ? new Date(at * 1000).toLocaleString() : "No expiry";
export function LocalAgents() {
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
                Agent
                <select
                  aria-label="Agent"
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
            <button className="secondary" onClick={() => setProfileOpen(true)}>
              <Plus size={16} /> New profile
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
          <div className="tabs" aria-label="Agent sections">
            {["chat", "bots", "tasks", "news", "memory", "settings"].map(
              (t) => (
                <button
                  key={t}
                  className={tab === t ? "active" : ""}
                  aria-current={tab === t ? "page" : undefined}
                  onClick={() => navigate(`agents/${t}`)}
                >
                  {t}
                </button>
              ),
            )}
          </div>
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
  const core = useCore();
  const [researchOpen, setResearchOpen] = useState(false);
  const [newConversation, setNewConversation] = useState(false);
  const [railOpen, setRailOpen] = useState(false);
  const [memoryOpen, setMemoryOpen] = useState(false);
  const [taskOpen, setTaskOpen] = useState(false);
  const [resultId, setResultId] = useState("");
  const [researchSelection, setResearchSelection] =
    useState<ResearchSelection>();
  const [newsSourceId, setNewsSourceId] = useState("");
  const [newsAge, setNewsAge] = useState(3600);
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
  const [taskName, setTaskName] = useState("");
  const [taskText, setTaskText] = useState("");
  const [bot, setBot] = useState("");
  const [interval, setInterval] = useState(60);
  const [maxRuns, setMaxRuns] = useState(1);
  const [reminderSource, setReminderSource] = useState("");
  const [botName, setBotName] = useState("");
  const [quantity, setQuantity] = useState(1);
  const [price, setPrice] = useState(100);
  const [budget, setBudget] = useState(500);
  const profileRevision = useRef(v.revision);
  const memoryRevision = useRef(v.revision);
  const runCards = useRef(new Map<string, HTMLElement>());
  function showRun(id: string) {
    setResultId(id);
    setTaskPanel("history");
    requestAnimationFrame(() => {
      const card = runCards.current.get(id);
      if (card?.isConnected) {
        card.scrollIntoView({ block: "center" });
        card.focus({ preventScroll: true });
      }
    });
  }
  const taskNames = new Map(
    v.tasks
      .filter((t) => t.profileId === v.profileId)
      .map((t) => [t.id, t.name]),
  );
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
                aria-label="New conversation"
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
                  Conversation name
                  <input
                    required
                    maxLength={80}
                    value={sessionName}
                    onChange={(e) => setSessionName(e.target.value)}
                  />
                </label>
                <button className="secondary" disabled={agents.busy}>
                  Create conversation
                </button>
              </form>
            )}
            <label>
              Search conversations
              <input
                type="search"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="Search conversations"
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
                  Sources
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
                <h3>No conversation selected</h3>
                <p>Create a conversation to start.</p>
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
              <p className="subtle">
                Preferences only. No API key, subscription or provider
                connection is configured here. Changing providers preserves
                conversations and memory. Personas cannot grant tools or wallet
                access.
              </p>
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
        <>
          <nav className="task-views tabs" aria-label="Task views">
            {["automations", "playbooks", "history", "inbox"].map((t) => (
              <button
                key={t}
                aria-pressed={taskPanel === t}
                onClick={() => {
                  setTaskPanel(t);
                  if (t === "history") setResultId("");
                }}
              >
                {t}
              </button>
            ))}
            <button
              className="secondary"
              onClick={() => {
                setTaskPanel("automations");
                setTaskOpen(true);
              }}
            >
              New task
            </button>
            <button
              className="text-button"
              onClick={() => void agents.refresh().catch(() => {})}
            >
              <RefreshCw size={14} /> Refresh
            </button>
          </nav>
          <div hidden={taskPanel !== "playbooks"}>
            <AgentPlaybooks key={v.profileId} view={v} />
          </div>
          <section
            className="panel task-board"
            hidden={taskPanel !== "automations"}
          >
            <div className="panel-heading">
              <h2>Automations</h2>
            </div>
            <div className="agent-record-body task-workspace">
              <div className="task-list">
                <details className="schedule-behavior">
                  <summary>Schedule behavior</summary>
                  <p>
                    Runs while this core is running, even with the browser
                    closed. Missed intervals coalesce into one evaluation. Pause
                    prevents new evaluations; resume schedules the next
                    interval.
                  </p>
                </details>
                <div className="table-scroll">
                  <table className="task-table" aria-label="Automations">
                    <thead>
                      <tr>
                        <th>Name</th>
                        <th>Status</th>
                        <th>Runs</th>
                        <th>Next run</th>
                        <th>Actions</th>
                      </tr>
                    </thead>
                    <tbody>
                      {v.tasks
                        .filter((t) => !t.wen)
                        .map((t) => {
                          const last = v.runs
                            .filter((r) => r.taskId === t.id)
                            .at(-1);
                          return (
                            <tr key={t.id}>
                              <td>
                                <strong>{t.name}</strong>
                                <small>
                                  {t.ai
                                    ? "Fixture AI"
                                    : t.botId
                                      ? "Paper evaluation"
                                      : "Reminder"}
                                </small>
                              </td>
                              <td>
                                <span className={`task-state ${t.state}`}>
                                  {t.state}
                                </span>
                              </td>
                              <td>
                                {t.runs} / {t.maxRuns}
                              </td>
                              <td>
                                {t.state === "scheduled" ? date(t.nextAt) : "—"}
                              </td>
                              <td>
                                <div className="row-actions">
                                  {!t.ai && (
                                    <button
                                      className="text-button"
                                      disabled={
                                        agents.busy || t.state !== "scheduled"
                                      }
                                      aria-label={`Run now: ${t.name}`}
                                      onClick={() =>
                                        void agents.change({
                                          action: "task-run",
                                          id: t.id,
                                        })
                                      }
                                    >
                                      Run
                                    </button>
                                  )}
                                  <button
                                    className="text-button"
                                    disabled={
                                      agents.busy ||
                                      t.state === "completed" ||
                                      t.runs >= t.maxRuns
                                    }
                                    aria-label={`${t.state === "paused" || t.state === "held" ? "Resume" : "Pause"}: ${t.name}`}
                                    onClick={() =>
                                      void agents.change({
                                        action:
                                          t.state === "paused" ||
                                          t.state === "held"
                                            ? "task-resume"
                                            : "task-pause",
                                        id: t.id,
                                      })
                                    }
                                  >
                                    {t.state === "paused" || t.state === "held"
                                      ? "Resume"
                                      : "Pause"}
                                  </button>
                                  {!t.news && !t.botId && !t.ai && (
                                    <button
                                      className="text-button"
                                      aria-label={`Reuse: ${t.name}`}
                                      onClick={() => setReminderSource(t.id)}
                                    >
                                      Reuse
                                    </button>
                                  )}
                                  <button
                                    className="text-button"
                                    aria-label={`Result: ${t.name}`}
                                    disabled={!last}
                                    onClick={() => {
                                      if (last) {
                                        setResultId(last.id);
                                        setTaskPanel("history");
                                      }
                                    }}
                                  >
                                    Result
                                  </button>
                                </div>
                              </td>
                            </tr>
                          );
                        })}
                    </tbody>
                  </table>
                </div>
                {!v.tasks.some((t) => !t.wen) && (
                  <p className="empty-row">No automations yet.</p>
                )}
                {reminderSource && (
                  <SavedReminderDraft
                    key={reminderSource}
                    view={v}
                    sourceID={reminderSource}
                    close={() => setReminderSource("")}
                  />
                )}
              </div>
              {taskOpen && (
                <form
                  className="task-composer"
                  onSubmit={submit(async () => {
                    if (
                      await agents.change({
                        action: "task-create",
                        newsSourceId,
                        newsMaxAgeSeconds: newsSourceId ? newsAge : undefined,
                        name: taskName,
                        text: taskText,
                        botId: bot,
                        intervalSeconds: interval,
                        maxRuns,
                      })
                    ) {
                      setTaskOpen(false);
                      setTaskName("");
                      setTaskText("");
                    }
                  })}
                >
                  <div className="panel-heading">
                    <h3>Create a task</h3>
                    <button
                      type="button"
                      className="text-button"
                      onClick={() => setTaskOpen(false)}
                    >
                      Cancel task
                    </button>
                  </div>
                  <NewsSourcePicker
                    view={v}
                    value={newsSourceId}
                    onChange={setNewsSourceId}
                    age={newsAge}
                    onAge={setNewsAge}
                  />
                  <label>
                    Task name
                    <input
                      required
                      maxLength={80}
                      value={taskName}
                      onChange={(e) => setTaskName(e.target.value)}
                    />
                  </label>
                  <label>
                    Task kind
                    <select
                      value={bot}
                      onChange={(e) => setBot(e.target.value)}
                    >
                      <option value="">Local reminder</option>
                      {v.bots.map((b) => (
                        <option key={b.id} value={b.id}>
                          Paper evaluation · {b.name}
                        </option>
                      ))}
                    </select>
                  </label>
                  {!bot && (
                    <label>
                      Reminder text
                      <textarea
                        required
                        maxLength={4000}
                        value={taskText}
                        onChange={(e) => setTaskText(e.target.value)}
                      />
                    </label>
                  )}
                  <div className="agent-grid">
                    <label>
                      Interval in seconds
                      <input
                        type="number"
                        min={60}
                        max={604800}
                        required
                        value={interval}
                        onChange={(e) => setInterval(Number(e.target.value))}
                      />
                    </label>
                    <label>
                      Maximum evaluations
                      <input
                        type="number"
                        min={1}
                        max={20}
                        required
                        value={maxRuns}
                        onChange={(e) => setMaxRuns(Number(e.target.value))}
                      />
                    </label>
                  </div>
                  <button className="primary" disabled={agents.busy}>
                    Create task
                  </button>
                </form>
              )}
            </div>
          </section>
          <div className="task-results">
            <section
              className="panel"
              aria-label="Run results"
              hidden={taskPanel !== "history"}
            >
              <div className="panel-heading">
                <h2>Execution journal</h2>
                <Badge>PAPER / REMINDERS</Badge>
              </div>
              <div className="agent-record-body">
                {v.runs.length === 0 ? (
                  <p>No evaluations yet.</p>
                ) : (
                  v.runs
                    .slice()
                    .reverse()
                    .filter((r) => !resultId || r.id === resultId)
                    .map((r) => (
                      <article
                        className="memory-card task-run"
                        key={r.id}
                        tabIndex={-1}
                        aria-label={`Run ${r.id}`}
                        ref={(node) => {
                          if (node) runCards.current.set(r.id, node);
                          else runCards.current.delete(r.id);
                        }}
                      >
                        <div className="panel-heading">
                          <strong>
                            {(r.profileId === v.profileId &&
                              taskNames.get(r.taskId)) ||
                              "Task unavailable"}
                          </strong>
                          <Badge>{r.outcome}</Badge>
                        </div>
                        <p>
                          {r.strategy} · {date(r.at)}
                        </p>
                        {r.wen && (
                          <p>
                            Unsigned WEN paper receipt · unsettled · no rewards
                            or claims. Receipt SHA-256:{" "}
                            <code className="wrap-anywhere">
                              {r.wen.receiptSha256}
                            </code>
                          </p>
                        )}
                        {r.botId && (
                          <p>
                            Inputs: quantity {r.quantity}, fixed price{" "}
                            {r.priceCents}, cost {r.costCents}, budget before{" "}
                            {r.budgetBefore}
                          </p>
                        )}
                        {r.news && (
                          <p className="news-provenance">
                            News snapshot {r.news.fetchId} · source {r.news.url}{" "}
                            · retrieved {date(r.news.fetchedAt)} · content{" "}
                            {r.news.bodySha256}
                          </p>
                        )}
                        <small>Run {r.id}</small>
                        {!r.wen &&
                          !r.botId &&
                          [
                            "reminder-v1",
                            "ai-step-v1/reminder-v1",
                            "ai-step-v1",
                          ].includes(r.strategy) && (
                            <button
                              className="secondary"
                              disabled={agents.busy}
                              onClick={async () => {
                                if (
                                  await agents.change({
                                    action: "task-run-explain",
                                    id: r.id,
                                  })
                                ) {
                                  setSession("");
                                  setSearch("");
                                  navigate("agents/chat");
                                }
                              }}
                            >
                              Discuss run
                            </button>
                          )}
                      </article>
                    ))
                )}
              </div>
            </section>
            <section className="panel" hidden={taskPanel !== "inbox"}>
              <div className="panel-heading">
                <h2>Task inbox</h2>
                <Badge>DURABLE</Badge>
              </div>
              <div className="agent-record-body">
                {v.notices.length === 0 ? (
                  <p>No task notifications.</p>
                ) : (
                  v.notices
                    .slice()
                    .reverse()
                    .map((n) => {
                      const canView =
                        n.profileId === v.profileId &&
                        v.runs.some(
                          (r) =>
                            r.id === n.runId && r.profileId === v.profileId,
                        );
                      return (
                        <article
                          className="memory-card"
                          key={n.id}
                          aria-label={`Task notification ${n.id}`}
                        >
                          <p>{n.text}</p>
                          <small>{date(n.at)}</small>
                          <button
                            className="secondary"
                            disabled={agents.busy || !canView}
                            onClick={() => {
                              if (!canView) return;
                              showRun(n.runId);
                            }}
                          >
                            {canView ? "View result" : "Result unavailable"}
                          </button>
                          {!n.read ? (
                            <button
                              className="text-button"
                              disabled={agents.busy}
                              onClick={() =>
                                void agents.change({
                                  action: "notice-read",
                                  id: n.id,
                                })
                              }
                            >
                              Mark read
                            </button>
                          ) : (
                            <small>Read</small>
                          )}
                        </article>
                      );
                    })
                )}
              </div>
            </section>
          </div>
          <details className="scheduled-ai-disclosure">
            <summary>Advanced AI schedules · fixtures</summary>
            <ScheduledAITasks view={v} />
          </details>
        </>
      )}
    </div>
  );
}
