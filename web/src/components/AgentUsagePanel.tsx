import { useCallback, useEffect, useRef, useState } from "react";
import { useCore } from "../core";
import { useUI } from "../ui-preferences";
import {
  formatCompactTokens,
  formatUsageCostUSD,
  getUsageV2,
  type AgentV2UsageSummary,
} from "../agents-v2";

const RANGE_OPTIONS: Array<{ days: 7 | 30 | 0; labelKey: string }> = [
  { days: 7, labelKey: "7d" },
  { days: 30, labelKey: "30d" },
  { days: 0, labelKey: "All time" },
];

export function AgentUsagePanel({ profileId }: { profileId: string }) {
  const { t } = useUI();
  const core = useCore();
  const [days, setDays] = useState<7 | 30 | 0>(30);
  const [usage, setUsage] = useState<AgentV2UsageSummary | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const genRef = useRef(0);

  const loadUsage = useCallback(
    async (selectedDays: 7 | 30 | 0) => {
      if (core.mode !== "ready") return;
      const epoch = ++genRef.current;
      setLoading(true);
      setError("");
      try {
        const next = await getUsageV2(core, {
          profileId,
          days: selectedDays,
        });
        if (epoch === genRef.current) {
          setUsage(next);
        }
      } catch (e) {
        if (epoch === genRef.current) {
          setError(e instanceof Error ? e.message : "Usage unavailable");
        }
      } finally {
        if (epoch === genRef.current) {
          setLoading(false);
        }
      }
    },
    [core.mode, core.instanceId, core.request, profileId],
  );

  useEffect(() => {
    setUsage(null);
    void loadUsage(days);
    return () => {
      genRef.current++;
    };
  }, [loadUsage, days]);

  return (
    <section className="panel" aria-label={t("Usage & Cost")}>
      <div className="panel-heading">
        <h2>{t("Usage & Cost")}</h2>
        <div className="usage-range-switch" role="group">
          {RANGE_OPTIONS.map((opt) => (
            <button
              key={opt.days}
              type="button"
              className={`usage-range-btn ${days === opt.days ? "is-active" : ""}`}
              aria-pressed={days === opt.days}
              disabled={loading && days === opt.days}
              onClick={() => setDays(opt.days)}
            >
              {t(opt.labelKey)}
            </button>
          ))}
        </div>
      </div>

      <div className="agent-record-body">
        {error && <p role="alert">{error}</p>}

        {!usage && loading && <p className="empty">{t("Working")}</p>}

        {usage && usage.turns === 0 && (
          <p className="empty">{t("No usage yet")}</p>
        )}

        {usage && usage.turns > 0 && (
          <>
            <div className="usage-summary-grid">
              <div className="usage-stat">
                <span>{t("Turns")}</span>
                <strong>{usage.turns.toLocaleString()}</strong>
              </div>
              <div
                className="usage-stat"
                title={`${usage.inputTokens.toLocaleString()} in / ${usage.outputTokens.toLocaleString()} out`}
              >
                <span>{t("Tokens")}</span>
                <strong>{formatCompactTokens(usage.totalTokens)}</strong>
              </div>
              <div className="usage-stat">
                <span>{t("Cached")}</span>
                <strong>{formatCompactTokens(usage.cachedTokens)}</strong>
              </div>
              <div className="usage-stat">
                <span>{t("Est. cost")}</span>
                <strong>
                  {formatUsageCostUSD(
                    usage.estimatedCostMicroUsd,
                    usage.costKnown,
                  )}
                </strong>
              </div>
            </div>

            {usage.models.length > 0 && (
              <div className="usage-model-list">
                {usage.models.map((row) => (
                  <div
                    className="usage-model-row"
                    key={`${row.provider}:${row.model}`}
                  >
                    <div className="usage-model-head">
                      <strong>{row.model || row.provider || "—"}</strong>
                      {row.provider && (
                        <span className="v2-pill">{row.provider}</span>
                      )}
                    </div>
                    <div className="usage-model-metrics">
                      <span>
                        {row.turns.toLocaleString()} {t("Turns").toLowerCase()}
                      </span>
                      <span
                        title={`${row.inputTokens.toLocaleString()} in / ${row.outputTokens.toLocaleString()} out`}
                      >
                        {formatCompactTokens(row.totalTokens)}{" "}
                        {t("Tokens").toLowerCase()}
                      </span>
                      <strong>
                        {formatUsageCostUSD(
                          row.estimatedCostMicroUsd,
                          row.costKnown,
                        )}
                      </strong>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </>
        )}
      </div>
    </section>
  );
}
