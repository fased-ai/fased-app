import { BookOpen, Grid2x2 } from "../../icons";
import type {
  PortfolioResponse,
  ProtocolStats,
  RoundTelemetry,
} from "../../mining-lv1x";
import { RANGE_LABELS, RANGE_NAMES } from "../MineAllocationCurve";

const SPECTRUM = [
  "#E0566F",
  "#D98466",
  "#6F7889",
  "#2FB88C",
  "#33A9B8",
] as const;

export function formatCompactNum(v: number, dp = 2): string {
  if (!Number.isFinite(v) || v <= 0) return "0";
  if (v >= 1_000_000)
    return `${(v / 1_000_000).toFixed(2).replace(/\.?0+$/, "")}M`;
  if (v >= 10_000) return `${(v / 1_000).toFixed(1).replace(/\.0$/, "")}k`;
  return v.toLocaleString("en-US", { maximumFractionDigits: dp });
}

interface MineLeftRailProps {
  round?: RoundTelemetry;
  stats: ProtocolStats | null;
  portfolio: PortfolioResponse | null;
  selectedWalletId: string;
  onOpenMarkets: () => void;
  onOpenRules: () => void;
  onOpenClaim: () => void;
  onApplyCrowd: (weightsBps: number[]) => void;
  onNavigateSub: (sub: "assets" | "stake" | "portfolio") => void;
}

