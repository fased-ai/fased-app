import { useEffect, useMemo, useRef, useState } from "react";
import { useCore } from "../../core";
import { useUI } from "../../ui-preferences";
import { RefreshCw } from "../../icons";
import type { CrosshairBus, FasedStreamClient } from "../../stream-store";
import {
  CHART_VENUES,
  DOM_SUPPORTED_VENUES,
  fetchCohorts,
  fetchFootprint,
  fetchMacroSeries,
  fetchOrderBook,
  fetchSECFilings,
  fetchTrades,
  formatCompactVolume,
  formatMarketPrice,
  TAPE_SUPPORTED_VENUES,
  type CohortSnapshot,
  type FootprintBar,
  type FootprintSeries,
  type Interval,
  type MacroPoint,
  type OrderBookSnapshot,
  type OrderflowTab,
  type SECFiling,
  type TradeTick,
} from "../../market-data";

type CanvasPalette = {
  surface: string;
  raised: string;
  border: string;
  text: string;
  muted: string;
  cyan: string;
  coral: string;
  violet: string;
};

function getCanvasPalette(): CanvasPalette {
  const s = getComputedStyle(document.documentElement);
  const g = (k: string, fb: string) => s.getPropertyValue(k).trim() || fb;
  return {
    surface: g("--wt-surface", "#0F1218"),
    raised: g("--wt-raised", "#161A22"),
    border: g("--wt-border", "rgba(255, 255, 255, 0.06)"),
    text: g("--wt-text", "#E6E9F0"),
    muted: g("--wt-muted", "#8D96A8"),
    cyan: g("--wt-ok-fill", g("--wt-cyan", "#2FB88C")),
    coral: g("--wt-danger-fill", g("--wt-coral", "#E0566F")),
    violet: g("--wt-violet", "#9B8AE0"),
  };
}

