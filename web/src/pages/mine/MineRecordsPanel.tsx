import { useState } from "react";
import { Pause, Plus, Trash2 } from "../../icons";
import type {
  ClaimableRoundItem,
  EntryHistoryItem,
  LeaderboardRound,
  PortfolioResponse,
  RoundTelemetry,
  SessionRecord,
  StreakEntry,
} from "../../mining-lv1x";
import { RANGE_NAMES } from "../MineAllocationCurve";

const SPECTRUM = [
  "#E0566F",
  "#D98466",
  "#6F7889",
  "#2FB88C",
  "#33A9B8",
] as const;

export type RecordsTab =
  "current" | "claims" | "history" | "sessions" | "protocol";

interface MineRecordsPanelProps {
  activeTab: RecordsTab;
  onTabChange: (tab: RecordsTab) => void;
  round?: RoundTelemetry;
  weightsBps: number[];
  sessions: SessionRecord[];
  portfolio: PortfolioResponse | null;
  protocolRounds: LeaderboardRound[];
  streak: StreakEntry[];
  cluster: string;
  mainnetGateNote?: string;
  selectedWalletId: string;
  busy: boolean;
  onDepositSession: (id: string, amountSol: number) => void;
  onStopSession: (id: string) => void;
  onCloseSession: (id: string) => void;
  onClaimAsset: (
    asset: "sol" | "sat" | "btc",
    item?: ClaimableRoundItem,
  ) => void;
  onClaimAll: () => void;
  onLoadCurveFromSlot: (selection: number) => void;
}

function shortSig(sig?: string): string {
  if (!sig) return "—";
  if (sig.length <= 12) return sig;
  return `${sig.slice(0, 5)}…${sig.slice(-5)}`;
}

function CrowdBar({ shares }: { shares: readonly number[] }) {
  const safe = shares.length === 5 ? shares : [0.2, 0.2, 0.2, 0.2, 0.2];
  return (
    <div className="mine-crowd-bar" aria-label="Crowd split">
      {safe.map((s, i) => (
        <i
          key={i}
          style={{
            width: `${Math.max(2, s * 100)}%`,
            background: SPECTRUM[i],
          }}
        />
      ))}
    </div>
  );
}

