import { useCallback, useEffect, useRef, useState } from "react";
import { Badge } from "../components/shared";
import {
  CandlestickChart,
  Check,
  FlaskConical,
  Pause,
  Play,
  Plus,
  RefreshCw,
  RotateCcw,
  Shield,
  Trash2,
  X,
  Zap,
} from "../icons";
import { useUI } from "../ui-preferences";
import { useWorkspace } from "../workspace";
import { parseTradeWorkspaceConfig, TRADE_STORAGE_KEY } from "../market-data";
import { TRIGGER_DRAFT_KEY, type TriggerDraftSeed } from "../automation";
import {
  approveDeskTicket,
  buildEquityPolyline,
  closeDeskPosition,
  deleteDeskLesson,
  deleteLabScript,
  deskStatusLabel,
  formatCurrencyUSD,
  formatPrice,
  formatRMultiple,
  formatSignedPct,
  getDeskState,
  getLabRun,
  listDeskCycles,
  listDeskJournal,
  listDeskLessons,
  listLabRuns,
  listLabScripts,
  markDeskPositions,
  normalizeHistogramHeights,
  rejectDeskTicket,
  runDeskCycle,
  runLabBacktest,
  runLabValidation,
  saveDeskLesson,
  saveLabScript,
  setDeskControl,
  slugifyScriptId,
  updateDeskLimits,
  type BacktestResult,
  type CycleResult,
  type DeskMode,
  type DeskRiskLimits,
  type DeskSnapshot,
  type JournalEntry,
  type LabScript,
  type LessonRecord,
  type ValidationRunRecord,
  type ValidationRunSummary,
} from "../quantlab-desk";
import "./QuantLabWorkspace.css";

type LabTab = "scripts" | "desk" | "journal";

function tabFromRoute(route: string): LabTab {
  if (route === "lab/desk") return "desk";
  if (route === "lab/journal") return "journal";
  return "scripts";
}

const OUTCOME_FILTERS = [
  "",
  "SHADOW",
  "PENDING_PM",
  "FILLED",
  "CLOSED_WIN",
  "CLOSED_LOSS",
  "KILLED_BY_SKEPTIC",
  "FAILED_RISK_CHECK",
  "REJECTED_BY_PM",
] as const;

