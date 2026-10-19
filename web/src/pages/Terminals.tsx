import {
  ArrowRight,
  ChevronDown,
  FlaskConical,
  LockKeyhole,
  Shield,
  TerminalSquare,
} from "../icons";
import { Badge, SectionHead, Empty, DemoChart } from "../components/shared";
import { terminalData } from "../fixtures";
import { useWorkspace } from "../workspace";
export function Terminals() {
  const { tab, navigate } = useWorkspace();
  const active = terminalData.find((t) => t.id === tab);
  return (
    <>
      <SectionHead
        eyebrow="FINANCIAL WORKSPACES"
        title={active?.name ?? "Terminals"}
        description="Four focused workspaces, with one shared foundation for accounts, permissions and execution."
        action={<Badge>ALL TERMINALS · PROTOTYPE</Badge>}
      />
      <div className="terminal-switcher">
        {terminalData.map((t) => (
          <button
            key={t.id}
            className={t.id === tab ? "active" : ""}
            onClick={() => navigate(`terminals/${t.id}`)}
          >
            <t.icon size={16} />
            {t.name}
          </button>
        ))}
      </div>
      {!active ? (
        <>
          <div className="terminal-grid full">
            {terminalData.map((t) => (
              <button
                className="terminal-card"
                key={t.id}
                onClick={() => navigate(`terminals/${t.id}`)}
              >
                <div className="card-top">
                  <span className={`feature-icon ${t.color}`}>
                    <t.icon size={24} />
                  </span>
                  <Badge>PROTOTYPE</Badge>
                </div>
                <h2>{t.name}</h2>
                <p>{t.desc}</p>
                <span className="terminal-link">
                  Explore layout <ArrowRight size={15} />
                </span>
              </button>
            ))}
          </div>
          <div className="info-banner">
            <FlaskConical size={18} />
            <div>
              <strong>Foundation first. Integrations when ready.</strong>
              <span>
                WEN contracts are still being developed. No terminal can quote,
                sign or submit a financial action.
              </span>
            </div>
          </div>
        </>
      ) : (
        <>
          <div className="info-banner amber">
            <FlaskConical size={18} />
            <div>
              <strong>{active.name} is a design prototype.</strong>
              <span>
                {["asset", "loss"].includes(active.id)
                  ? "WEN integration is deferred until its independent contract and acceptance are ready."
                  : "Venue integration, market data and financial execution have not been implemented."}{" "}
                All values below are illustrative.
              </span>
            </div>
          </div>
          <div className="terminal-workspace">
            <section className="panel">
              <div className="panel-heading">
                <div>
                  <h2>
                    {["asset", "loss"].includes(active.id)
                      ? "Workspace preview"
                      : "BTC / USD"}
                  </h2>
                  <small className="subtle">
                    {["asset", "loss"].includes(active.id)
                      ? "Illustrative district / arena layout"
                      : "Illustrative market · no live price"}
                  </small>
                </div>
                <Badge>DEMO DATA</Badge>
              </div>
              <DemoChart />
              <div className="detail-grid">
                <div>
                  <small>Connection</small>
                  <strong>Not implemented</strong>
                </div>
                <div>
                  <small>Available funds</small>
                  <strong>Not loaded</strong>
                </div>
                <div>
                  <small>Execution</small>
                  <strong>Disabled</strong>
                </div>
              </div>
            </section>
            <section className="panel ticket">
              <div className="panel-heading">
                <h2>
                  {active.id === "asset"
                    ? "Allocation preview"
                    : "Order preview"}
                </h2>
              </div>
              <label>Account</label>
              <div className="fake-input">
                No account connected <ChevronDown size={15} />
              </div>
              <label>Operating budget</label>
              <div className="fake-input">
                Not assigned <span>—</span>
              </div>
              <label>Owner mandate</label>
              <div className="fake-input">
                Not created <LockKeyhole size={14} />
              </div>
              <div className="ticket-note">
                <Shield size={16} />
                <p>
                  Reviewable permissions and transaction recovery are required
                  before this terminal can execute.
                </p>
              </div>
              <button className="primary" disabled>
                Execution unavailable
              </button>
              <button
                className="text-button"
                onClick={() => navigate("wallets/overview")}
              >
                Explore wallet modes <ArrowRight size={14} />
              </button>
            </section>
          </div>
          <section className="panel">
            <div className="panel-heading">
              <h2>
                {active.id === "asset"
                  ? "Orders & rewards"
                  : "Positions & orders"}
              </h2>
              <Badge>PROTOTYPE</Badge>
            </div>
            <Empty
              icon={<TerminalSquare size={24} />}
              title="No live positions or orders"
            >
              This layout reserves space for verified state, pending outcomes
              and recovery. It is not a connected account.
            </Empty>
          </section>
        </>
      )}
    </>
  );
}
