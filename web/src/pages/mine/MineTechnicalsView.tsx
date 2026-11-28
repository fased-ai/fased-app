import { useMemo } from "react";
import {
  TECHNICAL_BAND_COLORS,
  computeRetailTechnicals,
  type OhlcBar,
  type TechnicalGaugeSummary,
} from "./mine-technicals";

function fmtVal(v: number | null): string {
  if (v === null || !Number.isFinite(v)) return "—";
  if (Math.abs(v) >= 1000) {
    return v.toLocaleString("en-US", {
      minimumFractionDigits: 2,
      maximumFractionDigits: 2,
    });
  }
  return v.toFixed(2);
}

function arcPath(
  cx: number,
  cy: number,
  r: number,
  startDeg: number,
  endDeg: number,
): string {
  const rad = (deg: number) => (deg * Math.PI) / 180;
  const x1 = cx + r * Math.cos(rad(startDeg));
  const y1 = cy + r * Math.sin(rad(startDeg));
  const x2 = cx + r * Math.cos(rad(endDeg));
  const y2 = cy + r * Math.sin(rad(endDeg));
  return `M ${x1.toFixed(2)} ${y1.toFixed(2)} A ${r} ${r} 0 0 1 ${x2.toFixed(2)} ${y2.toFixed(2)}`;
}

function TechnicalGauge({
  title,
  gauge,
  large = false,
}: {
  title: string;
  gauge: TechnicalGaugeSummary;
  large?: boolean;
}) {
  const segments: [number, number][] = [
    [-180, -147],
    [-143, -110],
    [-106, -74],
    [-70, -37],
    [-33, 0],
  ];
  const safeScore = Number.isFinite(gauge.score) ? gauge.score : 0;
  const angleDeg = Math.max(-1, Math.min(1, safeScore)) * 78;
  const w = large ? 220 : 164;
  const h = large ? 124 : 96;
  const cx = w / 2;
  const cy = large ? 112 : 86;
  const r = large ? 86 : 64;
  const needleLen = large ? 66 : 48;

  return (
    <div className={`rmc-tech-gauge${large ? " is-large" : ""}`}>
      <svg
        viewBox={`0 0 ${w} ${h}`}
        focusable="false"
        className="rmc-tech-gauge-svg"
        role="img"
        aria-label={`${title}: ${gauge.label} (${gauge.sell} sell, ${gauge.neutral} neutral, ${gauge.buy} buy)`}
      >
        {segments.map(([a0, a1], idx) => (
          <path
            key={idx}
            d={arcPath(cx, cy, r, a0, a1)}
            fill="none"
            stroke={TECHNICAL_BAND_COLORS[idx]}
            strokeWidth={large ? 7.5 : 6}
            strokeLinecap="round"
            opacity={idx === gauge.bandIndex ? 1 : 0.42}
          />
        ))}
        <g transform={`translate(${cx}, ${cy}) rotate(${angleDeg.toFixed(1)})`}>
          <line
            x1="0"
            y1="0"
            x2="0"
            y2={-needleLen}
            style={{ stroke: "var(--text)" }}
            strokeWidth={large ? 3 : 2.4}
            strokeLinecap="round"
          />
          <circle
            cx="0"
            cy="0"
            r={large ? 5.5 : 4.5}
            style={{ fill: "var(--text)" }}
          />
          <circle
            cx="0"
            cy="0"
            r={large ? 2.2 : 1.8}
            style={{ fill: "var(--panel)" }}
          />
        </g>
      </svg>
      <div className="rmc-tech-gauge-title">{title}</div>
      <div className="rmc-tech-gauge-label" style={{ color: gauge.color }}>
        {gauge.label}
      </div>
      <div className="rmc-tech-counts">
        <span>
          <small>Sell</small>
          <b className="is-sell">{gauge.sell}</b>
        </span>
        <span>
          <small>Neutral</small>
          <b>{gauge.neutral}</b>
        </span>
        <span>
          <small>Buy</small>
          <b className="is-buy">{gauge.buy}</b>
        </span>
      </div>
    </div>
  );
}

export function MineTechnicalsView({
  bars,
  intervalLabel,
  onApplyCurve,
}: {
  bars: readonly OhlcBar[];
  intervalLabel: string;
  onApplyCurve?: (weightsBps: number[]) => void;
}) {
  const tech = useMemo(() => computeRetailTechnicals(bars), [bars]);

  if (!tech.ready) {
    return (
      <div className="rmc-empty" role="status">
        Collecting {intervalLabel} bars for technical gauges ({tech.barCount} /
        15).
      </div>
    );
  }

  return (
    <div
      className="rmc-tech-view"
      role="region"
      aria-label="Technical indicators summary"
    >
      <div className="rmc-tech-bar">
        <span className="rmc-tech-meta">
          <b>{intervalLabel}</b> · {tech.summary.total} signals
        </span>
        {onApplyCurve && (
          <button
            type="button"
            className="rmc-pulse-apply"
            onClick={() => onApplyCurve(tech.summary.suggestedWeights)}
          >
            Apply Technical Bias
          </button>
        )}
      </div>

      <div className="rmc-tech-gauges">
        <TechnicalGauge title="Oscillators" gauge={tech.oscillators} />
        <TechnicalGauge title="Summary" gauge={tech.summary} large />
        <TechnicalGauge title="Moving Averages" gauge={tech.movingAverages} />
      </div>

      <div className="rmc-tech-tables">
        <section className="rmc-tech-table-box" aria-label="Oscillators">
          <h4>Oscillators</h4>
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Value</th>
                <th>Action</th>
              </tr>
            </thead>
            <tbody>
              {tech.oscillatorRows.map((row) => (
                <tr key={row.name}>
                  <td>{row.name}</td>
                  <td>{fmtVal(row.value)}</td>
                  <td className={`is-${row.action.toLowerCase()}`}>
                    {row.action}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>

        <section className="rmc-tech-table-box" aria-label="Moving Averages">
          <h4>Moving Averages</h4>
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Value</th>
                <th>Action</th>
              </tr>
            </thead>
            <tbody>
              {tech.maRows.map((row) => (
                <tr key={row.name}>
                  <td>{row.name}</td>
                  <td>{fmtVal(row.value)}</td>
                  <td className={`is-${row.action.toLowerCase()}`}>
                    {row.action}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      </div>
    </div>
  );
}
