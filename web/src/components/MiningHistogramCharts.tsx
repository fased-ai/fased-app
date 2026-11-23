import type { CSSProperties } from "react";
import { useUI } from "../ui-preferences";

export type MiningPresetId = "balanced" | "center" | "tails";

export type MiningPreset = {
  id: MiningPresetId;
  label: string;
  weightsBps: readonly [number, number, number, number, number];
};

export const MINING_PRESETS: readonly MiningPreset[] = [
  {
    id: "balanced",
    label: "Balanced",
    weightsBps: [2000, 2000, 2000, 2000, 2000],
  },
  {
    id: "center",
    label: "Center Focus",
    weightsBps: [1000, 2000, 4000, 2000, 1000],
  },
  {
    id: "tails",
    label: "Tail Hedge",
    weightsBps: [3000, 1500, 1000, 1500, 3000],
  },
];

export const RANGE_LABELS = [
  "Range R1",
  "Range R2",
  "Range R3",
  "Range R4",
  "Range R5",
] as const;

export const ROUND_PHASES = [
  "Open",
  "Commit",
  "Reveal",
  "Settle",
  "Claim",
] as const;

/** Computes 5 non-negative BigInt lamport allocations conserving total amountLamports exactly. */
export function computePresetAllocations(
  amountLamports: string | undefined,
  weightsBps: readonly number[],
): [string, string, string, string, string] {
  if (
    !amountLamports ||
    !/^\d+$/.test(amountLamports) ||
    weightsBps.length !== 5
  ) {
    return ["0", "0", "0", "0", "0"];
  }
  const total = BigInt(amountLamports);
  if (total <= 0n) {
    return ["0", "0", "0", "0", "0"];
  }
  const out: [bigint, bigint, bigint, bigint, bigint] = [0n, 0n, 0n, 0n, 0n];
  let sum = 0n;
  let maxIdx = 0;
  for (let i = 0; i < 5; i++) {
    if (weightsBps[i] > weightsBps[maxIdx]) maxIdx = i;
    const alloc = (total * BigInt(weightsBps[i])) / 10000n;
    out[i] = alloc;
    sum += alloc;
  }
  out[maxIdx] += total - sum;
  return [
    out[0].toString(),
    out[1].toString(),
    out[2].toString(),
    out[3].toString(),
    out[4].toString(),
  ];
}

function lamportsShort(lamports: string): string {
  const n = BigInt(lamports || "0");
  const whole = n / 1_000_000_000n;
  const frac = ((n % 1_000_000_000n) / 1_000_000n).toString().padStart(3, "0");
  return `${whole}.${frac}`;
}

export function RetailAllocationChart({
  allocationsLamports,
  weightsBps,
}: {
  allocationsLamports: readonly string[];
  weightsBps: readonly number[];
}) {
  const { t } = useUI();
  const maxWeight = Math.max(...weightsBps, 1);
  return (
    <section
      className="panel wen-chart-panel"
      aria-label={t("Allocation histogram")}
    >
      <h2>{t("Allocation histogram")}</h2>
      <div className="wen-bars" role="list">
        {RANGE_LABELS.map((rangeKey, idx) => {
          const bps = weightsBps[idx] ?? 0;
          const pctHeight = Math.max(8, Math.round((bps / maxWeight) * 100));
          const sol = lamportsShort(allocationsLamports[idx] ?? "0");
          const barStyle = { "--wen-bar-h": `${pctHeight}%` } as CSSProperties;
          return (
            <div className="wen-bar-col" key={rangeKey} role="listitem">
              <span className="wen-bar-val mono">{sol}</span>
              <div className="wen-bar-track">
                <div className="wen-bar-fill wen-bar-alloc" style={barStyle} />
              </div>
              <span className="wen-bar-lbl">{t(rangeKey)}</span>
              <span className="wen-bar-bps">{(bps / 100).toFixed(0)}%</span>
            </div>
          );
        })}
      </div>
    </section>
  );
}

export function RetailMarketChart({
  poolWeightsBps = [1600, 2200, 3200, 1900, 1100],
  activePhase,
}: {
  poolWeightsBps?: readonly number[];
  activePhase?: string;
}) {
  const { t } = useUI();
  const maxPool = Math.max(...poolWeightsBps, 1);
  return (
    <section
      className="panel wen-chart-panel"
      aria-label={t("Market distribution")}
    >
      <h2>{t("Market distribution")}</h2>
      <div className="wen-bars" role="list">
        {RANGE_LABELS.map((rangeKey, idx) => {
          const bps = poolWeightsBps[idx] ?? 0;
          const pctHeight = Math.max(8, Math.round((bps / maxPool) * 100));
          const barStyle = { "--wen-bar-h": `${pctHeight}%` } as CSSProperties;
          return (
            <div className="wen-bar-col" key={rangeKey} role="listitem">
              <span className="wen-bar-val mono">
                {(bps / 100).toFixed(0)}%
              </span>
              <div className="wen-bar-track">
                <div className="wen-bar-fill wen-bar-market" style={barStyle} />
              </div>
              <span className="wen-bar-lbl">{t(rangeKey)}</span>
            </div>
          );
        })}
      </div>
      <div className="wen-phases" aria-label={t("Round phases")}>
        {ROUND_PHASES.map((phase) => {
          const current =
            activePhase === phase || (!activePhase && phase === "Open");
          return (
            <span
              key={phase}
              className={`wen-phase-pill${current ? " is-active" : ""}`}
            >
              {t(phase)}
            </span>
          );
        })}
      </div>
    </section>
  );
}
