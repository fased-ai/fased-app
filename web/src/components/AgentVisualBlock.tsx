import { createElement, Fragment, useState, type ReactNode } from "react";
import {
  parseFasedVisualSpec,
  parseSafeVisualHTML,
  type FasedVisualSpec,
  type SafeVisualNode,
} from "../agent-visuals";
import { Check, Copy } from "../icons";
import { useUI } from "../ui-preferences";

function renderSafeNode(node: SafeVisualNode, key: string): ReactNode {
  if (node.kind === "text") {
    return node.text;
  }
  const children = node.children.map((child, idx) =>
    renderSafeNode(child, `${key}-${idx}`),
  );
  if (node.tag === "br" || node.tag === "hr") {
    return createElement(node.tag, { key, ...node.attrs });
  }
  return createElement(node.tag, { key, ...node.attrs }, ...children);
}

function renderStructuredSpec(spec: FasedVisualSpec): ReactNode {
  if (spec.type === "metrics") {
    return (
      <div className="v2-visual-metrics">
        {spec.items.map((item, idx) => (
          <div key={idx} className="v2-visual-metric-item">
            <span className="v2-visual-metric-label">{item.label}</span>
            <strong className="v2-visual-metric-value">{item.value}</strong>
            {item.delta && (
              <span
                className={`v2-visual-metric-delta${item.tone ? ` is-${item.tone}` : ""}`}
              >
                {item.delta}
              </span>
            )}
          </div>
        ))}
      </div>
    );
  }

  if (spec.type === "bars") {
    const maxVal = Math.max(
      1,
      ...spec.items.map((item) => Math.max(0, Math.abs(item.value))),
    );
    return (
      <div className="v2-visual-bars">
        {spec.items.map((item, idx) => {
          const pct = Math.min(
            100,
            Math.max(0, (Math.max(0, Math.abs(item.value)) / maxVal) * 100),
          );
          const formatted =
            item.displayValue ??
            `${item.value}${spec.unit ? ` ${spec.unit}` : ""}`;
          return (
            <div key={idx} className="v2-visual-bar-row">
              <div className="v2-visual-bar-meta">
                <span>{item.label}</span>
                <span className="mono">{formatted}</span>
              </div>
              <div className="v2-visual-bar-track">
                <div
                  className="v2-visual-bar-fill"
                  style={{ width: `${pct.toFixed(1)}%` }}
                />
              </div>
            </div>
          );
        })}
      </div>
    );
  }

  if (spec.type === "table") {
    return (
      <div className="v2-visual-table-wrap">
        <table className="v2-visual-table">
          <thead>
            <tr>
              {spec.columns.map((col, idx) => (
                <th key={idx}>{col}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {spec.rows.map((row, rIdx) => (
              <tr key={rIdx}>
                {spec.columns.map((_, cIdx) => (
                  <td key={cIdx}>{row[cIdx] ?? ""}</td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    );
  }

  const values = spec.values;
  const min = Math.min(...values);
  const max = Math.max(...values);
  const span = max - min || 1;
  const width = 260;
  const height = 64;
  const pad = 6;
  const points = values
    .map((v, idx) => {
      const x =
        pad + (idx / Math.max(1, values.length - 1)) * (width - pad * 2);
      const y = height - pad - ((v - min) / span) * (height - pad * 2);
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");
  const last = values[values.length - 1];
  const firstLabel = spec.labels?.[0] ?? "";
  const lastLabel = spec.labels?.[values.length - 1] ?? "";

  return (
    <div className="v2-visual-sparkline">
      <div className="v2-visual-spark-meta mono">
        <span>
          {min}–{max}
          {spec.unit ? ` ${spec.unit}` : ""}
        </span>
        <strong>
          {last}
          {spec.unit ? ` ${spec.unit}` : ""}
        </strong>
      </div>
      <svg
        viewBox={`0 0 ${width} ${height}`}
        className="v2-visual-spark-svg"
        role="img"
        aria-label={spec.title ?? "Sparkline"}
      >
        <polyline
          className="v2-visual-spark-line"
          points={points}
          fill="none"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
      {(firstLabel || lastLabel) && (
        <div className="v2-visual-spark-labels">
          <span>{firstLabel}</span>
          <span>{lastLabel}</span>
        </div>
      )}
    </div>
  );
}

export function AgentVisualBlock({
  lang,
  code,
  defaultView = "preview",
}: {
  lang: string;
  code: string;
  defaultView?: "preview" | "code";
}) {
  const { t } = useUI();
  const [view, setView] = useState<"preview" | "code">(defaultView);
  const [copied, setCopied] = useState(false);

  const normalizedLang = lang.trim().toLowerCase();
  const visualSpec =
    normalizedLang === "fased-visual" ? parseFasedVisualSpec(code) : null;
  const safeNodes =
    normalizedLang === "fased-visual" ? [] : parseSafeVisualHTML(code);
  const canPreview = visualSpec !== null || safeNodes.length > 0;
  const effectiveView = canPreview ? view : "code";

  const title =
    visualSpec?.title ??
    (normalizedLang === "fased-visual"
      ? "Visual"
      : normalizedLang || "Preview");

  return (
    <div className="v2-visual-card">
      <div className="v2-visual-head">
        <strong className="v2-visual-title">{title}</strong>
        <div className="v2-visual-actions">
          {canPreview && (
            <div className="v2-visual-tabs" role="group">
              <button
                type="button"
                aria-pressed={effectiveView === "preview"}
                onClick={() => setView("preview")}
              >
                {t("Preview")}
              </button>
              <button
                type="button"
                aria-pressed={effectiveView === "code"}
                onClick={() => setView("code")}
              >
                {t("Source")}
              </button>
            </div>
          )}
          <button
            type="button"
            className="v2-code-copy"
            onClick={() => {
              void navigator.clipboard?.writeText(code);
              setCopied(true);
              setTimeout(() => setCopied(false), 1200);
            }}
          >
            {copied ? <Check size={12} /> : <Copy size={12} />}
            <span>{copied ? t("Copied") : t("Copy")}</span>
          </button>
        </div>
      </div>

      {effectiveView === "preview" && canPreview ? (
        <div className="v2-visual-body">
          {visualSpec ? (
            renderStructuredSpec(visualSpec)
          ) : (
            <div className="v2-visual-html">
              {createElement(
                Fragment,
                null,
                ...safeNodes.map((node, idx) =>
                  renderSafeNode(node, `n${idx}`),
                ),
              )}
            </div>
          )}
        </div>
      ) : (
        <pre className="v2-code-pre">
          <code>{code}</code>
        </pre>
      )}
    </div>
  );
}
