import "./settings-modal.css";
import { useEffect, useState } from "react";
import {
  useAIConnection,
  toggleConnectionEnabled,
  type Connection,
} from "../ai-connection";
import {
  clearModelManifestV2,
  getMCPDiscovery,
  getModelManifestV2,
  saveModelManifestV2,
  type MCPDiscovery,
  type ModelManifestStatus,
} from "../agents-v2";
import {
  ArrowRight,
  Check,
  Copy,
  ExternalLink,
  LockKeyhole,
  Monitor,
  Plug,
  Plus,
  RefreshCw,
  Search,
  Server,
  SlidersHorizontal,
} from "../icons";
import { AppearanceSettings, useUI } from "../ui-preferences";
import { resolveMcpDisplayOrigin } from "../desktop-runtime";
import { useWorkspace, type SettingsTab } from "../workspace";
import { ProviderModelsPanel } from "../pages/ProviderModelsPanel";
import { AddProviderWizard } from "./AddProviderWizard";
import { ApplicationUpdatesPanel } from "./ApplicationUpdatesPanel";
import { InstanceSummary, RemoteConnection } from "./InstanceConnection";
import { IntegrationsPanel } from "./IntegrationsPanel";
import { RemoteDevices } from "./RemoteDevices";
import { UpdateStatusPanel } from "./UpdateStatusPanel";
import { Dialog, ToggleSwitch } from "./shared";

const SETTINGS_TABS: {
  id: SettingsTab;
  label: string;
  icon: typeof SlidersHorizontal;
  readyOnly?: boolean;
}[] = [
  { id: "general", label: "General", icon: SlidersHorizontal },
  { id: "providers", label: "Providers", icon: Plug, readyOnly: true },
  { id: "instance", label: "Instance", icon: Server },
  { id: "updates", label: "Updates", icon: RefreshCw, readyOnly: true },
  {
    id: "diagnostics",
    label: "Diagnostics",
    icon: Monitor,
    readyOnly: true,
  },
];

