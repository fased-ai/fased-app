import "./HyperliquidTerminal.css";
import { useEffect, useRef, useState } from "react";
import type { AgentView } from "../agents";
import { Badge } from "../components/shared";
import { useCore } from "../core";
import {
  formatFundingRate,
  formatMicroUSD,
  hyperliquidOrderState,
  shortenHex,
  type HyperliquidCommand,
  type HyperliquidReview,
  type HyperliquidView,
} from "../hyperliquid";
import { RefreshCw } from "../icons";
import { useUI } from "../ui-preferences";
import { useWorkspace } from "../workspace";

export function HyperliquidTerminal() {
  const { t } = useUI();
  const core = useCore();
  const { agents } = useWorkspace();
  const profile = agents.view?.profileId ?? "operator";

  const [view, setView] = useState<HyperliquidView | undefined>(
    agents.view?.hyperliquid,
  );
  const [review, setReview] = useState<HyperliquidReview>();
  const [asset, setAsset] = useState("");
  const [isBuy, setIsBuy] = useState(true);
  const [limitPrice, setLimitPrice] = useState("");
  const [size, setSize] = useState("0.5");
  const [leverage, setLeverage] = useState("5");
  const [isCross, setIsCross] = useState(false);
  const [timeInForce, setTimeInForce] = useState<"Gtc" | "Ioc" | "Alo">("Gtc");
  const [reduceOnly, setReduceOnly] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const generation = useRef(0);
  const writing = useRef(false);

  const active = view ?? agents.view?.hyperliquid;
  const markets = active?.markets ?? [];
  const selectedMarket = markets.find((m) => m.symbol === asset) ?? markets[0];
  const isolatedOnly = Boolean(selectedMarket?.onlyIsolated);
  const effectiveCross = !isolatedOnly && isCross;

  useEffect(() => {
    if (markets.length === 0) return;
    if (!asset || !markets.some((m) => m.symbol === asset)) {
      setAsset(markets[0].symbol);
      setLimitPrice(markets[0].markPrice);
    } else if (!limitPrice && selectedMarket) {
      setLimitPrice(selectedMarket.markPrice);
    }
  }, [markets, asset, limitPrice, selectedMarket]);

  useEffect(() => {
    generation.current++;
    setReview(undefined);
    setError("");
    return () => {
      generation.current++;
    };
  }, [profile, core.instanceId, core.request]);

  async function refresh() {
    if (writing.current) return;
    const stamp = generation.current;
    setBusy(true);
    setError("");
    try {
      const current = await core.request<AgentView>(
        `agents?profile=${encodeURIComponent(profile)}`,
      );
      const next = await core.request<AgentView>("agents", "POST", {
        action: "hyperliquid-refresh",
        profileId: profile,
        revision: current.revision,
      });
      if (stamp !== generation.current) return;
      setView(next.hyperliquid);
      await agents.refresh();
    } catch (e) {
      if (stamp === generation.current)
        setError(e instanceof Error ? e.message : t("Unavailable"));
    } finally {
      if (stamp === generation.current) setBusy(false);
    }
  }

  async function command(action: string, hyperliquid: HyperliquidCommand) {
    if (writing.current) return;
    const stamp = generation.current;
    writing.current = true;
    setBusy(true);
    setError("");
    try {
      const current = await core.request<AgentView>(
        `agents?profile=${encodeURIComponent(profile)}`,
      );
      const next = await core.request<AgentView>("agents", "POST", {
        action,
        profileId: profile,
        revision: current.revision,
        hyperliquid,
      });
      if (stamp !== generation.current) return;
      setView(next.hyperliquid);
      setReview(
        action === "hyperliquid-review" ? next.hyperliquid?.review : undefined,
      );
      await agents.refresh();
    } catch (e) {
      if (stamp === generation.current)
        setError(e instanceof Error ? e.message : t("Unavailable"));
    } finally {
      if (stamp === generation.current) setBusy(false);
      writing.current = false;
    }
  }

  const parsedLev = Number.parseInt(leverage, 10);
  const effectiveAsset = selectedMarket?.symbol ?? asset;

  return (
    <>
      <div className="section-head">
        <h1>{t("Hyperliquid")}</h1>
        <div className="row-actions">
          <Badge>
            {t(
              active?.activationEnabled
                ? "Execution active"
                : "Execution disabled",
            )}
          </Badge>
          <Badge>
            {t(active?.network === "mainnet" ? "Mainnet" : "Testnet")}
          </Badge>
          {active?.signerAddress && (
            <Badge>{shortenHex(active.signerAddress)}</Badge>
          )}
          <button
            type="button"
            className="icon-btn"
            aria-label={t("Refresh Hyperliquid")}
            disabled={busy}
            onClick={() => void refresh()}
          >
            <RefreshCw size={16} />
          </button>
        </div>
      </div>

      {error && <p role="alert">{error}</p>}

      {markets.length > 0 && (
        <section className="hyperliquid-markets" aria-label={t("Markets")}>
          {markets.map((m) => (
            <button
              key={m.symbol}
              type="button"
              className="hyperliquid-market-btn"
              aria-pressed={effectiveAsset === m.symbol}
              disabled={busy}
              onClick={() => {
                setAsset(m.symbol);
                setLimitPrice(m.markPrice);
                if (m.onlyIsolated) setIsCross(false);
                setReview(undefined);
              }}
            >
              <div className="hyperliquid-market-top">
                <strong>{m.symbol}</strong>
                <span>${m.markPrice}</span>
              </div>
              <div className="hyperliquid-market-meta">
                <span>
                  {t("Funding")}: {formatFundingRate(m.fundingRate)}
                </span>
                <span>{m.maxLeverage}x</span>
              </div>
            </button>
          ))}
        </section>
      )}

      <div className="hyperliquid-grid">
        <div className="hyperliquid-main">
          <section className="panel" aria-label={t("Margin")}>
            <div className="detail-grid">
              <div>
                <small>{t("Account value")}</small>
                <strong>${active?.account?.accountValue ?? "0.00"}</strong>
              </div>
              <div>
                <small>{t("Margin used")}</small>
                <strong>${active?.account?.totalMarginUsed ?? "0.00"}</strong>
              </div>
              <div>
                <small>{t("Withdrawable")}</small>
                <strong>${active?.account?.withdrawable ?? "0.00"}</strong>
              </div>
              <div>
                <small>{t("Max notional")}</small>
                <strong>
                  {formatMicroUSD(active?.policy.maxNotionalMicroUsd)}
                </strong>
              </div>
              <div>
                <small>{t("Max leverage")}</small>
                <strong>{active?.policy.maxLeverage ?? 0}x</strong>
              </div>
              <div>
                <small>{t("Price collar")}</small>
                <strong>
                  {(
                    (active?.policy.maxOrderPriceBpsDeviation ?? 0) / 100
                  ).toFixed(1)}
                  %
                </strong>
              </div>
            </div>
          </section>

          <section className="panel" aria-label={t("Positions")}>
            <h2>{t("Positions")}</h2>
            {active?.account?.positions &&
            active.account.positions.length > 0 ? (
              <div className="hyperliquid-table-wrap">
                <table className="hyperliquid-table">
                  <thead>
                    <tr>
                      <th>{t("Symbol")}</th>
                      <th>{t("Size")}</th>
                      <th>{t("Entry price")}</th>
                      <th>{t("Position value")}</th>
                      <th>{t("PnL")}</th>
                      <th>{t("Leverage")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {active.account.positions.map((pos) => (
                      <tr key={pos.symbol}>
                        <td>{pos.symbol}</td>
                        <td className="mono">{pos.size}</td>
                        <td className="mono">${pos.entryPrice}</td>
                        <td className="mono">${pos.positionValue}</td>
                        <td className="mono">${pos.unrealizedPnl}</td>
                        <td>
                          {pos.leverage}x ({pos.marginMode})
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <p className="muted">{t("No open positions")}</p>
            )}
          </section>

          <section className="panel" aria-label={t("Controlled orders")}>
            <h2>{t("Controlled orders")}</h2>
            {active?.orders && active.orders.length > 0 ? (
              <div className="hyperliquid-table-wrap">
                <table className="hyperliquid-table">
                  <thead>
                    <tr>
                      <th>{t("Symbol")}</th>
                      <th>{t("Side")}</th>
                      <th>{t("Limit price")}</th>
                      <th>{t("Size")}</th>
                      <th>{t("Leverage")}</th>
                      <th>{t("EIP-712 digest")}</th>
                      <th>{t("Status")}</th>
                      <th>{t("Order")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {active.orders.map((ord) => (
                      <tr key={ord.id}>
                        <td>{ord.asset}</td>
                        <td>{t(ord.isBuy ? "Long" : "Short")}</td>
                        <td className="mono">${ord.limitPrice}</td>
                        <td className="mono">{ord.size}</td>
                        <td>{ord.leverage}x</td>
                        <td className="mono">
                          {shortenHex(ord.typedDataDigest)}
                        </td>
                        <td>
                          <Badge>{t(hyperliquidOrderState(ord.state))}</Badge>
                        </td>
                        <td>
                          <div className="row-actions">
                            {(ord.orderId > 0 ||
                              Boolean(ord.clientOrderId)) && (
                              <button
                                type="button"
                                className="text-button"
                                disabled={busy}
                                onClick={() =>
                                  void command("hyperliquid-reconcile", {
                                    orderId: ord.orderId,
                                    ...(ord.clientOrderId
                                      ? { clientOrderId: ord.clientOrderId }
                                      : {}),
                                  })
                                }
                              >
                                {t("Reconcile")}
                              </button>
                            )}
                            {ord.orderId > 0 &&
                              ord.state !== "cancelled" &&
                              ord.state !== "canceled" && (
                                <button
                                  type="button"
                                  className="text-button"
                                  disabled={busy}
                                  onClick={() =>
                                    void command("hyperliquid-cancel", {
                                      orderId: ord.orderId,
                                    })
                                  }
                                >
                                  {t("Cancel")}
                                </button>
                              )}
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <p className="muted">{t("No controlled orders")}</p>
            )}
          </section>
        </div>

        <section className="panel hyperliquid-ticket" aria-label={t("Order")}>
          <h2>{t("Order")}</h2>
          <div
            className="segmented-control"
            role="group"
            aria-label={t("Side")}
          >
            <button
              type="button"
              aria-pressed={isBuy}
              disabled={busy}
              onClick={() => {
                setIsBuy(true);
                setReview(undefined);
              }}
            >
              {t("Long")}
            </button>
            <button
              type="button"
              aria-pressed={!isBuy}
              disabled={busy}
              onClick={() => {
                setIsBuy(false);
                setReview(undefined);
              }}
            >
              {t("Short")}
            </button>
          </div>

          <div
            className="segmented-control"
            role="group"
            aria-label={t("Margin")}
          >
            <button
              type="button"
              aria-pressed={!effectiveCross}
              disabled={busy}
              onClick={() => {
                setIsCross(false);
                setReview(undefined);
              }}
            >
              {t("Isolated")}
            </button>
            <button
              type="button"
              aria-pressed={effectiveCross}
              disabled={busy || isolatedOnly}
              onClick={() => {
                setIsCross(true);
                setReview(undefined);
              }}
            >
              {t("Cross")}
            </button>
          </div>

          <div className="account-form">
            <label>
              {t("Limit price")}
              <input
                aria-label={t("Limit price")}
                value={limitPrice}
                disabled={busy}
                onChange={(e) => {
                  setLimitPrice(e.target.value);
                  setReview(undefined);
                }}
              />
            </label>
            <label>
              {t("Size")}
              <input
                aria-label={t("Size")}
                value={size}
                disabled={busy}
                onChange={(e) => {
                  setSize(e.target.value);
                  setReview(undefined);
                }}
              />
            </label>
            <label>
              {t("Leverage")}
              <input
                aria-label={t("Leverage")}
                value={leverage}
                disabled={busy}
                onChange={(e) => {
                  setLeverage(e.target.value);
                  setReview(undefined);
                }}
              />
            </label>
            <label>
              {t("Time in force")}
              <select
                aria-label={t("Time in force")}
                value={timeInForce}
                disabled={busy}
                onChange={(e) => {
                  setTimeInForce(e.target.value as "Gtc" | "Ioc" | "Alo");
                  setReview(undefined);
                }}
              >
                <option value="Gtc">Gtc</option>
                <option value="Ioc">Ioc</option>
                <option value="Alo">Alo</option>
              </select>
            </label>
          </div>

          <label className="check-row">
            <input
              type="checkbox"
              aria-label={t("Reduce only")}
              checked={reduceOnly}
              disabled={busy}
              onChange={(e) => {
                setReduceOnly(e.target.checked);
                setReview(undefined);
              }}
            />
            {t("Reduce only")}
          </label>

          <div className="row-actions">
            <button
              type="button"
              className="secondary"
              disabled={
                busy ||
                !active?.available ||
                !effectiveAsset ||
                !limitPrice ||
                !size ||
                !Number.isFinite(parsedLev) ||
                parsedLev < 1
              }
              onClick={() =>
                void command("hyperliquid-review", {
                  request: {
                    asset: effectiveAsset,
                    isBuy,
                    limitPrice,
                    size,
                    leverage: parsedLev,
                    isCross: effectiveCross,
                    reduceOnly,
                    timeInForce,
                  },
                })
              }
            >
              {t("Review")}
            </button>
            {review && (
              <button
                type="button"
                className="primary"
                disabled={busy}
                onClick={() =>
                  void command("hyperliquid-submit", { digest: review.digest })
                }
              >
                {t("Submit")}
              </button>
            )}
          </div>

          {review && (
            <div className="memory-card" aria-label={t("Order review")}>
              <dl className="task-facts">
                <dt>{t("Notional")}</dt>
                <dd>{formatMicroUSD(review.preview.notionalMicroUsd)}</dd>
                <dt>{t("Initial margin")}</dt>
                <dd>{formatMicroUSD(review.preview.initialMarginMicroUsd)}</dd>
                <dt>{t("Estimated fee")}</dt>
                <dd>{formatMicroUSD(review.preview.estimatedFeeMicroUsd)}</dd>
                <dt>{t("Deviation")}</dt>
                <dd>{review.preview.priceDeviationBps} bps</dd>
                <dt>{t("EIP-712 digest")}</dt>
                <dd className="mono">
                  {shortenHex(review.preview.typedDataDigest)}
                </dd>
              </dl>
            </div>
          )}
        </section>
      </div>
    </>
  );
}
