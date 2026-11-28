import { useRef, type CSSProperties } from "react";

export const RANGE_LABELS = ["A", "B", "C", "D", "E"] as const;
export const RANGE_NAMES = [
  "Strong down",
  "Down",
  "Center",
  "Up",
  "Strong up",
] as const;

export const BARS_PER_RANGE = 16;

// Exact 16-bar interpolated WEN spectrum colors per bucket (A..E).
const RANGE_COLORS = [
  ["#DA3855", "#DC435E", "#E0566F", "#E26178", "#E46C81", "#E6768B"],
  ["#D26D49", "#D47554", "#D98466", "#DC8C70", "#DE947A", "#E19D85"],
  ["#5F6775", "#656D7C", "#6F7889", "#757E8F", "#7C8595", "#838C9B"],
  ["#289C76", "#2AA67E", "#2FB88C", "#32C294", "#35CB9B", "#3FCEA0"],
  ["#2B8F9C", "#2E98A6", "#33A9B8", "#36B2C2", "#3CB9C9", "#46BDCC"],
] as const;

export function formatBoundaryTicks(
  innerBps = 5,
  outerBps = 20,
): readonly [string, string, string, string] {
  const inner = (innerBps / 100).toFixed(2);
  const outer = (outerBps / 100).toFixed(2);
  return [`−${outer}%`, `−${inner}%`, `+${inner}%`, `+${outer}%`];
}

/** Smooth 80-bar density weights matching WEN's RetailAllocationChart math. */
export function allocationMicrobarHeights(
  weights: readonly number[],
): number[][] {
  const values = RANGE_LABELS.map((_, i) =>
    Number.isFinite(weights[i]) && weights[i] >= 0 && weights[i] <= 10000
      ? weights[i]
      : 0,
  );
  const uniform = values.every((w) => w === values[0]);
  const groups = values.map((weight, i) => {
    if (!weight) return Array<number>(BARS_PER_RANGE).fill(0);
    const left =
      i === 0 ? (uniform ? weight : 0) : Math.min(values[i - 1], weight);
    const right =
      i === 4 ? (uniform ? weight : 0) : Math.min(weight, values[i + 1]);
    const remainder = weight - (left + right) / 2;
    const samples = Array.from({ length: BARS_PER_RANGE }, (_, j) => {
      const t = (j + 0.5) / BARS_PER_RANGE;
      const blend = t * t * (3 - 2 * t);
      return (
        left * (1 - blend) +
        right * blend +
        remainder * 30 * t * t * (1 - t) * (1 - t)
      );
    });
    const sum = samples.reduce((a, b) => a + b, 0);
    return samples.map((v) =>
      sum > 0 ? (v * weight * BARS_PER_RANGE) / sum : 0,
    );
  });
  const scale = Math.max(...groups.flat(), 0);
  return groups.map((group) =>
    group.map((value) => (scale > 0 ? (value / scale) * 100 : 0)),
  );
}

/** Normalizes 5 bucket bps weights back to an 80-bar fraction array summing to 1.0. */
export function weightsBpsToCurve80(weightsBps: readonly number[]): number[] {
  const safe = RANGE_LABELS.map((_, i) =>
    Math.max(0, Number(weightsBps[i] ?? 0)),
  );
  const raw: number[] = [];
  for (let b = 0; b < 5; b++) {
    const perBar = safe[b] / 10000 / BARS_PER_RANGE;
    for (let j = 0; j < BARS_PER_RANGE; j++) {
      raw.push(perBar);
    }
  }
  const sum = raw.reduce((a, b) => a + b, 0);
  if (sum <= 0) {
    return Array<number>(80).fill(1 / 80);
  }
  return raw.map((v) => v / sum);
}

/** Rebalances a single bucket in 200-bps steps while keeping total = 10000 bps. */
export function rebalanceBucketsBps(
  current: readonly number[],
  targetIndex: number,
  nextWeight: number,
): number[] {
  const clamped = Math.max(
    0,
    Math.min(10000, Math.round(nextWeight / 200) * 200),
  );
  const next = RANGE_LABELS.map((_, i) =>
    Math.max(0, Math.round(Number(current[i] ?? 0))),
  );
  next[targetIndex] = clamped;
  const remainingTarget = 10000 - clamped;
  const othersIdx = [0, 1, 2, 3, 4].filter((i) => i !== targetIndex);
  const othersSum = othersIdx.reduce((acc, idx) => acc + next[idx], 0);
  if (othersSum <= 0) {
    const base = Math.floor(remainingTarget / othersIdx.length);
    let rem = remainingTarget - base * othersIdx.length;
    for (const idx of othersIdx) {
      next[idx] = base + (rem > 0 ? 1 : 0);
      if (rem > 0) rem--;
    }
    return next;
  }
  let assigned = 0;
  for (let k = 0; k < othersIdx.length; k++) {
    const idx = othersIdx[k];
    if (k === othersIdx.length - 1) {
      next[idx] = Math.max(0, remainingTarget - assigned);
    } else {
      const share = Math.round((next[idx] / othersSum) * remainingTarget);
      next[idx] = Math.max(0, share);
      assigned += next[idx];
    }
  }
  return next;
}

function barColor(range: number, bar: number): string {
  const colors = RANGE_COLORS[range];
  const position = (bar * (colors.length - 1)) / (BARS_PER_RANGE - 1);
  const index = Math.min(Math.floor(position), colors.length - 2);
  const fraction = position - index;
  const channels = [1, 3, 5].map((offset) =>
    Math.round(
      parseInt(colors[index].slice(offset, offset + 2), 16) * (1 - fraction) +
        parseInt(colors[index + 1].slice(offset, offset + 2), 16) * fraction,
    ),
  );
  return `rgb(${channels.join(" ")})`;
}

