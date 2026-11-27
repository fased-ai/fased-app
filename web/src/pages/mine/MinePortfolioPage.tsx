import { useState } from "react";
import { Download, SlidersHorizontal } from "../../icons";
import type { PortfolioResponse } from "../../mining-lv1x";
import type { WalletV2 } from "../../wallets-v2";

type PortfolioTab = "overview" | "claims" | "history" | "sessions" | "receipts";

interface MinePortfolioPageProps {
  portfolio: PortfolioResponse | null;
  wallet: WalletV2 | undefined;
  busy: boolean;
  onClaimAll: () => void;
  onOpenPolicy: () => void;
}

export function MinePortfolioPage({
  portfolio,
  wallet,
  busy,
  onClaimAll,
  onOpenPolicy,
}: MinePortfolioPageProps) {
  const [tab, setTab] = useState<PortfolioTab>("overview");
  const claimItems = portfolio?.claimItems ?? [];
  const history = portfolio?.history ?? [];
  const sessions = portfolio?.sessions ?? [];

  function exportPortfolio() {
    if (!portfolio) return;
    const blob = new Blob([JSON.stringify(portfolio, null, 2)], {
      type: "application/json",
    });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `wen-portfolio-${portfolio.walletId || "devnet"}.json`;
    a.click();
    window.setTimeout(() => URL.revokeObjectURL(url), 1000);
  }

  return (
    <div className="mine-subpage" aria-label="Mining Portfolio">
      <div className="mine-kpi-strip">
        <div className="wt-kpi-box">
          <span>Wallet SOL</span>
          <strong>{(portfolio?.solBalanceSol ?? 0).toFixed(4)} SOL</strong>
        </div>
        <div className="wt-kpi-box">
          <span>Wallet WENSAT</span>
          <strong>{(portfolio?.satBalance ?? 0).toFixed(2)} SAT</strong>
        </div>
        <div className="wt-kpi-box">
          <span>Wallet cbBTC</span>
          <strong>{(portfolio?.btcBalance ?? 0).toFixed(6)} BTC</strong>
        </div>
        <div className="wt-kpi-box">
          <span>Claimable Total</span>
          <strong>
            {(portfolio?.claimableSol ?? 0).toFixed(3)} SOL ·{" "}
            {(portfolio?.vestedSat ?? 0).toFixed(1)} SAT
          </strong>
        </div>
      </div>

      <div className="wt-wpanel mine-subpanel">
        <div className="mine-subpage-head">
          <div
            className="wt-tabs-underline"
            role="tablist"
            aria-label="Portfolio tabs"
          >
            {(
              [
                ["overview", "Overview"],
                ["claims", `Claims (${claimItems.length})`],
                ["history", `History (${history.length})`],
                ["sessions", `Sessions (${sessions.length})`],
                ["receipts", "Receipts"],
              ] as const
            ).map(([k, lbl]) => (
              <button
                key={k}
                type="button"
                role="tab"
                aria-selected={tab === k}
                onClick={() => setTab(k)}
              >
                {lbl}
              </button>
            ))}
          </div>

          <div className="mine-row-actions">
            <button
              type="button"
              className="wt-btn-soft"
              disabled={!wallet}
              onClick={onOpenPolicy}
            >
              <SlidersHorizontal size={13} aria-hidden="true" />
              <span>Edit Policy</span>
            </button>
            <button
              type="button"
              className="wt-btn-soft"
              disabled={!portfolio}
              onClick={exportPortfolio}
            >
              <Download size={13} aria-hidden="true" />
              <span>Export</span>
            </button>
          </div>
        </div>

        {tab === "overview" && (
          <div className="wt-hairline-table">
            <div className="wt-hairline-row">
              <span>Custody Wallet</span>
              <span className="f-mono">
                {wallet
                  ? `${wallet.name} (${wallet.publicKey})`
                  : "No RoleMining wallet selected"}
              </span>
            </div>
            <div className="wt-hairline-row">
              <span>Approval Policy</span>
              <strong>{wallet?.policy.approvalMode ?? "—"}</strong>
            </div>
            <div className="wt-hairline-row">
              <span>Per-Round / Daily Cap</span>
              <span>
                {wallet
                  ? `${(Number(wallet.policy.solCap.maxPerTxRaw) / 1e9).toFixed(2)} / ${(Number(wallet.policy.solCap.maxDailyRaw) / 1e9).toFixed(2)} SOL`
                  : "—"}
              </span>
            </div>
            <div className="wt-hairline-row">
              <span>Bootstrap Vesting</span>
              <span>
                {(portfolio?.vestingProgressPct ?? 100).toFixed(1)}% unlocked
              </span>
            </div>
          </div>
        )}

        {tab === "claims" && (
          <div className="mine-claims-wrap">
            <button
              type="button"
              className="wt-btn-rose-sm"
              disabled={busy || !wallet || claimItems.length === 0}
              onClick={onClaimAll}
            >
              Claim All Ready Rewards
            </button>
            <div className="mine-table-scroll">
              <table className="mine-table">
                <thead>
                  <tr>
                    <th>Round</th>
                    <th>Range</th>
                    <th>SOL Ready</th>
                    <th>SAT Vested</th>
                    <th>cbBTC</th>
                  </tr>
                </thead>
                <tbody>
                  {claimItems.map((c) => (
                    <tr key={`${c.roundSlot}-${c.entryRow}`}>
                      <td>#{c.roundSlot}</td>
                      <td>{c.selectionCode}</td>
                      <td>{(Number(c.solLamports) / 1e9).toFixed(4)}</td>
                      <td>{(Number(c.vestedSatAtoms) / 1e9).toFixed(2)}</td>
                      <td>{(Number(c.btcAtoms) / 1e8).toFixed(6)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        )}

        {(tab === "history" || tab === "receipts") && (
          <div className="mine-table-scroll">
            <table className="mine-table">
              <thead>
                <tr>
                  <th>Action</th>
                  <th>Round</th>
                  <th>Cluster</th>
                  <th>Status</th>
                  <th>Tx Signature</th>
                </tr>
              </thead>
              <tbody>
                {history.map((h) => (
                  <tr key={h.id}>
                    <td>{h.kind}</td>
                    <td>{h.roundSlot > 0 ? `#${h.roundSlot}` : "Staking"}</td>
                    <td>{h.cluster}</td>
                    <td>{h.status}</td>
                    <td className="f-mono">{h.txSignature || "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        {tab === "sessions" && (
          <div className="mine-table-scroll">
            <table className="mine-table">
              <thead>
                <tr>
                  <th>ID</th>
                  <th>Strategy</th>
                  <th>Mode</th>
                  <th>Per Round</th>
                  <th>Status</th>
                </tr>
              </thead>
              <tbody>
                {sessions.map((s) => (
                  <tr key={s.id}>
                    <td className="f-mono">{s.id}</td>
                    <td>{s.strategyId}</td>
                    <td>{s.mode}</td>
                    <td>{s.perRoundSol.toFixed(2)} SOL</td>
                    <td>{s.status}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
