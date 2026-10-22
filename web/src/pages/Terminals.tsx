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
import { WENPaperJourney } from "./WENPaperJourney";
export function Terminals() {
  const { tab, navigate } = useWorkspace();
  const active = terminalData.find((t) => t.id === tab);
  const mining = active?.id === "satcoin";
  return (
    <>
      <SectionHead
        eyebrow="FINANCIAL WORKSPACES"
        title={active?.name ?? "Terminals"}
        description="Three terminal prototypes for mining and trading, with shared accounts and permissions."
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
                {mining
                  ? "The local paper journey uses public fixtures. Live WEN integration awaits independent contract acceptance."
                  : "Venue integration, market data and financial execution have not been implemented."}{" "}
                All values below are illustrative.
              </span>
            </div>
          </div>
          {mining && <WENPaperJourney />}
          <div className="terminal-workspace">
            <section className="panel">
              <div className="panel-heading">
                <div>
                  <h2>{mining ? "BTC context & ranges" : "BTC / USD"}</h2>
                  <small className="subtle">
                    {mining
                      ? "Illustrative layout · no live BTC data"
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
                <h2>{mining ? "Mining session preview" : "Order preview"}</h2>
              </div>
              <label>Account</label>
              <div className="fake-input">
                No account connected <ChevronDown size={15} />
              </div>
              <label>{mining ? "SOL budget" : "Operating budget"}</label>
              <div className="fake-input">
                Not assigned <span>—</span>
              </div>
              {mining && (
                <>
                  <label>BTC range</label>
                  <div className="fake-input">Not selected</div>
                  <label>Commitment deadline</label>
                  <div className="fake-input">Unavailable</div>
                </>
              )}
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
              <h2>{mining ? "Results & claims" : "Positions & orders"}</h2>
              <Badge>PROTOTYPE</Badge>
            </div>
            <Empty
              icon={<TerminalSquare size={24} />}
              title={
                mining
                  ? "No mining session or claim loaded"
                  : "No live positions or orders"
              }
            >
              {mining
                ? "Pending states, settled results and SAT/BTC claims require the accepted WEN integration. No rewards or claimable amounts are loaded."
                : "This layout reserves space for verified state, pending outcomes and recovery. It is not a connected account."}
            </Empty>
          </section>
        </>
      )}
    </>
  );
}
