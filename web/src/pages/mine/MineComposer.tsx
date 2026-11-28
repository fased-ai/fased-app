import {
  Clock3,
  Copy,
  ExternalLink,
  Layers,
  Plus,
  RotateCcw,
  Settings2,
  SlidersHorizontal,
} from "../../icons";
import type {
  CommitPreview,
  MiningMode,
  PortfolioResponse,
  RoundTelemetry,
  StrategyPreset,
} from "../../mining-lv1x";
import type { WalletV2 } from "../../wallets-v2";
import {
  MineAllocationCurve,
  MinePresetIcon,
  RANGE_LABELS,
  RANGE_NAMES,
} from "../MineAllocationCurve";

export type ComposerTab = "mine" | "claim" | "swap";

interface MineComposerProps {
  composerTab: ComposerTab;
  onComposerTabChange: (tab: ComposerTab) => void;
  mode: MiningMode;
  onModeChange: (m: MiningMode) => void;
  presets: StrategyPreset[];
  presetId: string;
  weightsBps: number[];
  selectedBucket: number | null;
  round?: RoundTelemetry;
  roundsCount: number;
  onQuickRounds: (n: number) => void;
  onOpenScheduleGrid: () => void;
  amountStr: string;
  onAmountChange: (v: string) => void;
  walletSolBalance: number;
  rollingBudget: boolean;
  skipRiskWarning?: string;
  autoClaim: boolean;
  onAutoClaimChange: (v: boolean) => void;
  stopOnReversal: boolean;
  onStopOnReversalChange: (v: boolean) => void;
  wallets: WalletV2[];
  selectedWalletId: string;
  onSelectPreset: (p: StrategyPreset) => void;
  onCurveRebalance: (idx: number, bps: number) => void;
  onSelectBucket: (idx: number) => void;
  onShareCurve: () => void;
  onOpenSaveStrategy: () => void;
  onOpenPolicy: () => void;
  onCreateTask: () => void;
  onPrimarySubmit: () => void;
  onClaimAsset: (asset: "sol" | "sat" | "btc") => void;
  onClaimAll: () => void;
  onCopyMint: (mint: string) => void;
  onOpenJupiter: () => void;
  portfolio: PortfolioResponse | null;
  preview: CommitPreview | null;
  countdownSec: number;
  mainnetLocked: boolean;
  mainnetGateNote?: string;
  mintAddress: string;
  btcMintAddress: string;
  busy: boolean;
  notice: string;
  error: string;
}

const QUICK_AMOUNTS = ["0.05", "0.1", "0.2"] as const;

function formatCountdown(remainingSec: number): string {
  const safe = Math.max(0, Math.floor(remainingSec));
  const m = Math.floor(safe / 60);
  const s = safe % 60;
  return `${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`;
}

