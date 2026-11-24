import "./settings-modal.css";
import { useEffect, useMemo, useState } from "react";
import {
  ACCENT_SWATCHES,
  PROVIDER_API_KEY_URLS,
  defaultLocalEndpoint,
  useAIConnection,
  type Connection,
  type HostCLIStatus,
} from "../ai-connection";
import { Check, Copy, ExternalLink, RefreshCw } from "../icons";
import { useUI } from "../ui-preferences";
import { useWorkspace } from "../workspace";
import { Dialog } from "./shared";

type WizardStep = 1 | 2 | 3;
type WizardMethod = "sign-in" | "cli" | "api-key" | "local";

const PRIMARY_PROVIDERS = [
  "openai",
  "anthropic",
  "google",
  "xai",
  "openrouter",
  "kimi-code",
  "qwen",
  "local",
] as const;

export function AddProviderWizard({
  onClose,
  onAdded,
  initialProvider,
  initialBinary,
  initialStep = 1,
}: {
  onClose: () => void;
  onAdded?: (ref: string) => void;
  initialProvider?: string;
  initialBinary?: string;
  initialStep?: WizardStep;
}) {
  const { t } = useUI();
  const { view: root, setView, core } = useAIConnection();
  const { setProvidersOpen, closeSettingsModal } = useWorkspace();

  const [step, setStep] = useState<WizardStep>(initialStep);
  const [showMore, setShowMore] = useState(false);
  const [provider, setProvider] = useState(initialProvider || "openai");
  const [label, setLabel] = useState("");
  const [accentColor, setAccentColor] = useState("");
  const [method, setMethod] = useState<WizardMethod>("api-key");
  const [methodTouched, setMethodTouched] = useState(false);

  const [cliBinary, setCliBinary] = useState(initialBinary || "");
  const [binaryPath, setBinaryPath] = useState("");
  const [configDir, setConfigDir] = useState("");
  const [shadowHome, setShadowHome] = useState("");
  const [launchArgs, setLaunchArgs] = useState("");
  const [key, setKey] = useState("");
  const [workspace, setWorkspace] = useState("");
  const [googleProject, setGoogleProject] = useState("");
  const [endpoint, setEndpoint] = useState(defaultLocalEndpoint);
  const [passphrase, setPassphrase] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [copiedText, setCopiedText] = useState("");

  const catalogProviders = root?.catalog?.providers ?? [];
  const primaryCatalog = useMemo(() => {
    const parents = catalogProviders.filter(
      (p) => !p.family || p.family === p.id,
    );
    const ordered: typeof parents = [];
    for (const id of PRIMARY_PROVIDERS) {
      const match = parents.find((p) => p.id === id);
      if (match) ordered.push(match);
    }
    const extra = parents.filter(
      (p) =>
        !PRIMARY_PROVIDERS.includes(p.id as (typeof PRIMARY_PROVIDERS)[number]),
    );
    return { primary: ordered, extra };
  }, [catalogProviders]);

  const activeCatalogProvider =
    catalogProviders.find((p) => p.id === provider) ?? null;
  const familyId = activeCatalogProvider?.family || provider;
  const regionalProviders = catalogProviders.filter(
    (p) => p.family && p.family === familyId,
  );
  const providerClis: HostCLIStatus[] = (root?.hostClis ?? []).filter(
    (c) => c.provider === provider || c.provider === familyId,
  );
  const cliStatus =
    providerClis.find((c) => c.binary === cliBinary) ??
    providerClis.find((c) => c.authenticated) ??
    providerClis.find((c) => c.installed) ??
    providerClis[0] ??
    null;

  const hasSignIn =
    !!activeCatalogProvider?.methods.some((m) => m.id === "sign-in") ||
    provider === "google" ||
    provider === "openrouter";
  const signInAvailable =
    provider === "google"
      ? !!root?.googleSignInAvailable
      : !!root?.signInAvailable;
  const isLocal = provider === "local";
  const needsPassphrase = !root?.osVaultAvailable;

  useEffect(() => {
    if (methodTouched) return;
    if (isLocal) {
      setMethod("local");
    } else if (providerClis.some((c) => c.installed && c.authenticated)) {
      setMethod("cli");
    } else if (hasSignIn && signInAvailable && provider === "openai") {
      setMethod("sign-in");
    } else {
      setMethod("api-key");
    }
  }, [
    provider,
    isLocal,
    hasSignIn,
    signInAvailable,
    providerClis,
    methodTouched,
  ]);

  function selectProvider(nextProvider: string) {
    setProvider(nextProvider);
    setCliBinary("");
    setShadowHome("");
    setLaunchArgs("");
    setMethodTouched(false);
    setError("");
    setKey("");
    setWorkspace("");
    setGoogleProject("");
    setEndpoint(defaultLocalEndpoint);
  }

  async function copyText(text: string) {
    try {
      await navigator.clipboard?.writeText?.(text);
      setCopiedText(text);
      window.setTimeout(() => setCopiedText(""), 1500);
    } catch {
      // Clipboard write unavailable.
    }
  }

  async function submitAction(body: Record<string, unknown>) {
    setBusy(true);
    setError("");
    try {
      const next = await core.request<Connection>(
        "ai/connection",
        "POST",
        body,
      );
      const refreshed = next.connections
        ? next
        : await core.request<Connection>("ai/connection");
      setView(refreshed);
      window.dispatchEvent(new Event("ai-connection-changed"));
      return { next, refreshed };
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save connection");
      return null;
    } finally {
      setBusy(false);
    }
  }

  async function handleStartSignIn() {
    const res = await submitAction({
      action: "sign-in-start",
      provider,
      remote: core.connection?.location === "remote",
      ...(provider === "google" && googleProject
        ? { googleProject: googleProject.trim() }
        : {}),
      ...(label.trim() ? { name: label.trim() } : {}),
      ...(accentColor ? { accentColor } : {}),
    });
    if (res) {
      onClose();
      closeSettingsModal();
      setProvidersOpen(true);
    }
  }

  async function handleAddInstance() {
    const cleanName =
      label.trim() || cliStatus?.label || activeCatalogProvider?.name || "";
    if (method === "cli") {
      const res = await submitAction({
        action: "cli-connect",
        provider,
        ...(cliStatus?.binary ? { binary: cliStatus.binary } : {}),
        ...(cleanName ? { name: cleanName } : {}),
        ...(binaryPath.trim() ? { binaryPath: binaryPath.trim() } : {}),
        ...(configDir.trim() ? { configDir: configDir.trim() } : {}),
        ...(cliStatus?.binary === "codex" && shadowHome.trim()
          ? { shadowHome: shadowHome.trim() }
          : {}),
        ...(launchArgs.trim() ? { launchArgs: launchArgs.trim() } : {}),
        ...(accentColor ? { accentColor } : {}),
        ...(needsPassphrase && passphrase ? { passphrase } : {}),
      });
      if (res) {
        const addedRef = res.next.ref || res.refreshed.ref || "";
        onAdded?.(addedRef);
        onClose();
      }
      return;
    }

    if (method === "sign-in") {
      await handleStartSignIn();
      return;
    }

    const res = await submitAction({
      action: "add",
      provider,
      ...(cleanName ? { name: cleanName } : {}),
      ...(isLocal ? { endpoint: endpoint.trim() } : {}),
      ...(key ? { key } : {}),
      ...(provider === "anthropic" && workspace.trim()
        ? { workspace: workspace.trim() }
        : {}),
      ...(accentColor ? { accentColor } : {}),
      ...(needsPassphrase && passphrase ? { passphrase } : {}),
    });
    if (res) {
      const addedRef = res.next.ref || res.refreshed.ref || "";
      onAdded?.(addedRef);
      onClose();
    }
  }

  async function handleStartCLIAuth() {
    const res = await submitAction({
      action: "cli-auth-start",
      provider,
      ...(cliStatus?.binary ? { binary: cliStatus.binary } : {}),
    });
    if (res) {
      onClose();
      closeSettingsModal();
      setProvidersOpen(true);
    }
  }

  const canSubmit = (() => {
    if (busy || !root) return false;
    if (method === "cli") {
      return (
        !!cliStatus?.installed &&
        !!cliStatus?.authenticated &&
        (!needsPassphrase || passphrase.length >= 12)
      );
    }
    if (method === "sign-in") {
      return (
        signInAvailable &&
        (provider !== "google" ||
          /^[a-z][a-z0-9-]{4,28}[a-z0-9]$/.test(googleProject.trim()))
      );
    }
    if (method === "local") {
      return !!endpoint.trim() && (!needsPassphrase || passphrase.length >= 12);
    }
    return !!key && (!needsPassphrase || passphrase.length >= 12);
  })();

  const displayedProviders = showMore
    ? [...primaryCatalog.primary, ...primaryCatalog.extra]
    : primaryCatalog.primary;

  const stepMeta: { id: WizardStep; label: string }[] = [
    { id: 1, label: "Provider" },
    { id: 2, label: "Identity" },
    { id: 3, label: "Config" },
  ];

  return (
    <Dialog
      title={t("Add provider")}
      wide
      className="provider-wizard"
      onClose={onClose}
    >
      <div
        className="wizard-steps"
        aria-label={t("Step {step} of {total}", { step, total: 3 })}
      >
        {stepMeta.map((s) => {
          const state =
            step === s.id ? "active" : step > s.id ? "done" : "todo";
          return (
            <div key={s.id} className="wizard-step" data-state={state}>
              <span className="wizard-step-badge">
                {state === "done" ? <Check size={12} /> : s.id}
              </span>
              <span>{t(s.label)}</span>
            </div>
          );
        })}
      </div>

      <div className="wizard-body">
        {error && <p role="alert">{t(error)}</p>}

        {step === 1 && (
          <>
            <div
              className="wizard-provider-grid"
              role="group"
              aria-label={t("Provider")}
            >
              {displayedProviders.map((p) => {
                const clis = (root?.hostClis ?? []).filter(
                  (c) => c.provider === p.id,
                );
                const readyCli = clis.find(
                  (c) => c.installed && c.authenticated,
                );
                const installedCli = clis.find((c) => c.installed);
                const cli = readyCli ?? installedCli;
                return (
                  <button
                    key={p.id}
                    type="button"
                    className="wizard-provider-card"
                    aria-pressed={familyId === p.id}
                    onClick={() => selectProvider(p.id)}
                  >
                    <span className="wizard-provider-card-main">
                      {cli && (
                        <span
                          className="cli-status-dot"
                          data-state={readyCli ? "ready" : "installed"}
                          aria-hidden="true"
                        />
                      )}
                      <span>{t(p.name)}</span>
                    </span>
                    {cli?.version ? (
                      <span className="provider-model-pill cli-version-pill">
                        {`v${cli.version}`}
                      </span>
                    ) : familyId === p.id ? (
                      <Check size={14} aria-hidden="true" />
                    ) : null}
                  </button>
                );
              })}
            </div>
            {primaryCatalog.extra.length > 0 && (
              <div>
                <button
                  type="button"
                  className="text-button"
                  onClick={() => setShowMore((v) => !v)}
                >
                  {t(showMore ? "Show less" : "Show more")}
                </button>
              </div>
            )}
          </>
        )}

        {step === 2 && (
          <>
            <label>
              {t("Label")}
              <input
                value={label}
                maxLength={80}
                autoComplete="off"
                placeholder={
                  cliStatus?.label || activeCatalogProvider?.name || provider
                }
                onChange={(e) => setLabel(e.target.value)}
              />
            </label>

            {!isLocal && (
              <div className="agent-field">
                <span className="agent-field-label">{t("Method")}</span>
                <div
                  className="segmented-control"
                  role="group"
                  aria-label={t("Method")}
                >
                  {hasSignIn && (provider !== "google" || signInAvailable) && (
                    <button
                      type="button"
                      aria-pressed={method === "sign-in"}
                      disabled={!signInAvailable}
                      onClick={() => {
                        setMethodTouched(true);
                        setMethod("sign-in");
                      }}
                    >
                      {t("Sign in")}
                    </button>
                  )}
                  <button
                    type="button"
                    aria-pressed={method === "api-key"}
                    onClick={() => {
                      setMethodTouched(true);
                      setMethod("api-key");
                    }}
                  >
                    {t("API key")}
                  </button>
                  {cliStatus && (
                    <button
                      type="button"
                      aria-pressed={method === "cli"}
                      onClick={() => {
                        setMethodTouched(true);
                        setMethod("cli");
                      }}
                    >
                      {t("CLI")}
                    </button>
                  )}
                </div>
              </div>
            )}

            <div className="agent-field">
              <span className="agent-field-label">{t("Accent color")}</span>
              <div
                className="wizard-swatches"
                role="group"
                aria-label={t("Accent color")}
              >
                <button
                  type="button"
                  className="wizard-swatch-none"
                  aria-pressed={accentColor === ""}
                  onClick={() => setAccentColor("")}
                >
                  {t("None")}
                </button>
                {ACCENT_SWATCHES.map((s) => (
                  <button
                    key={s.hex}
                    type="button"
                    className="wizard-swatch-btn"
                    aria-label={s.hex}
                    aria-pressed={accentColor === s.hex}
                    style={{ backgroundColor: `var(${s.cssVar})` }}
                    onClick={() => setAccentColor(s.hex)}
                  >
                    {accentColor === s.hex && <Check size={13} />}
                  </button>
                ))}
              </div>
            </div>
          </>
        )}

        {step === 3 && (
          <>
            {method === "cli" && (
              <>
                {providerClis.length > 1 && (
                  <div
                    className="segmented-control"
                    role="group"
                    aria-label={t("Runtime")}
                  >
                    {providerClis.map((c) => (
                      <button
                        key={c.binary}
                        type="button"
                        aria-pressed={cliStatus?.binary === c.binary}
                        disabled={busy}
                        onClick={() => setCliBinary(c.binary)}
                      >
                        {c.label}
                      </button>
                    ))}
                  </div>
                )}
                <div className="cli-status-card">
                  <div className="cli-status-head">
                    <strong>
                      {cliStatus?.label || activeCatalogProvider?.name}
                    </strong>
                    {cliStatus?.version && (
                      <span className="provider-model-pill cli-version-pill">
                        {`v${cliStatus.version}`}
                      </span>
                    )}
                    <span
                      className="provider-model-pill"
                      data-state={cliStatus?.authenticated ? "ready" : "stale"}
                    >
                      {t(
                        cliStatus?.authenticated
                          ? "Connected"
                          : cliStatus?.installed
                            ? "Sign in required"
                            : "Not installed",
                      )}
                    </span>
                  </div>
                  {cliStatus?.account && (
                    <p className="cli-status-account">{cliStatus.account}</p>
                  )}
                </div>
                {cliStatus &&
                  (!cliStatus.installed || !cliStatus.authenticated) && (
                    <div className="cli-hint-row">
                      <code>
                        {cliStatus.installed
                          ? cliStatus.loginCommand
                          : cliStatus.installHint}
                      </code>
                      <button
                        type="button"
                        className="icon-button"
                        aria-label={t(
                          copiedText ===
                            (cliStatus.installed
                              ? cliStatus.loginCommand
                              : cliStatus.installHint)
                            ? "Copied"
                            : "Copy",
                        )}
                        onClick={() =>
                          void copyText(
                            cliStatus.installed
                              ? cliStatus.loginCommand
                              : cliStatus.installHint,
                          )
                        }
                      >
                        <Copy size={14} />
                      </button>
                    </div>
                  )}
                {cliStatus?.installed && !cliStatus.authenticated && (
                  <div className="agent-actions">
                    {(cliStatus.supportsDevice || provider === "anthropic") && (
                      <button
                        type="button"
                        className="primary"
                        disabled={busy}
                        onClick={() => void handleStartCLIAuth()}
                      >
                        {t("Sign in via CLI")}
                      </button>
                    )}
                    <button
                      type="button"
                      className="secondary"
                      disabled={busy}
                      onClick={() => void submitAction({ action: "cli-probe" })}
                    >
                      <RefreshCw size={14} className={busy ? "spin" : ""} />
                      <span>{t("Check again")}</span>
                    </button>
                  </div>
                )}
                {cliStatus?.installed && cliStatus.authenticated && (
                  <>
                    <label>
                      {t("Binary path")}
                      <input
                        value={binaryPath}
                        maxLength={260}
                        autoComplete="off"
                        spellCheck={false}
                        placeholder={cliStatus.binaryPath || cliStatus.binary}
                        onChange={(e) => setBinaryPath(e.target.value)}
                      />
                    </label>
                    <label>
                      {t("Config directory")}
                      <input
                        value={configDir}
                        maxLength={260}
                        autoComplete="off"
                        spellCheck={false}
                        onChange={(e) => setConfigDir(e.target.value)}
                      />
                    </label>
                    {cliStatus.binary === "codex" && (
                      <label>
                        {t("Shadow home path")}
                        <input
                          value={shadowHome}
                          maxLength={260}
                          autoComplete="off"
                          spellCheck={false}
                          placeholder="~/.codex-fased/personal"
                          onChange={(e) => setShadowHome(e.target.value)}
                        />
                      </label>
                    )}
                    <label>
                      {t("Launch arguments")}
                      <input
                        value={launchArgs}
                        maxLength={512}
                        autoComplete="off"
                        spellCheck={false}
                        onChange={(e) => setLaunchArgs(e.target.value)}
                      />
                    </label>
                  </>
                )}
              </>
            )}

            {method === "api-key" && (
              <>
                {regionalProviders.length > 1 && (
                  <div
                    className="segmented-control"
                    role="group"
                    aria-label={t("Region")}
                  >
                    {regionalProviders.map((rp) => (
                      <button
                        key={rp.id}
                        type="button"
                        aria-pressed={provider === rp.id}
                        onClick={() => setProvider(rp.id)}
                      >
                        {t(rp.route || rp.name)}
                      </button>
                    ))}
                  </div>
                )}
                <label>
                  {t("API key")}
                  <input
                    type="password"
                    autoComplete="off"
                    value={key}
                    maxLength={256}
                    onChange={(e) => setKey(e.target.value)}
                  />
                </label>
                {PROVIDER_API_KEY_URLS[provider] && (
                  <div>
                    <a
                      className="secondary"
                      href={PROVIDER_API_KEY_URLS[provider]}
                      target="_blank"
                      rel="noopener noreferrer"
                    >
                      <ExternalLink size={14} />
                      <span>{t("Get API key")}</span>
                    </a>
                  </div>
                )}
                {provider === "anthropic" && (
                  <label>
                    {t("Workspace (optional)")}
                    <input
                      value={workspace}
                      maxLength={71}
                      autoComplete="off"
                      spellCheck={false}
                      placeholder="wrkspc_…"
                      onChange={(e) => setWorkspace(e.target.value)}
                    />
                  </label>
                )}
              </>
            )}

            {method === "sign-in" && provider === "google" && (
              <label>
                {t("Project")}
                <input
                  value={googleProject}
                  maxLength={30}
                  autoComplete="off"
                  onChange={(e) => setGoogleProject(e.target.value)}
                />
              </label>
            )}

            {method === "local" && (
              <>
                {activeCatalogProvider?.servers && (
                  <div
                    className="segmented-control"
                    role="group"
                    aria-label={t("Server")}
                  >
                    {activeCatalogProvider.servers.map((srv) => (
                      <button
                        key={srv.id}
                        type="button"
                        aria-pressed={endpoint === srv.endpoint}
                        onClick={() => setEndpoint(srv.endpoint)}
                      >
                        {srv.name}
                      </button>
                    ))}
                  </div>
                )}
                <label>
                  {t("Endpoint")}
                  <input
                    type="url"
                    value={endpoint}
                    maxLength={256}
                    spellCheck={false}
                    onChange={(e) => setEndpoint(e.target.value)}
                  />
                </label>
                <label>
                  {t("API key (optional)")}
                  <input
                    type="password"
                    autoComplete="off"
                    value={key}
                    maxLength={256}
                    onChange={(e) => setKey(e.target.value)}
                  />
                </label>
              </>
            )}

            {needsPassphrase &&
              method !== "sign-in" &&
              (method !== "cli" || !!cliStatus?.authenticated) && (
                <label>
                  {t("Vault passphrase")}
                  <input
                    type="password"
                    autoComplete="off"
                    value={passphrase}
                    maxLength={1024}
                    onChange={(e) => setPassphrase(e.target.value)}
                  />
                </label>
              )}
          </>
        )}
      </div>

      <div className="wizard-footer">
        {step === 1 && (
          <>
            <button type="button" className="text-button" onClick={onClose}>
              {t("Cancel")}
            </button>
            <button
              type="button"
              className="secondary"
              disabled={busy}
              onClick={() => setStep(2)}
            >
              {t("Configure manually")}
            </button>
            {provider === "openai" && signInAvailable ? (
              <button
                type="button"
                className="primary"
                disabled={busy}
                onClick={() => void handleStartSignIn()}
              >
                {t("Continue with ChatGPT")}
              </button>
            ) : (
              <button
                type="button"
                className="primary"
                disabled={busy}
                onClick={() => setStep(2)}
              >
                {t("Next")}
              </button>
            )}
          </>
        )}
        {step === 2 && (
          <>
            <button
              type="button"
              className="secondary"
              disabled={busy}
              onClick={() => setStep(1)}
            >
              {t("Back")}
            </button>
            <button
              type="button"
              className="primary"
              disabled={busy}
              onClick={() => setStep(3)}
            >
              {t("Next")}
            </button>
          </>
        )}
        {step === 3 && (
          <>
            <button
              type="button"
              className="secondary"
              disabled={busy}
              onClick={() => setStep(2)}
            >
              {t("Back")}
            </button>
            <button
              type="button"
              className="primary"
              disabled={!canSubmit}
              onClick={() => void handleAddInstance()}
            >
              {t(
                method === "sign-in"
                  ? provider === "google"
                    ? "Continue with Google"
                    : provider === "openrouter"
                      ? "Continue"
                      : "Continue with ChatGPT"
                  : "Add instance",
              )}
            </button>
          </>
        )}
      </div>
    </Dialog>
  );
}
