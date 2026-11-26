import { useCallback, useEffect, useState } from "react";
import { Badge } from "../components/shared";
import { Globe2, RefreshCw } from "../icons";
import {
  broadcastSwarmSignal,
  getCEXStatus,
  getEquitiesStatus,
  getOptionsChain,
  getSwarmStatus,
  installHubPackage,
  listHubPackages,
  previewCEXStatus,
  previewEquitiesStatus,
  previewHubList,
  previewSwarmStatus,
  submitCEXOrder,
  submitEquitiesOrder,
  updateSwarmConfig,
  type CEXStatusView,
  type EquitiesStatusView,
  type HubListView,
  type OptionsChainView,
  type SwarmStatusView,
} from "../network";
import { useUI } from "../ui-preferences";
import { useWorkspace } from "../workspace";
import "./NetworkWorkspace.css";

type NetworkTab = "hub" | "swarm" | "execution";

export function NetworkWorkspace() {
  const { t } = useUI();
  const { core } = useWorkspace();
  const [tab, setTab] = useState<NetworkTab>("hub");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [statusNote, setStatusNote] = useState("");

  const [hub, setHub] = useState<HubListView>(previewHubList);
  const [swarm, setSwarm] = useState<SwarmStatusView>(previewSwarmStatus);
  const [cex, setCex] = useState<CEXStatusView>(previewCEXStatus);
  const [equities, setEquities] = useState<EquitiesStatusView>(
    previewEquitiesStatus,
  );
  const [optionsChain, setOptionsChain] = useState<OptionsChainView | null>(
    null,
  );

  const [signalSymbol, setSignalSymbol] = useState("SOL");
  const [signalDirection, setSignalDirection] = useState<
    "LONG" | "SHORT" | "NEUTRAL"
  >("LONG");
  const [signalThesis, setSignalThesis] = useState(
    "Spot momentum with bounded paper confirmation",
  );
  const [signalConfidence, setSignalConfidence] = useState("0.75");

  const [cexVenue, setCexVenue] = useState("binance");
  const [cexSymbol, setCexSymbol] = useState("BTCUSDT");
  const [cexQty, setCexQty] = useState("0.05");
  const [cexPrice, setCexPrice] = useState("60000");

  const [optionUnderlying, setOptionUnderlying] = useState("SPY");
  const [eqVenue, setEqVenue] = useState<"alpaca" | "ibkr">("alpaca");
  const [eqAccountID, setEqAccountID] = useState("DU1234567");
  const [eqSymbol, setEqSymbol] = useState("SPY");
  const [eqQty, setEqQty] = useState("1");
  const [eqLimit, setEqLimit] = useState("590");

  const refresh = useCallback(async () => {
    if (core.mode !== "ready") return;
    setLoading(true);
    setError("");
    try {
      const [h, s, c, e, chain] = await Promise.all([
        listHubPackages(core),
        getSwarmStatus(core),
        getCEXStatus(core),
        getEquitiesStatus(core),
        getOptionsChain(core, optionUnderlying),
      ]);
      setHub(h);
      setSwarm(s);
      setCex(c);
      setEquities(e);
      setOptionsChain(chain);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [core, optionUnderlying]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const handleInstallPackage = async (id: string) => {
    if (core.mode !== "ready") return;
    setError("");
    try {
      await installHubPackage(core, id);
      setStatusNote(id);
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const handleToggleSwarm = async (enabled: boolean) => {
    if (core.mode !== "ready") return;
    setError("");
    try {
      await updateSwarmConfig(core, { enabled });
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const handleCopyModeChange = async (copyMode: "shadow" | "paper") => {
    if (core.mode !== "ready") return;
    setError("");
    try {
      await updateSwarmConfig(core, { copyMode });
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const handleBroadcastSignal = async () => {
    if (core.mode !== "ready") return;
    setError("");
    try {
      const conf = Number(signalConfidence);
      await broadcastSwarmSignal(core, {
        symbol: signalSymbol,
        direction: signalDirection,
        thesis: signalThesis,
        confidence: Number.isFinite(conf) ? conf : 0.7,
      });
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const handleSubmitCEXPaper = async () => {
    if (core.mode !== "ready") return;
    setError("");
    try {
      await submitCEXOrder(core, {
        venue: cexVenue,
        symbol: cexSymbol,
        side: "BUY",
        orderType: "LIMIT",
        quantity: Number(cexQty) || 0.05,
        price: Number(cexPrice) || 60000,
        environment: "paper",
        simulateOnly: true,
      });
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const handleSubmitEquitiesPaper = async () => {
    if (core.mode !== "ready") return;
    setError("");
    try {
      await submitEquitiesOrder(core, {
        venue: eqVenue,
        accountId: eqVenue === "ibkr" ? eqAccountID : undefined,
        symbol: eqSymbol,
        side: "BUY",
        orderType: "LIMIT",
        quantity: Number(eqQty) || 1,
        limitPrice: Number(eqLimit) || 590,
        simulateOnly: true,
      });
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  return (
    <section className="network-workspace" aria-label={t("Network")}>
      <header className="network-toolbar">
        <div className="network-title-group">
          <h1>
            <Globe2 size={18} aria-hidden="true" /> {t("Network")}
          </h1>
          <Badge>{t("Shadow / Paper")}</Badge>
          {hub.wenHeld && <Badge tone="warn">{t("WEN SAT Held")}</Badge>}
        </div>
        <div className="network-tabs" role="tablist" aria-label={t("Network")}>
          {(
            [
              ["hub", t("Community Hub")],
              ["swarm", t("Swarm")],
              ["execution", t("Execution")],
            ] as const
          ).map(([id, label]) => (
            <button
              key={id}
              type="button"
              role="tab"
              aria-selected={tab === id}
              className={`network-tab-btn${tab === id ? " is-selected" : ""}`}
              onClick={() => setTab(id)}
            >
              {label}
            </button>
          ))}
          <button
            type="button"
            className="secondary"
            aria-label={t("Refresh")}
            onClick={() => void refresh()}
            disabled={loading}
          >
            <RefreshCw size={14} aria-hidden="true" /> {t("Refresh")}
          </button>
        </div>
      </header>

      {error && (
        <div className="storage-warning" role="alert">
          {error}
        </div>
      )}

      {tab === "hub" && (
        <div className="network-card">
          <div className="network-card-head">
            <h2>{t("Community Hub")}</h2>
            <div className="network-pill-row">
              <Badge>{hub.satStatus}</Badge>
              {statusNote && <Badge tone="ok">{statusNote}</Badge>}
            </div>
          </div>
          <div className="network-list">
            {hub.packages.map((pkg) => (
              <article key={pkg.id} className="network-row">
                <div className="network-row-top">
                  <strong>
                    {pkg.name} ({pkg.version})
                  </strong>
                  <div className="network-pill-row">
                    <Badge>{pkg.kind}</Badge>
                    <Badge tone={pkg.verified ? "ok" : "warn"}>
                      {pkg.verified ? t("Verified") : t("Unverified")}
                    </Badge>
                    <Badge>
                      {pkg.satBond.amountSat} SAT · {pkg.satBond.status}
                    </Badge>
                    <button
                      type="button"
                      className="primary"
                      disabled={core.mode !== "ready" || pkg.installed}
                      onClick={() => void handleInstallPackage(pkg.id)}
                    >
                      {pkg.installed ? t("Installed") : t("Install package")}
                    </button>
                  </div>
                </div>
                <details className="network-payload-details">
                  <summary>{t("Details")}</summary>
                  <pre className="network-code">{pkg.payload}</pre>
                </details>
              </article>
            ))}
          </div>
        </div>
      )}

      {tab === "swarm" && (
        <div className="network-grid-two">
          <div className="network-card">
            <div className="network-card-head">
              <h2>{t("Swarm Controls")}</h2>
              <div className="network-pill-row">
                <Badge tone={swarm.config.enabled ? "ok" : undefined}>
                  {swarm.config.enabled ? t("Enabled") : t("Opt-in Off")}
                </Badge>
                <Badge>{swarm.config.copyMode}</Badge>
              </div>
            </div>
            <div className="network-actions-row">
              <button
                type="button"
                className="primary"
                disabled={core.mode !== "ready"}
                onClick={() => void handleToggleSwarm(!swarm.config.enabled)}
              >
                {swarm.config.enabled ? t("Disable Swarm") : t("Enable Swarm")}
              </button>
              <button
                type="button"
                className="secondary"
                disabled={core.mode !== "ready"}
                onClick={() => void handleCopyModeChange("shadow")}
              >
                {t("Shadow")}
              </button>
              <button
                type="button"
                className="secondary"
                disabled={core.mode !== "ready"}
                onClick={() => void handleCopyModeChange("paper")}
              >
                {t("Paper")}
              </button>
            </div>
            <div className="network-field-grid">
              <div className="network-field">
                <label htmlFor="swarm-symbol">{t("Symbol")}</label>
                <input
                  id="swarm-symbol"
                  value={signalSymbol}
                  onChange={(e) => setSignalSymbol(e.target.value)}
                />
              </div>
              <div className="network-field">
                <label htmlFor="swarm-dir">{t("Direction")}</label>
                <select
                  id="swarm-dir"
                  value={signalDirection}
                  onChange={(e) =>
                    setSignalDirection(
                      e.target.value as "LONG" | "SHORT" | "NEUTRAL",
                    )
                  }
                >
                  <option value="LONG">LONG</option>
                  <option value="SHORT">SHORT</option>
                  <option value="NEUTRAL">NEUTRAL</option>
                </select>
              </div>
              <div className="network-field">
                <label htmlFor="swarm-conf">{t("Confidence")}</label>
                <input
                  id="swarm-conf"
                  value={signalConfidence}
                  onChange={(e) => setSignalConfidence(e.target.value)}
                />
              </div>
            </div>
            <div className="network-field">
              <label htmlFor="swarm-thesis">{t("Thesis")}</label>
              <input
                id="swarm-thesis"
                value={signalThesis}
                onChange={(e) => setSignalThesis(e.target.value)}
              />
            </div>
            <div className="network-actions-row">
              <button
                type="button"
                className="primary"
                disabled={core.mode !== "ready" || !swarm.config.enabled}
                onClick={() => void handleBroadcastSignal()}
              >
                {t("Broadcast signal")}
              </button>
            </div>
          </div>

          <div className="network-card">
            <div className="network-card-head">
              <h2>
                {t("Peers")} ({swarm.peerCount}) & {t("Signals")} (
                {swarm.signalCount})
              </h2>
              <Badge>{t("Untrusted")}</Badge>
            </div>
            <div className="network-list">
              {swarm.peers.map((peer) => (
                <div key={peer.peerId} className="network-row">
                  <div className="network-row-top">
                    <strong>{peer.alias}</strong>
                    <Badge tone="ok">{peer.status}</Badge>
                  </div>
                </div>
              ))}
              {swarm.recentSignals.map((sig) => (
                <div key={sig.envelopeId} className="network-row">
                  <div className="network-row-top">
                    <strong>
                      {sig.symbol} · {sig.direction}
                    </strong>
                    <Badge tone="warn">
                      {sig.untrusted ? "untrusted=true" : "trusted"}
                    </Badge>
                  </div>
                  <span>{sig.thesis}</span>
                </div>
              ))}
            </div>
          </div>
        </div>
      )}

      {tab === "execution" && (
        <div className="network-grid-two">
          <div className="network-card">
            <div className="network-card-head">
              <h2>{t("CEX Venues")}</h2>
              <div className="network-pill-row">
                <Badge tone="warn">
                  {cex.mainnetLocked ? t("Mainnet Locked") : t("Paper")}
                </Badge>
                <Badge>{t("Withdrawal-disabled")}</Badge>
              </div>
            </div>
            <div className="network-table-wrap">
              <table className="network-table">
                <thead>
                  <tr>
                    <th>{t("Venue")}</th>
                    <th>{t("Auth")}</th>
                    <th>{t("Origin")}</th>
                  </tr>
                </thead>
                <tbody>
                  {cex.supportedVenues.map((v) => (
                    <tr key={v.venue}>
                      <td>{v.displayName}</td>
                      <td>{v.authScheme}</td>
                      <td>{v.testnetOrigin}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <div className="network-field-grid">
              <div className="network-field">
                <label htmlFor="cex-venue">{t("Venue")}</label>
                <select
                  id="cex-venue"
                  value={cexVenue}
                  onChange={(e) => setCexVenue(e.target.value)}
                >
                  {cex.supportedVenues.map((v) => (
                    <option key={v.venue} value={v.venue}>
                      {v.venue}
                    </option>
                  ))}
                </select>
              </div>
              <div className="network-field">
                <label htmlFor="cex-symbol">{t("Symbol")}</label>
                <input
                  id="cex-symbol"
                  value={cexSymbol}
                  onChange={(e) => setCexSymbol(e.target.value)}
                />
              </div>
              <div className="network-field">
                <label htmlFor="cex-qty">{t("Quantity")}</label>
                <input
                  id="cex-qty"
                  value={cexQty}
                  onChange={(e) => setCexQty(e.target.value)}
                />
              </div>
              <div className="network-field">
                <label htmlFor="cex-price">{t("Price")}</label>
                <input
                  id="cex-price"
                  value={cexPrice}
                  onChange={(e) => setCexPrice(e.target.value)}
                />
              </div>
            </div>
            <div className="network-actions-row">
              <button
                type="button"
                className="primary"
                disabled={core.mode !== "ready"}
                onClick={() => void handleSubmitCEXPaper()}
              >
                {t("Paper CEX order")}
              </button>
            </div>
            {cex.recentOrders.slice(0, 3).map((ord) => (
              <div key={ord.id} className="network-row">
                <div className="network-row-top">
                  <strong>
                    {ord.venue} {ord.symbol}
                  </strong>
                  <Badge>{ord.status}</Badge>
                </div>
              </div>
            ))}
          </div>

          <div className="network-card">
            <div className="network-card-head">
              <h2>{t("Equities & OCC Options")}</h2>
              <Badge tone="warn">
                {equities.liveLocked ? t("Live Locked") : t("Paper")}
              </Badge>
            </div>
            <div className="network-field-grid">
              <div className="network-field">
                <label htmlFor="opt-underlying">{t("Underlying")}</label>
                <input
                  id="opt-underlying"
                  value={optionUnderlying}
                  onChange={(e) => setOptionUnderlying(e.target.value)}
                />
              </div>
              <div className="network-field">
                <label htmlFor="eq-venue">{t("Venue")}</label>
                <select
                  id="eq-venue"
                  value={eqVenue}
                  onChange={(e) =>
                    setEqVenue(e.target.value as "alpaca" | "ibkr")
                  }
                >
                  <option value="alpaca">alpaca</option>
                  <option value="ibkr">ibkr</option>
                </select>
              </div>
              {eqVenue === "ibkr" && (
                <div className="network-field">
                  <label htmlFor="eq-acct">{t("DU* Account")}</label>
                  <input
                    id="eq-acct"
                    value={eqAccountID}
                    onChange={(e) => setEqAccountID(e.target.value)}
                  />
                </div>
              )}
              <div className="network-field">
                <label htmlFor="eq-sym">{t("Ticker / OCC")}</label>
                <input
                  id="eq-sym"
                  value={eqSymbol}
                  onChange={(e) => setEqSymbol(e.target.value)}
                />
              </div>
              <div className="network-field">
                <label htmlFor="eq-qty">{t("Quantity")}</label>
                <input
                  id="eq-qty"
                  value={eqQty}
                  onChange={(e) => setEqQty(e.target.value)}
                />
              </div>
              <div className="network-field">
                <label htmlFor="eq-limit">{t("Limit")}</label>
                <input
                  id="eq-limit"
                  value={eqLimit}
                  onChange={(e) => setEqLimit(e.target.value)}
                />
              </div>
            </div>
            <div className="network-actions-row">
              <button
                type="button"
                className="primary"
                disabled={core.mode !== "ready"}
                onClick={() => void handleSubmitEquitiesPaper()}
              >
                {t("Paper equities order")}
              </button>
            </div>
            {optionsChain && (
              <div className="network-table-wrap">
                <table className="network-table">
                  <thead>
                    <tr>
                      <th>OCC</th>
                      <th>{t("Strike")}</th>
                      <th>{t("Bid")}</th>
                      <th>{t("Ask")}</th>
                      <th>IV</th>
                    </tr>
                  </thead>
                  <tbody>
                    {optionsChain.contracts.slice(0, 6).map((c) => (
                      <tr key={c.occSymbol}>
                        <td>{c.occSymbol}</td>
                        <td>{c.strike}</td>
                        <td>{c.bid}</td>
                        <td>{c.ask}</td>
                        <td>{c.impliedVolatility}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            {equities.recentOrders.slice(0, 3).map((ord) => (
              <div key={ord.id} className="network-row">
                <div className="network-row-top">
                  <strong>
                    {ord.venue} {ord.symbol}
                  </strong>
                  <Badge>{ord.status}</Badge>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
    </section>
  );
}
