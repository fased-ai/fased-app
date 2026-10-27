import { useUI } from "../ui-preferences";
import {
  ArrowRight,
  ArrowUpRight,
  BookOpen,
  Check,
  FlaskConical,
  Newspaper,
  Pause,
  Play,
  Plus,
  Shield,
  SlidersHorizontal,
  Sparkles,
  Trash2,
} from "../icons";
import { providerNames, type Provider } from "../state";
import { Badge, Avatar, SectionHead, Empty } from "../components/shared";
import { useWorkspace } from "../workspace";
export function Agents() {
  const { t: uiText } = useUI();
  const {
    state,
    dispatch,
    setActionOpen,
    setProfileOpen,
    messages,
    setMessages,
    message,
    setMessage,
    memoryText,
    setMemoryText,
    profile,
    tab,
    navigate,
    notify,
    sendChat,
  } = useWorkspace();
  return (
    <>
      <SectionHead
        title={uiText("Agents")}
        action={
          <button className="secondary" onClick={() => setProfileOpen(true)}>
            <Plus size={16} />
            {uiText(" New agent")}
          </button>
        }
      />
      <div className="agent-picker">
        {state.profiles.map((p) => (
          <button
            className={p.id === profile.id ? "selected" : ""}
            key={p.id}
            aria-pressed={p.id === profile.id}
            onClick={() => dispatch({ type: "profile", id: p.id })}
          >
            <Avatar profile={p} small />
            <span>
              <strong>{p.name}</strong>
              <small>{p.role}</small>
            </span>
            {p.id === profile.id && <Check size={15} />}
          </button>
        ))}
      </div>
      <div className="tabs" aria-label={uiText("Agent sections")}>
        {["chat", "bots", "tasks", "memory", "settings"].map((t) => (
          <button
            key={t}
            className={tab === t ? "active" : ""}
            aria-current={tab === t ? "page" : undefined}
            onClick={() => navigate(`agents/${t}`)}
          >
            {t}
          </button>
        ))}
      </div>
      {tab === "chat" && (
        <section className="chat-panel panel">
          <div className="panel-heading">
            <div className="flex">
              <Avatar profile={profile} small />
              <div>
                <h2>{profile.name}</h2>
                <small className="subtle">
                  {providerNames[profile.provider]}
                  {uiText(" · prototype conversation")}
                </small>
              </div>
            </div>
            <button
              className="text-button"
              onClick={() => {
                setMessages([]);
                notify("Prototype conversation cleared");
              }}
            >
              {uiText("New conversation ")}
              <Plus size={14} />
            </button>
          </div>
          <div className="chat-messages" aria-live="polite">
            {messages.length === 0 ? (
              <div className="chat-welcome">
                <span className="agent-symbol">
                  <Sparkles size={28} />
                </span>
                <p className="eyebrow">{uiText("A THOUGHTFUL START")}</p>
                <h2>{uiText("What would you like to work through?")}</h2>
                <p>
                  {uiText(
                    "Explore the conversation flow. Responses below are scripted examples, not AI output.",
                  )}
                </p>
                <div className="suggestions">
                  {[
                    "Explain the wallet modes",
                    "Draft a bounded strategy",
                    "Compare mining allocations",
                  ].map((text) => (
                    <button key={text} onClick={() => setMessage(text)}>
                      {text}
                      <ArrowUpRight size={14} />
                    </button>
                  ))}
                </div>
              </div>
            ) : (
              messages.map((m, i) => (
                <div className={`message ${m.role}`} key={`${profile.id}-${i}`}>
                  <span className="message-author">
                    {m.role === "you"
                      ? uiText("You")
                      : `${profile.name} · scripted preview`}
                  </span>
                  <p>{m.text}</p>
                  {m.role === "agent" && (
                    <button
                      className="text-button"
                      onClick={() => setActionOpen(true)}
                    >
                      {uiText("Preview a review card ")}
                      <ArrowRight size={13} />
                    </button>
                  )}
                </div>
              ))
            )}
          </div>
          <form className="chat-composer" onSubmit={sendChat}>
            <label className="sr-only" htmlFor="chat-message">
              {uiText("Message your agent")}
            </label>
            <textarea
              id="chat-message"
              placeholder={uiText(
                "Describe a goal, ask for context, or draft a task…",
              )}
              maxLength={1000}
              value={message}
              onChange={(e) => setMessage(e.target.value)}
            />
            <div>
              <span>
                <FlaskConical size={13} />
                {uiText(" Scripted prototype · no provider requests")}
              </span>
              <button
                className="primary icon-button"
                disabled={!message.trim()}
                aria-label={uiText("Send prototype message")}
              >
                <ArrowUpRight size={17} />
              </button>
            </div>
          </form>
        </section>
      )}
      {tab === "bots" && (
        <>
          <div className="info-banner">
            <FlaskConical size={18} />
            <div>
              <strong>{uiText("Paper behavior first.")}</strong>
              <span>
                {uiText(
                  "This sample bot changes local prototype state only. No strategy engine or financial adapter is running.",
                )}
              </span>
            </div>
          </div>
          <section className="panel bot-card">
            <div className="flex">
              <span className="feature-icon lime">
                <SlidersHorizontal size={22} />
              </span>
              <div>
                <h2>{uiText("Allocation review")}</h2>
                <p className="subtle">
                  {uiText("Example strategy · version 1 · ")}
                  {profile.name}
                </p>
              </div>
              <Badge tone={state.paused ? "muted" : "lime"}>
                {state.paused
                  ? uiText("PAUSED · DEMO")
                  : uiText("RUNNING · DEMO")}
              </Badge>
            </div>
            <div className="detail-grid">
              <div>
                <small>{uiText("Execution")}</small>
                <strong>{uiText("Simulated preview")}</strong>
              </div>
              <div>
                <small>{uiText("Capital")}</small>
                <strong>{uiText("Not assigned")}</strong>
              </div>
              <div>
                <small>{uiText("Next action")}</small>
                <strong>
                  {state.paused
                    ? uiText("Owner resumes demo")
                    : uiText("Awaiting simulated signal")}
                </strong>
              </div>
            </div>
            <div className="panel-footer">
              <span className="subtle">
                {uiText("No permissions or wallet assigned.")}
              </span>
              <div className="flex">
                <button
                  className="secondary"
                  onClick={() => setActionOpen(true)}
                >
                  {uiText("Inspect strategy")}
                </button>
                <button
                  className="primary"
                  onClick={() => {
                    dispatch({ type: "pause", paused: !state.paused });
                    notify(
                      state.paused ? "Demo bot resumed" : "Demo bot paused",
                    );
                  }}
                >
                  {state.paused ? <Play size={14} /> : <Pause size={14} />}{" "}
                  {state.paused ? uiText("Resume demo") : uiText("Pause demo")}
                </button>
              </div>
            </div>
          </section>
        </>
      )}
      {tab === "tasks" && (
        <section className="panel">
          <div className="panel-heading">
            <h2>{uiText("Task previews")}</h2>
            <Badge>{uiText("NO SCHEDULER CONNECTED")}</Badge>
          </div>
          {[
            [
              "Morning research brief",
              "News sources → structured summary → owner review",
              Newspaper,
            ],
            [
              "Review strategy limits",
              "Read configuration → compare declared limits → explain",
              Shield,
            ],
          ].map(([title, text, Icon]) => {
            const I = Icon as typeof Newspaper;
            return (
              <div className="task-row" key={String(title)}>
                <I size={20} />
                <div>
                  <strong>{String(title)}</strong>
                  <p>{String(text)}</p>
                </div>
                <button
                  className="secondary"
                  onClick={() =>
                    notify(
                      "Task preview recorded",
                      `${String(title)} was simulated. No schedule or API request was created.`,
                    )
                  }
                >
                  {uiText("Preview run ")}
                  <Play size={13} />
                </button>
              </div>
            );
          })}
        </section>
      )}
      {tab === "memory" && (
        <>
          <div className="info-banner">
            <BookOpen size={18} />
            <div>
              <strong>{uiText("Memory you can inspect.")}</strong>
              <span>
                {uiText("Notes are scoped to ")}
                {profile.name}
                {uiText(
                  ". They do not grant wallet authority or replace financial records.",
                )}
              </span>
            </div>
          </div>
          <section className="panel">
            <div className="panel-heading">
              <h2>{uiText("Saved context")}</h2>
              <Badge>{uiText("LOCAL PROTOTYPE")}</Badge>
            </div>
            {state.memories
              .filter((m) => m.profileId === profile.id)
              .map((m) => (
                <article className="memory-card" key={m.id}>
                  <div>
                    <p>{m.text}</p>
                    <small>
                      {m.source} <span>·</span> {m.verified}
                    </small>
                  </div>
                  <button
                    className="icon-button"
                    aria-label={`Delete memory: ${m.text}`}
                    onClick={() =>
                      dispatch({ type: "memory-delete", id: m.id })
                    }
                  >
                    <Trash2 size={16} />
                  </button>
                </article>
              ))}
            {!state.memories.some((m) => m.profileId === profile.id) && (
              <Empty
                icon={<BookOpen size={24} />}
                title={uiText("A clean context")}
              >
                {uiText(
                  "Add a non-sensitive draft preference for this profile.",
                )}
              </Empty>
            )}
            <form
              className="memory-form"
              onSubmit={(e) => {
                e.preventDefault();
                if (!memoryText.trim()) return;
                dispatch({
                  type: "memory-add",
                  text: memoryText,
                  id: crypto.randomUUID(),
                });
                setMemoryText("");
                notify("Prototype memory saved");
              }}
            >
              <label htmlFor="memory-note">{uiText("Add a draft note")}</label>
              <textarea
                id="memory-note"
                value={memoryText}
                maxLength={500}
                placeholder={uiText(
                  "A preference or workflow note. Do not enter credentials.",
                )}
                onChange={(e) => setMemoryText(e.target.value)}
              />
              <button className="secondary" disabled={!memoryText.trim()}>
                <Plus size={14} />
                {uiText(" Save note")}
              </button>
            </form>
          </section>
        </>
      )}
      {tab === "settings" && (
        <section className="panel">
          <div className="panel-heading">
            <h2>{uiText("Profile preferences")}</h2>
            <Badge>{uiText("NO AUTHORITY CHANGES")}</Badge>
          </div>
          <div className="settings-body">
            <label htmlFor="profile-provider">{uiText("Provider")}</label>
            <select
              id="profile-provider"
              value={profile.provider}
              onChange={(e) =>
                dispatch({
                  type: "provider",
                  provider: e.target.value as Provider,
                })
              }
            >
              {Object.entries(providerNames).map(([id, name]) => (
                <option key={id} value={id}>
                  {name}
                  {id !== "none" ? uiText(" · not connected") : ""}
                </option>
              ))}
            </select>
            <p className="subtle">
              {uiText(
                "A preference only. No authentication or model request is performed. Other profiles keep their own selection.",
              )}
            </p>
            <div className="inline-detail">
              <span>{uiText("Wallet permissions")}</span>
              <strong>{uiText("None assigned")}</strong>
              <button
                className="text-button"
                onClick={() => navigate("wallets/permissions")}
              >
                {uiText("Inspect policy model ")}
                <ArrowRight size={14} />
              </button>
            </div>
          </div>
        </section>
      )}
    </>
  );
}