export function MineLeftRail({
  round,
  stats,
  portfolio,
  selectedWalletId,
  onOpenMarkets,
  onOpenRules,
  onOpenClaim,
  onApplyCrowd,
  onNavigateSub,
}: MineLeftRailProps) {
  const lpSolPct = stats?.lpSolMin
    ? Math.min(100, (stats.lpSol / stats.lpSolMin) * 100)
    : 0;
  const lpSatPct = stats?.lpSatMin
    ? Math.min(100, (stats.lpSat / stats.lpSatMin) * 100)
    : 0;
  const boostPct = stats?.boostBudgetSat
    ? Math.min(100, (stats.boostAwardedSat / stats.boostBudgetSat) * 100)
    : 0;

  const buckets = round?.buckets ?? [];
  const moveBps = round?.moveBps ?? 0;
  const innerBps = round?.innerBps ?? 5;
  const outerBps = round?.outerBps ?? 20;
  const winIdx =
    moveBps < -outerBps
      ? 0
      : moveBps < -innerBps
        ? 1
        : moveBps < innerBps
          ? 2
          : moveBps < outerBps
            ? 3
            : 4;
  const needlePct = ((winIdx + 0.5) / 5) * 100;

  const crowdWeights = useCrowdWeights(buckets);
  const totalClaimableUsdEst =
    (portfolio?.claimableSol ?? 0) +
    (portfolio?.claimableSat ?? 0) +
    (portfolio?.claimableBtc ?? 0);

  return (
    <aside
      className="mine-left-rail"
      aria-label="Protocol and position sidebar"
    >
      <div className="wt-wpanel mine-rail-card">
        <div className="mine-asset-row">
          <button
            type="button"
            className="mine-asset-btn"
            onClick={onOpenMarkets}
          >
            <img
              src="/wen/sat-icon-small.svg"
              alt=""
              width={22}
              height={22}
              className="mine-asset-icon"
              aria-hidden="true"
            />
            <span className="mine-asset-copy">
              <strong>WEN Satcoin</strong>
              <small>BTC/USD · 10m</small>
            </span>
            <Grid2x2 size={14} aria-hidden="true" />
          </button>
          <button
            type="button"
            className="icon-btn"
            aria-label="Mining rules"
            title="Rules"
            onClick={onOpenRules}
          >
            <BookOpen size={14} aria-hidden="true" />
          </button>
        </div>

        <div className="mine-phase-head">
          <span className="mine-phase-pill">
            {stats?.phaseName ?? "Bootstrap"}
          </span>
          <span className="mine-muted-note">
            {stats ? `${stats.boostMultiplier}× boost` : "8× boost"}
          </span>
          <button
            type="button"
            className="mine-link-btn"
            onClick={() => onNavigateSub("assets")}
          >
            Analytics →
          </button>
        </div>

        {stats?.phase === 0 && (
          <div className="mine-progress-stack">
            <div className="mine-prog-row">
              <span>LP SOL</span>
              <span>
                {formatCompactNum(stats.lpSol, 2)} /{" "}
                {formatCompactNum(stats.lpSolMin, 0)}
              </span>
            </div>
            <div className="mine-prog-track">
              <div
                className="mine-prog-fill"
                style={{ width: `${lpSolPct.toFixed(1)}%` }}
              />
            </div>
            <div className="mine-prog-row">
              <span>LP SAT</span>
              <span>
                {formatCompactNum(stats.lpSat, 0)} /{" "}
                {formatCompactNum(stats.lpSatMin, 0)}
              </span>
            </div>
            <div className="mine-prog-track">
              <div
                className="mine-prog-fill"
                style={{ width: `${lpSatPct.toFixed(1)}%` }}
              />
            </div>
            <div className="mine-prog-row">
              <span>Boost SAT</span>
              <span>
                {formatCompactNum(stats.boostAwardedSat, 0)} /{" "}
                {formatCompactNum(stats.boostBudgetSat, 0)} (
                {boostPct.toFixed(0)}%)
              </span>
            </div>
          </div>
        )}
      </div>

      <div className="wt-wpanel mine-rail-card">
        <div className="mine-section-head">
          <h2>Global Protocol</h2>
          <span className="mine-muted-note">{stats?.cluster ?? "devnet"}</span>
        </div>
        <div className="mine-kv-grid">
          <div className="mine-kv-item">
            <span>Minted</span>
            <strong>{formatCompactNum(stats?.mintedSat ?? 0, 0)} / 30M</strong>
          </div>
          <div className="mine-kv-item">
            <span>BTC Treasury</span>
            <strong>
              {formatCompactNum(stats?.btcTreasurySat ?? 0, 0)} sats
            </strong>
          </div>
          <div className="mine-kv-item">
            <span>Total Staked</span>
            <strong>{(stats?.stakedPct ?? 0).toFixed(1)}%</strong>
          </div>
          <div className="mine-kv-item">
            <span>24H Volume</span>
            <strong>{formatCompactNum(stats?.volume24hSol ?? 0, 2)} SOL</strong>
          </div>
          {stats?.feeSplit && stats.feeSplit.length === 4 && (
            <div className="mine-kv-item">
              <span>Fee Split</span>
              <strong>{stats.feeSplit.join("/")}</strong>
            </div>
          )}
          {(stats?.lpBurnedSol ?? 0) > 0 && (
            <div className="mine-kv-item">
              <span>LP Burned</span>
              <strong>
                {formatCompactNum(stats?.lpBurnedSol ?? 0, 2)} SOL
              </strong>
            </div>
          )}
        </div>
      </div>

      <div className="wt-wpanel mine-rail-card">
        <div className="mine-section-head">
          <h2>Your Position</h2>
          <button
            type="button"
            className="mine-link-btn"
            onClick={() => onNavigateSub("stake")}
          >
            Stake →
          </button>
        </div>
        <div className="mine-kv-grid">
          <div className="mine-kv-item">
            <span>Wallet SAT</span>
            <strong>{formatCompactNum(portfolio?.satBalance ?? 0, 2)}</strong>
          </div>
          <div className="mine-kv-item">
            <span>Claim SOL</span>
            <strong>{(portfolio?.claimableSol ?? 0).toFixed(3)}</strong>
          </div>
          <div className="mine-kv-item">
            <span>Claim SAT</span>
            <strong>{formatCompactNum(portfolio?.claimableSat ?? 0, 1)}</strong>
          </div>
          <div className="mine-kv-item">
            <span>Claim cbBTC</span>
            <strong>{(portfolio?.claimableBtc ?? 0).toFixed(6)}</strong>
          </div>
        </div>
        <div className="mine-pos-actions">
          <button
            type="button"
            className="wt-btn-soft"
            disabled={!selectedWalletId && totalClaimableUsdEst <= 0}
            onClick={onOpenClaim}
          >
            Claim Rewards
          </button>
          <button
            type="button"
            className="wt-btn-soft"
            onClick={() => onNavigateSub("portfolio")}
          >
            Portfolio
          </button>
        </div>
      </div>

      <div className="wt-wpanel mine-rail-card">
        <div className="mine-section-head">
          <h2>Distribution</h2>
          <button
            type="button"
            className="mine-link-btn"
            onClick={() => onApplyCrowd(crowdWeights)}
          >
            Apply
          </button>
        </div>
        <div
          className="mine-dist-gauge"
          role="img"
          aria-label="Live crowd stake distribution across A to E"
        >
          {RANGE_LABELS.map((code, rIdx) => {
            const share = buckets[rIdx]?.crowdShare ?? 0.2;
            const normH = Math.min(100, Math.max(8, share * 260));
            return (
              <div
                key={code}
                className="mine-dist-group"
                title={`${code} ${RANGE_NAMES[rIdx]} · ${(share * 100).toFixed(1)}%`}
              >
                {Array.from({ length: 10 }, (_, bIdx) => {
                  const taper =
                    0.8 + 0.35 * Math.sin(((bIdx + 0.5) / 10) * Math.PI);
                  const h = Math.min(100, normH * taper);
                  return (
                    <i
                      key={bIdx}
                      style={{
                        height: `${h}%`,
                        background: SPECTRUM[rIdx],
                      }}
                    />
                  );
                })}
              </div>
            );
          })}
          <span
            className="mine-dist-needle"
            style={{ left: `${needlePct}%` }}
            title={`Mark ${moveBps >= 0 ? "+" : ""}${moveBps} bps (${RANGE_LABELS[winIdx]})`}
          />
        </div>
        <div className="mine-dist-foot">
          {RANGE_LABELS.map((code, idx) => (
            <span key={code}>
              {code} {((buckets[idx]?.crowdShare ?? 0.2) * 100).toFixed(0)}%
            </span>
          ))}
        </div>
      </div>
    </aside>
  );
}

function useCrowdWeights(buckets: readonly { crowdShare: number }[]): number[] {
  if (buckets.length !== 5) return [2000, 2000, 2000, 2000, 2000];
  const raw = buckets.map((b) =>
    Math.max(0, Math.round((b.crowdShare || 0.2) * 50) * 200),
  );
  const sum = raw.reduce((a, b) => a + b, 0);
  if (sum !== 10000) {
    raw[2] = Math.max(0, raw[2] + (10000 - sum));
  }
  return raw;
}