export function SettingsModal() {
  const { t, preferences, update } = useUI();
  const {
    core,
    state,
    settingsModalTab,
    closeSettingsModal,
    setSetupOpen,
    setResetOpen,
    navigate,
  } = useWorkspace();
  const {
    view: root,
    setView,
    error: connError,
    setError: setConnError,
    refresh,
  } = useAIConnection();

  const [activeTab, setActiveTab] = useState<SettingsTab>(
    settingsModalTab ?? "general",
  );
  const [navSearch, setNavSearch] = useState("");
  const [selectedRef, setSelectedRef] = useState("");
  const [busy, setBusy] = useState(false);
  const [wizardSeed, setWizardSeed] = useState<{
    provider?: string;
    binary?: string;
    step?: 1 | 2 | 3;
  } | null>(null);
  const [wizardOpen, setWizardOpen] = useState(false);
  const [mcp, setMcp] = useState<MCPDiscovery | null>(null);
  const [mcpCopied, setMcpCopied] = useState(false);
  const [manifest, setManifest] = useState<ModelManifestStatus | null>(null);
  const [manifestUrl, setManifestUrl] = useState("");
  const [manifestBusy, setManifestBusy] = useState(false);
  const [manifestError, setManifestError] = useState("");

  useEffect(() => {
    if (settingsModalTab) {
      setActiveTab(
        core.mode !== "ready" &&
          (settingsModalTab === "providers" ||
            settingsModalTab === "updates" ||
            settingsModalTab === "diagnostics")
          ? "general"
          : settingsModalTab,
      );
    }
  }, [settingsModalTab, core.mode]);

  useEffect(() => {
    if (
      settingsModalTab &&
      activeTab === "providers" &&
      core.mode === "ready"
    ) {
      void refresh();
    }
  }, [settingsModalTab, activeTab, core.mode, refresh]);

  useEffect(() => {
    if (
      !settingsModalTab ||
      activeTab !== "diagnostics" ||
      core.mode !== "ready"
    ) {
      return;
    }
    let cancelled = false;
    void Promise.allSettled([
      getMCPDiscovery(core),
      getModelManifestV2(core),
    ]).then(([mcpRes, manifestRes]) => {
      if (cancelled) return;
      if (mcpRes.status === "fulfilled") setMcp(mcpRes.value);
      if (manifestRes.status === "fulfilled") setManifest(manifestRes.value);
    });
    return () => {
      cancelled = true;
    };
  }, [settingsModalTab, activeTab, core]);

  if (!settingsModalTab) return null;

  const availableTabs = SETTINGS_TABS.filter(
    (tab) =>
      (!tab.readyOnly || core.mode === "ready") &&
      (!navSearch.trim() ||
        t(tab.label).toLowerCase().includes(navSearch.trim().toLowerCase())),
  );
  const currentTabMeta =
    SETTINGS_TABS.find((tab) => tab.id === activeTab) ?? SETTINGS_TABS[0];

  const accounts = root?.connections ?? [];
  const activeAccount =
    accounts.find((a) => a.ref === selectedRef) ?? accounts[0] ?? null;

  function providerDisplayName(providerId?: string) {
    if (!providerId) return "";
    const cat = root?.catalog?.providers ?? [];
    const match = cat.find((p) => p.id === providerId);
    const parent = match?.family
      ? cat.find((p) => p.id === match.family)
      : match;
    return t(parent?.name || match?.name || providerId);
  }

  async function onToggleEnabled(ref: string, disabled: boolean) {
    setBusy(true);
    try {
      await toggleConnectionEnabled(
        core,
        root,
        setView,
        setConnError,
        ref,
        disabled,
      );
    } finally {
      setBusy(false);
    }
  }

  async function refreshAccountModels(ref: string) {
    setBusy(true);
    setConnError("");
    try {
      const next = await core.request<Connection>("ai/connection", "POST", {
        action: "models-refresh",
        ref,
      });
      setView(
        next.connections
          ? next
          : await core.request<Connection>("ai/connection"),
      );
      window.dispatchEvent(new Event("ai-connection-changed"));
    } catch (e) {
      setConnError(e instanceof Error ? e.message : "Could not load models");
    } finally {
      setBusy(false);
    }
  }

  async function probeCLIs() {
    setBusy(true);
    setConnError("");
    try {
      const next = await core.request<Connection>("ai/connection", "POST", {
        action: "cli-probe",
      });
      setView(
        next.connections
          ? next
          : await core.request<Connection>("ai/connection"),
      );
    } catch {
      await refresh();
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <Dialog
        wide
        className="settings-modal"
        title={`${t("Settings")} / ${t(currentTabMeta.label)}`}
        onClose={closeSettingsModal}
      >
        <div className="settings-modal-layout">
          <aside className="settings-modal-nav" aria-label={t("Settings")}>
            <label className="settings-modal-search">
              <Search size={14} aria-hidden="true" />
              <input
                aria-label={t("Search settings")}
                placeholder={t("Search")}
                value={navSearch}
                onChange={(e) => setNavSearch(e.target.value)}
              />
            </label>
            <nav className="settings-modal-tabs">
              {availableTabs.map((tab) => (
                <button
                  key={tab.id}
                  type="button"
                  aria-current={activeTab === tab.id ? "page" : undefined}
                  onClick={() => setActiveTab(tab.id)}
                >
                  <tab.icon size={15} aria-hidden="true" />
                  <span>{t(tab.label)}</span>
                </button>
              ))}
            </nav>
          </aside>

          <div className="settings-modal-pane">
            {activeTab === "general" && (
              <>
                <div className="settings-modal-card">
                  <AppearanceSettings />
                </div>
                <div className="settings-modal-card">
                  <div className="settings-modal-row">
                    <strong>{t("Navigation rail")}</strong>
                    <ToggleSwitch
                      checked={preferences.rail}
                      label={t("Navigation rail")}
                      hideLabel
                      onChange={(rail) => update({ rail })}
                    />
                  </div>
                </div>
              </>
            )}

            {activeTab === "providers" && core.mode === "ready" && (
              <>
                {connError && <p role="alert">{t(connError)}</p>}
                <div className="settings-providers-grid">
                  <div className="settings-providers-list">
                    <div className="settings-providers-head">
                      <strong>{t("Configured")}</strong>
                      <div className="agent-actions">
                        <button
                          type="button"
                          className="icon-button"
                          aria-label={t("Refresh")}
                          title={t("Refresh")}
                          disabled={busy}
                          onClick={() => void probeCLIs()}
                        >
                          <RefreshCw size={14} className={busy ? "spin" : ""} />
                        </button>
                        <button
                          type="button"
                          className="primary"
                          onClick={() => {
                            setWizardSeed(null);
                            setWizardOpen(true);
                          }}
                        >
                          <Plus size={14} aria-hidden="true" />
                          <span>{t("Add provider")}</span>
                        </button>
                      </div>
                    </div>

                    {accounts.map((a) => {
                      const labelText =
                        a.name || providerDisplayName(a.provider);
                      const methodText = t(
                        a.authMethod === "oauth" || a.authMethod === "sign-in"
                          ? "Sign in"
                          : a.authMethod === "cli"
                            ? "CLI"
                            : a.authMethod === "local"
                              ? "Server"
                              : "API key",
                      );
                      return (
                        <div
                          key={a.ref}
                          className="settings-provider-row"
                          data-active={activeAccount?.ref === a.ref}
                          data-disabled={a.disabled ? "true" : undefined}
                        >
                          <button
                            type="button"
                            className="settings-provider-select"
                            onClick={() => setSelectedRef(a.ref)}
                          >
                            <span
                              className="provider-accent-dot"
                              style={
                                a.accentColor
                                  ? { backgroundColor: a.accentColor }
                                  : undefined
                              }
                              aria-hidden="true"
                            />
                            <span className="settings-provider-meta">
                              <strong>{labelText}</strong>
                              <small>
                                {methodText}
                                {a.cliVersion ? ` · v${a.cliVersion}` : ""}
                                {a.disabled ? ` · ${t("Disabled")}` : ""}
                              </small>
                            </span>
                            {a.locked && <LockKeyhole size={13} />}
                          </button>
                          <ToggleSwitch
                            checked={!a.disabled}
                            disabled={busy}
                            hideLabel
                            label={`${t("Enabled")} ${labelText}`}
                            onChange={(checked) =>
                              void onToggleEnabled(a.ref, !checked)
                            }
                          />
                        </div>
                      );
                    })}

                    {(root?.hostClis ?? []).some((c) => c.installed) && (
                      <div className="settings-runtimes-group">
                        <span className="settings-runtimes-label">
                          {t("Available runtimes")}
                        </span>
                        {(root?.hostClis ?? [])
                          .filter((c) => c.installed)
                          .map((c) => (
                            <button
                              key={`${c.provider}:${c.binary}`}
                              type="button"
                              className="settings-runtime-chip"
                              onClick={() => {
                                setWizardSeed({
                                  provider: c.provider,
                                  binary: c.binary,
                                  step: 2,
                                });
                                setWizardOpen(true);
                              }}
                            >
                              <span>
                                <span
                                  className="cli-status-dot"
                                  data-state={
                                    c.authenticated ? "ready" : "installed"
                                  }
                                  aria-hidden="true"
                                />
                                <span>{c.label}</span>
                              </span>
                              <span className="provider-model-pill cli-version-pill">
                                {c.version
                                  ? `v${c.version}`
                                  : c.authenticated
                                    ? t("Connected")
                                    : t("Sign in required")}
                              </span>
                            </button>
                          ))}
                      </div>
                    )}
                  </div>

                  <div className="settings-providers-detail">
                    {activeAccount ? (
                      <>
                        <div className="settings-modal-row">
                          <strong>
                            {activeAccount.name ||
                              providerDisplayName(activeAccount.provider)}
                          </strong>
                          <span
                            className="provider-model-pill"
                            data-state={
                              activeAccount.disabled || activeAccount.locked
                                ? "stale"
                                : "ready"
                            }
                          >
                            {t(
                              activeAccount.disabled
                                ? "Disabled"
                                : activeAccount.locked
                                  ? "Locked"
                                  : "Unlocked",
                            )}
                          </span>
                        </div>
                        <dl className="settings-detail-kv">
                          <div>
                            <dt>{t("Provider")}</dt>
                            <dd>
                              {providerDisplayName(activeAccount.provider)}
                            </dd>
                          </div>
                          <div>
                            <dt>{t("Method")}</dt>
                            <dd>
                              {t(
                                activeAccount.authMethod === "cli"
                                  ? "CLI"
                                  : activeAccount.authMethod === "sign-in" ||
                                      activeAccount.authMethod === "oauth"
                                    ? "Sign in"
                                    : activeAccount.authMethod === "local"
                                      ? "Server"
                                      : "API key",
                              )}
                            </dd>
                          </div>
                          {activeAccount.binaryPath && (
                            <div>
                              <dt>{t("Binary path")}</dt>
                              <dd>{activeAccount.binaryPath}</dd>
                            </div>
                          )}
                          {activeAccount.configDir && (
                            <div>
                              <dt>{t("Config directory")}</dt>
                              <dd>{activeAccount.configDir}</dd>
                            </div>
                          )}
                          {activeAccount.shadowHome && (
                            <div>
                              <dt>{t("Shadow home path")}</dt>
                              <dd>{activeAccount.shadowHome}</dd>
                            </div>
                          )}
                          {activeAccount.launchArgs && (
                            <div>
                              <dt>{t("Launch arguments")}</dt>
                              <dd>{activeAccount.launchArgs}</dd>
                            </div>
                          )}
                        </dl>
                        <div className="agent-actions">
                          <button
                            type="button"
                            className="secondary"
                            onClick={() => {
                              closeSettingsModal();
                              navigate("providers");
                            }}
                          >
                            <ExternalLink size={14} aria-hidden="true" />
                            <span>{t("Open full page")}</span>
                          </button>
                        </div>
                        <ProviderModelsPanel
                          view={activeAccount}
                          provider={activeAccount.provider || "openai"}
                          busy={busy}
                          onRefresh={() =>
                            void refreshAccountModels(activeAccount.ref)
                          }
                        />
                      </>
                    ) : (
                      <div className="settings-modal-card">
                        <strong>{t("No connection saved")}</strong>
                      </div>
                    )}
                  </div>
                </div>
              </>
            )}

            {activeTab === "instance" && (
              <>
                <div className="settings-modal-card">
                  <InstanceSummary />
                  <RemoteConnection />
                  <div className="agent-actions">
                    <button
                      type="button"
                      className="secondary"
                      onClick={() => {
                        closeSettingsModal();
                        setSetupOpen(true);
                      }}
                    >
                      <span>
                        {core.mode === "ready" ? t("Setup") : t("Review setup")}
                      </span>
                      <ArrowRight size={14} />
                    </button>
                  </div>
                </div>
                <RemoteDevices />
                {core.mode !== "ready" && (
                  <div className="settings-modal-card">
                    <strong>{t("Browser demo data")}</strong>
                    <div className="agent-actions">
                      <button
                        type="button"
                        className="secondary danger"
                        onClick={() => {
                          closeSettingsModal();
                          setResetOpen(true);
                        }}
                      >
                        {t("Reset prototype")}
                      </button>
                    </div>
                  </div>
                )}
              </>
            )}

            {activeTab === "updates" && core.mode === "ready" && (
              <ApplicationUpdatesPanel />
            )}

            {activeTab === "diagnostics" && core.mode === "ready" && (
              <>
                <div className="settings-modal-card">
                  <dl className="settings-detail-kv">
                    <div>
                      <dt>{t("Saved deployment preference")}</dt>
                      <dd>
                        {state.setup.host === "vps" ? t("Remote") : t("Local")}
                      </dd>
                    </div>
                  </dl>
                </div>

                <div className="settings-modal-card">
                  <div className="settings-modal-row">
                    <strong>{t("MCP endpoint")}</strong>
                    <span className="pill">{t("Read & draft")}</span>
                  </div>
                  <div className="settings-endpoint-row">
                    <code className="mono">
                      {`${resolveMcpDisplayOrigin(window.location.href)}${mcp?.endpoint ?? "/api/v1/mcp"}`}
                    </code>
                    <button
                      type="button"
                      className="secondary"
                      onClick={() => {
                        const target = `${resolveMcpDisplayOrigin(window.location.href)}${mcp?.endpoint ?? "/api/v1/mcp"}`;
                        void navigator.clipboard?.writeText(target);
                        setMcpCopied(true);
                        setTimeout(() => setMcpCopied(false), 1200);
                      }}
                    >
                      {mcpCopied ? <Check size={13} /> : <Copy size={13} />}
                      <span>{mcpCopied ? t("Copied") : t("Copy")}</span>
                    </button>
                  </div>
                  {mcp && (
                    <dl className="settings-detail-kv">
                      <div>
                        <dt>{t("Tools")}</dt>
                        <dd>{mcp.toolCount}</dd>
                      </div>
                      <div>
                        <dt>{t("Resources")}</dt>
                        <dd>{mcp.resourceCount}</dd>
                      </div>
                    </dl>
                  )}
                </div>

                <div className="settings-modal-card">
                  <div className="settings-modal-row">
                    <strong>{t("Model manifest")}</strong>
                    <span className="pill">
                      {manifest?.loaded
                        ? t("{count} models", { count: manifest.modelCount })
                        : t("Built-in")}
                    </span>
                  </div>
                  {manifest?.loaded && (
                    <dl className="settings-detail-kv">
                      {manifest.source && (
                        <div>
                          <dt>{t("Source")}</dt>
                          <dd>{manifest.source}</dd>
                        </div>
                      )}
                      {manifest.sha256 && (
                        <div>
                          <dt>{t("SHA-256")}</dt>
                          <dd className="mono">
                            {manifest.sha256.slice(0, 12)}
                          </dd>
                        </div>
                      )}
                    </dl>
                  )}
                  <form
                    className="settings-inline-form"
                    onSubmit={async (e) => {
                      e.preventDefault();
                      const rawUrl = manifestUrl.trim();
                      if (!rawUrl || manifestBusy) return;
                      setManifestBusy(true);
                      setManifestError("");
                      try {
                        const updated = await saveModelManifestV2(core, {
                          url: rawUrl,
                        });
                        setManifest(updated);
                        setManifestUrl("");
                        window.dispatchEvent(
                          new Event("ai-connection-changed"),
                        );
                      } catch (err) {
                        setManifestError(
                          err instanceof Error
                            ? err.message
                            : "Could not import model manifest",
                        );
                      } finally {
                        setManifestBusy(false);
                      }
                    }}
                  >
                    <input
                      type="url"
                      aria-label={t("Manifest URL")}
                      placeholder={t("Manifest URL")}
                      value={manifestUrl}
                      disabled={manifestBusy}
                      onChange={(e) => setManifestUrl(e.target.value)}
                    />
                    <button
                      type="submit"
                      className="primary"
                      disabled={manifestBusy || !manifestUrl.trim()}
                    >
                      {t("Import")}
                    </button>
                    {manifest?.loaded && (
                      <button
                        type="button"
                        className="secondary"
                        disabled={manifestBusy}
                        onClick={async () => {
                          setManifestBusy(true);
                          setManifestError("");
                          try {
                            const updated = await clearModelManifestV2(core);
                            setManifest(updated);
                            window.dispatchEvent(
                              new Event("ai-connection-changed"),
                            );
                          } catch (err) {
                            setManifestError(
                              err instanceof Error
                                ? err.message
                                : "Could not clear model manifest",
                            );
                          } finally {
                            setManifestBusy(false);
                          }
                        }}
                      >
                        {t("Clear")}
                      </button>
                    )}
                  </form>
                  {manifestError && <p role="alert">{manifestError}</p>}
                </div>

                <IntegrationsPanel />

                <UpdateStatusPanel />
              </>
            )}
          </div>
        </div>
      </Dialog>

      {wizardOpen && (
        <AddProviderWizard
          initialProvider={wizardSeed?.provider}
          initialBinary={wizardSeed?.binary}
          initialStep={wizardSeed?.step}
          onAdded={(ref) => {
            if (ref) setSelectedRef(ref);
          }}
          onClose={() => setWizardOpen(false)}
        />
      )}
    </>
  );
}
