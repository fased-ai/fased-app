import {
  Clock3,
  ExternalLink,
  Layers,
  Settings2,
  SlidersHorizontal,
} from "../../icons";
import type {
  PortfolioResponse,
  RoundTelemetry,
  StrategyPreset,
} from "../../mining-lv1x";
import {
  MineAllocationCurve,
  MinePresetIcon,
  RANGE_LABELS,
  RANGE_NAMES,
} from "../MineAllocationCurve";
import { formatBtcCompact, formatCompactNum } from "./MineLeftRail";

export type ComposerTab = "mine" | "claim" | "swap";

interface MineComposerProps {
  composerTab: ComposerTab;
  onComposerTabChange: (tab: ComposerTab) => void;
  presets: StrategyPreset[];
  presetId: string;
  weightsBps: number[];
  selectedBucket: number | null;
  round?: RoundTelemetry;
  roundsCount: number;
  startMode: "now" | "next" | "plus2";
  onStartModeChange: (m: "now" | "next" | "plus2") => void;
  onQuickRounds: (n: number) => void;
  onOpenScheduleGrid: () => void;
  amountStr: string;
  onAmountChange: (v: string) => void;
  walletSolBalance: number;
  selectedWalletId: string;
  onSelectPreset: (p: StrategyPreset) => void;
  onCurveRebalance: (idx: number, bps: number) => void;
  onOpenAllocationModal: (focusBucket?: number) => void;
  onShareCurve: () => void;
  onOpenSaveStrategy: () => void;
  onOpenRules: () => void;
  onPrimarySubmit: () => void;
  onClaimAsset: (asset: "sol" | "sat" | "btc") => void;
  onClaimAll: () => void;
  onCopyMint: (mint: string) => void;
  onOpenJupiter: () => void;
  portfolio: PortfolioResponse | null;
  countdownSec: number;
  mainnetLocked: boolean;
  mainnetGateNote?: string;
  mintAddress: string;
  busy: boolean;
  notice: string;
  error: string;
}

const MIN_PER_ROUND_SOL = 0.0505;
const POOL_GATE_SOL = 5;

function trimNum(v: number, dp = 3): string {
  if (!Number.isFinite(v) || v <= 0) return "0";
  return v.toFixed(dp).replace(/\.?0+$/, "");
}

function formatCountdown(remainingSec: number): string {
  const safe = Math.max(0, Math.floor(remainingSec));
  const m = Math.floor(safe / 60);
  const s = safe % 60;
  return `${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`;
}

function formatHm(ms: number): string {
  return new Intl.DateTimeFormat(undefined, {
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  }).format(ms);
}

function getTzShort(): string {
  try {
    return (
      new Intl.DateTimeFormat("en-US", { timeZoneName: "short" })
        .formatToParts(Date.now())
        .find((p) => p.type === "timeZoneName")?.value ?? ""
    );
  } catch {
    return "";
  }
}

function ShareNetworkIcon() {
  return (
    <svg
      width="13"
      height="13"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <circle cx="12.2" cy="3.5" r="1.7" />
      <circle cx="3.8" cy="8" r="1.7" />
      <circle cx="12.2" cy="12.5" r="1.7" />
      <path d="M5.3 7.2l5.4-2.9M5.3 8.8l5.4 2.9" />
    </svg>
  );
}

function InfoCircleIcon() {
  return (
    <svg
      width="13"
      height="13"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <circle cx="8" cy="8" r="6.25" />
      <path d="M8 7.2v3.6" />
      <circle cx="8" cy="4.9" r="0.6" fill="currentColor" stroke="none" />
    </svg>
  );
}

