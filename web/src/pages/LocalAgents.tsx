import { MemoryRecall } from "./MemoryRecall";
import { ChatMemoryReview } from "./ChatMemoryReview";
import { NewsInputs, NewsSourcePicker } from "./NewsInputs";
import { AIConnectionSettings } from "./AIConnection";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { Plus, RefreshCw } from "../icons";
import { Badge, SectionHead } from "../components/shared";
import { useWorkspace } from "../workspace";
import { providerNames, type Provider } from "../state";
import type { AgentView } from "../agents";
import "../agents.css";
import { ScheduledAITasks } from "./ScheduledAITasks";
import { AgentAIReview } from "./AgentAIReview";

const date = (at: number) =>
  at ? new Date(at * 1000).toLocaleString() : "No expiry";
export function LocalAgents() {
  const { agents, setProfileOpen, tab, navigate } = useWorkspace();
  return (
    <>
      <SectionHead
        eyebrow="YOUR LOCAL WORKSPACE"
        title="Agents"
        description="Profiles, conversations and bounded tasks saved to this instance."
        action={
          <button className="secondary" onClick={() => setProfileOpen(true)}>
            <Plus size={16} /> New profile
          </button>
        }
      />
      <p className="prototype-caption">
        Local prototype · AI providers are not connected · Paper results use
        fixed example prices · Real wallets remain disabled.
      </p>
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
          <div className="agent-picker local-profile-picker">
            {agents.view.profiles.map((p) => (
              <button
                key={p.id}
                disabled={agents.busy}
                className={p.id === agents.view!.profileId ? "selected" : ""}
                aria-pressed={p.id === agents.view!.profileId}
                onClick={() => void agents.select(p.id)}
              >
                <span>
                  <strong>{p.name}</strong>
                  <small>{providerNames[p.provider]}</small>
                </span>
              </button>
            ))}
          </div>
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
          <LocalAgentEditor key={agents.view.profileId} view={agents.view} />
        </>
      )}
    </>
  );
}
function LocalAgentEditor({ view: v }: { view: AgentView }) {
  const { agents, tab, navigate } = useWorkspace();
  const [newsSourceId, setNewsSourceId] = useState("");
  const [newsAge, setNewsAge] = useState(3600);
  const profile = v.profiles.find((p) => p.id === v.profileId)!;
  const [session, setSession] = useState("");
  const [sessionName, setSessionName] = useState("");
  const [search, setSearch] = useState("");
  const [message, setMessage] = useState("");
  const [memoryID, setMemoryID] = useState("");
  const [memory, setMemory] = useState("");
  const [source, setSource] = useState("");
  const [expiry, setExpiry] = useState("");
  const [name, setName] = useState(profile.name);
  const [persona, setPersona] = useState(profile.persona);
  const [provider, setProvider] = useState(profile.provider);
  const [model, setModel] = useState(profile.model);
  const [taskName, setTaskName] = useState("");
  const [taskText, setTaskText] = useState("");
  const [bot, setBot] = useState("");
  const [interval, setInterval] = useState(60);
  const [maxRuns, setMaxRuns] = useState(1);
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
  const activeSession =
    v.sessions.find((s) => s.id === session) ?? v.sessions.at(-1);
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
  function resetMemory() {
    setMemoryID("");
    setMemory("");
    setSource("");
    setExpiry("");
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
      {tab === "settings" && <AIConnectionSettings />}
      {tab === "chat" && (
        <section className="panel">
          <div className="panel-heading">
            <h2>{profile.name} · Conversations</h2>
            <Badge>LOCAL NOTES</Badge>
          </div>
          <div className="agent-record-body">
            <form
              className="agent-inline"
              onSubmit={submit(async () => {
                if (
                  await agents.change({
                    action: "session-create",
                    name: sessionName,
                  })
                ) {
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
            <label>
              Search conversations
              <input
                type="search"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="Title, message or completed AI question/answer"
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
                  onClick={() => setSession(s.id)}
                >
                  {s.name}
                </button>
              ))}
              {matching.length === 0 && <p>No matching conversations.</p>}
            </div>
            {activeSession && (
              <>
                <h3>{activeSession.name}</h3>
                <div className="local-messages" aria-live="polite">
                  {v.messages
                    .filter((m) => m.sessionId === activeSession.id)
                    .map((m) => (
                      <article key={m.id}>
                        <small>
                          {m.role === "user" ? "You" : "Local system"} ·{" "}
                          {date(m.createdAt)}
                        </small>
                        <p>{m.text}</p>
                        {m.role === "user" && (
                          <ChatMemoryReview
                            key={`${v.profileId}:${m.id}`}
                            view={v}
                            sourceId={m.id}
                            sourceKind="message"
                            sourceText={m.text}
                            sessionId={m.sessionId}
                          />
                        )}
                      </article>
                    ))}
                </div>
                <form
                  onSubmit={submit(async () => {
                    if (
                      await agents.change({
                        action: "message-add",
                        id: activeSession.id,
                        text: message,
                      })
                    )
                      setMessage("");
                  })}
                >
                  <label>
                    Message
                    <textarea
                      required
                      maxLength={4000}
                      value={message}
                      onChange={(e) => setMessage(e.target.value)}
                      placeholder="Save context for this profile"
                    />
                  </label>
                  <p className="subtle">
                    Saved to your core. No model response is generated or
                    billed.
                  </p>
                  <button
                    className="primary"
                    disabled={agents.busy || !message.trim()}
                  >
                    Save message
                  </button>
                </form>
                <AgentAIReview view={v} sessionId={activeSession.id} />
              </>
            )}
          </div>
        </section>
      )}
      {tab === "settings" && (
        <section className="panel">
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
              Preferences only. No API key, subscription or provider connection
              is configured here. Changing providers preserves conversations and
              memory. Personas cannot grant tools or wallet access.
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
      )}
      {tab === "memory" && (
        <section className="panel">
          <div className="panel-heading">
            <h2>Memory and corrections</h2>
            <Badge>PROFILE ONLY</Badge>
          </div>
          <div className="agent-record-body">
            <p className="subtle">
              Owner-authored notes with evidence and expiry. These are not
              trading facts, permissions or model training.
            </p>
            <MemoryRecall
              view={v}
              onOpen={(source) => {
                setSession(source.sessionId!);
                setSearch("");
                navigate("agents/chat");
              }}
              onCorrect={(m) => {
                memoryRevision.current = v.revision;
                setMemoryID(m.id);
                setMemory(m.text);
                setSource(m.source);
                setExpiry(
                  m.expiresAt
                    ? new Date(
                        m.expiresAt * 1000 -
                          new Date(m.expiresAt * 1000).getTimezoneOffset() *
                            60000,
                      )
                        .toISOString()
                        .slice(0, 16)
                    : "",
                );
              }}
            />
            <form
              onSubmit={submit(async () => {
                if (
                  await agents.change({
                    action: "memory-put",
                    revision: memoryID ? memoryRevision.current : undefined,
                    id: memoryID || undefined,
                    text: memory,
                    source,
                    expiresAt: expiry
                      ? Math.floor(new Date(expiry).getTime() / 1000)
                      : 0,
                  })
                )
                  resetMemory();
              })}
            >
              <h3>{memoryID ? "Correct a memory" : "Add a memory"}</h3>
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
                  value={expiry}
                  onChange={(e) => setExpiry(e.target.value)}
                />
              </label>
              <div className="agent-inline">
                <button className="primary" disabled={agents.busy}>
                  {memoryID ? "Save correction" : "Save memory"}
                </button>
                {memoryID && (
                  <button
                    type="button"
                    className="secondary"
                    onClick={resetMemory}
                  >
                    Cancel correction
                  </button>
                )}
              </div>
            </form>
          </div>
        </section>
      )}
      {tab === "bots" && (
        <section className="panel">
          <div className="panel-heading">
            <h2>Paper strategies</h2>
            <Badge>FIXED EXAMPLE INPUTS</Badge>
          </div>
          <div className="agent-record-body">
            <p>
              No exchange, wallet, live price or return calculation is
              connected. These examples exercise budgets and restart handling
              using integer fixture units.
            </p>
            {v.bots.map((b) => (
              <article className="memory-card" key={b.id}>
                <strong>{b.name}</strong>
                <p>
                  {b.strategy} · Quantity {b.quantity} × fixed price{" "}
                  {b.priceCents} · Used {b.spentCents} / {b.budgetCents} fixture
                  units
                </p>
              </article>
            ))}
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
            </form>
          </div>
        </section>
      )}
      {tab === "tasks" && (
        <>
          <section className="panel">
            <div className="panel-heading">
              <h2>Bounded tasks</h2>
              <button
                className="text-button"
                onClick={() => void agents.refresh().catch(() => {})}
              >
                <RefreshCw size={14} /> Refresh
              </button>
            </div>
            <div className="agent-record-body">
              <p className="subtle">
                Runs while this core is running, even with the browser closed.
                Missed intervals coalesce into one evaluation. Pause prevents
                new evaluations; resume schedules the next interval.
              </p>
              {v.tasks.some((t) => t.wen) && (
                <p>
                  WEN paper tasks are managed in the{" "}
                  <a href="#terminals/satcoin">Satcoin Mining terminal</a>.
                </p>
              )}
              {v.tasks
                .filter((t) => !t.ai && !t.wen)
                .map((t) => (
                  <article className="memory-card" key={t.id}>
                    <strong>{t.name}</strong>
                    {t.news && (
                      <p>
                        News input: {t.news.url} · maximum age{" "}
                        {t.news.maxAgeSeconds}s. Failure holds this task.
                      </p>
                    )}
                    <p>
                      {t.state} · {t.runs}/{t.maxRuns} evaluations ·{" "}
                      {t.state === "scheduled"
                        ? `Next ${date(t.nextAt)}`
                        : "No evaluation scheduled"}
                    </p>
                    <div className="agent-inline">
                      <button
                        className="secondary"
                        disabled={agents.busy || t.state !== "scheduled"}
                        onClick={() =>
                          void agents.change({ action: "task-run", id: t.id })
                        }
                      >
                        Run now: {t.name}
                      </button>
                      <button
                        className="text-button"
                        disabled={
                          agents.busy ||
                          t.state === "completed" ||
                          t.runs >= t.maxRuns
                        }
                        onClick={() =>
                          void agents.change({
                            action:
                              t.state === "paused" || t.state === "held"
                                ? "task-resume"
                                : "task-pause",
                            id: t.id,
                          })
                        }
                      >
                        {t.state === "paused" || t.state === "held"
                          ? "Resume"
                          : "Pause"}
                        : {t.name}
                      </button>
                    </div>
                  </article>
                ))}
              <form
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
                    setTaskName("");
                    setTaskText("");
                  }
                })}
              >
                <h3>Create a task</h3>
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
                  <select value={bot} onChange={(e) => setBot(e.target.value)}>
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
            </div>
          </section>
          <ScheduledAITasks view={v} />
          <section className="panel">
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
                  .map((r) => (
                    <article className="memory-card" key={r.id}>
                      <strong>{r.outcome}</strong>
                      <p>
                        {r.strategy} · {date(r.at)}
                      </p>
                      {r.wen && (
                        <p>
                          Unsigned WEN paper receipt · unsettled · no rewards or
                          claims. Receipt SHA-256:{" "}
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
                          News snapshot {r.news.fetchId} · source {r.news.url} ·
                          retrieved {date(r.news.fetchedAt)} · content{" "}
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
          <section className="panel">
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
                  .map((n) => (
                    <article className="memory-card" key={n.id}>
                      <p>{n.text}</p>
                      <small>{date(n.at)}</small>
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
                  ))
              )}
            </div>
          </section>
        </>
      )}
    </div>
  );
}
