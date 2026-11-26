import { useEffect, useState } from "react";
import { ChevronDown, Shield } from "../../icons";
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

const STRATEGY_TILES: readonly {
  id: TradeStrategy;
  label: string;
}[] = [
  { id: "market", label: "Market" },
  { id: "limit", label: "Limit" },
  { id: "bracket", label: "Bracket" },
  { id: "trailing_atr", label: "Trailing ATR" },
  { id: "grid", label: "Grid" },
  { id: "dca", label: "DCA" },
];

function StrategyGlyph({ id }: { id: TradeStrategy }) {
  if (id === "market") {
    return (
      <svg viewBox="0 0 28 16" className="wt-strat-svg" aria-hidden="true">
        <rect x="3" y="4" width="3" height="10" rx="1" />
        <rect x="9" y="2" width="3" height="12" rx="1" />
        <rect x="15" y="5" width="3" height="9" rx="1" />
        <rect x="21" y="1" width="3" height="13" rx="1" />
      </svg>
    );
  }
  if (id === "limit") {
    return (
      <svg viewBox="0 0 28 16" className="wt-strat-svg" aria-hidden="true">
        <line x1="2" y1="8" x2="26" y2="8" strokeWidth="1.8" strokeDasharray="3 2" />
        <circle cx="14" cy="8" r="2.8" />
      </svg>
    );
  }
  if (id === "bracket") {
    return (
      <svg viewBox="0 0 28 16" className="wt-strat-svg" aria-hidden="true">
        <line x1="4" y1="3" x2="24" y2="3" strokeWidth="1.5" />
        <line x1="4" y1="8" x2="24" y2="8" strokeWidth="2" />
        <line x1="4" y1="13" x2="24" y2="13" strokeWidth="1.5" />
      </svg>
    );
  }
  if (id === "trailing_atr") {
    return (
      <svg viewBox="0 0 28 16" className="wt-strat-svg" aria-hidden="true">
        <polyline points="2,13 10,9 18,5 26,3" fill="none" strokeWidth="1.8" />
        <polyline points="2,15 10,12 18,9 26,8" fill="none" strokeWidth="1.2" opacity="0.6" />
      </svg>
    );
  }
  if (id === "grid") {
    return (
      <svg viewBox="0 0 28 16" className="wt-strat-svg" aria-hidden="true">
        <rect x="3" y="10" width="4" height="4" rx="1" />
        <rect x="12" y="6" width="4" height="8" rx="1" />
        <rect x="21" y="2" width="4" height="12" rx="1" />
      </svg>
    );
  }
  return (
    <svg viewBox="0 0 28 16" className="wt-strat-svg" aria-hidden="true">
      <rect x="3" y="6" width="3" height="8" rx="1" />
      <rect x="9" y="6" width="3" height="8" rx="1" />
      <rect x="15" y="6" width="3" height="8" rx="1" />
      <rect x="21" y="6" width="3" height="8" rx="1" />
    </svg>
  );
}

