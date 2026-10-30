import { useUI } from "../ui-preferences";
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
import { WENSessionRehearsal } from "./WENSessionRehearsal";
import { WENSessionAutomatic } from "./WENSessionAutomatic";
import { OnchainSwap } from "./OnchainSwap";
export function Terminals() {
  const { t: uiText } = useUI();
  const { tab, navigate } = useWorkspace();
  const active = terminalData.find((t) => t.id === tab);
  const mining = active?.id === "satcoin";
  if (active?.id === "jupiter") return <OnchainSwap />;
  return (
    <>
      <SectionHead
        eyebrow={uiText("FINANCIAL WORKSPACES")}
        title={active?.name ?? uiText("Terminals")}
        description={uiText(
          "Three terminal prototypes for mining and trading, with shared accounts and permissions.",
        )}
        action={<Badge>{uiText("ALL TERMINALS · PROTOTYPE")}</Badge>}
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
                  <Badge>{uiText("PROTOTYPE")}</Badge>
                </div>
                <h2>{t.name}</h2>
                <p>{t.desc}</p>
                <span className="terminal-link">
                  {uiText("Explore layout ")}
                  <ArrowRight size={15} />
                </span>
              </button>
            ))}
          </div>
          <div className="info-banner">
            <FlaskConical size={18} />
            <div>
              <strong>
                {uiText("Foundation first. Integrations when ready.")}
              </strong>
              <span>
                {uiText(
                  "WEN contracts are still being developed. No terminal can quote, sign or submit a financial action.",
                )}
              </span>
            </div>
          </div>
        </>
      ) : (
        <>
          <div className="info-banner amber">
            <FlaskConical size={18} />
            <div>
              <strong>
                {active.name}
                {uiText(" is a design prototype.")}
              </strong>
              <span>
                {mining
                  ? uiText(
                      "The local paper journey uses public fixtures. Live WEN integration awaits independent contract acceptance.",
                    )
                  : uiText(
                      "Venue integration, market data and financial execution have not been implemented.",
                    )}{" "}
                {uiText("All values below are illustrative.")}
              </span>
            </div>
          </div>
          {mining && <WENPaperJourney />}
          {mining && <WENSessionRehearsal />}
          {mining && <WENSessionAutomatic />}
          <div className="terminal-workspace">
            <section className="panel">
              <div className="panel-heading">
                <div>
                  <h2>
                    {mining
                      ? uiText("BTC context & ranges")
                      : uiText("BTC / USD")}
                  </h2>
                  <small className="subtle">
                    {mining
                      ? uiText("Illustrative layout · no live BTC data")
                      : uiText("Illustrative market · no live price")}
                  </small>
                </div>
                <Badge>{uiText("DEMO DATA")}</Badge>
              </div>
              <DemoChart />
              <div className="detail-grid">
                <div>
                  <small>{uiText("Connection")}</small>
                  <strong>{uiText("Not implemented")}</strong>
                </div>
                <div>
                  <small>{uiText("Available funds")}</small>
                  <strong>{uiText("Not loaded")}</strong>
                </div>
                <div>
                  <small>{uiText("Execution")}</small>
                  <strong>{uiText("Disabled")}</strong>
                </div>
              </div>
            </section>
            <section className="panel ticket">
              <div className="panel-heading">
                <h2>
                  {mining
                    ? uiText("Mining session preview")
                    : uiText("Order preview")}
                </h2>
              </div>
              <label>{uiText("Account")}</label>
              <div className="fake-input">
                {uiText("No account connected ")}
                <ChevronDown size={15} />
              </div>
              <label>
                {mining ? uiText("SOL budget") : uiText("Operating budget")}
              </label>
              <div className="fake-input">
                {uiText("Not assigned ")}
                <span>—</span>
              </div>
              {mining && (
                <>
                  <label>{uiText("BTC range")}</label>
                  <div className="fake-input">{uiText("Not selected")}</div>
                  <label>{uiText("Commitment deadline")}</label>
                  <div className="fake-input">{uiText("Unavailable")}</div>
                </>
              )}
              <label>{uiText("Owner mandate")}</label>
              <div className="fake-input">
                {uiText("Not created ")}
                <LockKeyhole size={14} />
              </div>
              <div className="ticket-note">
                <Shield size={16} />
                <p>
                  {uiText(
                    "Reviewable permissions and transaction recovery are required before this terminal can execute.",
                  )}
                </p>
              </div>
              <button className="primary" disabled>
                {uiText("Execution unavailable")}
              </button>
              <button
                className="text-button"
                onClick={() => navigate("wallets/overview")}
              >
                {uiText("Explore wallet modes ")}
                <ArrowRight size={14} />
              </button>
            </section>
          </div>
          <section className="panel">
            <div className="panel-heading">
              <h2>
                {mining
                  ? uiText("Results & claims")
                  : uiText("Positions & orders")}
              </h2>
              <Badge>{uiText("PROTOTYPE")}</Badge>
            </div>
            <Empty
              icon={<TerminalSquare size={24} />}
              title={
                mining
                  ? uiText("No mining session or claim loaded")
                  : uiText("No live positions or orders")
              }
            >
              {mining
                ? uiText(
                    "Pending states, settled results and SAT/BTC claims require the accepted WEN integration. No rewards or claimable amounts are loaded.",
                  )
                : uiText(
                    "This layout reserves space for verified state, pending outcomes and recovery. It is not a connected account.",
                  )}
            </Empty>
          </section>
        </>
      )}
    </>
  );
}
