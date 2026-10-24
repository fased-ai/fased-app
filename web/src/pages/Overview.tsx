import { ArrowRight, Bot, ChevronRight, Check } from "../icons";
import { Badge, Avatar, SectionHead } from "../components/shared";
import { terminalData } from "../fixtures";
import { useWorkspace } from "../workspace";

export function Overview() {
  const { state, core, dispatch, setSetupOpen, navigate } = useWorkspace();
  const steps = [
    {
      title: state.setup.complete ? "Review setup" : "Set up workspace",
      done: state.setup.complete,
      action: () => setSetupOpen(true),
    },
    {
      title: "Public account records",
      done: false,
      action: () => navigate("wallets/overview"),
    },
    {
      title: "Agent tasks",
      done: false,
      action: () => navigate("agents/tasks"),
    },
  ];
  return (
    <>
      <SectionHead
        title="Overview"
        action={
          <button className="primary" onClick={() => navigate("agents/chat")}>
            Open Chat <ArrowRight size={16} />
          </button>
        }
      />
      <dl className="workspace-metrics">
        <div>
          <dt>Agent profiles</dt>
          <dd>{state.profiles.length}</dd>
        </div>
        <div>
          <dt>Connection</dt>
          <dd>
            {core.connection
              ? core.connection.location === "remote"
                ? "Remote"
                : "Local"
              : "Preview"}
          </dd>
        </div>
        <div>
          <dt>Financial execution</dt>
          <dd>
            <Badge>DISABLED</Badge>
          </dd>
        </div>
      </dl>
      <div className="overview-workspace">
        <section className="panel" aria-labelledby="overview-agents">
          <div className="panel-heading">
            <h2 id="overview-agents">
              <Bot size={16} /> Agents
            </h2>
            <button
              className="text-button"
              onClick={() => navigate("agents/memory")}
            >
              Memory <ArrowRight size={14} />
            </button>
          </div>
          <div className="workspace-rows">
            {state.profiles.map((p) => (
              <button
                key={p.id}
                className="workspace-row"
                onClick={() => {
                  dispatch({ type: "profile", id: p.id });
                  navigate("agents/chat");
                }}
              >
                <Avatar profile={p} small />
                <span className="workspace-row-copy">
                  <strong>{p.name}</strong>
                  <span>{p.role}</span>
                </span>
                <ChevronRight size={16} />
              </button>
            ))}
          </div>
        </section>
        <section className="panel" aria-labelledby="overview-workspace">
          <div className="panel-heading">
            <h2 id="overview-workspace">Workspace</h2>
          </div>
          <div className="workspace-rows">
            {steps.map((step) => (
              <button
                className="workspace-row"
                key={step.title}
                onClick={step.action}
              >
                <span className="workspace-row-copy">
                  <strong>{step.title}</strong>
                </span>
                {step.done ? (
                  <Check size={16} aria-hidden="true" />
                ) : (
                  <ChevronRight size={16} />
                )}
              </button>
            ))}
          </div>
        </section>
        <section
          className="panel workspace-terminals"
          aria-labelledby="overview-terminals"
        >
          <div className="panel-heading">
            <h2 id="overview-terminals">Terminals</h2>
            <Badge>PREVIEW ONLY</Badge>
          </div>
          <div className="workspace-rows">
            {terminalData.map((t) => (
              <button
                className="workspace-row"
                key={t.id}
                onClick={() => navigate(`terminals/${t.id}`)}
              >
                <t.icon size={18} />
                <span className="workspace-row-copy">
                  <strong>{t.name}</strong>
                </span>
                <Badge>{t.id === "satcoin" ? "DEFERRED" : "DISABLED"}</Badge>
                <ChevronRight size={16} />
              </button>
            ))}
          </div>
        </section>
      </div>
    </>
  );
}