export function OrderExecutionWidget({
  venue,
  symbol,
  interval,
  trade,
  accounts = [],
  busy = false,
  error = "",
  initialMode = "manual",
  onSubmitTrade,
}: {
  venue: string;
  symbol: string;
  interval: Interval;
  trade: TradeSnapshot | null;
  accounts?: TradeAccountOption[];
  busy?: boolean;
  error?: string;
  initialMode?: TradeMode;
  onSubmitTrade: (req: TradeOrderRequest) => void;
}) {
  const { t } = useUI();
  const [accountId, setAccountId] = useState("paper");
  const [accountMenuOpen, setAccountMenuOpen] = useState(false);
  const [mode, setMode] = useState<TradeMode>(initialMode);
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
    setMode(initialMode);
  }, [initialMode]);

  const accountOptions: TradeAccountOption[] =
    accounts.length > 0
      ? accounts
      : [{ id: "paper", label: "Paper", kind: "paper", executionMode: "paper" }];

  useEffect(() => {
    if (!accountOptions.some((a) => a.id === accountId)) {
      setAccountId(accountOptions[0].id);
    }
  }, [accountOptions, accountId]);

  const selectedAccount =
    accountOptions.find((a) => a.id === accountId) ?? accountOptions[0];
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
    <form
      className="wt-composer-form"
      data-testid="trade-ticket-pane"
      onSubmit={handleSubmit}
    >
      <div className="wt-field-row">
        <span className="wt-field-label">{t("Account")}</span>
        <div className="trade-tile-menu-anchor">
          <button
            type="button"
            className="wt-pill-btn"
            aria-label={t("Account")}
            aria-expanded={accountMenuOpen}
            onClick={() => setAccountMenuOpen((o) => !o)}
          >
            <span>
              {selectedAccount.id === "paper"
                ? t("Paper")
                : selectedAccount.label}
            </span>
            {selectedAccount.role && (
              <span className="wt-role-chip">{selectedAccount.role}</span>
            )}
            <ChevronDown size={13} aria-hidden="true" />
          </button>
          {accountMenuOpen && (
            <div className="trade-tile-popover is-list wt-account-popover">
              {accountOptions.map((a) => (
                <button
                  key={a.id}
                  type="button"
                  className={accountId === a.id ? "is-selected" : ""}
                  onClick={() => {
                    setAccountId(a.id);
                    setAccountMenuOpen(false);
                  }}
                >
                  <span>{a.id === "paper" ? t("Paper") : a.label}</span>
                  <span className="mono wt-muted-mini">
                    {a.role || a.executionMode}
                  </span>
                </button>
              ))}
            </div>
          )}
        </div>
      </div>

      <div className="wt-seg-bar" role="group" aria-label={t("Order side")}>
        <button
          type="button"
          className={side === "buy" ? "is-buy" : ""}
          aria-pressed={side === "buy"}
          onClick={() => setSide("buy")}
        >
          {t("Buy")}
        </button>
        <button
          type="button"
          className={side === "sell" ? "is-sell" : ""}
          aria-pressed={side === "sell"}
          onClick={() => setSide("sell")}
        >
          {t("Sell")}
        </button>
      </div>

      <div className="wt-strat-head">
        <span className="wt-field-label">{t("Strategy")}</span>
        <button
          type="button"
          className={`trade-mini-btn ${strategy === "twap" ? "is-selected" : ""}`}
          onClick={() => setStrategy("twap")}
        >
          {t("TWAP")}
        </button>
      </div>

      <div className="wt-strat-grid" role="group" aria-label={t("Strategy")}>
        {STRATEGY_TILES.map((st) => (
          <button
            key={st.id}
            type="button"
            className={`wt-strat-tile ${strategy === st.id ? "is-selected" : ""}`}
            aria-pressed={strategy === st.id}
            onClick={() => setStrategy(st.id)}
          >
            <StrategyGlyph id={st.id} />
            <span>{t(st.label)}</span>
          </button>
        ))}
      </div>

      <div className="wt-amount-card">
        <div className="wt-amount-top">
          <span className="wt-field-label">{t("Quantity")}</span>
          <div className="wt-chip-group">
            {["0.25", "0.5", "1", "5"].map((preset) => (
              <button
                key={preset}
                type="button"
                className={`trade-mini-btn ${qty === preset ? "is-selected" : ""}`}
                onClick={() => setQty(preset)}
              >
                {preset}
              </button>
            ))}
          </div>
        </div>
        <div className="wt-amount-input-row">
          <input
            aria-label={t("Quantity")}
            type="number"
            step="any"
            min="0"
            className="wt-hero-input mono"
            value={qty}
            onChange={(e) => setQty(e.target.value)}
          />
          <span className="wt-unit-badge mono">{symbol}</span>
        </div>
      </div>

      {strategy !== "market" && (
        <div className="wt-inline-fields">
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
        </div>
      )}

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
        <div className="wt-inline-fields">
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
        </div>
      )}

      {strategy === "grid" && (
        <div className="wt-inline-fields">
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
          </div>
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

      <div className="wt-kv-rows">
        <div>
          <span>{t("Max order")}</span>
          <strong className="mono">
            {formatCurrencyUSD(trade?.policy.maxOrderNotionalUsd ?? 25000)}
          </strong>
        </div>
        <div>
          <span>{t("Daily loss")}</span>
          <strong className="mono">
            {`${formatCurrencyUSD(trade?.policy.dailyLossUsd ?? 0)} / ${formatCurrencyUSD(trade?.policy.maxDailyLossUsd ?? 2000)}`}
          </strong>
        </div>
      </div>

      {error && <p className="trade-pane-error">{error}</p>}

      <button
        type="submit"
        className="wt-btn-rose"
        disabled={busy || Boolean(trade?.killSwitchActive)}
      >
        {trade?.killSwitchActive ? (
          <>
            <Shield size={15} aria-hidden="true" />
            <span>{t("Kill switch")}</span>
          </>
        ) : (
          <span>
            {`${t(side === "buy" ? "Buy" : "Sell")} ${symbol} · ${t("Submit order")} →`}
          </span>
        )}
      </button>
    </form>
  );
}