export function QuantLabWorkspace() {
  const { t } = useUI();
  const { core, route, streamClient, navigate } = useWorkspace();

  const [tab, setTab] = useState<LabTab>(() => tabFromRoute(route));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const limitsDirtyRef = useRef(false);

  // Lab scripts & runner state
  const [scripts, setScripts] = useState<LabScript[]>([]);
  const [selectedScriptId, setSelectedScriptId] = useState("");
  const [scriptIdInput, setScriptIdInput] = useState("custom-script");
  const [scriptNameInput, setScriptNameInput] = useState("Custom Strategy");
  const [scriptCatInput, setScriptCatInput] = useState("trend");
  const [scriptSourceInput, setScriptSourceInput] = useState(
    'const f = input("fast", 8, 4, 20, 2);\nconst ef = ema(close, f);\nconst es = ema(close, 21);\nplot("Fast EMA", ef, "accent", "overlay");\nstrategy({ long: crossover(ef, es), exitLong: crossunder(ef, es), stopLossPct: 2.0 });',
  );

  const [venue, setVenue] = useState("hyperliquid");
  const [symbol, setSymbol] = useState("BTC");
  const [interval, setInterval] = useState("1h");
  const [backtest, setBacktest] = useState<BacktestResult | null>(null);
  const [validationRun, setValidationRun] =
    useState<ValidationRunRecord | null>(null);
  const [recentRuns, setRecentRuns] = useState<ValidationRunSummary[]>([]);

  // Desk state
  const [desk, setDesk] = useState<DeskSnapshot | null>(null);
  const [latestCycle, setLatestCycle] = useState<CycleResult | null>(null);
  const [journal, setJournal] = useState<JournalEntry[]>([]);
  const [outcomeFilter, setOutcomeFilter] = useState("");
  const [lessons, setLessons] = useState<LessonRecord[]>([]);

  const [limitsDraft, setLimitsDraft] = useState<DeskRiskLimits>({
    mode: "shadow",
    initialCapitalUsd: 100000,
    riskPerTradePct: 1,
    maxPositionPct: 10,
    maxOpenPositions: 5,
    maxDailyLossPct: 2,
    maxConsecutiveLosses: 3,
    cooldownHours: 24,
    maxSlippageBps: 25,
    feeBps: 5,
    minChartScore: 7,
    minRiskReward: 1.5,
    staleDataMaxAgeSec: 3600,
    requirePmTap: true,
    requireFourGatePass: false,
  });
  const [confirmUnkill, setConfirmUnkill] = useState("");
  const [cycleMode, setCycleMode] = useState<DeskMode>("shadow");

  const [lessonCat, setLessonCat] = useState("risk");
  const [lessonSymbol, setLessonSymbol] = useState("BTC");
  const [lessonRule, setLessonRule] = useState("");

  const selectScriptRecord = useCallback((s: LabScript) => {
    setSelectedScriptId(s.id);
    setScriptIdInput(s.id);
    setScriptNameInput(s.name);
    setScriptCatInput(s.category || "trend");
    setScriptSourceInput(s.source);
  }, []);

  const loadLab = useCallback(async () => {
    if (core.mode !== "ready") return;
    try {
      const [scRes, runsRes] = await Promise.all([
        listLabScripts(core),
        listLabRuns(core, 15),
      ]);
      const items = scRes.scripts ?? [];
      setScripts(items);
      setRecentRuns(runsRes.runs ?? []);
      if (items.length > 0) {
        setSelectedScriptId((prev) => {
          if (prev && items.some((s) => s.id === prev)) return prev;
          const first = items.find((s) => s.id === "ema-rsi-trend") ?? items[0];
          setScriptIdInput(first.id);
          setScriptNameInput(first.name);
          setScriptCatInput(first.category || "trend");
          setScriptSourceInput(first.source);
          return first.id;
        });
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load Quant Lab");
    }
  }, [core]);

  const loadDesk = useCallback(async () => {
    if (core.mode !== "ready") return;
    try {
      const [snap, cyclesRes, journalRes, lessonsRes] = await Promise.all([
        getDeskState(core),
        listDeskCycles(core, 10),
        listDeskJournal(core, { outcome: outcomeFilter, limit: 80 }),
        listDeskLessons(core, 50),
      ]);
      setDesk(snap);
      if (!limitsDirtyRef.current) {
        setLimitsDraft(snap.limits);
        setCycleMode(snap.limits.mode === "paper" ? "paper" : "shadow");
      }
      if ((cyclesRes.cycles ?? []).length > 0) {
        setLatestCycle(cyclesRes.cycles[0]);
      }
      setJournal(journalRes.journal ?? []);
      setLessons(lessonsRes.lessons ?? []);
    } catch (err) {
      setError(
        err instanceof Error ? err.message : "Failed to load Trading Desk",
      );
    }
  }, [core, outcomeFilter]);

  const loadDeskRef = useRef(loadDesk);
  loadDeskRef.current = loadDesk;

  useEffect(() => {
    if (route.startsWith("lab/")) {
      setTab(tabFromRoute(route));
    }
  }, [route]);

  useEffect(() => {
    void loadLab();
  }, [loadLab]);

  useEffect(() => {
    void loadDesk();
  }, [loadDesk]);

  useEffect(() => {
    if (!streamClient || core.mode !== "ready") return;
    let reloadTimer: number | undefined;
    const unsub = streamClient.subscribe("desk", () => {
      if (reloadTimer !== undefined) window.clearTimeout(reloadTimer);
      reloadTimer = window.setTimeout(() => {
        reloadTimer = undefined;
        void loadDeskRef.current();
      }, 150);
    });
    return () => {
      unsub();
      if (reloadTimer !== undefined) window.clearTimeout(reloadTimer);
    };
  }, [streamClient, core.mode]);

  async function onSaveScript() {
    if (core.mode !== "ready") return;
    setBusy(true);
    setError("");
    try {
      const id = scriptIdInput.trim() || slugifyScriptId(scriptNameInput);
      const saved = await saveLabScript(core, {
        id,
        name: scriptNameInput.trim() || id,
        category: scriptCatInput,
        source: scriptSourceInput,
      });
      await loadLab();
      selectScriptRecord(saved);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Script save failed");
    } finally {
      setBusy(false);
    }
  }

  async function onDeleteScript(id: string) {
    if (core.mode !== "ready") return;
    setBusy(true);
    setError("");
    try {
      await deleteLabScript(core, id);
      if (selectedScriptId === id) setSelectedScriptId("");
      await loadLab();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Script delete failed");
    } finally {
      setBusy(false);
    }
  }

  async function onRunBacktest() {
    if (core.mode !== "ready") return;
    setBusy(true);
    setError("");
    try {
      const res = await runLabBacktest(core, {
        source: scriptSourceInput,
        config: { venue, symbol, interval, limit: 240 },
      });
      setBacktest(res);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Backtest failed");
    } finally {
      setBusy(false);
    }
  }

  async function onRunValidation() {
    if (core.mode !== "ready") return;
    setBusy(true);
    setError("");
    try {
      const selectedScript = scripts.find((s) => s.id === selectedScriptId);
      const sourceEdited =
        !selectedScript || scriptSourceInput !== selectedScript.source;
      const rec = await runLabValidation(core, {
        scriptId: selectedScriptId || undefined,
        source: sourceEdited ? scriptSourceInput : undefined,
        config: {
          venue,
          symbol,
          interval,
          limit: 240,
          permutations: 1000,
          wfPermutations: 200,
          seed: 42,
        },
      });
      setValidationRun(rec);
      await loadLab();
    } catch (err) {
      setError(err instanceof Error ? err.message : "4-Gate validation failed");
    } finally {
      setBusy(false);
    }
  }

  async function onOpenRun(id: string) {
    if (core.mode !== "ready") return;
    setBusy(true);
    setError("");
    try {
      const rec = await getLabRun(core, id);
      setValidationRun(rec);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Run load failed");
    } finally {
      setBusy(false);
    }
  }

  async function onDeskControl(
    action: "pause" | "resume" | "kill" | "unkill" | "reset_paper",
  ) {
    if (core.mode !== "ready") return;
    setBusy(true);
    setError("");
    try {
      await setDeskControl(core, {
        action,
        confirm: action === "unkill" ? confirmUnkill : undefined,
        reason: `UI ${action}`,
      });
      if (action === "unkill") setConfirmUnkill("");
      if (action === "reset_paper") limitsDirtyRef.current = false;
      await loadDesk();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Desk control failed");
    } finally {
      setBusy(false);
    }
  }

  async function onSaveLimits() {
    if (core.mode !== "ready") return;
    setBusy(true);
    setError("");
    try {
      const saved = await updateDeskLimits(core, limitsDraft);
      limitsDirtyRef.current = false;
      setLimitsDraft(saved);
      setCycleMode(saved.mode === "paper" ? "paper" : "shadow");
      await loadDesk();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save limits");
    } finally {
      setBusy(false);
    }
  }

  async function onRunCycle() {
    if (core.mode !== "ready") return;
    setBusy(true);
    setError("");
    try {
      const res = await runDeskCycle(core, {
        venue,
        symbol,
        interval,
        scriptId: selectedScriptId || "ema-rsi-trend",
        mode: cycleMode,
        limit: 120,
      });
      setLatestCycle(res);
      await loadDesk();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Desk cycle failed");
    } finally {
      setBusy(false);
    }
  }

  async function onTicketDecision(id: string, approve: boolean) {
    if (core.mode !== "ready") return;
    setBusy(true);
    setError("");
    try {
      if (approve) {
        await approveDeskTicket(core, id);
      } else {
        await rejectDeskTicket(core, id, "PM rejected in UI");
      }
      await loadDesk();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Ticket decision failed");
    } finally {
      setBusy(false);
    }
  }

  async function onMarkPositions() {
    if (core.mode !== "ready") return;
    setBusy(true);
    setError("");
    try {
      await markDeskPositions(core);
      await loadDesk();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Mark positions failed");
    } finally {
      setBusy(false);
    }
  }

  async function onClosePosition(id: string) {
    if (core.mode !== "ready") return;
    setBusy(true);
    setError("");
    try {
      await closeDeskPosition(core, id, { reason: "PM_MANUAL_CLOSE" });
      await loadDesk();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Close position failed");
    } finally {
      setBusy(false);
    }
  }

  async function onAddLesson() {
    if (core.mode !== "ready" || !lessonRule.trim()) return;
    setBusy(true);
    setError("");
    try {
      await saveDeskLesson(core, {
        category: lessonCat,
        symbol: lessonSymbol,
        rule: lessonRule,
      });
      setLessonRule("");
      await loadDesk();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save lesson");
    } finally {
      setBusy(false);
    }
  }

  function onPlotOnChart() {
    const activeScript =
      selectedScriptId || scriptIdInput.trim() || "ema-rsi-trend";
    try {
      const parsed = parseTradeWorkspaceConfig(
        localStorage.getItem(TRADE_STORAGE_KEY),
      );
      const updated = {
        ...parsed,
        tiles: parsed.tiles.map((tile) =>
          tile.id === parsed.activeTileId
            ? {
                ...tile,
                venue,
                symbol,
                interval: interval as typeof tile.interval,
                scriptId: activeScript,
              }
            : tile,
        ),
      };
      localStorage.setItem(TRADE_STORAGE_KEY, JSON.stringify(updated));
    } catch {}
    navigate("trade");
  }

  function onCreateTrigger() {
    const activeScript =
      selectedScriptId || scriptIdInput.trim() || "ema-rsi-trend";
    const seed: TriggerDraftSeed = {
      kind: "fasedscript",
      name: `${scriptNameInput || activeScript} (${symbol})`.slice(0, 64),
      venue,
      symbol,
      interval,
      scriptId: activeScript,
    };
    try {
      sessionStorage.setItem(TRIGGER_DRAFT_KEY, JSON.stringify(seed));
    } catch {}
    navigate("agents/tasks");
  }

  async function onDeleteLesson(id: string) {
    if (core.mode !== "ready") return;
    setBusy(true);
    setError("");
    try {
      await deleteDeskLesson(core, id);
      await loadDesk();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to delete lesson");
    } finally {
      setBusy(false);
    }
  }

  const pendingTickets = (desk?.tickets ?? []).filter(
    (tk) => tk.status === "PENDING_PM",
  );
  const openPositions = (desk?.positions ?? []).filter(
    (p) => p.status === "OPEN",
  );
  const halted = desk?.control.effectiveHalted ?? false;
  const killed =
    (desk?.control.killSwitchActive ?? false) ||
    (desk?.control.externalKillActive ?? false);

  const equityPolyline = buildEquityPolyline(backtest?.equity ?? [], 360, 84);
  const g2Heights = normalizeHistogramHeights(
    validationRun?.report.gate2.histogram ?? [],
  );
  const g4Heights = normalizeHistogramHeights(
    validationRun?.report.gate4.histogram ?? [],
  );

  return (
    <div className="lab-workspace">
      <header className="lab-toolbar">
        <div className="lab-title-group">
          <FlaskConical size={20} aria-hidden="true" />
          <h1>{t("Quant Lab")}</h1>
          <Badge>{t("Paper / Shadow")}</Badge>
        </div>

        <div className="lab-tabs" role="tablist">
          <button
            type="button"
            role="tab"
            data-testid="lab-tab-scripts"
            aria-selected={tab === "scripts"}
            className={`lab-tab-btn ${tab === "scripts" ? "is-selected" : ""}`}
            onClick={() => setTab("scripts")}
          >
            {t("FasedScript")}
          </button>
          <button
            type="button"
            role="tab"
            data-testid="lab-tab-desk"
            aria-selected={tab === "desk"}
            className={`lab-tab-btn ${tab === "desk" ? "is-selected" : ""}`}
            onClick={() => setTab("desk")}
          >
            {t("Trading Desk")}
          </button>
          <button
            type="button"
            role="tab"
            data-testid="lab-tab-journal"
            aria-selected={tab === "journal"}
            className={`lab-tab-btn ${tab === "journal" ? "is-selected" : ""}`}
            onClick={() => setTab("journal")}
          >
            {t("Journal & Lessons")}
          </button>
        </div>
      </header>

      {error && (
        <div className="lab-banner-error" role="alert">
          {error}
        </div>
      )}

      {tab === "scripts" && (
        <div className="lab-grid-three">
          <section className="lab-card lab-scripts-rail">
            <div className="lab-card-head">
              <h2>{t("FasedScript")}</h2>
              <button
                type="button"
                className="secondary"
                disabled={busy}
                onClick={() => {
                  setSelectedScriptId("");
                  setScriptIdInput("custom-script");
                  setScriptNameInput("Custom Strategy");
                  setScriptCatInput("trend");
                  setScriptSourceInput(
                    'const ef = ema(close, 8);\nconst es = ema(close, 21);\nplot("EF", ef, "accent", "overlay");\nstrategy({ long: crossover(ef, es), exitLong: crossunder(ef, es), stopLossPct: 2.0 });',
                  );
                }}
              >
                <Plus size={14} aria-hidden="true" />
                <span>{t("New script")}</span>
              </button>
            </div>

            <div className="lab-script-list">
              {scripts.map((s) => (
                <div
                  key={s.id}
                  className={`lab-row-card ${selectedScriptId === s.id ? "is-selected" : ""}`}
                >
                  <div className="lab-row-top">
                    <button
                      type="button"
                      className="lab-row-title"
                      title={s.description || s.name}
                      onClick={() => selectScriptRecord(s)}
                    >
                      {s.name}
                    </button>
                    <div className="lab-actions-row">
                      <span className="lab-status-pill">
                        {s.builtIn ? t("Built-in") : s.category}
                      </span>
                      {!s.builtIn && (
                        <button
                          type="button"
                          className="icon-button"
                          aria-label={`${t("Delete")} ${s.name}`}
                          disabled={busy}
                          onClick={() => void onDeleteScript(s.id)}
                        >
                          <Trash2 size={14} aria-hidden="true" />
                        </button>
                      )}
                    </div>
                  </div>
                </div>
              ))}
            </div>
          </section>

          <section className="lab-card">
            <div className="lab-card-head">
              <h2>{t("Script editor")}</h2>
              <button
                type="button"
                className="primary"
                disabled={busy || core.mode !== "ready"}
                onClick={() => void onSaveScript()}
              >
                {t("Save script")}
              </button>
            </div>

            <div className="lab-field-grid">
              <div className="lab-field">
                <label htmlFor="lab-script-id">{t("Script ID")}</label>
                <input
                  id="lab-script-id"
                  value={scriptIdInput}
                  onChange={(e) => setScriptIdInput(e.target.value)}
                />
              </div>
              <div className="lab-field">
                <label htmlFor="lab-script-name">{t("Name")}</label>
                <input
                  id="lab-script-name"
                  value={scriptNameInput}
                  onChange={(e) => setScriptNameInput(e.target.value)}
                />
              </div>
              <div className="lab-field">
                <label htmlFor="lab-script-cat">{t("Category")}</label>
                <select
                  id="lab-script-cat"
                  value={scriptCatInput}
                  onChange={(e) => setScriptCatInput(e.target.value)}
                >
                  <option value="trend">trend</option>
                  <option value="mean_reversion">mean_reversion</option>
                  <option value="breakout">breakout</option>
                  <option value="volatility">volatility</option>
                  <option value="indicator">indicator</option>
                </select>
              </div>
            </div>

            <div className="lab-field">
              <label htmlFor="lab-script-code">{t("Source code")}</label>
              <textarea
                id="lab-script-code"
                className="lab-code-editor"
                value={scriptSourceInput}
                onChange={(e) => setScriptSourceInput(e.target.value)}
              />
            </div>
          </section>

          <div className="lab-col">
            <section className="lab-card">
              <div className="lab-card-head">
                <h2>{t("Validation")}</h2>
                <div className="lab-actions-row">
                  <button
                    type="button"
                    className="secondary"
                    disabled={busy || core.mode !== "ready"}
                    onClick={() => void onRunBacktest()}
                  >
                    <Play size={14} aria-hidden="true" />
                    <span>{t("Run backtest")}</span>
                  </button>
                  <button
                    type="button"
                    className="primary"
                    disabled={busy || core.mode !== "ready"}
                    onClick={() => void onRunValidation()}
                  >
                    <Shield size={14} aria-hidden="true" />
                    <span>{t("Run 4-Gate")}</span>
                  </button>
                  <button
                    type="button"
                    className="secondary"
                    onClick={onPlotOnChart}
                  >
                    <CandlestickChart size={14} aria-hidden="true" />
                    <span>{t("Plot on chart")}</span>
                  </button>
                  <button
                    type="button"
                    className="secondary"
                    onClick={onCreateTrigger}
                  >
                    <Zap size={14} aria-hidden="true" />
                    <span>{t("Create trigger")}</span>
                  </button>
                </div>
              </div>

              <div className="lab-field-grid">
                <div className="lab-field">
                  <label htmlFor="lab-venue">{t("Venue")}</label>
                  <select
                    id="lab-venue"
                    value={venue}
                    onChange={(e) => setVenue(e.target.value)}
                  >
                    <option value="hyperliquid">hyperliquid</option>
                    <option value="binance">binance</option>
                    <option value="bybit">bybit</option>
                    <option value="kraken">kraken</option>
                    <option value="solana">solana</option>
                  </select>
                </div>
                <div className="lab-field">
                  <label htmlFor="lab-symbol">{t("Symbol")}</label>
                  <input
                    id="lab-symbol"
                    value={symbol}
                    onChange={(e) => setSymbol(e.target.value.toUpperCase())}
                  />
                </div>
                <div className="lab-field">
                  <label htmlFor="lab-interval">{t("Interval")}</label>
                  <select
                    id="lab-interval"
                    value={interval}
                    onChange={(e) => setInterval(e.target.value)}
                  >
                    <option value="1m">1m</option>
                    <option value="5m">5m</option>
                    <option value="15m">15m</option>
                    <option value="1h">1h</option>
                    <option value="4h">4h</option>
                    <option value="1d">1d</option>
                  </select>
                </div>
              </div>

              {backtest && (
                <div className="lab-list" data-testid="lab-backtest-metrics">
                  <div className="lab-metrics-grid">
                    <div className="lab-metric-tile">
                      <span>{t("Return")}</span>
                      <strong>
                        {formatSignedPct(backtest.metrics.totalReturnPct)}
                      </strong>
                    </div>
                    <div className="lab-metric-tile">
                      <span>{t("Sharpe")}</span>
                      <strong>
                        {backtest.metrics.annualizedSharpe.toFixed(2)}
                      </strong>
                    </div>
                    <div className="lab-metric-tile">
                      <span>{t("Max DD")}</span>
                      <strong>
                        {backtest.metrics.maxDrawdownPct.toFixed(2)}%
                      </strong>
                    </div>
                    <div className="lab-metric-tile">
                      <span>{t("Win rate")}</span>
                      <strong>{backtest.metrics.winRatePct.toFixed(1)}%</strong>
                    </div>
                    <div className="lab-metric-tile">
                      <span>{t("Trades")}</span>
                      <strong>{backtest.metrics.tradeCount}</strong>
                    </div>
                    <div className="lab-metric-tile">
                      <span>{t("Profit factor")}</span>
                      <strong>
                        {backtest.metrics.profitFactor.toFixed(2)}
                      </strong>
                    </div>
                  </div>
                  {equityPolyline && (
                    <div className="lab-chart-frame">
                      <svg
                        className="lab-equity-svg"
                        viewBox="0 0 360 84"
                        role="img"
                        aria-label={t("Equity curve")}
                      >
                        <polyline
                          className="lab-equity-line"
                          points={equityPolyline}
                        />
                      </svg>
                    </div>
                  )}
                </div>
              )}

              {validationRun && (
                <div className="lab-list" data-testid="lab-four-gate">
                  <div className="lab-row-top">
                    <strong>
                      {`${t("Grade")} ${validationRun.report.grade} (${validationRun.report.gatesPassed}/4)`}
                    </strong>
                    <span
                      className={`lab-status-pill ${validationRun.report.promotionReady ? "is-ok" : "is-warn"}`}
                    >
                      {validationRun.report.promotionReady
                        ? t("Paper ready")
                        : t("Paper / Shadow")}
                    </span>
                  </div>

                  <div className="lab-gates-grid">
                    <div
                      className={`lab-gate-card ${validationRun.report.gate1.passed ? "is-pass" : "is-fail"}`}
                    >
                      <div className="lab-row-top">
                        <strong>{t("Gate 1: Plateau")}</strong>
                        <span>
                          {`${validationRun.report.gate1.passingCells}/${validationRun.report.gate1.testedCells}`}
                        </span>
                      </div>
                      <div className="lab-plateau-grid">
                        {validationRun.report.gate1.cells
                          .slice(0, 12)
                          .map((cell, i) => (
                            <span
                              key={i}
                              className={`lab-plateau-cell ${cell.passed ? "is-pass" : "is-fail"}`}
                            >
                              {cell.sharpe.toFixed(1)}
                            </span>
                          ))}
                      </div>
                    </div>

                    <div
                      className={`lab-gate-card ${validationRun.report.gate2.passed ? "is-pass" : "is-fail"}`}
                    >
                      <div className="lab-row-top">
                        <strong>{t("Gate 2: Permutation")}</strong>
                        <span>
                          {`p=${validationRun.report.gate2.pValue.toFixed(3)}`}
                        </span>
                      </div>
                      <div className="lab-muted">
                        {`${validationRun.report.gate2.iterations}x · beat=${validationRun.report.gate2.beatCount}`}
                      </div>
                      <div className="lab-histogram">
                        {g2Heights.map((h, i) => (
                          <div
                            key={i}
                            className="lab-hist-bar"
                            style={{ height: `${Math.max(4, h)}%` }}
                          />
                        ))}
                      </div>
                    </div>

                    <div
                      className={`lab-gate-card ${validationRun.report.gate3.passed ? "is-pass" : "is-fail"}`}
                    >
                      <div className="lab-row-top">
                        <strong>{t("Gate 3: Walk-Forward")}</strong>
                        <span>
                          {`OOS ${validationRun.report.gate3.oosSharpe.toFixed(2)}`}
                        </span>
                      </div>
                      <div className="lab-muted">
                        {`Holdout ${validationRun.report.gate3.holdoutSharpe.toFixed(2)} · ${validationRun.report.gate3.holdoutTrades} trades`}
                      </div>
                    </div>

                    <div
                      className={`lab-gate-card ${validationRun.report.gate4.passed ? "is-pass" : "is-fail"}`}
                    >
                      <div className="lab-row-top">
                        <strong>{t("Gate 4: WF Permutation")}</strong>
                        <span>
                          {`p=${validationRun.report.gate4.pValue.toFixed(3)}`}
                        </span>
                      </div>
                      <div className="lab-muted">
                        {`${validationRun.report.gate4.iterations}x · beat=${validationRun.report.gate4.beatCount}`}
                      </div>
                      <div className="lab-histogram">
                        {g4Heights.map((h, i) => (
                          <div
                            key={i}
                            className="lab-hist-bar"
                            style={{ height: `${Math.max(4, h)}%` }}
                          />
                        ))}
                      </div>
                    </div>
                  </div>
                </div>
              )}

              {recentRuns.length > 0 && (
                <div className="lab-list">
                  {recentRuns.slice(0, 5).map((r) => (
                    <button
                      key={r.id}
                      type="button"
                      className="lab-row-card"
                      onClick={() => void onOpenRun(r.id)}
                    >
                      <div className="lab-row-top">
                        <strong>{`${r.scriptName} · ${r.symbol} ${r.interval}`}</strong>
                        <span className="lab-status-pill">
                          {`${r.grade} (${r.gatesPassed}/4)`}
                        </span>
                      </div>
                    </button>
                  ))}
                </div>
              )}
            </section>
          </div>
        </div>
      )}

      {tab === "desk" && (
        <div className="lab-col">
          <div className="lab-metrics-grid">
            <div className="lab-metric-tile">
              <span>{t("Equity")}</span>
              <strong>
                {formatCurrencyUSD(desk?.account.equityUsd ?? 100000)}
              </strong>
            </div>
            <div className="lab-metric-tile">
              <span>{t("Cash")}</span>
              <strong>
                {formatCurrencyUSD(desk?.account.cashUsd ?? 100000)}
              </strong>
            </div>
            <div className="lab-metric-tile">
              <span>{t("Realized PnL")}</span>
              <strong>
                {formatCurrencyUSD(desk?.account.realizedPnlUsd ?? 0)}
              </strong>
            </div>
            <div className="lab-metric-tile">
              <span>{t("Daily loss")}</span>
              <strong>{`${(desk?.account.dailyLossPct ?? 0).toFixed(2)}%`}</strong>
            </div>
            <div className="lab-metric-tile">
              <span>{t("Open positions")}</span>
              <strong>{desk?.account.openPositions ?? 0}</strong>
            </div>
            <div className="lab-metric-tile">
              <span>{t("Decay")}</span>
              <strong>{desk?.decay.status ?? "WARMING_UP"}</strong>
            </div>
          </div>

          <div
            className={`lab-kill-bar ${halted ? "is-killed" : ""}`}
            data-testid="desk-kill-bar"
          >
            <div className="lab-pill-row">
              <span
                className={`lab-status-pill ${killed ? "is-danger" : desk?.control.paused ? "is-warn" : "is-ok"}`}
              >
                {t(deskStatusLabel(desk?.control))}
              </span>
              {desk?.control.reason && (
                <span className="lab-text-sm">{desk.control.reason}</span>
              )}
            </div>

            <div className="lab-actions-row">
              {!killed && (
                <>
                  <button
                    type="button"
                    className="secondary"
                    disabled={busy || core.mode !== "ready"}
                    onClick={() =>
                      void onDeskControl(
                        desk?.control.paused ? "resume" : "pause",
                      )
                    }
                  >
                    {desk?.control.paused ? (
                      <Play size={14} aria-hidden="true" />
                    ) : (
                      <Pause size={14} aria-hidden="true" />
                    )}
                    <span>{t(desk?.control.paused ? "Resume" : "Pause")}</span>
                  </button>
                  <button
                    type="button"
                    className="secondary"
                    disabled={busy || core.mode !== "ready"}
                    onClick={() => void onDeskControl("kill")}
                  >
                    <span>{t("Kill switch")}</span>
                  </button>
                </>
              )}

              {desk?.control.killSwitchActive &&
                !desk?.control.externalKillActive && (
                  <>
                    <input
                      aria-label={t("Confirm")}
                      placeholder="CONFIRM"
                      value={confirmUnkill}
                      onChange={(e) => setConfirmUnkill(e.target.value)}
                    />
                    <button
                      type="button"
                      className="primary"
                      disabled={busy}
                      onClick={() => void onDeskControl("unkill")}
                    >
                      <span>{t("Clear kill switch")}</span>
                    </button>
                  </>
                )}

              <button
                type="button"
                className="secondary"
                disabled={busy || core.mode !== "ready"}
                onClick={() => void onDeskControl("reset_paper")}
              >
                <RotateCcw size={14} aria-hidden="true" />
                <span>{t("Reset paper")}</span>
              </button>
            </div>
          </div>

          <section className="lab-card" data-testid="desk-seat-roster">
            <div className="lab-card-head">
              <h2>{t("Desk seats")}</h2>
              <span className="lab-status-pill">{desk?.seats.length ?? 8}</span>
            </div>
            <div className="lab-seats-grid">
              {(desk?.seats ?? []).map((seat) => {
                const art = latestCycle?.artifacts.find(
                  (a) => a.seat === seat.id,
                );
                return (
                  <article
                    className={`lab-seat-card ${art ? "has-artifact" : ""}`}
                    key={seat.id}
                  >
                    <div className="lab-row-top">
                      <strong>{seat.title}</strong>
                      <div className="lab-actions-row">
                        <span
                          className="lab-status-pill"
                          title={seat.allowedTools.join(", ")}
                        >
                          {seat.allowedTools.length}
                        </span>
                        <span
                          className={`lab-status-pill ${seat.canExecutePaper ? "is-warn" : ""}`}
                        >
                          {seat.canExecutePaper
                            ? t("Paper only")
                            : t("Read only")}
                        </span>
                      </div>
                    </div>
                    {art && (
                      <div className="lab-text-sm">
                        {`${art.verdict}: ${art.summary}`}
                      </div>
                    )}
                  </article>
                );
              })}
            </div>
          </section>

          <div className="lab-grid-two">
            <div className="lab-col">
              <section className="lab-card">
                <div className="lab-card-head">
                  <h2>{t("Risk limits")}</h2>
                  <button
                    type="button"
                    className="secondary"
                    disabled={busy || core.mode !== "ready"}
                    onClick={() => void onSaveLimits()}
                  >
                    <span>{t("Save limits")}</span>
                  </button>
                </div>

                <div className="lab-field-grid">
                  <div className="lab-field">
                    <label htmlFor="desk-mode">{t("Mode")}</label>
                    <select
                      id="desk-mode"
                      value={limitsDraft.mode}
                      onChange={(e) => {
                        limitsDirtyRef.current = true;
                        setLimitsDraft((cur) => ({
                          ...cur,
                          mode: e.target.value as DeskMode,
                        }));
                      }}
                    >
                      <option value="shadow">{t("Shadow")}</option>
                      <option value="paper">{t("Paper")}</option>
                    </select>
                  </div>
                  <div className="lab-field">
                    <label htmlFor="desk-risk-pct">
                      {t("Risk per trade (%)")}
                    </label>
                    <input
                      id="desk-risk-pct"
                      type="number"
                      step="0.1"
                      value={limitsDraft.riskPerTradePct}
                      onChange={(e) => {
                        limitsDirtyRef.current = true;
                        setLimitsDraft((cur) => ({
                          ...cur,
                          riskPerTradePct: Number(e.target.value) || 0,
                        }));
                      }}
                    />
                  </div>
                  <div className="lab-field">
                    <label htmlFor="desk-daily-pct">
                      {t("Max daily loss (%)")}
                    </label>
                    <input
                      id="desk-daily-pct"
                      type="number"
                      step="0.5"
                      value={limitsDraft.maxDailyLossPct}
                      onChange={(e) => {
                        limitsDirtyRef.current = true;
                        setLimitsDraft((cur) => ({
                          ...cur,
                          maxDailyLossPct: Number(e.target.value) || 0,
                        }));
                      }}
                    />
                  </div>
                </div>

                <div className="lab-pill-row">
                  <label className="lab-checkbox-row">
                    <input
                      type="checkbox"
                      checked={limitsDraft.requirePmTap}
                      onChange={(e) => {
                        limitsDirtyRef.current = true;
                        setLimitsDraft((cur) => ({
                          ...cur,
                          requirePmTap: e.target.checked,
                        }));
                      }}
                    />
                    <span>{t("Require PM tap")}</span>
                  </label>
                  <label className="lab-checkbox-row">
                    <input
                      type="checkbox"
                      checked={limitsDraft.requireFourGatePass}
                      onChange={(e) => {
                        limitsDirtyRef.current = true;
                        setLimitsDraft((cur) => ({
                          ...cur,
                          requireFourGatePass: e.target.checked,
                        }));
                      }}
                    />
                    <span>{t("Require 4-Gate")}</span>
                  </label>
                </div>
              </section>

              <section className="lab-card" data-testid="desk-tickets-list">
                <div className="lab-card-head">
                  <h2>{t("Pending tickets")}</h2>
                  <span className="lab-status-pill">
                    {pendingTickets.length}
                  </span>
                </div>
                {pendingTickets.length === 0 ? (
                  <div className="lab-muted">{t("No pending tickets")}</div>
                ) : (
                  <div className="lab-list">
                    {pendingTickets.map((tk) => (
                      <div className="lab-row-card" key={tk.id}>
                        <div className="lab-row-top">
                          <strong>{`${tk.symbol} ${tk.side.toUpperCase()} · ${tk.quantity} @ ${formatPrice(tk.entryPrice)}`}</strong>
                          <span className="lab-status-pill">
                            {`R:R ${tk.riskReward.toFixed(2)}`}
                          </span>
                        </div>
                        <div className="lab-muted">
                          {`Stop ${formatPrice(tk.stopPrice)} · Target ${formatPrice(tk.targetPrice)} · Risk ${formatCurrencyUSD(tk.riskUsd)}`}
                        </div>
                        <div className="lab-actions-row">
                          <button
                            type="button"
                            className="primary"
                            disabled={busy}
                            onClick={() => void onTicketDecision(tk.id, true)}
                          >
                            <Check size={14} aria-hidden="true" />
                            <span>{t("Approve")}</span>
                          </button>
                          <button
                            type="button"
                            className="secondary"
                            disabled={busy}
                            onClick={() => void onTicketDecision(tk.id, false)}
                          >
                            <X size={14} aria-hidden="true" />
                            <span>{t("Reject")}</span>
                          </button>
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </section>

              <section className="lab-card" data-testid="desk-positions-list">
                <div className="lab-card-head">
                  <h2>{t("Open positions")}</h2>
                  <button
                    type="button"
                    className="secondary"
                    disabled={busy || core.mode !== "ready"}
                    onClick={() => void onMarkPositions()}
                  >
                    <RefreshCw size={14} aria-hidden="true" />
                    <span>{t("Mark")}</span>
                  </button>
                </div>
                {openPositions.length === 0 ? (
                  <div className="lab-muted">{t("No open positions")}</div>
                ) : (
                  <div className="lab-list">
                    {openPositions.map((p) => (
                      <div className="lab-row-card" key={p.id}>
                        <div className="lab-row-top">
                          <strong>{`${p.symbol} ${p.side.toUpperCase()} · ${p.quantity} @ ${formatPrice(p.entryPrice)}`}</strong>
                          <span className="lab-status-pill">
                            {formatCurrencyUSD(p.unrealizedPnlUsd)}
                          </span>
                        </div>
                        <div className="lab-actions-row">
                          <button
                            type="button"
                            className="secondary"
                            disabled={busy}
                            onClick={() => void onClosePosition(p.id)}
                          >
                            <span>{t("Close")}</span>
                          </button>
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </section>
            </div>

            <div className="lab-col">
              <section className="lab-card">
                <div className="lab-card-head">
                  <h2>{t("Cycle runner")}</h2>
                  <button
                    type="button"
                    className="primary"
                    disabled={busy || core.mode !== "ready"}
                    onClick={() => void onRunCycle()}
                  >
                    <Play size={14} aria-hidden="true" />
                    <span>{t("Run 8-Seat Cycle")}</span>
                  </button>
                </div>
                <div className="lab-field-grid">
                  <div className="lab-field">
                    <label htmlFor="desk-cycle-mode">{t("Cycle mode")}</label>
                    <select
                      id="desk-cycle-mode"
                      value={cycleMode}
                      onChange={(e) => setCycleMode(e.target.value as DeskMode)}
                    >
                      <option value="shadow">{t("Shadow")}</option>
                      <option value="paper">{t("Paper")}</option>
                    </select>
                  </div>
                  <div className="lab-field">
                    <label htmlFor="desk-cycle-script">
                      {t("FasedScript")}
                    </label>
                    <select
                      id="desk-cycle-script"
                      value={selectedScriptId || "ema-rsi-trend"}
                      onChange={(e) => {
                        const found = scripts.find(
                          (s) => s.id === e.target.value,
                        );
                        if (found) selectScriptRecord(found);
                        else setSelectedScriptId(e.target.value);
                      }}
                    >
                      {scripts.map((s) => (
                        <option key={s.id} value={s.id}>
                          {s.name}
                        </option>
                      ))}
                    </select>
                  </div>
                  <div className="lab-field">
                    <label htmlFor="desk-cycle-symbol">{t("Symbol")}</label>
                    <input
                      id="desk-cycle-symbol"
                      value={symbol}
                      onChange={(e) => setSymbol(e.target.value.toUpperCase())}
                    />
                  </div>
                </div>
                {latestCycle && (
                  <div className="lab-row-card">
                    <div className="lab-row-top">
                      <strong>{`${latestCycle.symbol} · ${latestCycle.mode}`}</strong>
                      <span className="lab-status-pill">
                        {latestCycle.finalStatus}
                      </span>
                    </div>
                    <div className="lab-muted">
                      {latestCycle.riskCheck.summary}
                    </div>
                  </div>
                )}
              </section>
            </div>
          </div>
        </div>
      )}

      {tab === "journal" && (
        <div className="lab-grid-two">
          <div className="lab-col">
            <section className="lab-card">
              <div className="lab-card-head">
                <h2>{t("Decay monitor")}</h2>
                <span
                  className={`lab-status-pill ${desk?.decay.decaying ? "is-danger" : "is-ok"}`}
                >
                  {desk?.decay.status ?? "WARMING_UP"}
                </span>
              </div>
              <div className="lab-metrics-grid">
                <div className="lab-metric-tile">
                  <span>{t("Closed trades")}</span>
                  <strong>{desk?.decay.closedTrades ?? 0}</strong>
                </div>
                <div className="lab-metric-tile">
                  <span>{t("Paper Avg R")}</span>
                  <strong>{formatRMultiple(desk?.decay.paperAvgR ?? 0)}</strong>
                </div>
                <div className="lab-metric-tile">
                  <span>{t("Backtest Avg R")}</span>
                  <strong>
                    {formatRMultiple(desk?.decay.backtestAvgR ?? 0.5)}
                  </strong>
                </div>
                <div className="lab-metric-tile">
                  <span>{t("Skeptic kills")}</span>
                  <strong>{desk?.decay.skepticKills ?? 0}</strong>
                </div>
              </div>
              {desk?.decay.summary && (
                <div className="lab-muted">{desk.decay.summary}</div>
              )}
            </section>

            <section className="lab-card" data-testid="desk-journal-list">
              <div className="lab-card-head">
                <h2>{t("Trade journal")}</h2>
                <select
                  aria-label={t("Outcome")}
                  value={outcomeFilter}
                  onChange={(e) => setOutcomeFilter(e.target.value)}
                >
                  {OUTCOME_FILTERS.map((f) => (
                    <option key={f || "all"} value={f}>
                      {f || t("All")}
                    </option>
                  ))}
                </select>
              </div>
              <div className="lab-list">
                {journal.map((entry) => (
                  <div className="lab-row-card" key={entry.id}>
                    <div className="lab-row-top">
                      <strong>{`${entry.symbol} · ${entry.mode}`}</strong>
                      <span className="lab-status-pill">{entry.outcome}</span>
                    </div>
                    <div className="lab-muted">
                      {`Score ${entry.score.toFixed(1)} · Risk ${formatCurrencyUSD(entry.riskUsd)} · PnL ${formatCurrencyUSD(entry.pnlUsd)} (${formatRMultiple(entry.pnlR)})`}
                    </div>
                    {entry.reason && (
                      <div className="lab-text-sm">{entry.reason}</div>
                    )}
                    {entry.outcome === "CLOSED_LOSS" && (
                      <div className="lab-actions-row">
                        <button
                          type="button"
                          className="secondary"
                          onClick={() => {
                            setLessonCat("risk");
                            setLessonSymbol(entry.symbol);
                            setLessonRule(
                              entry.reason ||
                                `${entry.symbol} closed loss (${formatRMultiple(entry.pnlR)})`,
                            );
                          }}
                        >
                          <span>{t("Save lesson")}</span>
                        </button>
                      </div>
                    )}
                  </div>
                ))}
                {journal.length === 0 && (
                  <div className="lab-muted">{t("No journal entries")}</div>
                )}
              </div>
            </section>
          </div>

          <div className="lab-col">
            <section className="lab-card" data-testid="desk-lessons-list">
              <div className="lab-card-head">
                <h2>{t("Lessons")}</h2>
                <span className="lab-status-pill">{lessons.length}</span>
              </div>

              <div className="lab-field-grid">
                <div className="lab-field">
                  <label htmlFor="lesson-cat">{t("Category")}</label>
                  <select
                    id="lesson-cat"
                    value={lessonCat}
                    onChange={(e) => setLessonCat(e.target.value)}
                  >
                    <option value="risk">risk</option>
                    <option value="entry">entry</option>
                    <option value="exit">exit</option>
                    <option value="skeptic">skeptic</option>
                    <option value="quant">quant</option>
                    <option value="macro">macro</option>
                  </select>
                </div>
                <div className="lab-field">
                  <label htmlFor="lesson-sym">{t("Symbol")}</label>
                  <input
                    id="lesson-sym"
                    value={lessonSymbol}
                    onChange={(e) =>
                      setLessonSymbol(e.target.value.toUpperCase())
                    }
                  />
                </div>
              </div>
              <div className="lab-field">
                <label htmlFor="lesson-rule">{t("Rule")}</label>
                <input
                  id="lesson-rule"
                  value={lessonRule}
                  onChange={(e) => setLessonRule(e.target.value)}
                />
              </div>
              <div className="lab-actions-row">
                <button
                  type="button"
                  className="primary"
                  disabled={busy || !lessonRule.trim() || core.mode !== "ready"}
                  onClick={() => void onAddLesson()}
                >
                  <Plus size={14} aria-hidden="true" />
                  <span>{t("Add lesson")}</span>
                </button>
              </div>

              <div className="lab-list">
                {lessons.map((l) => (
                  <div className="lab-row-card" key={l.id}>
                    <div className="lab-row-top">
                      <strong>
                        {l.symbol ? `${l.category} · ${l.symbol}` : l.category}
                      </strong>
                      <button
                        type="button"
                        className="icon-button"
                        aria-label={`${t("Delete")} ${l.rule}`}
                        disabled={busy}
                        onClick={() => void onDeleteLesson(l.id)}
                      >
                        <Trash2 size={14} aria-hidden="true" />
                      </button>
                    </div>
                    <div className="lab-text-sm">{l.rule}</div>
                  </div>
                ))}
              </div>
            </section>
          </div>
        </div>
      )}
    </div>
  );
}
