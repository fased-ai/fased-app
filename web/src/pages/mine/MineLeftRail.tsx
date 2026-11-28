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

const RANGE_TEXT = [
  "var(--wt-danger)",
  "var(--wt-warn)",
  "var(--wt-muted)",
  "var(--wt-ok)",
  "var(--wt-cyan-text)",
] as const;

const SUB_DIGITS = ["₀", "₁", "₂", "₃", "₄", "₅", "₆", "₇", "₈", "₉"] as const;

export function formatCompactNum(v: number, dp = 2): string {
  if (!Number.isFinite(v) || v <= 0) return "0";
  if (v >= 1_000_000)
    return `${(v / 1_000_000).toFixed(2).replace(/\.?0+$/, "")}M`;
  if (v >= 1_000) return `${(v / 1_000).toFixed(1).replace(/\.0$/, "")}k`;
  return v.toLocaleString("en-US", { maximumFractionDigits: dp });
}

export function formatBtcCompact(btc: number): string {
  if (!Number.isFinite(btc) || btc <= 0) return "0";
  if (btc >= 0.01) return btc.toFixed(4).replace(/\.?0+$/, "");
  const s = btc.toFixed(8);
  const dot = s.indexOf(".");
  if (dot < 0) return s;
  const frac = s.slice(dot + 1);
  let zeros = 0;
  while (zeros < frac.length && frac[zeros] === "0") zeros++;
  if (zeros >= 3 && zeros < frac.length) {
    const sig = frac.slice(zeros, zeros + 2).replace(/0+$/, "") || "1";
    const sub = String(zeros)
      .split("")
      .map((d) => SUB_DIGITS[Number(d)] ?? d)
      .join("");
    return `0.0${sub}${sig}`;
  }
  return btc.toFixed(6).replace(/\.?0+$/, "");
}