export function MinePresetIcon({ weights }: { weights: readonly number[] }) {
  const maximum = Math.max(...weights, 1);
  return (
    <svg viewBox="0 0 24 16" aria-hidden="true" focusable="false">
      {weights.slice(0, 5).map((w, i) => (
        <rect
          key={i}
          x={i * 5}
          y={16 - (w / maximum) * 14}
          width="3"
          height={Math.max(1, (w / maximum) * 14)}
          rx=".6"
          fill="currentColor"
        />
      ))}
    </svg>
  );
}

export interface MineAllocationCurveProps {
  weightsBps: readonly number[];
  innerBps?: number;
  outerBps?: number;
  hideLabels?: boolean;
  onRebalance?: (rangeIndex: number, weightBps: number) => void;
}

export function MineAllocationCurve({
  weightsBps,
  innerBps = 5,
  outerBps = 20,
  hideLabels = false,
  onRebalance,
}: MineAllocationCurveProps) {
  const boundaryTicks = formatBoundaryTicks(innerBps, outerBps);
  const hostRef = useRef<HTMLDivElement | null>(null);
  const draggingRef = useRef(false);

  const values = RANGE_LABELS.map((_, i) => {
    const w = Number(weightsBps[i] ?? 0);
    return Number.isFinite(w) && w >= 0 && w <= 10000 ? w : 0;
  });
  const heights = allocationMicrobarHeights(values);

  const applyPointer = (clientX: number, clientY: number) => {
    if (!onRebalance || !hostRef.current) return;
    const rect = hostRef.current.getBoundingClientRect();
    if (rect.width <= 0 || rect.height <= 0) return;
    const relX = Math.max(
      0,
      Math.min(0.9999, (clientX - rect.left) / rect.width),
    );
    const idx = Math.min(4, Math.max(0, Math.floor(relX * 5)));
    const binEl = hostRef.current.querySelector(".mine-bin-group");
    const yRect = binEl ? binEl.getBoundingClientRect() : rect;
    const yHeight = yRect.height > 0 ? yRect.height : rect.height;
    const relY = Math.max(0, Math.min(1, 1 - (clientY - yRect.top) / yHeight));
    const snapped = Math.round(relY * 50) * 200;
    const clamped = Math.max(0, Math.min(10000, snapped));
    if (clamped !== values[idx]) onRebalance(idx, clamped);
  };

  return (
    <div
      ref={hostRef}
      className={`mine-allocation-chart${onRebalance ? " is-interactive" : ""}`}
      role={onRebalance ? "group" : "img"}
      aria-label={`Allocation curve: ${values.map((w, i) => `${RANGE_LABELS[i]} ${(w / 100).toFixed(0)}%`).join(", ")}`}
      onPointerDown={
        onRebalance
          ? (e) => {
              e.preventDefault();
              draggingRef.current = true;
              e.currentTarget.setPointerCapture?.(e.pointerId);
              applyPointer(e.clientX, e.clientY);
            }
          : undefined
      }
      onPointerMove={
        onRebalance
          ? (e) => {
              if (!draggingRef.current) return;
              applyPointer(e.clientX, e.clientY);
            }
          : undefined
      }
      onPointerUp={
        onRebalance
          ? (e) => {
              draggingRef.current = false;
              e.currentTarget.releasePointerCapture?.(e.pointerId);
            }
          : undefined
      }
      onPointerCancel={
        onRebalance
          ? () => {
              draggingRef.current = false;
            }
          : undefined
      }
    >
      {values.map((weight, index) => (
        <div
          key={RANGE_LABELS[index]}
          className={`mine-bar range-${index}`}
          tabIndex={onRebalance ? 0 : undefined}
          role={onRebalance ? "slider" : undefined}
          aria-label={
            onRebalance ? `Range ${RANGE_LABELS[index]} weight` : undefined
          }
          aria-valuemin={onRebalance ? 0 : undefined}
          aria-valuemax={onRebalance ? 100 : undefined}
          aria-valuenow={onRebalance ? Math.round(weight / 100) : undefined}
          aria-valuetext={
            onRebalance ? `${(weight / 100).toFixed(1)}%` : undefined
          }
          data-weight={weight}
          onKeyDown={
            onRebalance
              ? (e) => {
                  if (e.key === "ArrowUp" || e.key === "ArrowRight") {
                    e.preventDefault();
                    onRebalance(index, Math.min(10000, weight + 200));
                  } else if (e.key === "ArrowDown" || e.key === "ArrowLeft") {
                    e.preventDefault();
                    onRebalance(index, Math.max(0, weight - 200));
                  } else if (e.key === "Home") {
                    e.preventDefault();
                    onRebalance(index, 0);
                  } else if (e.key === "End") {
                    e.preventDefault();
                    onRebalance(index, 10000);
                  }
                }
              : undefined
          }
        >
          <div className="mine-bin-group">
            {heights[index].map((height, barIndex) => (
              <i
                key={barIndex}
                className={height > 0 ? "has-fill" : undefined}
                style={
                  {
                    "--microbar-height": `${height}%`,
                    "--microbar-color": barColor(index, barIndex),
                  } as CSSProperties
                }
              />
            ))}
          </div>
          {index < 4 && (
            <em className="mine-bound-tick" aria-hidden="true">
              {boundaryTicks[index]}
            </em>
          )}
          {!hideLabels && (
            <span className="mine-bar-code">{RANGE_LABELS[index]}</span>
          )}
        </div>
      ))}
    </div>
  );
}
