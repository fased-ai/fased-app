import { useMemo, useState } from "react";
import { Dialog } from "../../components/shared";
import { Pause, Plus, RotateCcw, Search, Trash2 } from "../../icons";
import type {
  ClaimableRoundItem,
  EntryHistoryItem,
  LeaderboardRound,
  PortfolioResponse,
  ProtocolStats,
  RoundTelemetry,
  SessionRecord,
  StakingOverviewResponse,
  StreakEntry,
} from "../../mining-lv1x";
import { RANGE_LABELS, RANGE_NAMES } from "../MineAllocationCurve";
import { formatBtcCompact, formatCompactNum } from "./MineLeftRail";

const SPECTRUM = [
  "#E0566F",
  "#D98466",
  "#6F7889",
  "#2FB88C",
  "#33A9B8",
] as const;

const RANGE_TEXT = [
  "var(--wt-danger)",
  "var(--wt-warn)",
  "var(--wt-muted)",
  "var(--wt-ok)",
  "var(--wt-cyan-text)",
] as const;

export type RecordsTab =
  "entries" | "sessions" | "rounds" | "claims" | "activity" | "protocol";

interface MineRecordsPanelProps {
  activeTab: RecordsTab;
  onTabChange: (tab: RecordsTab) => void;
  round?: RoundTelemetry;
  stats: ProtocolStats | null;
  staking: StakingOverviewResponse | null;
  sessions: SessionRecord[];
  portfolio: PortfolioResponse | null;
  protocolRounds: LeaderboardRound[];
  streak: StreakEntry[];
  cluster: string;
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
  onClaimStakingBtc: () => void;
  onCloseNonce?: (item: ClaimableRoundItem) => void;
  onLoadCurveFromSlot: (selection: number) => void;
  onNavigateSub: (sub: "assets" | "stake" | "portfolio") => void;
}

function shortSig(sig?: string): string {
  if (!sig) return "—";
  if (sig.length <= 12) return sig;
  return `${sig.slice(0, 5)}…${sig.slice(-5)}`;
}

