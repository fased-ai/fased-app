import { useCallback, useEffect, useMemo, useState } from "react";
import { useCore } from "../core";
import { useUI } from "../ui-preferences";
import { Plus, RefreshCw, Trash2 } from "../icons";
import {
  addCustomDataPlugin,
  deleteCustomDataPlugin,
  fetchDataPlugins,
  testDataPlugin,
  updateDataPlugin,
  type DataPluginView,
  type DataPluginsSummary,
  type PluginCategory,
} from "../data-plugins";
import { ToggleSwitch } from "./shared";

const CATEGORIES: { id: PluginCategory; label: string }[] = [
  { id: "exchanges", label: "Exchanges" },
  { id: "news", label: "News" },
  { id: "predictions", label: "Predictions" },
  { id: "rpcs", label: "Chain RPCs" },
];

export function DataPluginsPanel() {
  const { t } = useUI();
  const core = useCore();

  const [category, setCategory] = useState<PluginCategory>("exchanges");
  const [plugins, setPlugins] = useState<DataPluginView[]>([]);
  const [summary, setSummary] = useState<DataPluginsSummary>({
    activeTotal: 0,
    activeExchanges: 0,
    activeNews: 0,
    activePredictions: 0,
    activeRpcs: 0,
  });
  const [selectedId, setSelectedId] = useState<string>("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  // Editable fields for the selected plugin
  const [editEndpoint, setEditEndpoint] = useState("");
  const [editApiKey, setEditApiKey] = useState("");
  const [editApiSecret, setEditApiSecret] = useState("");
  const [addingCustom, setAddingCustom] = useState(false);
  const [customName, setCustomName] = useState("");
  const [customEndpoint, setCustomEndpoint] = useState("");

  const load = useCallback(async () => {
    if (core.mode !== "ready") return;
    setError("");
    try {
      const res = await fetchDataPlugins(core);
      setPlugins(res.plugins ?? []);
      if (res.summary) setSummary(res.summary);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load plugins");
    }
  }, [core.mode, core.request]);

  useEffect(() => {
    void load();
  }, [load]);

  const categoryPlugins = useMemo(
    () => plugins.filter((p) => p.category === category),
    [plugins, category],
  );

  const activePlugin =
    categoryPlugins.find((p) => p.id === selectedId) ??
    categoryPlugins[0] ??
    null;

  useEffect(() => {
    setEditEndpoint(activePlugin?.endpoint ?? "");
    setEditApiKey("");
    setEditApiSecret("");
  }, [activePlugin?.id, activePlugin?.endpoint]);

  async function onToggle(p: DataPluginView, nextEnabled: boolean) {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const updated = await updateDataPlugin(core, p.id, {
        enabled: nextEnabled,
        dataFeedEnabled: nextEnabled && p.supportsFeed,
      });
      setPlugins((cur) => cur.map((x) => (x.id === p.id ? updated : x)));
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Update failed");
    } finally {
      setBusy(false);
    }
  }

  async function onSaveDetail(e: React.FormEvent) {
    e.preventDefault();
    if (!activePlugin) return;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const patch: Record<string, unknown> = {
        endpoint: editEndpoint.trim(),
      };
      if (editApiKey.trim()) patch.apiKey = editApiKey.trim();
      if (editApiSecret.trim()) patch.apiSecret = editApiSecret.trim();
      const updated = await updateDataPlugin(core, activePlugin.id, patch);
      setPlugins((cur) =>
        cur.map((x) => (x.id === activePlugin.id ? updated : x)),
      );
      setEditApiKey("");
      setEditApiSecret("");
      setNotice(t("Saved"));
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Save failed");
    } finally {
      setBusy(false);
    }
  }

  async function onTest(id: string) {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const updated = await testDataPlugin(core, id);
      setPlugins((cur) => cur.map((x) => (x.id === id ? updated : x)));
      setNotice(
        updated.lastStatus === "ready"
          ? `${t("Ready")} (${updated.lastLatencyMs ?? 0}ms)`
          : updated.lastError || t("Error"),
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : "Probe failed");
    } finally {
      setBusy(false);
    }
  }

  async function onAddCustom(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const created = await addCustomDataPlugin(core, {
        name: customName.trim(),
        category: category === "predictions" ? "news" : category,
        endpoint: customEndpoint.trim(),
        enabled: true,
      });
      setCustomName("");
      setCustomEndpoint("");
      setAddingCustom(false);
      setSelectedId(created.id);
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Add custom failed");
    } finally {
      setBusy(false);
    }
  }

  async function onDeleteCustom(id: string) {
    setBusy(true);
    setError("");
    try {
      await deleteCustomDataPlugin(core, id);
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Delete failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="data-plugins-panel">
      <div className="settings-modal-row">
        <div className="appearance-segmented" role="tablist">
          {CATEGORIES.map((c) => {
            const count =
              c.id === "exchanges"
                ? summary.activeExchanges
                : c.id === "news"
                  ? summary.activeNews
                  : c.id === "predictions"
                    ? summary.activePredictions
                    : summary.activeRpcs;
            return (
              <button
                key={c.id}
                type="button"
                role="tab"
                aria-selected={category === c.id}
                className={category === c.id ? "active" : undefined}
                onClick={() => {
                  setCategory(c.id);
                  setSelectedId("");
                  setAddingCustom(false);
                }}
              >
                {t(c.label)} {count > 0 ? `(${count})` : ""}
              </button>
            );
          })}
        </div>
        <div className="agent-actions">
          <button
            type="button"
            className="icon-button"
            aria-label={t("Refresh")}
            onClick={() => void load()}
            disabled={busy}
          >
            <RefreshCw size={14} className={busy ? "spin" : ""} />
          </button>
          <button
            type="button"
            className="secondary"
            onClick={() => setAddingCustom((v) => !v)}
          >
            <Plus size={14} aria-hidden="true" />
            <span>{t("Add source")}</span>
          </button>
        </div>
      </div>

      {error && <p role="alert">{error}</p>}
      {notice && (
        <p className="subtle" role="status">
          {notice}
        </p>
      )}

      {addingCustom && (
        <form
          className="settings-modal-card"
          onSubmit={(e) => void onAddCustom(e)}
        >
          <div className="settings-modal-row">
            <strong>{t("Add custom source")}</strong>
          </div>
          <div className="agent-form-grid">
            <label>
              {t("Name")}
              <input
                value={customName}
                onChange={(e) => setCustomName(e.target.value)}
                required
                placeholder="Helius Mainnet / Custom RSS"
              />
            </label>
            <label>
              {t("Endpoint URL")}
              <input
                value={customEndpoint}
                onChange={(e) => setCustomEndpoint(e.target.value)}
                required
                placeholder="https://..."
              />
            </label>
          </div>
          <div className="agent-actions">
            <button type="submit" className="primary" disabled={busy}>
              {t("Add")}
            </button>
            <button
              type="button"
              className="secondary"
              onClick={() => setAddingCustom(false)}
            >
              {t("Cancel")}
            </button>
          </div>
        </form>
      )}

      <div className="settings-providers-grid">
        <div className="settings-providers-list">
          {categoryPlugins.map((p) => (
            <div
              key={p.id}
              className="settings-provider-row"
              data-active={activePlugin?.id === p.id}
              data-disabled={!p.enabled ? "true" : undefined}
            >
              <button
                type="button"
                className="settings-provider-select"
                onClick={() => setSelectedId(p.id)}
              >
                <span
                  className="cli-status-dot"
                  data-state={
                    !p.enabled
                      ? "disabled"
                      : p.lastStatus === "error"
                        ? "error"
                        : "ready"
                  }
                  aria-hidden="true"
                />
                <span className="settings-provider-meta">
                  <strong>{p.name}</strong>
                  <small>
                    {p.enabled ? t("Active") : t("Disabled")}
                    {p.tradingEnabled ? ` · ${t("Trade")}` : ""}
                    {p.hasApiKey ? ` · ${t("Key")}` : ""}
                  </small>
                </span>
              </button>
              <ToggleSwitch
                checked={p.enabled}
                disabled={busy}
                hideLabel
                label={`${t("Enable")} ${p.name}`}
                onChange={(checked) => void onToggle(p, checked)}
              />
            </div>
          ))}
        </div>

        <div className="settings-providers-detail">
          {activePlugin ? (
            <form
              onSubmit={(e) => void onSaveDetail(e)}
              className="data-plugin-detail-form"
            >
              <div className="settings-modal-row">
                <strong>{activePlugin.name}</strong>
                <span
                  className="provider-model-pill"
                  data-state={activePlugin.enabled ? "ready" : "stale"}
                >
                  {activePlugin.enabled ? t("Active") : t("Disabled")}
                </span>
              </div>

              {activePlugin.supportsFeed && (
                <div className="settings-modal-row">
                  <span>{t("Data Feed")}</span>
                  <ToggleSwitch
                    checked={activePlugin.dataFeedEnabled}
                    disabled={busy}
                    hideLabel
                    label={`${t("Data Feed")} ${activePlugin.name}`}
                    onChange={(v) =>
                      void updateDataPlugin(core, activePlugin.id, {
                        dataFeedEnabled: v,
                      }).then(() => load())
                    }
                  />
                </div>
              )}

              {activePlugin.supportsTrading && (
                <div className="settings-modal-row">
                  <span>{t("Trading API")}</span>
                  <ToggleSwitch
                    checked={activePlugin.tradingEnabled}
                    disabled={busy}
                    hideLabel
                    label={`${t("Trading API")} ${activePlugin.name}`}
                    onChange={(v) =>
                      void updateDataPlugin(core, activePlugin.id, {
                        tradingEnabled: v,
                      }).then(() => load())
                    }
                  />
                </div>
              )}

              {activePlugin.category === "exchanges" && (
                <div className="settings-modal-row">
                  <span>{t("Default Chart Source")}</span>
                  <ToggleSwitch
                    checked={Boolean(activePlugin.defaultChart)}
                    disabled={busy}
                    hideLabel
                    label={`${t("Default Chart Source")} ${activePlugin.name}`}
                    onChange={(v) =>
                      void updateDataPlugin(core, activePlugin.id, {
                        defaultChart: v,
                      }).then(() => load())
                    }
                  />
                </div>
              )}

              <label>
                {t("Endpoint")}
                <input
                  value={editEndpoint}
                  onChange={(e) => setEditEndpoint(e.target.value)}
                  placeholder="https://..."
                />
              </label>

              <label>
                {t("API Key")}
                <input
                  type="password"
                  autoComplete="off"
                  value={editApiKey}
                  onChange={(e) => setEditApiKey(e.target.value)}
                  placeholder={
                    activePlugin.hasApiKey
                      ? activePlugin.maskedApiKey || "Configured"
                      : t("Optional")
                  }
                />
              </label>

              {activePlugin.supportsTrading &&
                activePlugin.category === "exchanges" && (
                  <label>
                    {t("API Secret")}
                    <input
                      type="password"
                      autoComplete="off"
                      value={editApiSecret}
                      onChange={(e) => setEditApiSecret(e.target.value)}
                      placeholder={
                        activePlugin.hasApiSecret ? "Configured" : t("Optional")
                      }
                    />
                  </label>
                )}

              <div className="agent-actions">
                <button type="submit" className="primary" disabled={busy}>
                  {t("Save")}
                </button>
                <button
                  type="button"
                  className="secondary"
                  disabled={busy || !activePlugin.endpoint}
                  onClick={() => void onTest(activePlugin.id)}
                >
                  {t("Test")}
                </button>
                {activePlugin.custom && (
                  <button
                    type="button"
                    className="secondary"
                    disabled={busy}
                    onClick={() => void onDeleteCustom(activePlugin.id)}
                  >
                    <Trash2 size={14} aria-hidden="true" />
                    <span>{t("Remove")}</span>
                  </button>
                )}
              </div>
            </form>
          ) : (
            <p className="subtle">{t("Select a plugin")}</p>
          )}
        </div>
      </div>
    </div>
  );
}