export function MineRecordsPanel({
  activeTab,
  onTabChange,
  round,
  weightsBps,
  sessions,
  portfolio,
  protocolRounds,
  cluster,
  mainnetGateNote,
  selectedWalletId,
  busy,
  onDepositSession,
  onStopSession,
  onCloseSession,
  onClaimAsset,
  onClaimAll,
  onLoadCurveFromSlot,
}: MineRecordsPanelProps) {
  const [protoWindow, setProtoWindow] = useState<"24h" | "all">("24h");
  const activeSessions = sessions.filter((s) => s.status === "active");
  const claimItems = portfolio?.claimItems ?? [];
  const history: EntryHistoryItem[] = portfolio?.history ?? [];

  return (
    <div className="wt-wpanel mine-records-panel">
      {activeSessions.length > 0 && (
        <div
          className="mine-active-sessions-strip"
          aria-label="Active mining sessions"
        >
          {activeSessions.slice(0, 3).map((sess) => (
            <div key={sess.id} className="mine-active-sess-pill">
              <div>
                <strong>{sess.strategyId}</strong>
                <small>
                  {sess.mode} · {sess.perRoundSol.toFixed(2)} SOL/r ·{" "}
                  {(sess.depositSol ?? 0).toFixed(2)} SOL pool
                </small>
              </div>
              <div className="mine-sess-mini-actions">
                <button
                  type="button"
                  className="icon-btn"
                  title="Deposit +0.05 SOL to session"
                  aria-label={`Deposit session ${sess.id}`}
                  disabled={busy}
                  onClick={() => onDepositSession(sess.id, 0.05)}
                >
                  <Plus size={12} aria-hidden="true" />
                </button>
                <button
                  type="button"
                  className="icon-btn"
                  title="Stop session"
                  aria-label={`Stop active session ${sess.id}`}
                  disabled={busy}
                  onClick={() => onStopSession(sess.id)}
                >
                  <Pause size={12} aria-hidden="true" />
                </button>
              </div>
            </div>
          ))}
        </div>
      )}

      <div
        className="wt-tabs-underline"
        role="tablist"
        aria-label="Mining records and claims"
      >
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === "current"}
          onClick={() => onTabChange("current")}
        >
          Current Round
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === "claims"}
          onClick={() => onTabChange("claims")}
        >
          Claims ({claimItems.length})
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === "history"}
          onClick={() => onTabChange("history")}
        >
          History ({history.length})
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === "sessions"}
          onClick={() => onTabChange("sessions")}
        >
          Sessions ({sessions.length})
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === "protocol"}
          onClick={() => onTabChange("protocol")}
        >
          Protocol
        </button>
      </div>

      {activeTab === "current" && (
        <div className="mine-table-scroll">
          <table className="mine-table" aria-label="Round buckets">
            <thead>
              <tr>
                <th>Range</th>
                <th>Return Band</th>
                <th>Weight</th>
                <th>Pool SOL</th>
                <th>Crowd %</th>
                <th>Model %</th>
                <th>EV ×</th>
              </tr>
            </thead>
            <tbody>
              {(round?.buckets ?? []).map((b) => (
                <tr key={b.code}>
                  <td>
                    <strong>{b.code}</strong> {RANGE_NAMES[b.index] ?? ""}
                  </td>
                  <td>{b.label}</td>
                  <td>{((weightsBps[b.index] ?? 0) / 100).toFixed(1)}%</td>
                  <td>{b.poolSol.toFixed(2)} SOL</td>
                  <td>{(b.crowdShare * 100).toFixed(1)}%</td>
                  <td>{(b.modelProb * 100).toFixed(1)}%</td>
                  <td>{b.evMultiple.toFixed(2)}×</td>
                </tr>
              ))}
            </tbody>
          </table>

          <details className="mine-deploy-details">
            <summary>Pinned LV1X Deployment ({cluster})</summary>
            <div className="wt-hairline-table">
              <div className="wt-hairline-row">
                <span>LV1X Program ID</span>
                <span className="f-mono">{round?.programId ?? "—"}</span>
              </div>
              <div className="wt-hairline-row">
                <span>Ledger PDA</span>
                <span className="f-mono">{round?.ledgerId ?? "—"}</span>
              </div>
              <div className="wt-hairline-row">
                <span>Mainnet Gate</span>
                <span>{mainnetGateNote ?? "Disabled by policy"}</span>
              </div>
            </div>
          </details>
        </div>
      )}

      {activeTab === "claims" && (
        <div className="mine-claims-wrap">
          <div className="mine-claims-kpis">
            <div className="wt-kpi-box">
              <span>SOL Ready</span>
              <strong>{(portfolio?.claimableSol ?? 0).toFixed(4)} SOL</strong>
              <button
                type="button"
                className="wt-btn-soft"
                disabled={busy || !selectedWalletId || claimItems.length === 0}
                onClick={() => onClaimAsset("sol", claimItems[0])}
              >
                Claim SOL
              </button>
            </div>
            <div className="wt-kpi-box">
              <span>SAT Ready</span>
              <strong>{(portfolio?.vestedSat ?? 0).toFixed(2)} SAT</strong>
              <button
                type="button"
                className="wt-btn-soft"
                disabled={busy || !selectedWalletId || claimItems.length === 0}
                onClick={() => onClaimAsset("sat", claimItems[0])}
              >
                Claim SAT
              </button>
            </div>
            <div className="wt-kpi-box">
              <span>cbBTC Ready</span>
              <strong>{(portfolio?.claimableBtc ?? 0).toFixed(6)} BTC</strong>
              <button
                type="button"
                className="wt-btn-soft"
                disabled={busy || !selectedWalletId || claimItems.length === 0}
                onClick={() => onClaimAsset("btc", claimItems[0])}
              >
                Claim cbBTC
              </button>
            </div>
          </div>

          <div className="mine-vesting-bar-wrap">
            <div className="mine-prog-row">
              <span>Bootstrap Vesting Schedule</span>
              <span>
                {(portfolio?.vestingProgressPct ?? 100).toFixed(1)}% unlocked (
                {(portfolio?.vestedSat ?? 0).toFixed(1)} /{" "}
                {(portfolio?.claimableSat ?? 0).toFixed(1)} SAT)
              </span>
            </div>
            <div className="mine-prog-track">
              <div
                className="mine-prog-fill"
                style={{
                  width: `${Math.min(100, portfolio?.vestingProgressPct ?? 100).toFixed(1)}%`,
                }}
              />
            </div>
            <button
              type="button"
              className="wt-btn-rose-sm"
              disabled={busy || !selectedWalletId || claimItems.length === 0}
              onClick={onClaimAll}
            >
              Claim All Rewards
            </button>
          </div>

          <div className="mine-table-scroll">
            <table className="mine-table" aria-label="Claimable rounds">
              <thead>
                <tr>
                  <th>Round</th>
                  <th>Range</th>
                  <th>SOL Ready</th>
                  <th>SAT Vested</th>
                  <th>cbBTC</th>
                  <th>Action</th>
                </tr>
              </thead>
              <tbody>
                {claimItems.length === 0 ? (
                  <tr>
                    <td colSpan={6} className="mine-muted-note">
                      No pending claimable entries for this Mining wallet yet.
                    </td>
                  </tr>
                ) : (
                  claimItems.map((it) => (
                    <tr key={`${it.roundSlot}-${it.entryRow}`}>
                      <td>#{it.roundSlot}</td>
                      <td>{it.selectionCode}</td>
                      <td>
                        {it.solClaimed
                          ? "Claimed"
                          : `${(Number(it.solLamports) / 1e9).toFixed(4)}`}
                      </td>
                      <td>{(Number(it.vestedSatAtoms) / 1e9).toFixed(2)}</td>
                      <td>
                        {it.btcClaimed
                          ? "Claimed"
                          : `${(Number(it.btcAtoms) / 1e8).toFixed(6)}`}
                      </td>
                      <td className="mine-row-actions">
                        <button
                          type="button"
                          className="mine-link-btn"
                          disabled={busy || it.solClaimed}
                          onClick={() => onClaimAsset("sol", it)}
                        >
                          SOL
                        </button>
                        <button
                          type="button"
                          className="mine-link-btn"
                          onClick={() => onLoadCurveFromSlot(it.selection)}
                        >
                          Load curve
                        </button>
                      </td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {activeTab === "history" && (
        <div className="mine-table-scroll">
          <table className="mine-table" aria-label="Mining action history">
            <thead>
              <tr>
                <th>Action</th>
                <th>Round</th>
                <th>Amount</th>
                <th>Status</th>
                <th>Signature</th>
              </tr>
            </thead>
            <tbody>
              {history.length === 0 ? (
                <tr>
                  <td colSpan={5} className="mine-muted-note">
                    No recorded mining actions yet.
                  </td>
                </tr>
              ) : (
                history.map((h) => (
                  <tr key={h.id}>
                    <td>{h.kind}</td>
                    <td>{h.roundSlot > 0 ? `#${h.roundSlot}` : "Staking"}</td>
                    <td>
                      {h.amountSol
                        ? `${h.amountSol.toFixed(3)} SOL`
                        : h.amountSat
                          ? `${h.amountSat.toFixed(2)} SAT`
                          : h.amountBtc
                            ? `${h.amountBtc.toFixed(6)} BTC`
                            : "Fee"}
                    </td>
                    <td>{h.status}</td>
                    <td className="f-mono">
                      {h.txSignature ? (
                        <a
                          href={`https://solscan.io/tx/${encodeURIComponent(h.txSignature)}?cluster=devnet`}
                          target="_blank"
                          rel="noreferrer"
                        >
                          {shortSig(h.txSignature)}
                        </a>
                      ) : (
                        "—"
                      )}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      )}

      {activeTab === "sessions" && (
        <div className="mine-table-scroll">
          <table className="mine-table" aria-label="Mining sessions">
            <thead>
              <tr>
                <th>Strategy</th>
                <th>Mode</th>
                <th>Schedule</th>
                <th>Per Round</th>
                <th>Deposit</th>
                <th>Status</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {sessions.length === 0 ? (
                <tr>
                  <td colSpan={7} className="mine-muted-note">
                    No active mining sessions. Switch composer to Semi-Auto or
                    Auto to run continuous rounds.
                  </td>
                </tr>
              ) : (
                sessions.map((sess) => (
                  <tr key={sess.id}>
                    <td>{sess.strategyId}</td>
                    <td>{sess.mode}</td>
                    <td>
                      {sess.scheduleMode ?? "every"} ·{" "}
                      {sess.roundsCount ?? Math.max(1, sess.roundsPlayed)}r
                    </td>
                    <td>{sess.perRoundSol.toFixed(2)} SOL</td>
                    <td>
                      {(sess.depositSol ?? sess.perRoundSol).toFixed(2)} SOL
                    </td>
                    <td>{sess.status}</td>
                    <td className="mine-row-actions">
                      {sess.status === "active" && (
                        <>
                          <button
                            type="button"
                            className="icon-btn"
                            aria-label={`Deposit session ${sess.id}`}
                            title="Deposit +0.05 SOL"
                            onClick={() => onDepositSession(sess.id, 0.05)}
                          >
                            <Plus size={13} aria-hidden="true" />
                          </button>
                          <button
                            type="button"
                            className="icon-btn"
                            aria-label={`Stop session ${sess.id}`}
                            title="Stop session"
                            onClick={() => onStopSession(sess.id)}
                          >
                            <Pause size={13} aria-hidden="true" />
                          </button>
                        </>
                      )}
                      {sess.status !== "closed" && (
                        <button
                          type="button"
                          className="icon-btn"
                          aria-label={`Close session ${sess.id}`}
                          title="Close session"
                          onClick={() => onCloseSession(sess.id)}
                        >
                          <Trash2 size={13} aria-hidden="true" />
                        </button>
                      )}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      )}

      {activeTab === "protocol" && (
        <div className="mine-proto-wrap">
          <div className="mine-section-head">
            <div className="wt-seg" role="group" aria-label="Protocol window">
              <button
                type="button"
                aria-pressed={protoWindow === "24h"}
                onClick={() => setProtoWindow("24h")}
              >
                24h
              </button>
              <button
                type="button"
                aria-pressed={protoWindow === "all"}
                onClick={() => setProtoWindow("all")}
              >
                All
              </button>
            </div>
            <span className="mine-muted-note">
              {protocolRounds.length} settled rounds
            </span>
          </div>

          <div className="mine-table-scroll">
            <table className="mine-table" aria-label="Protocol settled rounds">
              <thead>
                <tr>
                  <th>Round</th>
                  <th>Move</th>
                  <th>Win</th>
                  <th>Pool SOL</th>
                  <th>Crowd Split</th>
                  <th>SAT Minted</th>
                </tr>
              </thead>
              <tbody>
                {(protoWindow === "24h"
                  ? protocolRounds.slice(0, 12)
                  : protocolRounds.slice(0, 32)
                ).map((r) => (
                  <tr key={r.roundSlot}>
                    <td>#{r.roundSlot}</td>
                    <td>
                      {r.moveBps >= 0 ? "+" : ""}
                      {r.moveBps} bps
                    </td>
                    <td>
                      <strong>{r.code}</strong>
                    </td>
                    <td>{r.poolSol.toFixed(2)} SOL</td>
                    <td>
                      <CrowdBar shares={r.crowdShares} />
                    </td>
                    <td>{r.minerSat.toFixed(1)} SAT</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}