function trimSol(v: number): string {
  if (!Number.isFinite(v) || v <= 0) return "0";
  if (v >= 10) return v.toFixed(2).replace(/\.?0+$/, "");
  if (v >= 1) return v.toFixed(3).replace(/\.?0+$/, "");
  return v.toFixed(4).replace(/\.?0+$/, "");
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
  stats,
  staking,
  sessions,
  portfolio,
  protocolRounds,
  streak,
  cluster,
  selectedWalletId,
  busy,
  onDepositSession,
  onStopSession,
  onCloseSession,
  onClaimAsset,
  onClaimAll,
  onClaimStakingBtc,
  onCloseNonce,
  onLoadCurveFromSlot,
  onNavigateSub,
}: MineRecordsPanelProps) {
  const [protoWindow, setProtoWindow] = useState<"24h" | "all">("24h");
  const [lookupOpen, setLookupOpen] = useState(false);
  const [lookupInput, setLookupInput] = useState("");
  const [focusedSlot, setFocusedSlot] = useState<number | null>(null);

  const currentSlot = round?.roundSlot ?? 0;
  const claimItems = portfolio?.claimItems ?? [];
  const history: EntryHistoryItem[] = portfolio?.history ?? [];

  const activeRoundEntries = useMemo(() => {
    if (!currentSlot) return [];
    return history.filter(
      (h) =>
        h.roundSlot === currentSlot && h.kind.toLowerCase().includes("commit"),
    );
  }, [history, currentSlot]);

  const pastRounds = useMemo(() => {
    const map = new Map<
      number,
      {
        roundSlot: number;
        selection: number;
        selectionCode: string;
        solReady: number;
        satVested: number;
        btcReady: number;
        claimItem?: ClaimableRoundItem;
        status: string;
      }
    >();
    for (const c of claimItems) {
      map.set(c.roundSlot, {
        roundSlot: c.roundSlot,
        selection: c.selection,
        selectionCode: c.selectionCode || RANGE_LABELS[c.selection] || "C",
        solReady: c.solClaimed ? 0 : Number(c.solLamports) / 1e9,
        satVested: Number(c.vestedSatAtoms) / 1e9,
        btcReady: c.btcClaimed ? 0 : Number(c.btcAtoms) / 1e8,
        claimItem: c,
        status:
          c.solClaimed && c.btcClaimed && Number(c.vestedSatAtoms) === 0
            ? "Claimed"
            : "Claimable",
      });
    }
    for (const h of history) {
      if (
        h.roundSlot > 0 &&
        h.roundSlot !== currentSlot &&
        !map.has(h.roundSlot)
      ) {
        map.set(h.roundSlot, {
          roundSlot: h.roundSlot,
          selection: 2,
          selectionCode: "—",
          solReady: h.amountSol ?? 0,
          satVested: h.amountSat ?? 0,
          btcReady: h.amountBtc ?? 0,
          status: h.status || "Settled",
        });
      }
    }
    return Array.from(map.values()).sort((a, b) => b.roundSlot - a.roundSlot);
  }, [claimItems, history, currentSlot]);

  const latestWinBadge = useMemo(() => {
    const firstClaim = claimItems.find((c) => Number(c.vestedSatAtoms) > 0);
    if (firstClaim) {
      return {
        slot: firstClaim.roundSlot,
        sat: Number(firstClaim.vestedSatAtoms) / 1e9,
      };
    }
    const firstHist = history.find(
      (h) => h.roundSlot > 0 && (h.amountSat ?? 0) > 0,
    );
    if (firstHist) {
      return { slot: firstHist.roundSlot, sat: firstHist.amountSat ?? 0 };
    }
    const firstProto = protocolRounds[0];
    if (firstProto && firstProto.minerSat > 0) {
      return { slot: firstProto.roundSlot, sat: firstProto.minerSat };
    }
    return null;
  }, [claimItems, history, protocolRounds]);

  const viewedSlot = focusedSlot ?? currentSlot;
  const focusedClaimItem = useMemo(
    () => claimItems.find((c) => c.roundSlot === viewedSlot),
    [claimItems, viewedSlot],
  );

  const claimsSolReady = focusedClaimItem
    ? focusedClaimItem.solClaimed
      ? 0
      : Number(focusedClaimItem.solLamports) / 1e9
    : (portfolio?.claimableSol ?? 0);
  const claimsSatReady = focusedClaimItem
    ? Number(focusedClaimItem.vestedSatAtoms) / 1e9
    : (portfolio?.vestedSat ?? portfolio?.claimableSat ?? 0);
  const claimsBtcReady = focusedClaimItem
    ? focusedClaimItem.btcClaimed
      ? 0
      : Number(focusedClaimItem.btcAtoms) / 1e8
    : (portfolio?.claimableBtc ?? 0);

  const firstPendingClaim = claimItems[0];
  const stakingBtcReady = staking?.claimableBtc ?? 0;
  const vestingPct = portfolio?.vestingProgressPct ?? 100;

  const visibleProtoRounds =
    protoWindow === "24h"
      ? protocolRounds.slice(0, 12)
      : protocolRounds.slice(0, 32);

  const protoKpis = useMemo(() => {
    const list = visibleProtoRounds;
    const totalSol = list.reduce((s, r) => s + (r.poolSol || 0), 0);
    const totalSat = list.reduce((s, r) => s + (r.minerSat || 0), 0);
    const avgCrowdWin =
      list.length > 0
        ? list.reduce(
            (s, r) => s + (r.crowdShares?.[r.winningRange ?? 2] ?? 0.2) * 100,
            0,
          ) / list.length
        : 0;
    const paidBtc = (stats?.btcTreasurySat ?? 0) / 1e8;
    return { totalSol, totalSat, avgCrowdWin, paidBtc };
  }, [visibleProtoRounds, stats]);

  const solscanCluster = cluster === "mainnet-beta" ? "" : "?cluster=devnet";

  const applyLookupSlot = (slotNum: number) => {
    if (!Number.isFinite(slotNum) || slotNum <= 0) return;
    setFocusedSlot(Math.floor(slotNum));
    setLookupOpen(false);
    onTabChange("claims");
  };

  return (
    <section
      className="wt-wpanel mine-records-panel"
      aria-label="Round details"
    >
      <div className="mine-round-head">
        <h2>Round</h2>
        <div className="mine-round-head-pills">
          {latestWinBadge && (
            <button
              type="button"
              className="mine-round-win-pill"
              title={`Inspect Round #${latestWinBadge.slot}`}
              onClick={() => {
                setFocusedSlot(latestWinBadge.slot);
                onTabChange("claims");
              }}
            >
              #{latestWinBadge.slot} · +
              {formatCompactNum(latestWinBadge.sat, 1)} SAT
            </button>
          )}
          <span className="mine-round-slot-pill">
            Round {currentSlot > 0 ? currentSlot : "—"}
          </span>
        </div>
      </div>

      <div
        className="wt-tabs-underline mine-round-tabs"
        role="tablist"
        aria-label="Mining records and claims"
      >
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === "entries"}
          onClick={() => onTabChange("entries")}
        >
          <span>Entries</span>
          {activeRoundEntries.length > 0 && (
            <em className="mine-tab-badge">{activeRoundEntries.length}</em>
          )}
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === "sessions"}
          onClick={() => onTabChange("sessions")}
        >
          <span>Sessions</span>
          {sessions.length > 0 && (
            <em className="mine-tab-badge">{sessions.length}</em>
          )}
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === "rounds"}
          onClick={() => onTabChange("rounds")}
        >
          <span>Rounds</span>
          {pastRounds.length > 0 && (
            <em className="mine-tab-badge">{pastRounds.length}</em>
          )}
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === "claims"}
          onClick={() => onTabChange("claims")}
        >
          <span>Claims</span>
          {claimItems.length > 0 && (
            <em className="mine-tab-badge is-alert">{claimItems.length}</em>
          )}
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === "activity"}
          onClick={() => onTabChange("activity")}
        >
          <span>Activity</span>
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === "protocol"}
          onClick={() => onTabChange("protocol")}
        >
          <span>Protocol</span>
        </button>
      </div>

      {activeTab === "entries" && (
        <div className="mine-entries-wrap">
          {activeRoundEntries.length === 0 ? (
            <div className="mine-empty-row">
              <span>
                No entries in Round #{currentSlot > 0 ? currentSlot : "—"} yet.
              </span>
              {firstPendingClaim && (
                <button
                  type="button"
                  className="mine-link-btn"
                  onClick={() => {
                    setFocusedSlot(firstPendingClaim.roundSlot);
                    onTabChange("claims");
                  }}
                >
                  Open Round #{firstPendingClaim.roundSlot} →
                </button>
              )}
            </div>
          ) : (
            <div className="mine-table-scroll">
              <table className="mine-table" aria-label="Active round entries">
                <thead>
                  <tr>
                    <th>Round</th>
                    <th>Action</th>
                    <th>Committed</th>
                    <th>Status</th>
                    <th>Tx</th>
                  </tr>
                </thead>
                <tbody>
                  {activeRoundEntries.map((entry) => (
                    <tr key={entry.id}>
                      <td>#{entry.roundSlot}</td>
                      <td>{entry.kind}</td>
                      <td>{trimSol(entry.amountSol ?? 0)} SOL</td>
                      <td>{entry.status}</td>
                      <td className="f-mono">
                        {entry.txSignature ? (
                          <a
                            href={`https://solscan.io/tx/${encodeURIComponent(entry.txSignature)}${solscanCluster}`}
                            target="_blank"
                            rel="noreferrer"
                          >
                            {shortSig(entry.txSignature)}
                          </a>
                        ) : (
                          "—"
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      {activeTab === "sessions" && (
        <div className="mine-table-scroll">
          <table className="mine-table" aria-label="Mining sessions">
            <thead>
              <tr>
                <th>Strategy</th>
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
                  <td colSpan={6} className="mine-muted-note">
                    No active mining sessions. Select 2r, 6r, or 12r in the
                    composer to run multi-round sessions.
                  </td>
                </tr>
              ) : (
                sessions.map((sess) => (
                  <tr key={sess.id}>
                    <td>{sess.strategyId}</td>
                    <td>
                      {sess.scheduleMode ?? "every"} ·{" "}
                      {sess.roundsCount ?? Math.max(1, sess.roundsPlayed)}r
                    </td>
                    <td>{trimSol(sess.perRoundSol)} SOL</td>
                    <td>{trimSol(sess.depositSol ?? sess.perRoundSol)} SOL</td>
                    <td>{sess.status}</td>
                    <td className="mine-row-actions">
                      {sess.status === "active" && (
                        <>
                          <button
                            type="button"
                            className="icon-btn"
                            aria-label={`Deposit session ${sess.id}`}
                            title="Deposit +0.05 SOL"
                            disabled={busy}
                            onClick={() => onDepositSession(sess.id, 0.05)}
                          >
                            <Plus size={13} aria-hidden="true" />
                          </button>
                          <button
                            type="button"
                            className="icon-btn"
                            aria-label={`Stop session ${sess.id}`}
                            title="Stop session"
                            disabled={busy}
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
                          disabled={busy}
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

      {activeTab === "rounds" && (
        <div className="mine-table-scroll">
          <table className="mine-table" aria-label="Past rounds">
            <thead>
              <tr>
                <th>Round</th>
                <th>Range</th>
                <th>SOL</th>
                <th>SAT</th>
                <th>BTC</th>
                <th>Status</th>
                <th>Action</th>
              </tr>
            </thead>
            <tbody>
              {pastRounds.length === 0 ? (
                <tr>
                  <td colSpan={7} className="mine-muted-note">
                    No settled rounds for this wallet yet.
                  </td>
                </tr>
              ) : (
                pastRounds.map((pr) => (
                  <tr key={pr.roundSlot}>
                    <td>#{pr.roundSlot}</td>
                    <td>
                      <strong
                        style={{
                          color: RANGE_TEXT[pr.selection] ?? "var(--wt-text-2)",
                        }}
                      >
                        {pr.selectionCode}
                      </strong>{" "}
                      <span className="mine-muted-note">
                        {RANGE_NAMES[pr.selection] ?? ""}
                      </span>
                    </td>
                    <td>{trimSol(pr.solReady)}</td>
                    <td>{formatCompactNum(pr.satVested, 1)}</td>
                    <td>{formatBtcCompact(pr.btcReady)}</td>
                    <td>{pr.status}</td>
                    <td className="mine-row-actions">
                      <button
                        type="button"
                        className="mine-link-btn"
                        onClick={() => {
                          setFocusedSlot(pr.roundSlot);
                          onTabChange("claims");
                        }}
                      >
                        Open
                      </button>
                      <button
                        type="button"
                        className="icon-btn"
                        title={`Re-mine Range ${pr.selectionCode}`}
                        aria-label={`Load curve from round ${pr.roundSlot}`}
                        onClick={() => onLoadCurveFromSlot(pr.selection)}
                      >
                        <RotateCcw size={12} aria-hidden="true" />
                      </button>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      )}

      {activeTab === "claims" && (
        <div className="mine-claims-wrap">
          <div className="mine-claims-toolbar">
            <span className="mine-claims-round-tag">
              Round #{viewedSlot > 0 ? viewedSlot : "—"}
            </span>
            {focusedSlot !== null && focusedSlot !== currentSlot && (
              <button
                type="button"
                className="mine-claims-tool-btn"
                onClick={() => setFocusedSlot(null)}
              >
                Live
              </button>
            )}
            <button
              type="button"
              className="mine-claims-tool-btn"
              onClick={() => {
                setLookupInput(String(viewedSlot || currentSlot || ""));
                setLookupOpen(true);
              }}
            >
              <Search size={12} aria-hidden="true" />
              <span>Lookup</span>
            </button>
          </div>

          <div className="mine-claims-summary">
            <dl className="mine-claims-metrics">
              <div>
                <dt>SOL ready</dt>
                <dd>{trimSol(claimsSolReady)}</dd>
              </div>
              <div>
                <dt>SAT ready</dt>
                <dd>{formatCompactNum(claimsSatReady, 1)}</dd>
              </div>
              <div>
                <dt>BTC ready</dt>
                <dd>{formatBtcCompact(claimsBtcReady)}</dd>
              </div>
            </dl>

            <button
              type="button"
              className="wt-btn-rose mine-claims-cta"
              disabled={busy || !selectedWalletId || claimItems.length === 0}
              onClick={() => {
                if (
                  !focusedClaimItem &&
                  firstPendingClaim &&
                  viewedSlot === currentSlot
                ) {
                  setFocusedSlot(firstPendingClaim.roundSlot);
                } else {
                  onClaimAll();
                }
              }}
            >
              {claimItems.length === 0
                ? "Nothing to claim"
                : !focusedClaimItem &&
                    firstPendingClaim &&
                    viewedSlot === currentSlot
                  ? `Open Round #${firstPendingClaim.roundSlot}`
                  : "Claim All"}
            </button>
          </div>

          {viewedSlot === currentSlot && currentSlot > 0 && (
            <p className="mine-claims-caption">
              Round #{currentSlot} settles before claims open.
            </p>
          )}

          {vestingPct < 99.9 && (
            <div className="mine-vesting-bar-wrap">
              <div className="mine-prog-row">
                <span>Bootstrap Vesting</span>
                <span>
                  {vestingPct.toFixed(1)}% (
                  {formatCompactNum(portfolio?.vestedSat ?? 0, 1)} /{" "}
                  {formatCompactNum(portfolio?.claimableSat ?? 0, 1)} SAT)
                </span>
              </div>
              <div className="mine-prog-track">
                <div
                  className="mine-prog-fill"
                  style={{ width: `${Math.min(100, vestingPct).toFixed(1)}%` }}
                />
              </div>
            </div>
          )}

          <div className="mine-claims-staking-row">
            <span>
              <strong>Staking BTC</strong> · {formatBtcCompact(stakingBtcReady)}{" "}
              BTC
            </span>
            <button
              type="button"
              className="mine-claims-btc-btn"
              disabled={busy || !selectedWalletId || stakingBtcReady <= 0}
              onClick={onClaimStakingBtc}
            >
              Claim BTC
            </button>
          </div>

          {claimItems.length > 0 && (
            <div className="mine-table-scroll">
              <table className="mine-table" aria-label="Claimable rounds">
                <thead>
                  <tr>
                    <th>Round</th>
                    <th>Range</th>
                    <th>SOL</th>
                    <th>SAT</th>
                    <th>BTC</th>
                    <th>Action</th>
                  </tr>
                </thead>
                <tbody>
                  {claimItems.map((it) => (
                    <tr key={`${it.roundSlot}-${it.entryRow}`}>
                      <td>#{it.roundSlot}</td>
                      <td>
                        <strong
                          style={{
                            color: RANGE_TEXT[it.selection] ?? "var(--wt-text)",
                          }}
                        >
                          {it.selectionCode}
                        </strong>
                      </td>
                      <td>
                        {it.solClaimed
                          ? "Claimed"
                          : trimSol(Number(it.solLamports) / 1e9)}
                      </td>
                      <td>
                        {formatCompactNum(Number(it.vestedSatAtoms) / 1e9, 1)}
                      </td>
                      <td>
                        {it.btcClaimed
                          ? "Claimed"
                          : formatBtcCompact(Number(it.btcAtoms) / 1e8)}
                      </td>
                      <td className="mine-row-actions">
                        {!it.solClaimed && (
                          <button
                            type="button"
                            className="mine-link-btn"
                            disabled={busy}
                            onClick={() => onClaimAsset("sol", it)}
                          >
                            {it.solEscape ? "Escape" : "SOL"}
                          </button>
                        )}
                        {Number(it.vestedSatAtoms) > 0 && (
                          <button
                            type="button"
                            className="mine-link-btn"
                            disabled={busy}
                            onClick={() => onClaimAsset("sat", it)}
                          >
                            SAT
                          </button>
                        )}
                        {!it.btcClaimed && Number(it.btcAtoms) > 0 && (
                          <button
                            type="button"
                            className="mine-link-btn"
                            disabled={busy}
                            onClick={() => onClaimAsset("btc", it)}
                          >
                            BTC
                          </button>
                        )}
                        {onCloseNonce &&
                          !it.nonceClosed &&
                          (it.nonce ?? 0) > 0 && (
                            <button
                              type="button"
                              className="mine-link-btn"
                              disabled={busy}
                              onClick={() => onCloseNonce(it)}
                            >
                              Close Nonce
                            </button>
                          )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      {activeTab === "activity" && (
        <div className="mine-table-scroll">
          <table className="mine-table" aria-label="Mining activity history">
            <thead>
              <tr>
                <th>Action</th>
                <th>Round</th>
                <th>Amount</th>
                <th>Status</th>
                <th>Tx</th>
              </tr>
            </thead>
            <tbody>
              {history.length === 0 ? (
                <tr>
                  <td colSpan={5} className="mine-muted-note">
                    No recorded mining activity yet.
                  </td>
                </tr>
              ) : (
                history.map((h) => (
                  <tr key={h.id}>
                    <td>{h.kind}</td>
                    <td>{h.roundSlot > 0 ? `#${h.roundSlot}` : "Staking"}</td>
                    <td>
                      {h.amountSol
                        ? `${trimSol(h.amountSol)} SOL`
                        : h.amountSat
                          ? `${formatCompactNum(h.amountSat, 2)} SAT`
                          : h.amountBtc
                            ? `${formatBtcCompact(h.amountBtc)} BTC`
                            : "Fee"}
                    </td>
                    <td>{h.status}</td>
                    <td className="f-mono">
                      {h.txSignature ? (
                        <a
                          href={`https://solscan.io/tx/${encodeURIComponent(h.txSignature)}${solscanCluster}`}
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
            <button
              type="button"
              className="mine-link-btn"
              onClick={() => onNavigateSub("assets")}
            >
              Full Assets →
            </button>
          </div>

          <div className="mine-proto-kpis">
            <div className="mine-proto-kpi">
              <span>SOL Mined</span>
              <strong>{formatCompactNum(protoKpis.totalSol, 1)} SOL</strong>
            </div>
            <div className="mine-proto-kpi">
              <span>Crowd Win Rate</span>
              <strong>{protoKpis.avgCrowdWin.toFixed(1)}%</strong>
            </div>
            <div className="mine-proto-kpi">
              <span>SAT Minted</span>
              <strong>{formatCompactNum(protoKpis.totalSat, 1)}</strong>
            </div>
            <div className="mine-proto-kpi">
              <span>BTC Paid</span>
              <strong>{formatBtcCompact(protoKpis.paidBtc)} BTC</strong>
            </div>
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
                {visibleProtoRounds.map((r) => (
                  <tr key={r.roundSlot}>
                    <td>#{r.roundSlot}</td>
                    <td>
                      {r.moveBps >= 0 ? "+" : ""}
                      {(r.moveBps / 100).toFixed(2)}%
                    </td>
                    <td>
                      <strong
                        style={{
                          color:
                            RANGE_TEXT[r.winningRange ?? 2] ?? "var(--wt-text)",
                        }}
                      >
                        {r.code}
                      </strong>
                    </td>
                    <td>{trimSol(r.poolSol)} SOL</td>
                    <td>
                      <CrowdBar shares={r.crowdShares} />
                    </td>
                    <td>{formatCompactNum(r.minerSat, 1)} SAT</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {lookupOpen && (
        <Dialog title="Round Lookup" onClose={() => setLookupOpen(false)}>
          <div className="mine-modal-form">
            <div className="mine-field">
              <label htmlFor="mine-round-lookup-input">Round Number</label>
              <input
                id="mine-round-lookup-input"
                className="mine-modal-input"
                inputMode="numeric"
                value={lookupInput}
                onChange={(e) => setLookupInput(e.target.value)}
                placeholder={String(currentSlot || 1)}
              />
            </div>
            <div className="mine-quick-chips">
              {currentSlot > 1 && (
                <button
                  type="button"
                  className="mine-chip"
                  onClick={() => applyLookupSlot(Math.max(1, viewedSlot - 1))}
                >
                  Prev
                </button>
              )}
              {viewedSlot < currentSlot && (
                <button
                  type="button"
                  className="mine-chip"
                  onClick={() => applyLookupSlot(viewedSlot + 1)}
                >
                  Next
                </button>
              )}
              <button
                type="button"
                className="mine-chip"
                onClick={() => {
                  setFocusedSlot(null);
                  setLookupOpen(false);
                }}
              >
                Live (#{currentSlot})
              </button>
              {streak.slice(0, 5).map((st) => (
                <button
                  key={st.roundSlot}
                  type="button"
                  className="mine-chip"
                  onClick={() => applyLookupSlot(st.roundSlot)}
                >
                  #{st.roundSlot} {st.code}
                </button>
              ))}
            </div>
            <div className="mine-modal-actions">
              <button
                type="button"
                className="wt-btn-soft"
                onClick={() => setLookupOpen(false)}
              >
                Cancel
              </button>
              <button
                type="button"
                className="wt-btn-rose-sm"
                onClick={() => applyLookupSlot(Number(lookupInput))}
              >
                Open Round
              </button>
            </div>
          </div>
        </Dialog>
      )}
    </section>
  );
}