export function FootprintCanvas({
  bars,
  stepSize = 0,
  focusedTime,
}: {
  bars: FootprintBar[];
  stepSize?: number;
  focusedTime: number | null;
}) {
  const { t } = useUI();
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;

    const draw = () => {
      const parent = canvas.parentElement;
      const w = Math.max(parent?.clientWidth ?? 300, 220);
      const h = Math.max(parent?.clientHeight ?? 240, 180);
      const dpr = Math.min(window.devicePixelRatio || 1, 2);
      canvas.width = Math.floor(w * dpr);
      canvas.height = Math.floor(h * dpr);
      canvas.style.width = `${w}px`;
      canvas.style.height = `${h}px`;

      const ctx = canvas.getContext("2d");
      if (!ctx) return;
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);

      const p = getCanvasPalette();
      ctx.fillStyle = p.surface;
      ctx.fillRect(0, 0, w, h);

      if (bars.length === 0) {
        ctx.fillStyle = p.muted;
        ctx.font = "12px sans-serif";
        ctx.fillText(t("No footprint data"), 12, 24);
        return;
      }

      const visible = bars.slice(-14);
      let minP = Infinity;
      let maxP = -Infinity;
      let maxLvVol = 1;
      let maxDelta = 1;

      for (const b of visible) {
        minP = Math.min(minP, b.low);
        maxP = Math.max(maxP, b.high);
        maxDelta = Math.max(maxDelta, Math.abs(b.delta));
        for (const lv of b.levels) {
          minP = Math.min(minP, lv.price);
          maxP = Math.max(maxP, lv.price);
          maxLvVol = Math.max(maxLvVol, lv.bidVol, lv.askVol);
        }
      }
      if (!Number.isFinite(minP) || !Number.isFinite(maxP) || maxP <= minP) {
        maxP = minP + Math.max(minP * 0.01, 1e-6);
      }

      const bucketStep =
        stepSize > 0 ? stepSize : Math.max((maxP - minP) / 40, 1e-8);
      const vpMap = new Map<number, number>();
      for (const b of visible) {
        for (const lv of b.levels) {
          const key = Math.round(lv.price / bucketStep);
          vpMap.set(key, (vpMap.get(key) ?? 0) + lv.totalVol);
        }
      }

      const vpWidth = 42;
      const deltaHeight = 36;
      const plotW = w - vpWidth - 10;
      const plotH = h - deltaHeight - 16;
      const priceToY = (px: number) =>
        8 + ((maxP - px) / (maxP - minP)) * Math.max(plotH - 16, 40);

      // Value area band for latest bar.
      const last = visible[visible.length - 1];
      const yVAH = priceToY(last.vah);
      const yVAL = priceToY(last.val);
      ctx.fillStyle = p.raised;
      ctx.fillRect(4, yVAH, plotW, Math.max(yVAL - yVAH, 2));

      // VPOC line across footprint.
      ctx.strokeStyle = p.violet;
      ctx.lineWidth = 1;
      ctx.beginPath();
      const yPoc = priceToY(last.vpoc);
      ctx.moveTo(4, yPoc);
      ctx.lineTo(w - 4, yPoc);
      ctx.stroke();

      // Right-edge Volume Profile bars using sub-cent aware bucket step.
      let maxVP = 1;
      for (const v of vpMap.values()) maxVP = Math.max(maxVP, v);
      for (const [key, vol] of vpMap.entries()) {
        const y = priceToY(key * bucketStep);
        const bw = (vol / maxVP) * (vpWidth - 6);
        ctx.fillStyle = p.violet;
        ctx.globalAlpha = 0.55;
        ctx.fillRect(w - bw - 3, y - 2, bw, 4);
      }
      ctx.globalAlpha = 1;

      // Per-bar clusters + Delta bottom strip.
      const colW = Math.max(plotW / visible.length, 10);
      const deltaBaseY = h - 18;
      ctx.strokeStyle = p.border;
      ctx.beginPath();
      ctx.moveTo(4, h - deltaHeight);
      ctx.lineTo(w - 4, h - deltaHeight);
      ctx.stroke();

      visible.forEach((b, i) => {
        const x0 = 4 + i * colW;
        const cx = x0 + colW * 0.5;

        if (focusedTime !== null && b.time === focusedTime) {
          ctx.strokeStyle = p.violet;
          ctx.strokeRect(x0 + 1, 4, colW - 2, plotH - 2);
        }

        // Candle body spine.
        const yO = priceToY(b.open);
        const yC = priceToY(b.close);
        ctx.fillStyle = b.close >= b.open ? p.cyan : p.coral;
        ctx.fillRect(
          cx - 1.5,
          Math.min(yO, yC),
          3,
          Math.max(Math.abs(yC - yO), 2),
        );

        // Footprint bid (left) / ask (right) clusters.
        for (const lv of b.levels.slice(-16)) {
          const y = priceToY(lv.price);
          const half = Math.max(colW * 0.42, 4);
          const askW = (lv.askVol / maxLvVol) * half;
          const bidW = (lv.bidVol / maxLvVol) * half;
          ctx.fillStyle = p.cyan;
          ctx.globalAlpha = 0.72;
          ctx.fillRect(cx + 2, y - 2, askW, 4);
          ctx.fillStyle = p.coral;
          ctx.fillRect(cx - 2 - bidW, y - 2, bidW, 4);
        }
        ctx.globalAlpha = 1;

        // Delta bar bottom strip.
        const dh = (Math.abs(b.delta) / maxDelta) * 14;
        ctx.fillStyle = b.delta >= 0 ? p.cyan : p.coral;
        if (b.delta >= 0) {
          ctx.fillRect(
            x0 + 2,
            deltaBaseY - dh,
            Math.max(colW - 4, 3),
            Math.max(dh, 2),
          );
        } else {
          ctx.fillRect(
            x0 + 2,
            deltaBaseY,
            Math.max(colW - 4, 3),
            Math.max(dh, 2),
          );
        }
      });

      // CVD overlay line across delta strip.
      let minCvd = Infinity;
      let maxCvd = -Infinity;
      for (const b of visible) {
        minCvd = Math.min(minCvd, b.cvd);
        maxCvd = Math.max(maxCvd, b.cvd);
      }
      if (maxCvd > minCvd) {
        ctx.strokeStyle = p.text;
        ctx.lineWidth = 1.25;
        ctx.beginPath();
        visible.forEach((b, i) => {
          const x = 4 + (i + 0.5) * colW;
          const y =
            deltaBaseY - ((b.cvd - minCvd) / (maxCvd - minCvd) - 0.5) * 24;
          if (i === 0) ctx.moveTo(x, y);
          else ctx.lineTo(x, y);
        });
        ctx.stroke();
      }
    };

    draw();
    const ro = new ResizeObserver(draw);
    if (canvas.parentElement) ro.observe(canvas.parentElement);
    const mo = new MutationObserver(draw);
    mo.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["data-theme"],
    });
    return () => {
      ro.disconnect();
      mo.disconnect();
    };
  }, [bars, stepSize, focusedTime, t]);

  return (
    <div className="trade-canvas-box">
      <canvas
        ref={canvasRef}
        role="img"
        aria-label={t("Orderflow footprint canvas")}
      />
    </div>
  );
}

