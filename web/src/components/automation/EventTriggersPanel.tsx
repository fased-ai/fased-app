import { useCallback, useEffect, useState } from "react";
import {
  automationStateLabel,
  deleteEventTrigger,
  evaluateAutomationNow,
  getAutomationState,
  listEventTriggers,
  listNotifications,
  markNotificationsRead,
  saveEventTrigger,
  setAutomationState,
  slugifyTriggerId,
  summarizeTriggerCondition,
  testEventTrigger,
  type AutomationControlState,
  type EventTrigger,
  type NotificationItem,
  type NotificationSeverity,
  type TriggerActionType,
  type TriggerComparator,
  type TriggerIndicator,
  type TriggerKind,
} from "../../automation";
import { Bell, Pause, Play, RefreshCw, Shield, Trash2, Zap } from "../../icons";
import { useUI } from "../../ui-preferences";
import { useWorkspace } from "../../workspace";
import "./EventTriggersPanel.css";

export function EventTriggersPanel() {
  const { t } = useUI();
  const { core, openSettingsModal } = useWorkspace();

  const [state, setState] = useState<AutomationControlState | null>(null);
  const [triggers, setTriggers] = useState<EventTrigger[]>([]);
  const [notifications, setNotifications] = useState<NotificationItem[]>([]);
  const [unreadCount, setUnreadCount] = useState(0);
  const [unreadOnly, setUnreadOnly] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [statusMsg, setStatusMsg] = useState("");

  const [triggerId, setTriggerId] = useState("");
  const [name, setName] = useState("");
  const [kind, setKind] = useState<TriggerKind>("news");
  const [venue, setVenue] = useState("hyperliquid");
  const [symbol, setSymbol] = useState("BTC");
  const [interval, setInterval] = useState("1h");
  const [indicator, setIndicator] = useState<TriggerIndicator>("rsi_14");
  const [comparator, setComparator] = useState<TriggerComparator>("above");
  const [threshold, setThreshold] = useState("50000");
  const [category, setCategory] = useState("energy");
  const [region, setRegion] = useState("mena");
  const [ticker, setTicker] = useState("");
  const [minUrgency, setMinUrgency] = useState("0.6");
  const [minSpreadPct, setMinSpreadPct] = useState("8");
  const [cronExpr, setCronExpr] = useState("*/15 * * * *");
  const [actionType, setActionType] = useState<TriggerActionType>("notify");
  const [severity, setSeverity] = useState<NotificationSeverity>("warn");
  const [cooldown, setCooldown] = useState("300");
  const [sendTelegram, setSendTelegram] = useState(true);
  const [sendWebhook, setSendWebhook] = useState(false);

  const loadAll = useCallback(async () => {
    if (core.mode !== "ready") return;
    try {
      const [st, tr, ntfs] = await Promise.all([
        getAutomationState(core),
        listEventTriggers(core),
        listNotifications(core, { unreadOnly, limit: 40 }),
      ]);
      setState(st);
      setTriggers(tr.triggers ?? []);
      setNotifications(ntfs.notifications ?? []);
      setUnreadCount(ntfs.unreadCount ?? 0);
    } catch (err) {
      setError(
        err instanceof Error ? err.message : t("Failed to load automation"),
      );
    }
  }, [core, unreadOnly, t]);

  useEffect(() => {
    void loadAll();
  }, [loadAll]);

  if (core.mode !== "ready") return null;

  async function runStateAction(
    action: "pause" | "resume" | "kill" | "unkill",
  ) {
    if (busy) return;
    setBusy(true);
    setError("");
    setStatusMsg("");
    try {
      const next = await setAutomationState(
        core,
        action,
        action === "kill" ? "Owner emergency stop" : undefined,
      );
      setState(next);
      await loadAll();
    } catch (err) {
      setError(err instanceof Error ? err.message : t("State update failed"));
    } finally {
      setBusy(false);
    }
  }

  async function handleSaveTrigger() {
    if (busy || !name.trim()) return;
    setBusy(true);
    setError("");
    setStatusMsg("");
    const id = triggerId.trim() || slugifyTriggerId(name);
    const cd = Number(cooldown) || 300;
    try {
      await saveEventTrigger(core, {
        id,
        name: name.trim(),
        kind,
        enabled: true,
        cooldownSeconds: cd,
        condition:
          kind === "price" || kind === "indicator"
            ? {
                venue,
                symbol,
                interval,
                indicator: kind === "indicator" ? indicator : undefined,
                comparator,
                threshold: Number(threshold) || 0,
              }
            : kind === "news"
              ? {
                  category,
                  region,
                  ticker,
                  minUrgency: Number(minUrgency) || 0,
                }
              : kind === "prediction"
                ? {
                    minSpreadPct: Number(minSpreadPct) || 5,
                  }
                : {
                    cronExpr,
                  },
        action: {
          type: actionType,
          severity,
          sendTelegram,
          sendWebhook,
        },
      });
      setTriggerId("");
      setName("");
      setStatusMsg(t("Trigger saved"));
      await loadAll();
    } catch (err) {
      setError(err instanceof Error ? err.message : t("Save failed"));
    } finally {
      setBusy(false);
    }
  }

  async function handleEvaluateAll() {
    if (busy) return;
    setBusy(true);
    setError("");
    setStatusMsg("");
    try {
      const res = await evaluateAutomationNow(core);
      setStatusMsg(
        res.skipped
          ? `${t("Skipped")}: ${res.skipped}`
          : t("Evaluated {evaluated} triggers ({fired} fired)", {
              evaluated: res.evaluated,
              fired: res.fired,
            }),
      );
      await loadAll();
    } catch (err) {
      setError(err instanceof Error ? err.message : t("Evaluate failed"));
    } finally {
      setBusy(false);
    }
  }

  const stateBadge = automationStateLabel(state);

  return (
    <div className="auto-panel" data-testid="event-triggers-panel">
      <div className="auto-toolbar">
        <div className="auto-status-group">
          <span
            className={`auto-pill ${
              state?.killSwitchActive
                ? "is-critical"
                : state?.paused
                  ? "is-warn"
                  : ""
            }`}
            data-testid="automation-state-badge"
          >
            <Shield size={13} />
            {t(stateBadge)}
          </span>
          <span className="auto-pill" data-testid="automation-unread-badge">
            <Bell size={13} />
            {t("{count} unread", { count: unreadCount })}
          </span>
          {statusMsg && (
            <span className="auto-pill" role="status">
              {statusMsg}
            </span>
          )}
        </div>

        <div className="auto-actions-group">
          {!state?.killSwitchActive && (
            <button
              type="button"
              className="secondary"
              disabled={busy}
              onClick={() =>
                void runStateAction(state?.paused ? "resume" : "pause")
              }
            >
              {state?.paused ? <Play size={13} /> : <Pause size={13} />}
              {t(state?.paused ? "Resume" : "Pause")}
            </button>
          )}
          <button
            type="button"
            className={state?.killSwitchActive ? "secondary" : "primary"}
            disabled={busy}
            onClick={() =>
              void runStateAction(state?.killSwitchActive ? "unkill" : "kill")
            }
          >
            <Shield size={13} />
            {t(state?.killSwitchActive ? "Clear kill switch" : "Kill switch")}
          </button>
          <button
            type="button"
            className="primary"
            disabled={busy}
            onClick={() => void handleEvaluateAll()}
          >
            <Zap size={13} />
            {t("Evaluate now")}
          </button>
          <button
            type="button"
            className="secondary"
            onClick={() => openSettingsModal("notifications")}
          >
            {t("Telegram & webhook")}
          </button>
          <button
            type="button"
            className="secondary"
            disabled={busy}
            aria-label={t("Refresh")}
            onClick={() => void loadAll()}
          >
            <RefreshCw size={13} />
          </button>
        </div>
      </div>

      {error && <p role="alert">{error}</p>}

      <div className="auto-grid">
        <section className="auto-card" aria-label={t("New event trigger")}>
          <div className="auto-item-head">
            <strong>{t("New event trigger")}</strong>
            <span className="auto-pill">{triggers.length}</span>
          </div>
          <form
            className="auto-form"
            onSubmit={(e) => {
              e.preventDefault();
              void handleSaveTrigger();
            }}
          >
            <div className="auto-form-row">
              <div className="auto-field">
                <label htmlFor="auto-trig-name">{t("Trigger name")}</label>
                <input
                  id="auto-trig-name"
                  type="text"
                  aria-label={t("Trigger name")}
                  placeholder={t("Trigger name")}
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                />
              </div>
              <div className="auto-field">
                <label htmlFor="auto-trig-id">{t("Trigger ID")}</label>
                <input
                  id="auto-trig-id"
                  type="text"
                  aria-label={t("Trigger ID")}
                  placeholder={t("Auto slug")}
                  value={triggerId}
                  onChange={(e) => setTriggerId(e.target.value)}
                />
              </div>
            </div>

            <div className="auto-form-row">
              <div className="auto-field">
                <label htmlFor="auto-trig-kind">{t("Trigger kind")}</label>
                <select
                  id="auto-trig-kind"
                  aria-label={t("Trigger kind")}
                  value={kind}
                  onChange={(e) => setKind(e.target.value as TriggerKind)}
                >
                  <option value="news">{t("World Intel")}</option>
                  <option value="price">{t("Price")}</option>
                  <option value="indicator">{t("Indicator")}</option>
                  <option value="prediction">{t("Prediction spread")}</option>
                  <option value="cron">{t("Cron schedule")}</option>
                </select>
              </div>
              <div className="auto-field">
                <label htmlFor="auto-trig-action">{t("Action")}</label>
                <select
                  id="auto-trig-action"
                  aria-label={t("Action")}
                  value={actionType}
                  onChange={(e) =>
                    setActionType(e.target.value as TriggerActionType)
                  }
                >
                  <option value="notify">{t("Notify")}</option>
                  <option value="jev_triage">{t("Jev triage")}</option>
                  <option value="agent_prompt">{t("Agent prompt")}</option>
                  <option value="paper_alert">{t("Paper alert")}</option>
                </select>
              </div>
            </div>

            {(kind === "price" || kind === "indicator") && (
              <>
                <div className="auto-form-row">
                  <div className="auto-field">
                    <label htmlFor="auto-trig-venue">{t("Venue")}</label>
                    <select
                      id="auto-trig-venue"
                      aria-label={t("Venue")}
                      value={venue}
                      onChange={(e) => setVenue(e.target.value)}
                    >
                      <option value="hyperliquid">Hyperliquid</option>
                      <option value="binance">Binance</option>
                      <option value="bybit">Bybit</option>
                      <option value="kraken">Kraken</option>
                      <option value="solana">Solana</option>
                    </select>
                  </div>
                  <div className="auto-field">
                    <label htmlFor="auto-trig-symbol">{t("Symbol")}</label>
                    <input
                      id="auto-trig-symbol"
                      type="text"
                      aria-label={t("Symbol")}
                      value={symbol}
                      onChange={(e) => setSymbol(e.target.value)}
                    />
                  </div>
                </div>
                <div className="auto-form-row">
                  {kind === "indicator" ? (
                    <div className="auto-field">
                      <label htmlFor="auto-trig-ind">{t("Indicator")}</label>
                      <select
                        id="auto-trig-ind"
                        aria-label={t("Indicator")}
                        value={indicator}
                        onChange={(e) =>
                          setIndicator(e.target.value as TriggerIndicator)
                        }
                      >
                        <option value="rsi_14">RSI(14)</option>
                        <option value="ema_cross">EMA 9/21</option>
                        <option value="vwap_cross">VWAP Cross</option>
                        <option value="volume_spike">Volume Spike</option>
                        <option value="cvd_delta">CVD Delta</option>
                      </select>
                    </div>
                  ) : (
                    <div className="auto-field">
                      <label htmlFor="auto-trig-iv">{t("Interval")}</label>
                      <select
                        id="auto-trig-iv"
                        aria-label={t("Interval")}
                        value={interval}
                        onChange={(e) => setInterval(e.target.value)}
                      >
                        <option value="1m">1m</option>
                        <option value="5m">5m</option>
                        <option value="15m">15m</option>
                        <option value="1h">1h</option>
                        <option value="4h">4h</option>
                      </select>
                    </div>
                  )}
                  <div className="auto-field">
                    <label htmlFor="auto-trig-comp">{t("Comparator")}</label>
                    <select
                      id="auto-trig-comp"
                      aria-label={t("Comparator")}
                      value={comparator}
                      onChange={(e) =>
                        setComparator(e.target.value as TriggerComparator)
                      }
                    >
                      <option value="above">{t("Above")}</option>
                      <option value="below">{t("Below")}</option>
                      <option value="cross_above">{t("Cross above")}</option>
                      <option value="cross_below">{t("Cross below")}</option>
                      <option value="pct_change_gte">{t("% change")}</option>
                    </select>
                  </div>
                </div>
                <div className="auto-form-row">
                  <div className="auto-field">
                    <label htmlFor="auto-trig-thresh">{t("Threshold")}</label>
                    <input
                      id="auto-trig-thresh"
                      type="number"
                      step="any"
                      aria-label={t("Threshold")}
                      value={threshold}
                      onChange={(e) => setThreshold(e.target.value)}
                    />
                  </div>
                  <div className="auto-field">
                    <label htmlFor="auto-trig-cd">{t("Cooldown (s)")}</label>
                    <input
                      id="auto-trig-cd"
                      type="number"
                      min="10"
                      max="86400"
                      aria-label={t("Cooldown (s)")}
                      value={cooldown}
                      onChange={(e) => setCooldown(e.target.value)}
                    />
                  </div>
                </div>
              </>
            )}

            {kind === "news" && (
              <div className="auto-form-row">
                <div className="auto-field">
                  <label htmlFor="auto-trig-cat">{t("Category")}</label>
                  <select
                    id="auto-trig-cat"
                    aria-label={t("Category")}
                    value={category}
                    onChange={(e) => setCategory(e.target.value)}
                  >
                    <option value="">{t("All")}</option>
                    <option value="macro">{t("Macro")}</option>
                    <option value="crypto">{t("Crypto")}</option>
                    <option value="energy">{t("Energy")}</option>
                    <option value="geopolitics">{t("Geopolitics")}</option>
                    <option value="seismic">{t("Seismic")}</option>
                  </select>
                </div>
                <div className="auto-field">
                  <label htmlFor="auto-trig-urg">{t("Min urgency")}</label>
                  <input
                    id="auto-trig-urg"
                    type="number"
                    step="0.05"
                    min="0"
                    max="1"
                    aria-label={t("Min urgency")}
                    value={minUrgency}
                    onChange={(e) => setMinUrgency(e.target.value)}
                  />
                </div>
                <div className="auto-field">
                  <label htmlFor="auto-trig-reg">{t("Region")}</label>
                  <input
                    id="auto-trig-reg"
                    type="text"
                    aria-label={t("Region")}
                    value={region}
                    onChange={(e) => setRegion(e.target.value)}
                  />
                </div>
                <div className="auto-field">
                  <label htmlFor="auto-trig-ticker">{t("Ticker")}</label>
                  <input
                    id="auto-trig-ticker"
                    type="text"
                    aria-label={t("Ticker")}
                    placeholder="BTC"
                    value={ticker}
                    onChange={(e) => setTicker(e.target.value)}
                  />
                </div>
              </div>
            )}

            {kind === "prediction" && (
              <div className="auto-form-row">
                <div className="auto-field">
                  <label htmlFor="auto-trig-spread">{t("Min spread %")}</label>
                  <input
                    id="auto-trig-spread"
                    type="number"
                    step="0.5"
                    min="0"
                    max="100"
                    aria-label={t("Min spread %")}
                    value={minSpreadPct}
                    onChange={(e) => setMinSpreadPct(e.target.value)}
                  />
                </div>
                <div className="auto-field">
                  <label htmlFor="auto-trig-cd-pred">{t("Cooldown (s)")}</label>
                  <input
                    id="auto-trig-cd-pred"
                    type="number"
                    min="10"
                    max="86400"
                    value={cooldown}
                    onChange={(e) => setCooldown(e.target.value)}
                  />
                </div>
              </div>
            )}

            {kind === "cron" && (
              <div className="auto-form-row">
                <div className="auto-field">
                  <label htmlFor="auto-trig-cron">{t("Cron schedule")}</label>
                  <input
                    id="auto-trig-cron"
                    type="text"
                    aria-label={t("Cron schedule")}
                    value={cronExpr}
                    onChange={(e) => setCronExpr(e.target.value)}
                  />
                </div>
                <div className="auto-field">
                  <label htmlFor="auto-trig-cd-cron">{t("Cooldown (s)")}</label>
                  <input
                    id="auto-trig-cd-cron"
                    type="number"
                    min="10"
                    max="86400"
                    value={cooldown}
                    onChange={(e) => setCooldown(e.target.value)}
                  />
                </div>
              </div>
            )}

            <div className="auto-form-row">
              <div className="auto-field">
                <label htmlFor="auto-trig-sev">{t("Severity")}</label>
                <select
                  id="auto-trig-sev"
                  aria-label={t("Severity")}
                  value={severity}
                  onChange={(e) =>
                    setSeverity(e.target.value as NotificationSeverity)
                  }
                >
                  <option value="info">{t("Info")}</option>
                  <option value="warn">{t("Warning")}</option>
                  <option value="critical">{t("Critical")}</option>
                </select>
              </div>
              <div className="auto-checks">
                <label className="auto-checkbox">
                  <input
                    type="checkbox"
                    checked={sendTelegram}
                    onChange={(e) => setSendTelegram(e.target.checked)}
                  />
                  {t("Telegram")}
                </label>
                <label className="auto-checkbox">
                  <input
                    type="checkbox"
                    checked={sendWebhook}
                    onChange={(e) => setSendWebhook(e.target.checked)}
                  />
                  {t("Webhook")}
                </label>
              </div>
            </div>

            <button
              type="submit"
              className="primary"
              disabled={busy || !name.trim()}
            >
              {t("Save trigger")}
            </button>
          </form>

          <div className="auto-list" data-testid="triggers-list">
            {triggers.length === 0 ? (
              <div className="auto-empty">{t("No triggers configured")}</div>
            ) : (
              triggers.map((tr) => (
                <div
                  key={tr.id}
                  className="auto-item"
                  data-testid={`trigger-row-${tr.id}`}
                >
                  <div className="auto-item-head">
                    <span className="auto-item-title">{tr.name}</span>
                    <span className="auto-pill">
                      {tr.fireCount}x · {tr.kind}
                    </span>
                  </div>
                  <div className="auto-item-meta">
                    {summarizeTriggerCondition(tr)}
                  </div>
                  <div className="auto-item-actions">
                    <button
                      type="button"
                      className="secondary"
                      disabled={busy}
                      onClick={() => {
                        setBusy(true);
                        void saveEventTrigger(core, {
                          id: tr.id,
                          name: tr.name,
                          kind: tr.kind,
                          enabled: !tr.enabled,
                          cooldownSeconds: tr.cooldownSeconds,
                          condition: tr.condition,
                          action: tr.action,
                        })
                          .then(() => loadAll())
                          .finally(() => setBusy(false));
                      }}
                    >
                      {t(tr.enabled ? "Disable" : "Enable")}
                    </button>
                    <button
                      type="button"
                      className="secondary"
                      disabled={busy}
                      onClick={() => {
                        setBusy(true);
                        void testEventTrigger(core, tr.id, false)
                          .then((res) => {
                            setStatusMsg(res.summary);
                            return loadAll();
                          })
                          .catch((err) =>
                            setError(
                              err instanceof Error
                                ? err.message
                                : t("Test failed"),
                            ),
                          )
                          .finally(() => setBusy(false));
                      }}
                    >
                      {t("Test")}
                    </button>
                    <button
                      type="button"
                      className="secondary"
                      disabled={busy}
                      aria-label={`${t("Remove")}: ${tr.name}`}
                      onClick={() => {
                        setBusy(true);
                        void deleteEventTrigger(core, tr.id)
                          .then(() => loadAll())
                          .finally(() => setBusy(false));
                      }}
                    >
                      <Trash2 size={13} />
                    </button>
                  </div>
                </div>
              ))
            )}
          </div>
        </section>

        <section className="auto-card" aria-label={t("Alerts inbox")}>
          <div className="auto-item-head">
            <strong>{t("Alerts inbox")}</strong>
            <div className="auto-item-actions">
              <button
                type="button"
                className="secondary"
                aria-pressed={unreadOnly}
                onClick={() => setUnreadOnly((v) => !v)}
              >
                {t("Unread")}
              </button>
              <button
                type="button"
                className="secondary"
                disabled={busy || unreadCount === 0}
                onClick={() => {
                  setBusy(true);
                  void markNotificationsRead(core, { all: true })
                    .then(() => loadAll())
                    .finally(() => setBusy(false));
                }}
              >
                {t("Mark all read")}
              </button>
            </div>
          </div>

          <div className="auto-list" data-testid="notifications-list">
            {notifications.length === 0 ? (
              <div className="auto-empty">{t("No alerts")}</div>
            ) : (
              notifications.map((ntf) => (
                <div
                  key={ntf.id}
                  className={`auto-item ${
                    ntf.severity === "critical"
                      ? "is-critical"
                      : ntf.severity === "warn"
                        ? "is-warn"
                        : ""
                  }`}
                  data-testid={`notification-row-${ntf.id}`}
                >
                  <div className="auto-item-head">
                    <span className="auto-item-title">{ntf.title}</span>
                    <span
                      className={`auto-pill ${
                        ntf.severity === "critical"
                          ? "is-critical"
                          : ntf.severity === "warn"
                            ? "is-warn"
                            : ""
                      }`}
                    >
                      {ntf.severity}
                    </span>
                  </div>
                  <div className="auto-item-meta">{ntf.body}</div>
                  <div className="auto-item-actions">
                    {ntf.ticker && (
                      <span className="auto-pill mono">{ntf.ticker}</span>
                    )}
                    {ntf.deliveryStatus && (
                      <span className="auto-pill">{ntf.deliveryStatus}</span>
                    )}
                    {!ntf.read && (
                      <button
                        type="button"
                        className="secondary"
                        disabled={busy}
                        onClick={() => {
                          setBusy(true);
                          void markNotificationsRead(core, { id: ntf.id })
                            .then(() => loadAll())
                            .finally(() => setBusy(false));
                        }}
                      >
                        {t("Mark read")}
                      </button>
                    )}
                  </div>
                </div>
              ))
            )}
          </div>
        </section>
      </div>
    </div>
  );
}
