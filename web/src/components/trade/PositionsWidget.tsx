import { useState } from "react";
import { useUI } from "../../ui-preferences";
import {
  formatCurrencyUSD,
  formatPrice,
  type DeskSnapshot,
} from "../../quantlab-desk";
import type { TradeSnapshot } from "../../trade-engine";

type PositionsTab = "all" | "plans" | "positions" | "tickets";

export function PositionsWidget({
  trade,
  desk,
  tradeBusy,
  deskBusy,
  onEvaluateTrades,
  onCancelTrade,
  onApproveTicket,
  onRejectTicket,
  onClosePosition,
  onOpenLab,
}: {
  trade: TradeSnapshot | null;
  desk: DeskSnapshot | null;
  tradeBusy: boolean;
  deskBusy: boolean;
  onEvaluateTrades: () => void;
  onCancelTrade: (id: string) => void;
  onApproveTicket: (id: string) => void;
  onRejectTicket: (id: string) => void;
  onClosePosition: (id: string) => void;
  onOpenLab: () => void;
}) {
  const { t } = useUI();
  const [filter, setFilter] = useState<PositionsTab>("all");

  const pendingTickets = (desk?.tickets ?? []).filter(
    (tk) => tk.status === "PENDING_PM",
  );
  const openPositions = (desk?.positions ?? []).filter(
    (p) => p.status === "OPEN",
  );
  const plans = trade?.plans ?? [];

  return (
    <div className="trade-positions-widget" data-testid="trade-bottom-dock">
      <div className="trade-bottom-head">
        <div className="trade-bottom-stats">
          <span className="trade-source-pill">
            {t(desk?.limits.mode === "paper" ? "Paper" : "Shadow")}
          </span>
          <span>
            {`${t("Equity")}: ${formatCurrencyUSD(desk?.account.equityUsd ?? 100000)}`}
          </span>
          <span>{`${t("Open positions")}: ${openPositions.length}`}</span>
          <span>{`${t("Open plans")}: ${trade?.activeCount ?? 0}`}</span>
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

      <div className="trade-tabs" role="tablist">
        {(
          [
            { id: "all", label: "All" },
            { id: "plans", label: "Plans" },
            { id: "positions", label: "Positions" },
            { id: "tickets", label: "Tickets" },
          ] as const
        ).map((tab) => (
          <button
            key={tab.id}
            type="button"
            role="tab"
            aria-selected={filter === tab.id}
            className={filter === tab.id ? "is-selected" : ""}
            onClick={() => setFilter(tab.id)}
          >
            {t(tab.label)}
          </button>
        ))}
      </div>

      <div className="trade-bottom-rows">
        {(filter === "all" || filter === "plans") &&
          plans.slice(0, 12).map((pl) => (
            <div className="trade-bottom-row" key={pl.id}>
              <span>
                <span className="trade-source-pill">
                  {(pl.source || pl.mode || "manual").toUpperCase()}
                </span>{" "}
                {`${pl.venue.toUpperCase()} ${pl.symbol} ${pl.strategy.toUpperCase()} ${pl.side.toUpperCase()} · ${pl.quantity} · ${pl.status.toUpperCase()}`}
              </span>
              {pl.status === "active" && (
                <button
                  type="button"
                  className="trade-mini-btn"
                  disabled={tradeBusy}
                  onClick={() => onCancelTrade(pl.id)}
                >
                  {t("Cancel plan")}
                </button>
              )}
            </div>
          ))}

        {(filter === "all" || filter === "tickets") &&
          pendingTickets.map((tk) => (
            <div className="trade-bottom-row" key={tk.id}>
              <span>
                <span className="trade-source-pill">DESK</span>{" "}
                {`${tk.symbol} ${tk.side.toUpperCase()} @ ${formatPrice(tk.entryPrice)} · R:R ${tk.riskReward.toFixed(2)}`}
              </span>
              <div className="trade-desk-actions">
                <button
                  type="button"
                  className="trade-mini-btn trade-desk-run"
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

        {(filter === "all" || filter === "positions") &&
          openPositions.map((pos) => (
            <div className="trade-bottom-row" key={pos.id}>
              <span>
                {`${pos.symbol} ${pos.side.toUpperCase()} · ${pos.quantity} @ ${formatPrice(pos.entryPrice)} · ${formatCurrencyUSD(pos.unrealizedPnlUsd)}`}
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
          ))}

        {plans.length === 0 &&
          pendingTickets.length === 0 &&
          openPositions.length === 0 && <p className="subtle">{t("None")}</p>}
      </div>
    </div>
  );
}
