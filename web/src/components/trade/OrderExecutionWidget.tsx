import { useEffect, useState } from "react";
import { useUI } from "../../ui-preferences";
import { formatCurrencyUSD } from "../../quantlab-desk";
import type { Interval } from "../../market-data";
import type {
  TradeAccountOption,
  TradeMode,
  TradeOrderRequest,
  TradeSide,
  TradeSnapshot,
  TradeStrategy,
} from "../../trade-engine";

export function OrderExecutionWidget({
  venue,
  symbol,
  interval,
  trade,
  accounts = [],
  busy = false,
  error = "",
  onSubmitTrade,
}: {
  venue: string;
  symbol: string;
  interval: Interval;
  trade: TradeSnapshot | null;
  accounts?: TradeAccountOption[];
  busy?: boolean;
  error?: string;
  onSubmitTrade: (req: TradeOrderRequest) => void;
}) {
  const { t } = useUI();
  const [accountId, setAccountId] = useState("paper");
  const [mode, setMode] = useState<TradeMode>("manual");
  const [strategy, setStrategy] = useState<TradeStrategy>("market");
  const [side, setSide] = useState<TradeSide>("buy");
  const [qty, setQty] = useState("1");
  const [entryPrice, setEntryPrice] = useState("");
  const [stopPrice, setStopPrice] = useState("");
  const [targetPrice, setTargetPrice] = useState("");
  const [atrMult, setAtrMult] = useState("1.5");
  const [gridLower, setGridLower] = useState("");
  const [gridUpper, setGridUpper] = useState("");
  const [gridLevels, setGridLevels] = useState("4");
  const [slices, setSlices] = useState("4");
  const [cadence, setCadence] = useState("60");

  useEffect(() => {
    if (accounts.length > 0 && !accounts.some((a) => a.id === accountId)) {
      setAccountId(accounts[0].id);
    }
  }, [accounts, accountId]);

  const selectedAccount = accounts.find((a) => a.id === accountId);
  const execMode =
    selectedAccount?.executionMode === "live" ? "live" : "paper";

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    const q = Number(qty);
    const req: TradeOrderRequest = {
      venue,
      symbol,
      interval,
      mode,
      strategy,
      side,
      quantity: Number.isFinite(q) ? q : 0,
      accountId,
      executionMode: execMode,
    };
    const ep = Number(entryPrice);
    if (Number.isFinite(ep) && ep > 0) req.entryPrice = ep;
    if (strategy === "bracket") {
      const sp = Number(stopPrice);
      const tp = Number(targetPrice);
      if (Number.isFinite(sp) && sp > 0) req.stopPrice = sp;
      if (Number.isFinite(tp) && tp > 0) req.targetPrice = tp;
    } else if (strategy === "trailing_atr") {
      const m = Number(atrMult);
      if (Number.isFinite(m) && m > 0) req.atrMultiplier = m;
    } else if (strategy === "grid") {
      const lo = Number(gridLower);
      const hi = Number(gridUpper);
      const lv = Number.parseInt(gridLevels, 10);
      if (Number.isFinite(lo) && lo > 0) req.gridLower = lo;
      if (Number.isFinite(hi) && hi > 0) req.gridUpper = hi;
      if (Number.isFinite(lv) && lv > 0) req.gridLevels = lv;
    } else if (strategy === "dca" || strategy === "twap") {
      const sl = Number.parseInt(slices, 10);
      const cad = Number.parseInt(cadence, 10);
      if (Number.isFinite(sl) && sl > 0) req.slices = sl;
      if (Number.isFinite(cad) && cad > 0) req.intervalSeconds = cad;
    }
    onSubmitTrade(req);
  }

  return (
    <div className="trade-ticket-form" data-testid="trade-ticket-pane">
      <div className="trade-desk-head">
        <span className="trade-source-pill">
          {t(
            trade?.killSwitchActive
              ? "Kill switch"
              : execMode === "live"
                ? "Live"
                : "Paper",
          )}
        </span>
        <span className="subtle">
          {venue} · {symbol}
        </span>
      </div>

      <form className="trade-ticket-grid" onSubmit={handleSubmit}>
        <label>
          <span>{t("Account")}</span>
          <select
            aria-label={t("Account")}
            value={accountId}
            onChange={(e) => setAccountId(e.target.value)}
          >
            <option value="paper">{t("Paper")}</option>
            {accounts
              .filter((a) => a.id !== "paper")
              .map((a) => (
                <option key={a.id} value={a.id}>
                  {a.label}
                </option>
              ))}
          </select>
        </label>

        <div className="trade-ticket-pair">
          <label>
            <span>{t("Mode")}</span>
            <select
              aria-label={t("Execution mode")}
              value={mode}
              onChange={(e) => setMode(e.target.value as TradeMode)}
            >
              <option value="manual">{t("Manual")}</option>
              <option value="semi_auto">{t("Semi-Auto")}</option>
            </select>
          </label>
          <label>
            <span>{t("Strategy")}</span>
            <select
              aria-label={t("Strategy")}
              value={strategy}
              onChange={(e) => setStrategy(e.target.value as TradeStrategy)}
            >
              <option value="market">{t("Market")}</option>
              <option value="limit">{t("Limit")}</option>
              <option value="bracket">{t("Bracket")}</option>
              <option value="trailing_atr">{t("Trailing ATR")}</option>
              <option value="grid">{t("Grid")}</option>
              <option value="dca">{t("DCA")}</option>
              <option value="twap">{t("TWAP")}</option>
            </select>
          </label>
        </div>

        <div
          className="trade-side-toggle"
          role="group"
          aria-label={t("Order side")}
        >
          <button
            type="button"
            className={side === "buy" ? "is-selected is-buy" : ""}
            aria-pressed={side === "buy"}
            onClick={() => setSide("buy")}
          >
            {t("Buy")}
          </button>
          <button
            type="button"
            className={side === "sell" ? "is-selected is-sell" : ""}
            aria-pressed={side === "sell"}
            onClick={() => setSide("sell")}
          >
            {t("Sell")}
          </button>
        </div>

        <div className="trade-ticket-pair">
          <label>
            <span>{t("Quantity")}</span>
            <input
              aria-label={t("Quantity")}
              type="number"
              step="any"
              min="0"
              value={qty}
              onChange={(e) => setQty(e.target.value)}
            />
          </label>
          {strategy !== "market" && (
            <label>
              <span>{t("Entry price")}</span>
              <input
                aria-label={t("Entry price")}
                type="number"
                step="any"
                min="0"
                placeholder="0.00"
                value={entryPrice}
                onChange={(e) => setEntryPrice(e.target.value)}
              />
            </label>
          )}
        </div>

        {strategy === "bracket" && (
          <div className="trade-ticket-pair">
            <label>
              <span>{t("Stop price")}</span>
              <input
                aria-label={t("Stop price")}
                type="number"
                step="any"
                min="0"
                value={stopPrice}
                onChange={(e) => setStopPrice(e.target.value)}
              />
            </label>
            <label>
              <span>{t("Target price")}</span>
              <input
                aria-label={t("Target price")}
                type="number"
                step="any"
                min="0"
                value={targetPrice}
                onChange={(e) => setTargetPrice(e.target.value)}
              />
            </label>
          </div>
        )}

        {strategy === "trailing_atr" && (
          <label>
            <span>{t("ATR multiplier")}</span>
            <input
              aria-label={t("ATR multiplier")}
              type="number"
              step="0.1"
              min="0.1"
              value={atrMult}
              onChange={(e) => setAtrMult(e.target.value)}
            />
          </label>
        )}

        {strategy === "grid" && (
          <div className="trade-ticket-pair">
            <label>
              <span>{t("Grid lower")}</span>
              <input
                aria-label={t("Grid lower")}
                type="number"
                step="any"
                min="0"
                value={gridLower}
                onChange={(e) => setGridLower(e.target.value)}
              />
            </label>
            <label>
              <span>{t("Grid upper")}</span>
              <input
                aria-label={t("Grid upper")}
                type="number"
                step="any"
                min="0"
                value={gridUpper}
                onChange={(e) => setGridUpper(e.target.value)}
              />
            </label>
            <label>
              <span>{t("Grid levels")}</span>
              <input
                aria-label={t("Grid levels")}
                type="number"
                min="2"
                max="20"
                value={gridLevels}
                onChange={(e) => setGridLevels(e.target.value)}
              />
            </label>
          </div>
        )}

        {(strategy === "dca" || strategy === "twap") && (
          <div className="trade-ticket-pair">
            <label>
              <span>{t("Slices")}</span>
              <input
                aria-label={t("Slices")}
                type="number"
                min="2"
                max="50"
                value={slices}
                onChange={(e) => setSlices(e.target.value)}
              />
            </label>
            <label>
              <span>{t("Cadence (sec)")}</span>
              <input
                aria-label={t("Cadence (sec)")}
                type="number"
                min="5"
                max="86400"
                value={cadence}
                onChange={(e) => setCadence(e.target.value)}
              />
            </label>
          </div>
        )}

        <dl className="trade-metric-grid">
          <div>
            <dt>{t("Max order")}</dt>
            <dd>
              {formatCurrencyUSD(trade?.policy.maxOrderNotionalUsd ?? 25000)}
            </dd>
          </div>
          <div>
            <dt>{t("Daily loss")}</dt>
            <dd>{`${formatCurrencyUSD(trade?.policy.dailyLossUsd ?? 0)} / ${formatCurrencyUSD(trade?.policy.maxDailyLossUsd ?? 2000)}`}</dd>
          </div>
        </dl>

        {error && <p className="trade-pane-error">{error}</p>}

        <button
          type="submit"
          className="primary"
          disabled={busy || Boolean(trade?.killSwitchActive)}
        >
          {t("Submit order")}
        </button>
      </form>
    </div>
  );
}
