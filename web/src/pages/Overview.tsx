import {
  ArrowRight,
  ArrowUpRight,
  Bot,
  Check,
  ChevronRight,
  Shield,
  ShieldCheck,
  Wallet,
} from "../icons";
import { Badge, Avatar, SectionHead } from "../components/shared";
import { terminalData } from "../fixtures";
import { useWorkspace } from "../workspace";
export function Overview() {
  const { state, dispatch, setSetupOpen, navigate } = useWorkspace();
  const steps = [
    {
      title: "Make this workspace yours",
      detail: "Choose local or VPS, with a clear setup path.",
      done: state.setup.complete,
      action: () => setSetupOpen(true),
    },
    {
      title: "Choose how your wallet works",
      detail: "Watch-only, external approval, or bounded automation.",
      done: state.setup.complete && state.setup.walletMode !== "skip",
      action: () => navigate("wallets/overview"),
    },
    {
      title: "Give your agent a focus",
      detail: "Pick a profile. AI is optional and permissions stay explicit.",
      done: false,
      action: () => navigate("agents/chat"),
    },
  ];
  return (
    <>
      <SectionHead
        eyebrow="YOUR PRIVATE WORKSPACE"
        title="A clear view. A considered next move."
        description="One place for your agents, accounts and financial automations."
        action={
          <button className="primary" onClick={() => setSetupOpen(true)}>
            {state.setup.complete ? "Review setup" : "Set up workspace"}
            <ArrowRight size={16} />
          </button>
        }
      />
      <div className="hero-card">
        <div className="hero-copy">
          <Badge tone="lime">
            <span className="dot" /> BUILT AROUND YOUR CONTROL
          </Badge>
          <h2>
            Your strategy.
            <br />
            Your limits. Your workspace.
          </h2>
          <p>
            Start with a profile. Explore the terminals. Add a wallet and AI
            only when you need them.
          </p>
          <button
            className="text-button"
            onClick={() => navigate("agents/chat")}
          >
            Meet your agents <ArrowRight size={16} />
          </button>
        </div>
        <div className="hero-art" aria-hidden="true">
          <div className="orbital orbit-one" />
          <div className="orbital orbit-two" />
          <div className="orbital orbit-three" />
          <div className="core-mark">
            <span>f</span>
            <i />
          </div>
          <span className="orbit-tag tag-top">
            <ShieldCheck size={13} /> Owner permissions
          </span>
          <span className="orbit-tag tag-bottom">
            <Bot size={13} /> Persistent profiles
          </span>
        </div>
      </div>
      <div className="stat-grid">
        <div className="stat">
          <span>
            Agent profiles <Bot size={15} />
          </span>
          <strong>{String(state.profiles.length).padStart(2, "0")}</strong>
          <small>Shared runtime, individual context</small>
        </div>
        <div className="stat">
          <span>
            Wallet connection <Wallet size={15} />
          </span>
          <strong className="stat-word">Not connected</strong>
          <small>Prototype · no balances loaded</small>
        </div>
        <div className="stat">
          <span>
            Execution mode <Shield size={15} />
          </span>
          <strong className="stat-word">Preview only</strong>
          <small>Real signing is unavailable</small>
        </div>
      </div>
      <div className="two-col">
        <section className="panel">
          <div className="panel-heading">
            <h2>Your next steps</h2>
            <Badge>
              {state.setup.complete ? "SETUP PREVIEWED" : "GET STARTED"}
            </Badge>
          </div>
          <div className="steps-list">
            {steps.map((step, i) => (
              <button
                className="step-row"
                key={step.title}
                onClick={step.action}
              >
                <span className={`step-number ${step.done ? "done" : ""}`}>
                  {step.done ? <Check size={14} /> : `0${i + 1}`}
                </span>
                <span>
                  <strong>{step.title}</strong>
                  <small>{step.detail}</small>
                </span>
                <ChevronRight size={16} />
              </button>
            ))}
          </div>
        </section>
        <section className="panel">
          <div className="panel-heading">
            <h2>Agents at a glance</h2>
            <button
              className="text-button"
              onClick={() => navigate("agents/chat")}
            >
              View all <ArrowRight size={14} />
            </button>
          </div>
          <div className="profile-list">
            {state.profiles.slice(0, 3).map((p) => (
              <button
                key={p.id}
                onClick={() => {
                  dispatch({ type: "profile", id: p.id });
                  navigate("agents/chat");
                }}
              >
                <Avatar profile={p} small />
                <span>
                  <strong>{p.name}</strong>
                  <small>{p.role}</small>
                </span>
                <span className="status-text">Draft</span>
              </button>
            ))}
          </div>
        </section>
      </div>
      <div className="section-label">
        <div>
          <h2>Four workspaces. One foundation.</h2>
          <p className="subtle">
            Explore the product direction. Each terminal is a labelled mockup.
          </p>
        </div>
        <button className="text-button" onClick={() => navigate("terminals")}>
          Explore terminals <ArrowRight size={15} />
        </button>
      </div>
      <div className="terminal-grid">
        {terminalData.map((t) => (
          <button
            className="terminal-card"
            key={t.id}
            onClick={() => navigate(`terminals/${t.id}`)}
          >
            <div className="card-top">
              <span className={`feature-icon ${t.color}`}>
                <t.icon size={20} />
              </span>
              <Badge>PROTOTYPE</Badge>
            </div>
            <small className="eyebrow">{t.category}</small>
            <h3>{t.name}</h3>
            <p>{t.desc}</p>
            <span className="terminal-link">
              Open preview <ArrowUpRight size={15} />
            </span>
          </button>
        ))}
      </div>
    </>
  );
}