export function MineComposer({
  composerTab,
  onComposerTabChange,
  presets,
  presetId,
  weightsBps,
  selectedBucket,
  round,
  roundsCount,
  startMode,
  onStartModeChange,
  onQuickRounds,
  onOpenScheduleGrid,
  amountStr,
  onAmountChange,
  walletSolBalance,
  selectedWalletId,
  onSelectPreset,
  onCurveRebalance,
  onOpenAllocationModal,
  onShareCurve,
  onOpenSaveStrategy,
  onOpenRules,
  onPrimarySubmit,
  onClaimAsset,
  onClaimAll,
  onCopyMint,
  onOpenJupiter,
  portfolio,
  countdownSec,
  mainnetLocked,
  mainnetGateNote,
  mintAddress,
  busy,
  notice,
  error,
}: MineComposerProps) {
  const safeRounds = Math.max(1, roundsCount);
  const parsedTotal = Number(amountStr) || 0;
  const perRoundSol = parsedTotal > 0 ? parsedTotal / safeRounds : 0;
  const minTotalForRounds =
    Math.ceil(safeRounds * MIN_PER_ROUND_SOL * 1000) / 1000;
  const hasSkipRisk =
    safeRounds > 1 && parsedTotal > 0 && perRoundSol < MIN_PER_ROUND_SOL;

  const roundPeriodMs = 10 * 60 * 1000;
  const nowFloorMs = Math.floor(Date.now() / roundPeriodMs) * roundPeriodMs;
  const startOffset = startMode === "now" ? 0 : startMode === "next" ? 1 : 2;
  const schedStartMs = nowFloorMs + startOffset * roundPeriodMs;
  const schedEndMs = schedStartMs + safeRounds * roundPeriodMs;
  const tz = getTzShort();

  const poolSol = round?.totalPoolSol ?? 0;
  const poolGatePct = Math.min(100, (poolSol / POOL_GATE_SOL) * 100);
  const estSat =
    parsedTotal > 0
      ? (perRoundSol / Math.max(poolSol + perRoundSol, 0.5)) * 28.5 * safeRounds
      : 0;

  return (
    <aside className="wt-wpanel mine-composer" aria-label="Mining composer">
      <div
        className="wt-tabs-underline mine-composer-tabs"
        role="tablist"
        aria-label="Composer mode"
      >
        <button
          type="button"
          role="tab"
          aria-selected={composerTab === "mine"}
          onClick={() => onComposerTabChange("mine")}
        >
          Mine
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={composerTab === "claim"}
          onClick={() => onComposerTabChange("claim")}
        >
          Claim
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={composerTab === "swap"}
          onClick={() => onComposerTabChange("swap")}
        >
          Swap
        </button>
      </div>

      {composerTab === "mine" && (
        <>
          <div className="mine-composer-body">
            <div className="mine-sec-head">
              <div className="mine-sec-title-group">
                <span className="mine-sec-title">Strategy</span>
                <button
                  type="button"
                  className="mine-inline-icon"
                  title="Copy share curve URL"
                  aria-label="Share curve URL"
                  onClick={onShareCurve}
                >
                  <ShareNetworkIcon />
                </button>
                <button
                  type="button"
                  className="mine-inline-icon"
                  title="Save strategy to library"
                  aria-label="Save strategy"
                  onClick={onOpenSaveStrategy}
                >
                  <Layers size={13} aria-hidden="true" />
                </button>
                <button
                  type="button"
                  className="mine-inline-icon"
                  title="Strategy & curve rules"
                  aria-label="Strategy info"
                  onClick={onOpenRules}
                >
                  <InfoCircleIcon />
                </button>
              </div>
            </div>

            <div
              className="mine-presets-grid"
              role="group"
              aria-label="Strategy presets"
            >
              {presets.slice(0, 6).map((p) => (
                <button
                  key={p.id}
                  type="button"
                  className="mine-preset-btn"
                  aria-pressed={presetId === p.id}
                  onClick={() => onSelectPreset(p)}
                >
                  <MinePresetIcon
                    weights={p.id === "custom" ? weightsBps : p.bucketsBps}
                  />
                  <span>{p.name}</span>
                </button>
              ))}
            </div>

            <div className="mine-sec-head mine-alloc-head">
              <div className="mine-sec-title-group">
                <span className="mine-sec-title">Allocation</span>
                <button
                  type="button"
                  className="mine-inline-icon"
                  title="Drag bars or use sliders to weight ranges A–E"
                  aria-label="Allocation info"
                  onClick={() => onOpenAllocationModal()}
                >
                  <InfoCircleIcon />
                </button>
                {perRoundSol > 0 && (
                  <span className="mine-alloc-per-round-pill">
                    {trimNum(perRoundSol, 3)} SOL / r
                  </span>
                )}
              </div>
              <button
                type="button"
                className="mine-inline-icon"
                title="Fine-tune allocation"
                aria-label="Fine-tune allocation"
                onClick={() => onOpenAllocationModal()}
              >
                <SlidersHorizontal size={14} aria-hidden="true" />
              </button>
            </div>

            <MineAllocationCurve
              weightsBps={weightsBps}
              innerBps={round?.innerBps ?? 5}
              outerBps={round?.outerBps ?? 20}
              hideLabels
              onRebalance={onCurveRebalance}
            />

            <div
              className="mine-bucket-strip"
              role="group"
              aria-label="Range buckets"
            >
              {RANGE_LABELS.map((code, i) => {
                const pctNum = (weightsBps[i] ?? 0) / 100;
                const bucketSol = (perRoundSol * pctNum) / 100;
                return (
                  <button
                    key={code}
                    type="button"
                    className={`mine-bucket-cell${selectedBucket === i ? " is-selected" : ""}`}
                    onClick={() => onOpenAllocationModal(i)}
                    title={`${code} ${RANGE_NAMES[i]} — click to adjust`}
                  >
                    <span className="mine-bucket-code">{code}</span>
                    <strong className="mine-bucket-pct">
                      {pctNum.toFixed(0)}%
                    </strong>
                    <small className="mine-bucket-sol">
                      {trimNum(bucketSol, 3)}
                      <img
                        src="/wen/sol-icon.svg"
                        alt=""
                        width={10}
                        height={10}
                        aria-hidden="true"
                      />
                    </small>
                  </button>
                );
              })}
            </div>

            <div className="mine-rounds-row">
              <span className="mine-sec-title">Rounds</span>
              <div
                className="mine-rounds-chips"
                role="group"
                aria-label="Round count"
              >
                {[1, 2, 6, 12].map((n) => (
                  <button
                    key={n}
                    type="button"
                    className="mine-chip"
                    aria-pressed={safeRounds === n}
                    onClick={() => onQuickRounds(n)}
                  >
                    {n}r
                  </button>
                ))}
                <button
                  type="button"
                  className="mine-chip mine-edit-sched-chip"
                  onClick={onOpenScheduleGrid}
                >
                  <Clock3 size={11} aria-hidden="true" />
                  <span>Edit</span>
                </button>
              </div>
            </div>

            <div
              className="mine-start-row"
              role="group"
              aria-label="Target start round"
            >
              <div className="mine-start-left">
                <span className="mine-muted-note">Start</span>
                {(
                  [
                    ["now", "Now"],
                    ["next", "Next"],
                    ["plus2", "+2"],
                  ] as const
                ).map(([k, lbl]) => (
                  <button
                    key={k}
                    type="button"
                    className="mine-chip"
                    aria-pressed={startMode === k}
                    onClick={() => onStartModeChange(k)}
                  >
                    {lbl}
                  </button>
                ))}
              </div>
              <span className="mine-start-clock">{formatHm(schedStartMs)}</span>
            </div>

            <div className="mine-sched-bar">
              <strong>
                {safeRounds} {safeRounds === 1 ? "round" : "rounds"}
              </strong>
              <span>
                {formatHm(schedStartMs)} → {formatHm(schedEndMs)}
                {tz ? ` ${tz}` : ""}
              </span>
            </div>

            <div className="mine-deposit-sec">
              <div className="mine-deposit-head">
                <label htmlFor="mine-amount-input">Deposit</label>
                <button
                  type="button"
                  className="mine-bal-btn"
                  disabled={walletSolBalance <= 0}
                  onClick={() => {
                    if (walletSolBalance > 0.01) {
                      onAmountChange(
                        trimNum(Math.max(0.01, walletSolBalance - 0.005), 3),
                      );
                    }
                  }}
                >
                  Bal {trimNum(walletSolBalance, 2)} SOL
                </button>
              </div>

              <div className="mine-amount-box">
                <input
                  id="mine-amount-input"
                  className="mine-amount-input"
                  inputMode="decimal"
                  value={amountStr}
                  onChange={(e) => onAmountChange(e.target.value)}
                />
                <span className="mine-amount-unit">
                  <img
                    src="/wen/sol-icon.svg"
                    alt=""
                    width={16}
                    height={16}
                    aria-hidden="true"
                  />
                  <span>SOL</span>
                </span>
              </div>

              <div className="mine-quick-grid">
                <button
                  type="button"
                  className="mine-chip"
                  aria-pressed={amountStr === "0.1"}
                  onClick={() => onAmountChange("0.1")}
                >
                  0.1
                </button>
                <button
                  type="button"
                  className="mine-chip"
                  aria-pressed={amountStr === "0.2"}
                  onClick={() => onAmountChange("0.2")}
                >
                  0.2
                </button>
                <button
                  type="button"
                  className="mine-chip"
                  disabled={walletSolBalance <= 0}
                  onClick={() =>
                    onAmountChange(trimNum(walletSolBalance * 0.25, 3))
                  }
                >
                  25%
                </button>
                <button
                  type="button"
                  className="mine-chip"
                  disabled={walletSolBalance <= 0}
                  onClick={() =>
                    onAmountChange(trimNum(walletSolBalance * 0.5, 3))
                  }
                >
                  50%
                </button>
              </div>

              {safeRounds > 1 && parsedTotal > 0 && (
                <div className="mine-split-line">
                  {safeRounds}r × {trimNum(perRoundSol, 3)} SOL
                </div>
              )}

              {hasSkipRisk && (
                <div className="mine-skip-warn" role="status">
                  <span>
                    Min {trimNum(minTotalForRounds, 3)} SOL to avoid skipping
                    rounds
                  </span>
                  <button
                    type="button"
                    className="mine-skip-fix-btn"
                    onClick={() =>
                      onAmountChange(trimNum(minTotalForRounds, 3))
                    }
                  >
                    {trimNum(minTotalForRounds, 3)} SOL
                  </button>
                </div>
              )}

              {mainnetLocked && (
                <div className="mine-gate-note" role="status">
                  {mainnetGateNote ||
                    "Mainnet LV1X mining is locked off until official Mainnet pins are published."}
                </div>
              )}
            </div>

            {notice && (
              <p className="mine-notice" role="status">
                {notice}
              </p>
            )}
            {error && (
              <p className="mine-error" role="alert">
                {error}
              </p>
            )}
          </div>

          <div className="mine-composer-foot">
            <div className="mine-foot-meta">
              <span>
                Round {round?.roundSlot ?? "—"} · Pool {trimNum(poolSol, 1)} SOL
                {estSat > 0 ? ` · ~${formatCompactNum(estSat, 1)} SAT` : ""}
              </span>
              <strong>Entry · {formatCountdown(countdownSec)}</strong>
            </div>
            {poolSol < POOL_GATE_SOL && (
              <div className="mine-gate-bar" aria-hidden="true">
                <i style={{ width: `${poolGatePct.toFixed(1)}%` }} />
              </div>
            )}
            <button
              type="button"
              className="wt-btn-rose mine-cta-btn"
              disabled={busy || mainnetLocked || parsedTotal <= 0}
              onClick={onPrimarySubmit}
            >
              {mainnetLocked
                ? "Mainnet Gated"
                : safeRounds > 1
                  ? `Mine · ${safeRounds}r · ${trimNum(parsedTotal, 3)} SOL →`
                  : `Mine · ${trimNum(parsedTotal, 3)} SOL →`}
            </button>
          </div>
        </>
      )}

      {composerTab === "claim" && (
        <div className="mine-composer-body mine-composer-sub">
          <div className="mine-kv-grid">
            <div className="mine-kv-item">
              <span>SOL Ready</span>
              <strong>{trimNum(portfolio?.claimableSol ?? 0, 4)} SOL</strong>
            </div>
            <div className="mine-kv-item">
              <span>SAT Vested</span>
              <strong>
                {formatCompactNum(portfolio?.vestedSat ?? 0, 1)} SAT
              </strong>
            </div>
            <div className="mine-kv-item">
              <span>BTC Ready</span>
              <strong>
                {formatBtcCompact(portfolio?.claimableBtc ?? 0)} BTC
              </strong>
            </div>
            <div className="mine-kv-item">
              <span>Pending Rounds</span>
              <strong>{portfolio?.claimItems?.length ?? 0}</strong>
            </div>
          </div>

          <button
            type="button"
            className="wt-btn-rose mine-cta-btn"
            disabled={
              busy ||
              !selectedWalletId ||
              (portfolio?.claimItems?.length ?? 0) === 0
            }
            onClick={onClaimAll}
          >
            Claim All Rewards
          </button>

          <div className="mine-secondary-actions">
            <button
              type="button"
              className="wt-btn-soft"
              disabled={busy || !selectedWalletId}
              onClick={() => onClaimAsset("sol")}
            >
              SOL
            </button>
            <button
              type="button"
              className="wt-btn-soft"
              disabled={busy || !selectedWalletId}
              onClick={() => onClaimAsset("sat")}
            >
              SAT
            </button>
            <button
              type="button"
              className="wt-btn-soft"
              disabled={busy || !selectedWalletId}
              onClick={() => onClaimAsset("btc")}
            >
              BTC
            </button>
          </div>
        </div>
      )}

      {composerTab === "swap" && (
        <div className="mine-composer-body mine-composer-sub">
          <div className="mine-kv-grid">
            <div className="mine-kv-item">
              <span>SOL Balance</span>
              <strong>{trimNum(portfolio?.solBalanceSol ?? 0, 4)}</strong>
            </div>
            <div className="mine-kv-item">
              <span>WENSAT</span>
              <strong>{formatCompactNum(portfolio?.satBalance ?? 0, 2)}</strong>
            </div>
          </div>

          <div className="wt-hairline-table">
            <div className="wt-hairline-row">
              <span>WENSAT Mint</span>
              <button
                type="button"
                className="mine-link-btn f-mono"
                onClick={() => onCopyMint(mintAddress)}
              >
                {mintAddress
                  ? `${mintAddress.slice(0, 6)}…${mintAddress.slice(-6)}`
                  : "—"}
              </button>
            </div>
          </div>

          <button
            type="button"
            className="wt-btn-rose mine-cta-btn"
            onClick={onOpenJupiter}
          >
            <Settings2 size={14} aria-hidden="true" />
            <span>Open Jupiter Swap</span>
          </button>

          <a
            className="wt-btn-soft"
            href={`https://solscan.io/token/${encodeURIComponent(mintAddress)}?cluster=devnet`}
            target="_blank"
            rel="noreferrer"
          >
            <ExternalLink size={13} aria-hidden="true" />
            <span>Inspect WENSAT on Solscan</span>
          </a>
        </div>
      )}
    </aside>
  );
}