export function OrderflowPane({
  venue,
  symbol,
  interval,
  tab,
  syncedTime = null,
  streamClient,
  crosshairBus,
  syncCrosshair = true,
  onChangeTab,
}: {
  venue: string;
  symbol: string;
  interval: Interval;
  tab: OrderflowTab;
  syncedTime?: number | null;
  streamClient?: FasedStreamClient;
  crosshairBus?: CrosshairBus;
  syncCrosshair?: boolean;
  onChangeTab: (tab: OrderflowTab) => void;
}) {
  const { t } = useUI();
  const core = useCore();
  const [footprint, setFootprint] = useState<FootprintSeries | null>(null);
  const [book, setBook] = useState<OrderBookSnapshot | null>(null);
  const [trades, setTrades] = useState<TradeTick[]>([]);
  const [cohorts, setCohorts] = useState<CohortSnapshot | null>(null);
  const [macro, setMacro] = useState<MacroPoint[]>([]);
  const [secFilings, setSecFilings] = useState<SECFiling[]>([]);
  const [seriesId, setSeriesId] = useState("CPIAUCSL");
  const [cikDraft, setCikDraft] = useState("0000320193");
  const [cik, setCik] = useState("0000320193");
  const [paneError, setPaneError] = useState("");
  const [reloadNonce, setReloadNonce] = useState(0);
  const [busFocusedTime, setBusFocusedTime] = useState<number | null>(null);

  const domSupported = DOM_SUPPORTED_VENUES.has(venue);
  const tapeSupported = TAPE_SUPPORTED_VENUES.has(venue);
  const effectiveFocusedTime =
    crosshairBus && syncCrosshair ? busFocusedTime : syncedTime;

  useEffect(() => {
    if (!crosshairBus || !syncCrosshair) {
      setBusFocusedTime(null);
      return;
    }
    return crosshairBus.subscribe((pt) => {
      setBusFocusedTime(pt.time);
    });
  }, [crosshairBus, syncCrosshair]);

  useEffect(() => {
    const clean = cikDraft.trim();
    if (!/^\d{1,10}$/.test(clean) || clean === cik) return;
    const timer = window.setTimeout(() => {
      setCik(clean);
    }, 400);
    return () => window.clearTimeout(timer);
  }, [cikDraft, cik]);

  const candleVenue = CHART_VENUES.includes(
    venue as (typeof CHART_VENUES)[number],
  )
    ? venue
    : "hyperliquid";

  const tabs: { id: OrderflowTab; label: string }[] = [
    { id: "dom", label: "DOM" },
    { id: "tape", label: "Tape" },
    { id: "footprint", label: "Footprint" },
    { id: "intel", label: "Intel" },
  ];
  const effectiveTab: OrderflowTab =
    tab === "ticket" || tab === "desk" ? "dom" : tab;

  // Initial HTTP loads for Footprint, DOM, and Tape tabs with honest venue gating.
  useEffect(() => {
    if (effectiveTab === "intel") return;
    const controller = new AbortController();
    let mounted = true;
    setPaneError("");

    if (effectiveTab === "footprint") {
      fetchFootprint(core, candleVenue, symbol, interval, 24, controller.signal)
        .then((res) => {
          if (mounted) setFootprint(res);
        })
        .catch((err) => {
          if (!mounted || controller.signal.aborted) return;
          setPaneError(err instanceof Error ? err.message : t("Error"));
        });
    } else if (effectiveTab === "dom") {
      if (!domSupported) {
        setBook(null);
      } else {
        fetchOrderBook(core, venue, symbol, 12, controller.signal)
          .then((res) => {
            if (mounted) setBook(res);
          })
          .catch((err) => {
            if (!mounted || controller.signal.aborted) return;
            setPaneError(err instanceof Error ? err.message : t("Error"));
          });
      }
    } else if (tab === "tape") {
      if (!tapeSupported) {
        setTrades([]);
      } else {
        fetchTrades(core, venue, symbol, 35, controller.signal)
          .then((res) => {
            if (mounted) setTrades(res);
          })
          .catch((err) => {
            if (!mounted || controller.signal.aborted) return;
            setPaneError(err instanceof Error ? err.message : t("Error"));
          });
      }
    }

    return () => {
      mounted = false;
      controller.abort();
    };
  }, [
    core.mode,
    core.request,
    tab,
    venue,
    symbol,
    interval,
    domSupported,
    tapeSupported,
    reloadNonce,
    t,
  ]);

  // Independent Intel tab fetches (Cohorts, FRED Macro, SEC EDGAR) so changing CIK or
  // FRED series never refetches unaffected upstreams.
  useEffect(() => {
    if (tab !== "intel") return;
    const controller = new AbortController();
    let mounted = true;
    setPaneError("");

    fetchCohorts(core, "week", controller.signal)
      .then((res) => {
        if (mounted) setCohorts(res);
      })
      .catch((err) => {
        if (!mounted || controller.signal.aborted) return;
        setPaneError(err instanceof Error ? err.message : t("Error"));
      });

    return () => {
      mounted = false;
      controller.abort();
    };
  }, [core.mode, core.request, tab, reloadNonce, t]);

  useEffect(() => {
    if (tab !== "intel") return;
    const controller = new AbortController();
    let mounted = true;

    fetchMacroSeries(core, seriesId, 12, controller.signal)
      .then((res) => {
        if (mounted) setMacro(res);
      })
      .catch((err) => {
        if (!mounted || controller.signal.aborted) return;
        setPaneError(err instanceof Error ? err.message : t("Error"));
      });

    return () => {
      mounted = false;
      controller.abort();
    };
  }, [core.mode, core.request, tab, seriesId, reloadNonce, t]);

  useEffect(() => {
    if (tab !== "intel" || !/^\d{1,10}$/.test(cik)) return;
    const controller = new AbortController();
    let mounted = true;

    fetchSECFilings(core, cik, 6, controller.signal)
      .then((res) => {
        if (mounted) setSecFilings(res);
      })
      .catch((err) => {
        if (!mounted || controller.signal.aborted) return;
        setPaneError(err instanceof Error ? err.message : t("Error"));
      });

    return () => {
      mounted = false;
      controller.abort();
    };
  }, [core.mode, core.request, tab, cik, reloadNonce, t]);

  // Live WS/SSE stream subscriptions for active Orderflow tab.
  useEffect(() => {
    if (!streamClient || core.mode !== "ready") return;
    if (tab === "footprint") {
      return streamClient.subscribe(
        `footprint:${venue}:${symbol}:${interval}`,
        (msg) => {
          const bar = msg.payload as FootprintBar | undefined;
          if (!bar || typeof bar.time !== "number") return;
          setFootprint((prev) => {
            if (!prev) return prev;
            const bars = [...prev.bars];
            const idx = bars.findIndex((b) => b.time === bar.time);
            if (idx >= 0) {
              const prevCvd = idx > 0 ? bars[idx - 1].cvd : 0;
              bars[idx] = { ...bar, cvd: prevCvd + bar.delta };
              for (let k = idx + 1; k < bars.length; k++) {
                bars[k] = { ...bars[k], cvd: bars[k - 1].cvd + bars[k].delta };
              }
            } else {
              const prevCvd = bars.length > 0 ? bars[bars.length - 1].cvd : 0;
              bars.push({ ...bar, cvd: prevCvd + bar.delta });
              if (bars.length > 48) bars.shift();
            }
            return { ...prev, bars };
          });
        },
      );
    }
    if (tab === "dom" && domSupported) {
      return streamClient.subscribe(`orderbook:${venue}:${symbol}`, (msg) => {
        const snap = msg.payload as OrderBookSnapshot | undefined;
        if (!snap || !Array.isArray(snap.bids) || !Array.isArray(snap.asks)) {
          return;
        }
        setBook(snap);
      });
    }
    if (tab === "tape" && tapeSupported) {
      return streamClient.subscribe(`trade:${venue}:${symbol}`, (msg) => {
        const tick = msg.payload as TradeTick | undefined;
        if (!tick || typeof tick.price !== "number") return;
        setTrades((prev) => {
          if (
            prev.some(
              (existing) =>
                tick.id &&
                existing.id === tick.id &&
                existing.time === tick.time,
            )
          ) {
            return prev;
          }
          const next = [...prev, tick];
          return next.length > 60 ? next.slice(-60) : next;
        });
      });
    }
  }, [
    streamClient,
    core.mode,
    tab,
    venue,
    symbol,
    interval,
    domSupported,
    tapeSupported,
  ]);

  const activeBar = useMemo(() => {
    const bars = footprint?.bars ?? [];
    if (bars.length === 0) return null;
    if (effectiveFocusedTime !== null) {
      const exact = bars.find((b) => b.time === effectiveFocusedTime);
      if (exact) return exact;
    }
    return bars[bars.length - 1];
  }, [footprint, effectiveFocusedTime]);

  const provenanceLabel = useMemo(() => {
    if (!activeBar) return "";
    if (activeBar.sourceKind === "ticks") return t("Ticks");
    if (activeBar.sourceKind === "partial_ticks") {
      const pct = Math.round((activeBar.tickCoverage ?? 0) * 100);
      return `${t("Partial")} ${pct}%`;
    }
    return t("Estimated");
  }, [activeBar, t]);

  const maxDepthTotal = useMemo(() => {
    if (!book) return 1;
    const bidMax = book.bids[book.bids.length - 1]?.total ?? 0;
    const askMax = book.asks[book.asks.length - 1]?.total ?? 0;
    return Math.max(bidMax, askMax, 1);
  }, [book]);

  function commitCIK() {
    const clean = cikDraft.trim();
    if (/^\d{1,10}$/.test(clean)) {
      setCik(clean);
    } else {
      setCikDraft(cik);
    }
  }

  return (
    <aside className="panel trade-orderflow" aria-label={t("Orderflow")}>
      <div className="trade-pane-head">
        <div>
          <h2>{t("Orderflow")}</h2>
          <small className="subtle">
            {venue} · {symbol} · {interval}
          </small>
        </div>
        <button
          type="button"
          className="trade-mini-btn"
          aria-label={t("Refresh")}
          onClick={() => setReloadNonce((n) => n + 1)}
        >
          <RefreshCw size={14} />
        </button>
      </div>

      <div
        className="trade-tabs"
        role="tablist"
        aria-label={t("Orderflow views")}
      >
        {tabs.map((item) => (
          <button
            key={item.id}
            type="button"
            role="tab"
            aria-selected={effectiveTab === item.id}
            className={effectiveTab === item.id ? "is-selected" : ""}
            onClick={() => onChangeTab(item.id)}
          >
            {t(item.label)}
          </button>
        ))}
      </div>

      {paneError && (
        <p className="trade-pane-error" role="status">
          {paneError}
        </p>
      )}

      {effectiveTab === "footprint" && (
        <div className="trade-pane-body">
          <FootprintCanvas
            bars={footprint?.bars ?? []}
            stepSize={footprint?.stepSize ?? 0}
            focusedTime={effectiveFocusedTime}
          />
          {activeBar && (
            <>
              <div className="trade-provenance-row">
                <span className="trade-source-pill">{provenanceLabel}</span>
              </div>
              <dl className="trade-metric-grid">
                <div>
                  <dt>{t("VPOC")}</dt>
                  <dd>{formatMarketPrice(activeBar.vpoc)}</dd>
                </div>
                <div>
                  <dt>{t("VAH")}</dt>
                  <dd>{formatMarketPrice(activeBar.vah)}</dd>
                </div>
                <div>
                  <dt>{t("VAL")}</dt>
                  <dd>{formatMarketPrice(activeBar.val)}</dd>
                </div>
                <div>
                  <dt>{t("Delta")}</dt>
                  <dd>{formatCompactVolume(activeBar.delta)}</dd>
                </div>
                <div>
                  <dt>{t("CVD")}</dt>
                  <dd>{formatCompactVolume(activeBar.cvd)}</dd>
                </div>
              </dl>
            </>
          )}
        </div>
      )}

      {effectiveTab === "dom" && (
        <div className="trade-pane-body">
          {!domSupported ? (
            <div className="trade-pane-empty">
              {t("DOM unavailable for this venue")}
            </div>
          ) : (
            <>
              {book && (
                <div className="trade-dom-meta">
                  <span>
                    {t("Spread")}: {formatMarketPrice(book.spread)}
                  </span>
                  <span>
                    {t("Mid")}: {formatMarketPrice(book.midPrice)}
                  </span>
                </div>
              )}
              <div className="trade-dom-ladder">
                <div className="trade-dom-col">
                  <strong>{t("Asks")}</strong>
                  {(book?.asks ?? []).slice(0, 10).map((lv) => (
                    <div className="trade-dom-row is-ask" key={`a-${lv.price}`}>
                      <span
                        className="trade-dom-fill"
                        style={{
                          width: `${Math.min(100, (lv.total / maxDepthTotal) * 100)}%`,
                        }}
                      />
                      <span>{formatMarketPrice(lv.price)}</span>
                      <span>{lv.size.toFixed(2)}</span>
                    </div>
                  ))}
                </div>
                <div className="trade-dom-col">
                  <strong>{t("Bids")}</strong>
                  {(book?.bids ?? []).slice(0, 10).map((lv) => (
                    <div className="trade-dom-row is-bid" key={`b-${lv.price}`}>
                      <span
                        className="trade-dom-fill"
                        style={{
                          width: `${Math.min(100, (lv.total / maxDepthTotal) * 100)}%`,
                        }}
                      />
                      <span>{formatMarketPrice(lv.price)}</span>
                      <span>{lv.size.toFixed(2)}</span>
                    </div>
                  ))}
                </div>
              </div>
            </>
          )}
        </div>
      )}

      {effectiveTab === "tape" && (
        <div className="trade-pane-body">
          {!tapeSupported ? (
            <div className="trade-pane-empty">
              {t("Tape unavailable for this venue")}
            </div>
          ) : (
            <div className="trade-tape-list">
              {trades
                .slice(-24)
                .reverse()
                .map((tr, i) => (
                  <div
                    key={`${tr.id}-${i}`}
                    className={`trade-tape-row ${tr.side === "buy" ? "is-buy" : "is-sell"}`}
                  >
                    <span>{tr.side === "buy" ? t("Buy") : t("Sell")}</span>
                    <span>{formatMarketPrice(tr.price)}</span>
                    <span>{tr.size.toFixed(3)}</span>
                  </div>
                ))}
            </div>
          )}
        </div>
      )}

      {effectiveTab === "intel" && (
        <div className="trade-pane-body trade-intel-stack">
          <section>
            <h3>{t("Cohorts")}</h3>
            <ul className="trade-intel-list">
              {(cohorts?.entries ?? []).slice(0, 4).map((c) => (
                <li key={c.address}>
                  <span>
                    #{c.rank} {c.displayName || c.address.slice(0, 10)}
                  </span>
                  <span>{(c.weekRoi * 100).toFixed(1)}%</span>
                </li>
              ))}
            </ul>
          </section>

          <section>
            <div className="trade-intel-rowhead">
              <h3>{t("Macro")}</h3>
              <div className="trade-venue-filter-row" role="group" aria-label={t("FRED series")}>
                {["CPIAUCSL", "UNRATE", "DFF", "DGS10"].map((sid) => (
                  <button
                    key={sid}
                    type="button"
                    className={`trade-venue-filter-btn ${seriesId === sid ? "is-selected" : ""}`}
                    onClick={() => setSeriesId(sid)}
                  >
                    {sid}
                  </button>
                ))}
              </div>
            </div>
            <ul className="trade-intel-list">
              {macro
                .slice(-4)
                .reverse()
                .map((pt) => (
                  <li key={pt.date}>
                    <span>{pt.date}</span>
                    <span>{pt.value.toFixed(2)}</span>
                  </li>
                ))}
            </ul>
          </section>

          <section>
            <div className="trade-intel-rowhead">
              <h3>{t("Filings")}</h3>
              <input
                aria-label={t("SEC CIK")}
                className="trade-cik-input"
                maxLength={10}
                value={cikDraft}
                onChange={(e) => setCikDraft(e.target.value)}
                onBlur={commitCIK}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault();
                    commitCIK();
                  }
                }}
              />
            </div>
            <ul className="trade-intel-list">
              {secFilings.slice(0, 4).map((f) => (
                <li key={f.accessionNumber}>
                  <span>
                    {f.form} · {f.filingDate}
                  </span>
                  {f.url ? (
                    <a href={f.url} target="_blank" rel="noopener noreferrer">
                      EDGAR
                    </a>
                  ) : null}
                </li>
              ))}
            </ul>
          </section>
        </div>
      )}
    </aside>
  );
}