function InfoCircleIcon() {
  return (
    <svg
      width="14"
      height="14"
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

interface MineLeftRailProps {
  round?: RoundTelemetry;
  stats: ProtocolStats | null;
  portfolio: PortfolioResponse | null;
  stakedSat?: number;
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
  stakedSat = 0,
  selectedWalletId,
  onOpenMarkets,
  onOpenRules,
  onOpenClaim,
  onApplyCrowd,
  onNavigateSub,
}: MineLeftRailProps) {
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
  const claimSol = portfolio?.claimableSol ?? 0;
  const claimSat = portfolio?.vestedSat ?? portfolio?.claimableSat ?? 0;
  const claimBtc = portfolio?.claimableBtc ?? 0;
  const hasClaimable = claimSol > 0 || claimSat > 0 || claimBtc > 0;

  const isBootstrap = stats?.phase === 0;
  const phaseLabel = stats?.phaseName
    ? stats.phaseName
    : isBootstrap
      ? "Bootstrap"
      : "Graduated";
  const phaseTone = isBootstrap ? "is-bootstrap" : "is-graduated";

  const lpSolPct = stats?.lpSolMin
    ? Math.min(100, (stats.lpSol / stats.lpSolMin) * 100)
    : 0;
  const lpSatPct = stats?.lpSatMin
    ? Math.min(100, (stats.lpSat / stats.lpSatMin) * 100)
    : 0;

  const bullShare =
    (buckets[3]?.crowdShare ?? 0.2) + (buckets[4]?.crowdShare ?? 0.2);
  const bearShare =
    (buckets[0]?.crowdShare ?? 0.2) + (buckets[1]?.crowdShare ?? 0.2);
  const skew =
    bullShare - bearShare > 0.06
      ? "Bullish"
      : bearShare - bullShare > 0.06
        ? "Bearish"
        : moveBps > innerBps
          ? "Bullish"
          : moveBps < -innerBps
            ? "Bearish"
            : "Balanced";
  const movePctText = `${moveBps >= 0 ? "+" : ""}${(moveBps / 100).toFixed(2)}%`;

  const paidBtc = (stats?.btcTreasurySat ?? 0) / 1e8;

  return (
    <aside
      className="mine-left-rail"
      aria-label="Protocol and position sidebar"
    >
      <div className="mine-rail-head">
        <button
          type="button"
          className="mine-rail-asset-select"
          aria-label="Select mining market"
          onClick={onOpenMarkets}
        >
          <img
            src="/wen/sat-icon-small.svg"
            alt=""
            width={28}
            height={28}
            className="mine-rail-asset-logo"
            aria-hidden="true"
          />
          <span>WEN Satcoin</span>
          <span className="mine-rail-caret" aria-hidden="true">
            ▾
          </span>
        </button>
        <button
          type="button"
          className="mine-rail-info-btn"
          aria-label="Mining rules"
          title="Mining rules"
          onClick={onOpenRules}
        >
          <InfoCircleIcon />
        </button>
      </div>

      <dl className="mine-rail-price-dl">
        <div>
          <dt>BTC / USD</dt>
          <dd>
            $
            {(round?.markPriceUsd ?? 94265).toLocaleString("en-US", {
              minimumFractionDigits: 2,
              maximumFractionDigits: 2,
            })}
          </dd>
        </div>
      </dl>

      <div className="mine-rail-status-row">
        <span className={`mine-rail-phase ${phaseTone}`}>
          <i className="mine-rail-status-dot" aria-hidden="true" />
          {phaseLabel}
        </span>
        <button
          type="button"
          className="mine-rail-analytics-link"
          onClick={() => onNavigateSub("assets")}
        >
          Analytics →
        </button>
      </div>

      {isBootstrap && (stats?.lpSolMin ?? 0) > 0 && (
        <div className="mine-rail-bootstrap-card">
          <div className="mine-rail-prog-line">
            <span>SOL</span>
            <b>
              {formatCompactNum(stats?.lpSol ?? 0, 2)} /{" "}
              {formatCompactNum(stats?.lpSolMin ?? 0, 0)}
            </b>
          </div>
          <div className="mine-prog-track">
            <div
              className="mine-prog-fill"
              style={{ width: `${lpSolPct.toFixed(1)}%` }}
            />
          </div>
          <div className="mine-rail-prog-line">
            <span>SAT</span>
            <b>
              {formatCompactNum(stats?.lpSat ?? 0, 0)} /{" "}
              {formatCompactNum(stats?.lpSatMin ?? 0, 0)}
            </b>
          </div>
          <div className="mine-prog-track">
            <div
              className="mine-prog-fill"
              style={{ width: `${lpSatPct.toFixed(1)}%` }}
            />
          </div>
        </div>
      )}

      <section
        className="mine-rail-box-card"
        aria-label="Global protocol stats"
      >
        <dl className="mine-rail-stats-grid">
          <div
            title={`${formatCompactNum(stats?.mintedSat ?? 0, 0)} / 30M WENSAT`}
          >
            <dt>Minted</dt>
            <dd>{formatCompactNum(stats?.mintedSat ?? 0, 1)}</dd>
          </div>
          <div title={`${paidBtc.toFixed(8)} BTC`}>
            <dt>Paid</dt>
            <dd>
              {formatBtcCompact(paidBtc)}
              <small>BTC</small>
            </dd>
          </div>
          <div>
            <dt>Staked</dt>
            <dd>{(stats?.stakedPct ?? 0).toFixed(1)}%</dd>
          </div>
          <div>
            <dt>24H</dt>
            <dd>
              {formatCompactNum(stats?.volume24hSol ?? 0, 1)}
              <small>SOL</small>
            </dd>
          </div>
        </dl>
      </section>

      <section className="mine-rail-box-card" aria-label="Your position">
        <dl className="mine-rail-stats-grid">
          <div>
            <dt>Staked</dt>
            <dd>{selectedWalletId ? formatCompactNum(stakedSat, 1) : "—"}</dd>
          </div>
          <div>
            <dt>Claim</dt>
            <dd className="mine-rail-claim-vals">
              {!selectedWalletId ? (
                <span>—</span>
              ) : hasClaimable ? (
                <>
                  {claimSol > 0 && (
                    <span>
                      {claimSol.toFixed(3).replace(/\.?0+$/, "")}
                      <small>SOL</small>
                    </span>
                  )}
                  {claimSat > 0 && (
                    <span>
                      {formatCompactNum(claimSat, 1)}
                      <small>SAT</small>
                    </span>
                  )}
                  {claimBtc > 0 && (
                    <span>
                      {formatBtcCompact(claimBtc)}
                      <small>BTC</small>
                    </span>
                  )}
                </>
              ) : (
                <span>0</span>
              )}
            </dd>
          </div>
        </dl>
        {selectedWalletId && (
          <button
            type="button"
            className="mine-rail-claim-btn"
            disabled={!hasClaimable}
            onClick={onOpenClaim}
          >
            Claim
          </button>
        )}
        <button
          type="button"
          className="mine-rail-stake-btn"
          onClick={() => onNavigateSub("stake")}
        >
          Stake →
        </button>
      </section>

      <section className="mine-rail-pulse" aria-label="Distribution">
        <header className="mine-rail-pulse-head">
          <span>Distribution</span>
          <button
            type="button"
            className="mine-rail-apply-btn"
            onClick={() => onApplyCrowd(crowdWeights)}
          >
            Apply
          </button>
        </header>
        <div
          className="mine-dist-meter"
          role="img"
          aria-label="Live crowd stake distribution across A to E"
        >
          <div className="mine-dist-bars">
            {RANGE_LABELS.map((code, rIdx) => {
              const share = buckets[rIdx]?.crowdShare ?? 0.2;
              const normH = Math.min(100, Math.max(8, share * 260));
              return (
                <div
                  key={code}
                  className={`mine-dist-group${rIdx === winIdx ? " is-current" : ""}`}
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
          </div>
          <i
            className="mine-dist-needle"
            style={{ left: `${Math.max(2, Math.min(98, needlePct))}%` }}
            aria-hidden="true"
          />
        </div>
        <div className="mine-dist-axis">
          {RANGE_LABELS.map((code, i) => (
            <div
              key={code}
              className={`mine-dist-tick${i === winIdx ? " is-current" : ""}`}
            >
              <b style={{ color: RANGE_TEXT[i] }}>{code}</b>
              <small>{Math.round((buckets[i]?.crowdShare ?? 0) * 100)}%</small>
            </div>
          ))}
        </div>
        <div className="mine-rail-pulse-foot">
          <span className={`mine-rail-skew is-${skew.toLowerCase()}`}>
            {skew}
          </span>
          <b style={{ color: RANGE_TEXT[winIdx] }}>{movePctText}</b>
        </div>
      </section>
    </aside>
  );
}

function useCrowdWeights(buckets: readonly { crowdShare: number }[]): number[] {
  if (!buckets || buckets.length !== 5) return [2000, 2000, 2000, 2000, 2000];
  const raw = buckets.map((b) =>
    Math.max(0, Math.round(b.crowdShare * 50) * 200),
  );
  const sum = raw.reduce((a, v) => a + v, 0);
  if (sum !== 10000) raw[2] = Math.max(0, raw[2] + (10000 - sum));
  return raw;
}
