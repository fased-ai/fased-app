import { useEffect, useState } from "react";
import {
  formatChatIdsInput,
  getTelegramConfig,
  getWebhookConfig,
  parseChatIdsInput,
  saveTelegramConfig,
  saveWebhookConfig,
  sendTelegramTest,
  sendWebhookTest,
  simulateTelegramCommand,
  type TelegramCommandReply,
  type TelegramConfigView,
  type WebhookConfigView,
} from "../automation";
import {
  dispatchOSNotification,
  getOSNotificationPermission,
  getOSNotificationsEnabled,
  isOSNotificationSupported,
  requestOSNotificationPermission,
  setOSNotificationsEnabled,
  type OSNotificationPermission,
} from "../os-notifications";
import { useUI } from "../ui-preferences";
import { useWorkspace } from "../workspace";

export function NotificationsPanel() {
  const { t } = useUI();
  const { core } = useWorkspace();

  const [tg, setTg] = useState<TelegramConfigView | null>(null);
  const [tgEnabled, setTgEnabled] = useState(false);
  const [tgToken, setTgToken] = useState("");
  const [chatIdsText, setChatIdsText] = useState("");
  const [minUrgency, setMinUrgency] = useState("0.7");
  const [notifyApproval, setNotifyApproval] = useState(true);
  const [notifyKill, setNotifyKill] = useState(true);
  const [tgBusy, setTgBusy] = useState(false);
  const [tgError, setTgError] = useState("");

  const [simChatId, setSimChatId] = useState("");
  const [simCmd, setSimCmd] = useState("/status");
  const [simReply, setSimReply] = useState<TelegramCommandReply | null>(null);

  const [wh, setWh] = useState<WebhookConfigView | null>(null);
  const [whEnabled, setWhEnabled] = useState(false);
  const [whEndpoint, setWhEndpoint] = useState("");
  const [whSecret, setWhSecret] = useState("");
  const [whBusy, setWhBusy] = useState(false);
  const [whError, setWhError] = useState("");
  const [osNotifyEnabled, setOsNotifyEnabled] = useState<boolean>(() =>
    getOSNotificationsEnabled(),
  );
  const [osPerm, setOsPerm] = useState<OSNotificationPermission>(() =>
    getOSNotificationPermission(),
  );
  const [osNote, setOsNote] = useState("");

  useEffect(() => {
    if (core.mode !== "ready") return;
    let cancelled = false;
    void Promise.all([getTelegramConfig(core), getWebhookConfig(core)]).then(
      ([tgRes, whRes]) => {
        if (cancelled) return;
        setTg(tgRes);
        setTgEnabled(tgRes.enabled);
        setChatIdsText(formatChatIdsInput(tgRes.allowedChatIds));
        setMinUrgency(String(tgRes.minUrgency ?? 0.7));
        setNotifyApproval(tgRes.notifyOnApproval);
        setNotifyKill(tgRes.notifyOnKillSwitch);
        if (tgRes.allowedChatIds?.[0]) {
          setSimChatId(String(tgRes.allowedChatIds[0]));
        }
        setWh(whRes);
        setWhEnabled(whRes.enabled);
        setWhEndpoint(whRes.endpoint ?? "");
      },
      (err) => {
        if (cancelled) return;
        setTgError(err instanceof Error ? err.message : t("Status unknown"));
      },
    );
    return () => {
      cancelled = true;
    };
  }, [core, t]);

  if (core.mode !== "ready") return null;

  async function submitTelegram(clearToken = false) {
    if (tgBusy) return;
    setTgBusy(true);
    setTgError("");
    try {
      const chatIds = parseChatIdsInput(chatIdsText);
      const updated = await saveTelegramConfig(core, {
        enabled: tgEnabled,
        botToken: clearToken ? undefined : tgToken.trim() || undefined,
        clearToken,
        allowedChatIds: chatIds,
        minUrgency: Number(minUrgency) || 0,
        notifyOnApproval: notifyApproval,
        notifyOnKillSwitch: notifyKill,
      });
      setTg(updated);
      setTgToken("");
      if (!simChatId && updated.allowedChatIds?.[0]) {
        setSimChatId(String(updated.allowedChatIds[0]));
      }
    } catch (err) {
      setTgError(err instanceof Error ? err.message : t("Save failed"));
    } finally {
      setTgBusy(false);
    }
  }

  async function handleSendTest() {
    if (tgBusy) return;
    setTgBusy(true);
    setTgError("");
    try {
      const updated = await sendTelegramTest(core);
      setTg(updated);
    } catch (err) {
      setTgError(err instanceof Error ? err.message : t("Test failed"));
    } finally {
      setTgBusy(false);
    }
  }

  async function handleSimulateCommand() {
    if (tgBusy) return;
    const chatID = Number(simChatId.trim());
    if (!Number.isSafeInteger(chatID) || chatID === 0) {
      setTgError(t("Enter a valid numeric chat ID"));
      return;
    }
    setTgBusy(true);
    setTgError("");
    try {
      const res = await simulateTelegramCommand(core, chatID, simCmd);
      setSimReply(res);
      const refreshed = await getTelegramConfig(core);
      setTg(refreshed);
    } catch (err) {
      setTgError(err instanceof Error ? err.message : t("Command failed"));
    } finally {
      setTgBusy(false);
    }
  }

  async function submitWebhook(clearSecret = false) {
    if (whBusy) return;
    setWhBusy(true);
    setWhError("");
    try {
      const updated = await saveWebhookConfig(core, {
        enabled: whEnabled,
        endpoint: whEndpoint.trim(),
        secret: clearSecret ? undefined : whSecret.trim() || undefined,
        clearSecret,
      });
      setWh(updated);
      setWhSecret("");
    } catch (err) {
      setWhError(err instanceof Error ? err.message : t("Save failed"));
    } finally {
      setWhBusy(false);
    }
  }

  async function handleWebhookTest() {
    if (whBusy) return;
    setWhBusy(true);
    setWhError("");
    try {
      const updated = await sendWebhookTest(core);
      setWh(updated);
    } catch (err) {
      setWhError(err instanceof Error ? err.message : t("Test failed"));
    } finally {
      setWhBusy(false);
    }
  }

  async function handleToggleOSNotifications(checked: boolean) {
    setOsNote("");
    if (!isOSNotificationSupported()) {
      setOsPerm("unsupported");
      setOsNotifyEnabled(false);
      setOSNotificationsEnabled(false);
      return;
    }
    if (!checked) {
      setOsNotifyEnabled(false);
      setOSNotificationsEnabled(false);
      return;
    }
    const perm =
      osPerm === "granted"
        ? "granted"
        : await requestOSNotificationPermission();
    setOsPerm(perm);
    const enabled = perm === "granted";
    setOsNotifyEnabled(enabled);
    setOSNotificationsEnabled(enabled);
    if (!enabled) {
      setOsNote(t("Denied"));
    }
  }

  function handleTestOSNotification() {
    setOsNote("");
    if (osPerm !== "granted") {
      setOsNote(t("Denied"));
      return;
    }
    dispatchOSNotification(
      `test-${Date.now()}`,
      "Fased",
      t("Browser notifications"),
      true,
    );
  }

  return (
    <>
      <div className="settings-modal-card" data-testid="telegram-card">
        <div className="settings-modal-row">
          <strong>{t("Telegram bot")}</strong>
          <span className="pill">
            {tg?.enabled ? t("Enabled") : t("Disabled")}
          </span>
        </div>

        <dl className="settings-detail-kv">
          <div>
            <dt>{t("Bot token")}</dt>
            <dd>{tg?.hasBotToken ? t("Saved") : t("Not set")}</dd>
          </div>
          <div>
            <dt>{t("Allowed chat IDs")}</dt>
            <dd className="mono">
              {tg?.allowedChatIds?.length
                ? formatChatIdsInput(tg.allowedChatIds)
                : t("None")}
            </dd>
          </div>
          <div>
            <dt>{t("Min urgency")}</dt>
            <dd className="mono">
              {`${Math.round((tg?.minUrgency ?? 0.7) * 100)}%`}
            </dd>
          </div>
        </dl>

        <form
          className="settings-mcp-form"
          aria-label={t("Telegram bot")}
          onSubmit={(e) => {
            e.preventDefault();
            void submitTelegram(false);
          }}
        >
          <label className="settings-checkbox-label">
            <input
              type="checkbox"
              checked={tgEnabled}
              disabled={tgBusy}
              onChange={(e) => setTgEnabled(e.target.checked)}
            />
            {t("Enable Telegram bot")}
          </label>
          <div className="settings-inline-form">
            <input
              type="password"
              autoComplete="off"
              aria-label={t("Bot token")}
              placeholder={t("Bot token")}
              value={tgToken}
              disabled={tgBusy}
              onChange={(e) => setTgToken(e.target.value)}
            />
            <input
              type="text"
              aria-label={t("Allowed chat IDs")}
              placeholder="777001"
              value={chatIdsText}
              disabled={tgBusy}
              onChange={(e) => setChatIdsText(e.target.value)}
            />
            <input
              type="number"
              step="0.05"
              min="0"
              max="1"
              aria-label={t("Min urgency")}
              value={minUrgency}
              disabled={tgBusy}
              onChange={(e) => setMinUrgency(e.target.value)}
            />
          </div>
          <div className="settings-mcp-form-actions">
            <label className="settings-checkbox-label">
              <input
                type="checkbox"
                checked={notifyApproval}
                disabled={tgBusy}
                onChange={(e) => setNotifyApproval(e.target.checked)}
              />
              {t("Approval alerts")}
            </label>
            <label className="settings-checkbox-label">
              <input
                type="checkbox"
                checked={notifyKill}
                disabled={tgBusy}
                onChange={(e) => setNotifyKill(e.target.checked)}
              />
              {t("Kill switch alerts")}
            </label>
            <button type="submit" className="primary" disabled={tgBusy}>
              {t("Save")}
            </button>
            {tg?.hasBotToken && (
              <button
                type="button"
                className="secondary"
                disabled={tgBusy}
                onClick={() => void submitTelegram(true)}
              >
                {t("Clear token")}
              </button>
            )}
            <button
              type="button"
              className="secondary"
              disabled={tgBusy}
              onClick={() => void handleSendTest()}
            >
              {t("Send test")}
            </button>
          </div>
        </form>

        <form
          className="settings-inline-form"
          aria-label={t("Command simulator")}
          onSubmit={(e) => {
            e.preventDefault();
            void handleSimulateCommand();
          }}
        >
          <input
            type="text"
            aria-label={t("Simulator chat ID")}
            placeholder="777001"
            value={simChatId}
            disabled={tgBusy}
            onChange={(e) => setSimChatId(e.target.value)}
          />
          <input
            type="text"
            aria-label={t("Telegram command")}
            placeholder="/status"
            value={simCmd}
            disabled={tgBusy}
            onChange={(e) => setSimCmd(e.target.value)}
          />
          <button type="submit" className="secondary" disabled={tgBusy}>
            {t("Run command")}
          </button>
        </form>

        {simReply && (
          <div
            className="settings-mcp-server-row"
            data-testid="telegram-sim-result"
          >
            <div className="settings-mcp-server-badges">
              <span className="pill">
                {simReply.authorized ? t("Authorized") : t("Rejected")}
              </span>
              {simReply.command && (
                <code className="mono">{`/${simReply.command}`}</code>
              )}
            </div>
            <div className="settings-mcp-tools">{simReply.text}</div>
          </div>
        )}

        {tg?.recentDeliveries?.length ? (
          <div
            className="settings-mcp-server-list"
            data-testid="telegram-deliveries"
          >
            {tg.recentDeliveries.slice(0, 4).map((d) => (
              <div key={d.id} className="settings-mcp-server-row">
                <div className="settings-mcp-server-meta">
                  <strong>{d.title}</strong>
                  <code className="mono">{String(d.chatId)}</code>
                  <span className="pill">{d.status}</span>
                </div>
              </div>
            ))}
          </div>
        ) : null}

        {tgError && <p role="alert">{tgError}</p>}
      </div>

      <div className="settings-modal-card" data-testid="webhook-card">
        <div className="settings-modal-row">
          <strong>{t("Webhook delivery")}</strong>
          <span className="pill">
            {wh?.enabled ? t("Enabled") : t("Disabled")}
          </span>
        </div>

        <form
          className="settings-mcp-form"
          aria-label={t("Webhook delivery")}
          onSubmit={(e) => {
            e.preventDefault();
            void submitWebhook(false);
          }}
        >
          <label className="settings-checkbox-label">
            <input
              type="checkbox"
              checked={whEnabled}
              disabled={whBusy}
              onChange={(e) => setWhEnabled(e.target.checked)}
            />
            {t("Enable webhook")}
          </label>
          <div className="settings-inline-form">
            <input
              type="url"
              aria-label={t("Webhook URL")}
              placeholder="https://hooks.example.com/fased"
              value={whEndpoint}
              disabled={whBusy}
              onChange={(e) => setWhEndpoint(e.target.value)}
            />
            <input
              type="password"
              autoComplete="off"
              aria-label={t("Signing secret")}
              placeholder={t("Signing secret")}
              value={whSecret}
              disabled={whBusy}
              onChange={(e) => setWhSecret(e.target.value)}
            />
          </div>
          <div className="settings-mcp-form-actions">
            <button type="submit" className="primary" disabled={whBusy}>
              {t("Save")}
            </button>
            <button
              type="button"
              className="secondary"
              disabled={whBusy || !wh?.endpoint}
              onClick={() => void handleWebhookTest()}
            >
              {t("Test webhook")}
            </button>
            {wh?.hasSecret && (
              <button
                type="button"
                className="secondary"
                disabled={whBusy}
                onClick={() => void submitWebhook(true)}
              >
                {t("Clear secret")}
              </button>
            )}
            {wh?.lastStatus && (
              <span className="pill mono">{wh.lastStatus}</span>
            )}
          </div>
        </form>

        {whError && <p role="alert">{whError}</p>}
      </div>

      <div className="settings-modal-card" data-testid="browser-push-card">
        <div className="settings-modal-row">
          <strong>{t("Browser notifications")}</strong>
          <span className="pill">
            {osPerm === "granted"
              ? t("Granted")
              : osPerm === "denied"
                ? t("Denied")
                : osPerm === "unsupported"
                  ? t("Unsupported")
                  : t("Disabled")}
          </span>
        </div>
        <div className="settings-mcp-form-actions">
          <label className="settings-checkbox-label">
            <input
              type="checkbox"
              checked={osNotifyEnabled}
              disabled={!isOSNotificationSupported()}
              onChange={(e) =>
                void handleToggleOSNotifications(e.target.checked)
              }
            />
            {t("Browser notifications")}
          </label>
          <button
            type="button"
            className="secondary"
            disabled={!isOSNotificationSupported() || osPerm !== "granted"}
            onClick={handleTestOSNotification}
          >
            {t("Test notification")}
          </button>
          {osNote && <span className="pill">{osNote}</span>}
        </div>
      </div>
    </>
  );
}
