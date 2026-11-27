import { useState } from "react";
import { Download, Play, Plus, Trash2 } from "../../icons";
import type {
  SavedStrategy,
  SimulateResponse,
  StrategyPreset,
} from "../../mining-lv1x";
import {
  MineAllocationCurve,
  MinePresetIcon,
  RANGE_LABELS,
} from "../MineAllocationCurve";

const SPECTRUM = [
  "#E0566F",
  "#D98466",
  "#6F7889",
  "#2FB88C",
  "#33A9B8",
] as const;

interface MineStrategiesPageProps {
  presets: StrategyPreset[];
  savedStrategies: SavedStrategy[];
  onSaveStrategy: (input: {
    name: string;
    description: string;
    bucketsBps: number[];
  }) => Promise<void>;
  onDeleteStrategy: (id: string) => Promise<void>;
  onSimulate: (
    bucketsBps: number[],
    perRoundSol: number,
  ) => Promise<SimulateResponse>;
  onUseInMine: (id: string, weightsBps: number[]) => void;
  onLaunchTask: (id: string, weightsBps: number[]) => void;
}

function parseCurveInput(raw: string): number[] | null {
  const s = raw.trim();
  if (!s) return null;
  let listRaw = s;
  const idx = s.indexOf("curve=");
  if (idx >= 0) {
    listRaw = s.slice(idx + 6).split(/[&#\s]/)[0] ?? "";
  }
  const parts = listRaw.split(",").map((x) => Number(x.trim()));
  if (parts.length !== 5 || parts.some((n) => !Number.isFinite(n) || n < 0)) {
    return null;
  }
  const sum = parts.reduce((a, b) => a + b, 0);
  if (sum <= 0) return null;
  const bps = parts.map((n) => Math.round((n / sum) * 10000));
  const diff = 10000 - bps.reduce((a, b) => a + b, 0);
  if (diff !== 0) {
    bps[2] = Math.max(0, bps[2] + diff);
  }
  return bps;
}

export function MineStrategiesPage({
  presets,
  savedStrategies,
  onSaveStrategy,
  onDeleteStrategy,
  onSimulate,
  onUseInMine,
  onLaunchTask,
}: MineStrategiesPageProps) {
  const [importText, setImportText] = useState("");
  const [importName, setImportName] = useState("Imported Curve");
  const [simPerRound, setSimPerRound] = useState("0.05");
  const [simResult, setSimResult] = useState<{
    name: string;
    buckets: number[];
    res: SimulateResponse;
  } | null>(null);
  const [compareIds, setCompareIds] = useState<string[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const allItems = [
    ...presets
      .filter((p) => p.id !== "custom")
      .map((p) => ({
        id: p.id,
        name: p.name,
        description: p.description,
        buckets: p.bucketsBps,
        isSaved: false,
      })),
    ...savedStrategies.map((s) => ({
      id: s.id,
      name: s.name,
      description: s.description || "Custom saved strategy",
      buckets: s.bucketsBps,
      isSaved: true,
    })),
  ];

  async function handleSimulate(name: string, buckets: number[]) {
    setBusy(true);
    setError("");
    try {
      const res = await onSimulate(
        buckets,
        Math.max(0.01, Number(simPerRound) || 0.05),
      );
      setSimResult({ name, buckets, res });
    } catch (err) {
      setError(err instanceof Error ? err.message : "Simulation failed");
    } finally {
      setBusy(false);
    }
  }

  async function handleImport() {
    setError("");
    const parsed = parseCurveInput(importText);
    if (!parsed) {
      setError(
        "Paste a ?curve=a,b,c,d,e URL or 5 comma-separated weights (e.g. 10,20,40,20,10).",
      );
      return;
    }
    setBusy(true);
    try {
      await onSaveStrategy({
        name: importName.trim() || "Imported Curve",
        description: "Imported WEN curve",
        bucketsBps: parsed,
      });
      setImportText("");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Import failed");
    } finally {
      setBusy(false);
    }
  }

  function exportJson() {
    const payload = JSON.stringify(allItems, null, 2);
    const blob = new Blob([payload], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "wen-mining-strategies.json";
    a.click();
    window.setTimeout(() => URL.revokeObjectURL(url), 1000);
  }

  function toggleCompare(id: string) {
    setCompareIds((prev) => {
      if (prev.includes(id)) return prev.filter((x) => x !== id);
      if (prev.length >= 2) return [prev[1], id];
      return [...prev, id];
    });
  }

  const compared = compareIds
    .map((id) => allItems.find((x) => x.id === id))
    .filter(Boolean) as typeof allItems;

  return (
    <div
      className="mine-subpage"
      aria-label="Mining Strategy Library and Backtester"
    >
      <div className="wt-wpanel mine-subpanel">
        <div className="mine-section-head">
          <h2>Import &amp; Backtest</h2>
          <button type="button" className="wt-btn-soft" onClick={exportJson}>
            <Download size={13} aria-hidden="true" />
            <span>Export JSON</span>
          </button>
        </div>

        <div className="mine-import-bar">
          <input
            type="text"
            className="mine-modal-input"
            placeholder="Name (e.g. Chop Shield)"
            value={importName}
            onChange={(e) => setImportName(e.target.value)}
          />
          <input
            type="text"
            className="mine-modal-input"
            placeholder="Paste ?curve=10,20,40,20,10 or 5 weights"
            value={importText}
            onChange={(e) => setImportText(e.target.value)}
          />
          <input
            type="text"
            className="mine-modal-input mine-sim-sol-input"
            aria-label="Simulated SOL per round"
            value={simPerRound}
            onChange={(e) => setSimPerRound(e.target.value)}
          />
          <button
            type="button"
            className="wt-btn-rose-sm"
            disabled={busy}
            onClick={() => void handleImport()}
          >
            <Plus size={13} aria-hidden="true" />
            <span>Import Curve</span>
          </button>
        </div>
        {error && <p className="mine-error">{error}</p>}
      </div>

      {simResult && (
        <div
          className="wt-wpanel mine-subpanel"
          aria-label="Near-miss simulation report"
        >
          <div className="mine-section-head">
            <h2>Simulation Report: {simResult.name}</h2>
            <span className="mine-muted-note">
              {simResult.res.rounds} rounds · {simResult.res.totalCommitted} SOL
              total
            </span>
          </div>

          <div className="mine-kpi-strip">
            <div className="wt-kpi-box">
              <span>Exact Hit %</span>
              <strong>{simResult.res.exactHitPct.toFixed(1)}%</strong>
            </div>
            <div className="wt-kpi-box">
              <span>Avg Credit %</span>
              <strong>{simResult.res.avgCreditPct.toFixed(1)}%</strong>
            </div>
            <div className="wt-kpi-box">
              <span>SOL Retained</span>
              <strong>{simResult.res.retainedPct.toFixed(2)}%</strong>
            </div>
            <div className="wt-kpi-box">
              <span>Est. SAT</span>
              <strong>{simResult.res.estSat.toFixed(1)} SAT</strong>
            </div>
          </div>

          <MineAllocationCurve weightsBps={simResult.buckets} />

          <div
            className="mine-sim-outcomes"
            role="list"
            aria-label="Simulated outcomes"
          >
            {simResult.res.outcomes.map((o) => (
              <div
                key={o.roundSlot}
                role="listitem"
                className={`mine-sim-chip is-${o.kind}`}
                title={`#${o.roundSlot} · ${o.kind} (${o.returnPct.toFixed(1)}%)`}
              >
                <span>{o.kind[0].toUpperCase()}</span>
                <small>{o.returnPct.toFixed(0)}%</small>
              </div>
            ))}
          </div>
        </div>
      )}

      {compared.length === 2 && (
        <div
          className="wt-wpanel mine-subpanel"
          aria-label="Side by side strategy compare"
        >
          <div className="mine-section-head">
            <h2>
              Compare: {compared[0].name} vs. {compared[1].name}
            </h2>
            <button
              type="button"
              className="mine-link-btn"
              onClick={() => setCompareIds([])}
            >
              Clear
            </button>
          </div>
          <div className="mine-table-scroll">
            <table className="mine-table">
              <thead>
                <tr>
                  <th>Range</th>
                  <th>{compared[0].name}</th>
                  <th>{compared[1].name}</th>
                  <th>Delta</th>
                </tr>
              </thead>
              <tbody>
                {RANGE_LABELS.map((code, i) => {
                  const a = (compared[0].buckets[i] ?? 0) / 100;
                  const b = (compared[1].buckets[i] ?? 0) / 100;
                  return (
                    <tr key={code}>
                      <td style={{ color: SPECTRUM[i] }}>
                        <strong>{code}</strong>
                      </td>
                      <td>{a.toFixed(1)}%</td>
                      <td>{b.toFixed(1)}%</td>
                      <td>
                        {a - b >= 0 ? "+" : ""}
                        {(a - b).toFixed(1)}%
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}

      <div className="mine-strat-grid">
        {allItems.map((item) => (
          <div key={item.id} className="wt-wpanel mine-strat-card">
            <div className="mine-section-head">
              <div className="mine-strat-title">
                <MinePresetIcon weights={item.buckets} />
                <strong>{item.name}</strong>
              </div>
              <label className="mine-toggle-row">
                <input
                  type="checkbox"
                  checked={compareIds.includes(item.id)}
                  onChange={() => toggleCompare(item.id)}
                />
                <span>Compare</span>
              </label>
            </div>

            <div className="mine-strat-bars">
              {RANGE_LABELS.map((code, i) => {
                const pct = (item.buckets[i] ?? 0) / 100;
                return (
                  <div key={code} className="mine-strat-col">
                    <div className="mine-pulse-track">
                      <div
                        className="mine-pulse-fill"
                        style={{
                          height: `${Math.max(4, pct)}%`,
                          background: SPECTRUM[i],
                        }}
                      />
                    </div>
                    <small>
                      {code} {pct.toFixed(0)}%
                    </small>
                  </div>
                );
              })}
            </div>

            <div className="mine-strat-actions">
              <button
                type="button"
                className="wt-btn-soft"
                disabled={busy}
                onClick={() => void handleSimulate(item.name, item.buckets)}
              >
                <Play size={12} aria-hidden="true" />
                <span>Simulate</span>
              </button>
              <button
                type="button"
                className="wt-btn-rose-sm"
                onClick={() => onUseInMine(item.id, item.buckets)}
              >
                Use in Mine
              </button>
              <button
                type="button"
                className="wt-btn-soft"
                onClick={() => onLaunchTask(item.id, item.buckets)}
              >
                Task
              </button>
              {item.isSaved && (
                <button
                  type="button"
                  className="icon-btn"
                  aria-label={`Delete ${item.name}`}
                  onClick={() => void onDeleteStrategy(item.id)}
                >
                  <Trash2 size={13} aria-hidden="true" />
                </button>
              )}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