export function MineComposer({
  composerTab,
  onComposerTabChange,
  mode,
  onModeChange,
  presets,
  presetId,
  weightsBps,
  selectedBucket,
  round,
  roundsCount,
  onQuickRounds,
  onOpenScheduleGrid,
  amountStr,
  onAmountChange,
  walletSolBalance,
  skipRiskWarning,
  autoClaim,
  onAutoClaimChange,
  stopOnReversal,
  onStopOnReversalChange,
  selectedWalletId,
  onSelectPreset,
  onCurveRebalance,
  onSelectBucket,
  onShareCurve,
  onOpenSaveStrategy,
  onOpenPolicy,
  onCreateTask,
  onPrimarySubmit,
  onClaimAsset,
  onClaimAll,
  onCopyMint,
  onOpenJupiter,
  portfolio,
  preview,
  countdownSec,
  mainnetLocked,
  mainnetGateNote,
  mintAddress,
  busy,
  notice,
  error,
}: MineComposerProps) {
  const parsedAmount = Number(amountStr) || 0;

  return (
    <aside className="wt-wpanel mine-composer" aria-label="Mining composer">
      <div
        className="wt-tabs-underline"
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
          <div className="mine-section-head">
            <span className="mine-muted-note">Strategy</span>
            <div className="mine-composer-tools">
              <button
                type="button"
                className="icon-btn"
                title="Copy share URL (?curve=...)"
                aria-label="Share curve URL"
                onClick={onShareCurve}
              >
                <Copy size={13} aria-hidden="true" />
              </button>
              <button
                type="button"
                className="icon-btn"
                title="Save strategy to library"
                aria-label="Save strategy"
                onClick={onOpenSaveStrategy}
              >
                <Layers size={13} aria-hidden="true" />
              </button>
              <button
                type="button"
                className="icon-btn"
                aria-label="Reset curve to Wide preset"
                title="Reset to Wide"
                onClick={() => onSelectPreset(presets[0])}
              >
                <RotateCcw size={13} aria-hidden="true" />
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

          <MineAllocationCurve
            weightsBps={weightsBps}
            innerBps={round?.innerBps ?? 5}
            outerBps={round?.outerBps ?? 20}
            onRebalance={onCurveRebalance}
          />

          <div
            className="mine-bucket-strip"
            role="group"
            aria-label="Range buckets"
          >
            {RANGE_LABELS.map((code, i) => {
              const bucket = round?.buckets?.[i];
              const pct = ((weightsBps[i] ?? 0) / 100).toFixed(0);
              return (
                <button
                  key={code}
                  type="button"
                  className={`mine-bucket-cell${selectedBucket === i ? " is-selected" : ""}`}
                  onClick={() => onSelectBucket(i)}
                  title={`${code} ${RANGE_NAMES[i]}`}
                >
                  <span>{code}</span>
                  <strong>{pct}%</strong>
                  <small>
                    {bucket ? `${bucket.evMultiple.toFixed(2)}×` : "—"}
                  </small>
                </button>
              );
            })}
          </div>

          <div className="mine-field">
            <label>Rounds</label>
            <div className="mine-quick-chips">
              {[1, 2, 6, 12].map((n) => (
                <button
                  key={n}
                  type="button"
                  className="mine-chip"
                  aria-pressed={roundsCount === n}
                  onClick={() => onQuickRounds(n)}
                >
                  {n}r
                </button>
              ))}
              <button
                type="button"
                className="mine-chip"
                onClick={onOpenScheduleGrid}
              >
                Edit
              </button>
            </div>
          </div>

          <div className="mine-field">
            <label htmlFor="mine-amount-input">
              Deposit SOL {mode !== "manual" ? "(per round)" : ""}
            </label>
            <div className="mine-amount-box">
              <img
                src="/wen/sol-icon.svg"
                alt=""
                width={18}
                height={18}
                className="mine-amount-icon"
                aria-hidden="true"
              />
              <input
                id="mine-amount-input"
                className="mine-amount-input"
                inputMode="decimal"
                value={amountStr}
                onChange={(e) => onAmountChange(e.target.value)}
              />
              <span className="mine-amount-unit">SOL</span>
            </div>
            <div className="mine-quick-chips">
              {QUICK_AMOUNTS.map((v) => (
                <button
                  key={v}
                  type="button"
                  className="mine-chip"
                  aria-pressed={amountStr === v}
                  onClick={() => onAmountChange(v)}
                >
                  {v} SOL
                </button>
              ))}
              {walletSolBalance > 0 && (
                <>
                  <button
                    type="button"
                    className="mine-chip"
                    onClick={() =>
                      onAmountChange((walletSolBalance * 0.25).toFixed(3))
                    }
                  >
                    25%
                  </button>
                  <button
                    type="button"
                    className="mine-chip"
                    onClick={() =>
                      onAmountChange((walletSolBalance * 0.5).toFixed(3))
                    }
                  >
                    50%
                  </button>
                </>
              )}
            </div>
          </div>

          <div className="wt-seg" role="group" aria-label="Execution mode">
            <button
              type="button"
              aria-pressed={mode === "manual"}
              onClick={() => onModeChange("manual")}
            >
              Manual
            </button>
            <button
              type="button"
              aria-pressed={mode === "semi_auto"}
              onClick={() => onModeChange("semi_auto")}
            >
              Semi-Auto
            </button>
            <button
              type="button"
              aria-pressed={mode === "auto"}
              onClick={() => onModeChange("auto")}
            >
              Auto
            </button>
          </div>

          {mode !== "manual" && (
            <>
              <label className="mine-toggle-row">
                <input
                  type="checkbox"
                  checked={autoClaim}
                  onChange={(e) => onAutoClaimChange(e.target.checked)}
                />
                <span>Auto-claim SOL, SAT &amp; cbBTC</span>
              </label>
              <label className="mine-toggle-row">
                <input
                  type="checkbox"
                  checked={stopOnReversal}
                  onChange={(e) => onStopOnReversalChange(e.target.checked)}
                />
                <span>AI pause on BTC trend reversal</span>
              </label>
            </>
          )}

          {skipRiskWarning && (
            <div className="mine-gate-note" role="status">
              {skipRiskWarning}
            </div>
          )}

          {mainnetLocked && (
            <div className="mine-gate-note" role="status">
              {mainnetGateNote ||
                "Mainnet LV1X mining is locked off until official Mainnet program IDs and BPF ELF hashes are published."}
            </div>
          )}

          <div className="mine-composer-footer">
            <div className="mine-kv-row">
              <span>
                <Clock3 size={12} aria-hidden="true" /> Round #
                {round?.roundSlot ?? "—"} · {formatCountdown(countdownSec)}
              </span>
              <strong>
                {preview
                  ? `${preview.dominantCode} · EV ${preview.expectedEvMultiple.toFixed(2)}×`
                  : "—"}
              </strong>
            </div>

            <button
              type="button"
              className="wt-btn-rose"
              disabled={busy || mainnetLocked || parsedAmount <= 0}
              onClick={onPrimarySubmit}
            >
              {mode === "manual"
                ? "Commit Round"
                : mode === "semi_auto" && roundsCount > 1
                  ? "Start Session"
                  : "Start Session"}
            </button>

            <div className="mine-secondary-actions">
              <button
                type="button"
                className="wt-btn-soft"
                onClick={onCreateTask}
              >
                <Plus size={13} aria-hidden="true" />
                <span>Create Mining Task</span>
              </button>
              <button
                type="button"
                className="wt-btn-soft"
                disabled={!selectedWalletId}
                onClick={onOpenPolicy}
              >
                <SlidersHorizontal size={13} aria-hidden="true" />
                <span>Policy</span>
              </button>
            </div>
          </div>
        </>
      )}

      {composerTab === "claim" && (
        <div className="mine-composer-sub">
          <div className="mine-kv-grid">
            <div className="mine-kv-item">
              <span>SOL Ready</span>
              <strong>{(portfolio?.claimableSol ?? 0).toFixed(4)} SOL</strong>
            </div>
            <div className="mine-kv-item">
              <span>SAT Vested</span>
              <strong>{(portfolio?.vestedSat ?? 0).toFixed(2)} SAT</strong>
            </div>
            <div className="mine-kv-item">
              <span>cbBTC Ready</span>
              <strong>{(portfolio?.claimableBtc ?? 0).toFixed(6)} BTC</strong>
            </div>
            <div className="mine-kv-item">
              <span>Pending Rounds</span>
              <strong>{portfolio?.claimItems?.length ?? 0}</strong>
            </div>
          </div>

          <button
            type="button"
            className="wt-btn-rose"
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
              cbBTC
            </button>
          </div>
        </div>
      )}

      {composerTab === "swap" && (
        <div className="mine-composer-sub">
          <div className="mine-kv-grid">
            <div className="mine-kv-item">
              <span>SOL Balance</span>
              <strong>{(portfolio?.solBalanceSol ?? 0).toFixed(4)}</strong>
            </div>
            <div className="mine-kv-item">
              <span>WENSAT</span>
              <strong>{(portfolio?.satBalance ?? 0).toFixed(2)}</strong>
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
                {mintAddress.slice(0, 6)}…{mintAddress.slice(-6)}
              </button>
            </div>
          </div>

          <button type="button" className="wt-btn-rose" onClick={onOpenJupiter}>
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
    </aside>
  );
}
