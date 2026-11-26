import { useState } from "react";
import { useUI } from "../../ui-preferences";
import {
  formatCurrencyUSD,
  formatPrice,
  type DeskSnapshot,
} from "../../quantlab-desk";
import type { LabScript } from "../../quantlab-desk";
import type { Interval } from "../../market-data";
import type {
  TradeAccountOption,
  TradeBot,
  TradeSnapshot,
} from "../../trade-engine";
import type { OrderflowTab } from "../../market-data";
import type { CrosshairBus, FasedStreamClient } from "../../stream-store";
import { BotAgentWidget } from "./BotAgentWidget";
import { OrderflowPane } from "./OrderflowPane";

type DockTab = "positions" | "plans" | "bots" | "orderflow" | "history";

export function PositionsWidget({
  venue = "hyperliquid",
  symbol = "BTC",
  interval = "15m",
  trade,
  desk,
  bots = [],
  scripts = [],
  accounts = [],
  agentProfiles = [],
  orderflowTab = "dom",
  streamClient,
  crosshairBus,
  syncCrosshair = true,
  tradeBusy,
  deskBusy,
  onChangeOrderflowTab,
  onEvaluateTrades,
  onCancelTrade,
  onApproveTicket,
  onRejectTicket,
  onClosePosition,
  onCreateBot,
  onBotAction,
  onDeleteBot,
  onOpenLab,
}: {
  venue?: string;
  symbol?: string;
  interval?: Interval;
  trade: TradeSnapshot | null;
  desk: DeskSnapshot | null;
  bots?: TradeBot[];
  scripts?: LabScript[];
  accounts?: TradeAccountOption[];
  agentProfiles?: { id: string; name: string }[];
  orderflowTab?: OrderflowTab;
  streamClient?: FasedStreamClient;
  crosshairBus?: CrosshairBus;
  syncCrosshair?: boolean;
  tradeBusy: boolean;
  deskBusy: boolean;
  onChangeOrderflowTab?: (tab: OrderflowTab) => void;
  onEvaluateTrades: () => void;
  onCancelTrade: (id: string) => void;
  onApproveTicket: (id: string) => void;
  onRejectTicket: (id: string) => void;
  onClosePosition: (id: string) => void;
  onCreateBot?: (req: {
    name: string;
    kind: "fasedscript" | "ai_agent";
    accountId: string;
    venue: string;
    symbol: string;
    interval: string;
    scriptId?: string;
    agentId?: string;
    maxNotionalUsd: number;
  }) => void;
  onBotAction?: (id: string, action: "start" | "pause" | "stop") => void;
  onDeleteBot?: (id: string) => void;
  onOpenLab: () => void;
}) {
  const { t } = useUI();
  const [tab, setTab] = useState<DockTab>("positions");

  const pendingTickets = (desk?.tickets ?? []).filter(
    (tk) => tk.status === "PENDING_PM",
  );
  const openPositions = (desk?.positions ?? []).filter(
    (p) => p.status === "OPEN",
  );
  const allPlans = trade?.plans ?? [];
  const activePlans = allPlans.filter(
    (p) =>
      p.status === "active" || p.status === "pending" || p.status === "queued",
  );
  const historyPlans = allPlans.filter(
    (p) =>
      p.status !== "active" && p.status !== "pending" && p.status !== "queued",
  );

  return (
    <div
      className="trade-positions-widget wt-dock"
      data-testid="trade-bottom-dock"
    >
      <div className="wt-dock-top">
        <div className="wt-tabs-underline" role="tablist">
          {(
            [
              {
                id: "positions",
                label: "Positions",
                count: openPositions.length + pendingTickets.length,
              },
              { id: "plans", label: "Open Orders", count: activePlans.length },
              { id: "bots", label: "Bots", count: bots.length },
              { id: "orderflow", label: "DOM & Tape" },
              { id: "history", label: "History", count: historyPlans.length },
            ] as const
          ).map((item) => (
            <button
              key={item.id}
              type="button"
              role="tab"
              aria-selected={tab === item.id}
              className={tab === item.id ? "is-selected" : ""}
              onClick={() => setTab(item.id)}
            >
              <span>{t(item.label)}</span>
              {"count" in item && item.count > 0 && (
                <span className="wt-tab-count mono">{item.count}</span>
              )}
            </button>
          ))}
        </div>

        <div className="trade-desk-actions">
          <button
            type="button"
            className="trade-mini-btn"
            disabled={tradeBusy}
            onClick={onEvaluateTrades}
          >
            {t("Evaluate plans")}
          </button>
          <button type="button" className="trade-mini-btn" onClick={onOpenLab}>
            {t("Open Lab")}
          </button>
        </div>
      </div>

      {tab === "positions" && (
        <div className="wt-hairline-table">
          {pendingTickets.map((tk) => (
            <div className="wt-hairline-row" key={tk.id}>
              <span>
                <span className="trade-source-pill">DESK</span>{" "}
                {`${tk.symbol} ${tk.side.toUpperCase()} @ ${formatPrice(tk.entryPrice)} · R:R ${tk.riskReward.toFixed(2)}`}
              </span>
              <div className="trade-desk-actions">
                <button
                  type="button"
                  className="trade-mini-btn is-selected"
                  disabled={deskBusy}
                  onClick={() => onApproveTicket(tk.id)}
                >
                  {t("Approve")}
                </button>
                <button
                  type="button"
                  className="trade-mini-btn"
                  disabled={deskBusy}
                  onClick={() => onRejectTicket(tk.id)}
                >
                  {t("Reject")}
                </button>
              </div>
            </div>
          ))}
          {openPositions.map((pos) => (
            <div className="wt-hairline-row" key={pos.id}>
              <span className="mono">
                {`${pos.symbol} ${pos.side.toUpperCase()} · ${pos.quantity} @ ${formatPrice(pos.entryPrice)}`}
              </span>
              <div className="trade-desk-actions">
                <span
                  className={pos.unrealizedPnlUsd >= 0 ? "wt-pos" : "wt-neg"}
                >
                  {formatCurrencyUSD(pos.unrealizedPnlUsd)}
                </span>
                <button
                  type="button"
                  className="trade-mini-btn"
                  disabled={deskBusy}
                  onClick={() => onClosePosition(pos.id)}
                >
                  {t("Close")}
                </button>
              </div>
            </div>
          ))}
          {openPositions.length === 0 && pendingTickets.length === 0 && (
            <p className="subtle">{t("None")}</p>
          )}
        </div>
      )}

      {tab === "plans" && (
        <div className="wt-hairline-table">
          {activePlans.map((pl) => (
            <div className="wt-hairline-row" key={pl.id}>
              <span className="mono">
                <span className="trade-source-pill">
                  {(pl.source || pl.mode || "manual").toUpperCase()}
                </span>{" "}
                {`${pl.venue.toUpperCase()} ${pl.symbol} ${pl.strategy.toUpperCase()} ${pl.side.toUpperCase()} · ${pl.quantity}`}
              </span>
              <button
                type="button"
                className="trade-mini-btn"
                disabled={tradeBusy}
                onClick={() => onCancelTrade(pl.id)}
              >
                {t("Cancel plan")}
              </button>
            </div>
          ))}
          {activePlans.length === 0 && <p className="subtle">{t("None")}</p>}
        </div>
      )}

      {tab === "bots" && (
        <BotAgentWidget
          venue={venue}
          symbol={symbol}
          interval={interval}
          scripts={scripts}
          bots={bots}
          accounts={accounts}
          agentProfiles={agentProfiles}
          busy={tradeBusy}
          onCreateBot={onCreateBot}
          onBotAction={onBotAction}
          onDeleteBot={onDeleteBot}
        />
      )}

      {tab === "orderflow" && (
        <OrderflowPane
          venue={venue}
          symbol={symbol}
          interval={interval}
          tab={orderflowTab}
          streamClient={streamClient}
          crosshairBus={crosshairBus}
          syncCrosshair={syncCrosshair}
          onChangeTab={(nextTab) => onChangeOrderflowTab?.(nextTab)}
        />
      )}

      {tab === "history" && (
        <div className="wt-hairline-table">
          {historyPlans.slice(0, 20).map((pl) => (
            <div className="wt-hairline-row" key={pl.id}>
              <span className="mono">
                {`${pl.venue.toUpperCase()} ${pl.symbol} ${pl.strategy.toUpperCase()} ${pl.side.toUpperCase()} · ${pl.quantity}`}
              </span>
              <span
                className={`mono ${pl.status === "filled" ? "wt-pos" : "subtle"}`}
              >
                {pl.status.toUpperCase()}
              </span>
            </div>
          ))}
          {historyPlans.length === 0 && <p className="subtle">{t("None")}</p>}
        </div>
      )}
    </div>
  );
}
