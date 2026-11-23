import "./Providers.css";
import type { Provider } from "../state";
import {
  RefreshCw,
  Plus,
  ArrowLeft,
  Search,
  LockKeyhole,
  Pencil,
  ClipboardPaste,
  ExternalLink,
  Copy,
  TerminalSquare,
} from "../icons";
import { useUI } from "../ui-preferences";
import { planFailureDetails } from "../agents";
import { useEffect, useRef, useState } from "react";
import { useWorkspace } from "../workspace";
import { Dialog, ToggleSwitch } from "../components/shared";
import { AddProviderWizard } from "../components/AddProviderWizard";
import { TaskAccessSettings } from "./TaskAccess";
import { OpenAIPilotSettings } from "./OpenAIPilot";
import {
  defaultLocalEndpoint,
  PROVIDER_API_KEY_URLS,
  toggleConnectionEnabled,
  useAIConnection,
  type Connection,
  type CredentialMode,
  type HostCLIStatus,
  type CLIAuthView,
} from "../ai-connection";
import {
  ConnectionPicker,
  AIModelPicker,
  AIAdmissionReview,
} from "./AIConnectionPickers";
import { ProviderModelsPanel } from "./ProviderModelsPanel";
import { isCompleteSignInCallback } from "../provider-signin-ui";

export {
  useAIConnection,
  type Connection,
  ConnectionPicker,
  AIModelPicker,
  AIAdmissionReview,
};

export function AIConnectionSettings({
  startNew = false,
}: { startNew?: boolean } = {}) {
  const { t: uiText } = useUI();
  const {
    view: root,
    setView,
    error,
    setError,
    refresh,
    core,
  } = useAIConnection();
  const { setProvidersOpen, providersPreset, clearProvidersPreset, agents } =
    useWorkspace();
  const assignedAccount =
    agents.view?.profiles.find((p) => p.id === agents.view?.profileId)
      ?.connectionRef || "";
  const [search, setSearch] = useState("");
  const pageHeading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    pageHeading.current?.focus({ preventScroll: true });
    window.scrollTo(0, 0);
  }, []);
  useEffect(() => {
    if (core.mode === "ready") void refresh();
  }, [core.mode, core.instanceId, refresh]);

  const [selected, setSelected] = useState(startNew ? "new" : assignedAccount);
  const selectionChosen = useRef(false);
  const methodChosen = useRef(false);
  useEffect(() => {
    if (!selectionChosen.current && assignedAccount)
      setSelected(assignedAccount);
  }, [assignedAccount]);
  const [newProvider, setNewProvider] = useState("openai");
  const [newMethod, setNewMethod] = useState("api-key");
  const [cliName, setCliName] = useState("");
  const [cliBinary, setCliBinary] = useState("");
  const [cliBinaryPath, setCliBinaryPath] = useState("");
  const [cliConfigDir, setCliConfigDir] = useState("");
  const [copiedText, setCopiedText] = useState("");
  const copiedTimer = useRef<number | null>(null);
  const [callback, setCallback] = useState("");
  const [editOpen, setEditOpen] = useState(false);
  const [wizardOpen, setWizardOpen] = useState(false);

  useEffect(() => {
    return () => {
      if (copiedTimer.current !== null) {
        window.clearTimeout(copiedTimer.current);
      }
    };
  }, []);

  async function copyText(text: string) {
    try {
      await navigator.clipboard?.writeText?.(text);
      setCopiedText(text);
      if (copiedTimer.current !== null) {
        window.clearTimeout(copiedTimer.current);
      }
      copiedTimer.current = window.setTimeout(() => {
        setCopiedText("");
      }, 1500);
    } catch {
      // Clipboard write unavailable; user can select code directly.
    }
  }

  useEffect(() => {
    if (!providersPreset) return;
    const nextProvider = providersPreset.provider || "openai";
    selectionChosen.current = true;
    methodChosen.current = !!providersPreset.method;
    setSelected("new");
    setNewProvider(nextProvider);
    setGoogleProject("");
    setNewMethod(
      providersPreset.method ||
        (nextProvider === "local"
          ? "local"
          : nextProvider === "openai" && root?.signInAvailable
            ? "sign-in"
            : "api-key"),
    );
    setServerPreset(defaultLocalEndpoint);
    setEndpoint(defaultLocalEndpoint);
    setKey("");
    setPassphrase("");
    setCliName("");
    setCliBinary("");
    setCliBinaryPath("");
    setCliConfigDir("");
    setCallback("");
    clearProvidersPreset();
  }, [providersPreset, clearProvidersPreset, root?.signInAvailable]);
  useEffect(() => {
    if (
      root?.signInAvailable &&
      !root?.signIn &&
      !methodChosen.current &&
      !providersPreset &&
      (selected === "new" || (!root.ref && !root.connections?.length)) &&
      newProvider === "openai" &&
      newMethod !== "sign-in"
    ) {
      setNewMethod("sign-in");
    }
  }, [
    root?.signInAvailable,
    root?.signIn,
    root?.ref,
    root?.connections?.length,
    providersPreset,
    selected,
    newProvider,
    newMethod,
  ]);
  useEffect(() => {
    if (!root?.signIn) return;
    selectionChosen.current = true;
    if (root.signIn.ref) {
      setSelected(root.signIn.ref);
    } else {
      setSelected("new");
      setNewProvider(root.signIn.provider);
      setNewMethod("sign-in");
    }
  }, [root?.signIn?.id, root?.signIn?.provider, root?.signIn?.ref]);
  const [googleProject, setGoogleProject] = useState("");
  useEffect(() => {
    if (selected && selected !== "new")
      setGoogleProject(
        root?.connections?.find((a) => a.ref === selected)?.googleProject || "",
      );
  }, [selected, root?.connections]);

  useEffect(() => {
    setEditOpen(false);
  }, [selected, core.instanceId, root?.signIn?.id]);

  const view =
    selected === "new" || (!root?.ref && !root?.connections?.length)
      ? root
        ? {
            ...root,
            provider: newProvider,
            authMethod: newMethod as
              "api-key" | "sign-in" | "local" | "oauth" | "cli",
            ref: "",
            locked: true,
            planPaused: false,
            planFailure: "",
            taskAccess: undefined,
          }
        : null
      : selected
        ? (root?.connections?.find((a) => a.ref === selected) ?? null)
        : root?.ref
          ? root
          : (root?.connections?.[0] ?? root);
  const [key, setKey] = useState("");
  const [endpoint, setEndpoint] = useState(defaultLocalEndpoint);
  const [serverPreset, setServerPreset] = useState(defaultLocalEndpoint);
  const [workspace, setWorkspace] = useState("");
  useEffect(() => {
    setWorkspace(view?.workspace || "");
  }, [selected, core.instanceId, view?.workspace]);
  const [passphrase, setPassphrase] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    setKey("");
    setPassphrase("");
    setCallback("");
  }, [core.instanceId]);
  const provider =
    view?.provider || (selected && selected !== "new" ? "" : newProvider);
  const isLocal = provider === "local";
  useEffect(() => {
    setEndpoint(
      isLocal && view?.ref
        ? view.locked
          ? ""
          : view.endpoint || ""
        : isLocal
          ? serverPreset
          : defaultLocalEndpoint,
    );
  }, [
    core.instanceId,
    selected,
    isLocal,
    view?.ref,
    view?.locked,
    view?.endpoint,
    serverPreset,
  ]);
  const catalogProvider = root?.catalog?.providers.find(
    (p) => p.id === provider,
  );
  const providerFamily = catalogProvider?.family || provider;
  const regionalProviders =
    root?.catalog?.providers.filter(
      (p) => p.family && p.family === catalogProvider?.family,
    ) || [];
  const providerName =
    root?.catalog?.providers.find((p) => p.id === catalogProvider?.family)
      ?.name ||
    catalogProvider?.name ||
    (isLocal ? uiText("Server") : uiText("Providers"));
  const method = isLocal
    ? "local"
    : view?.ref
      ? view.authMethod === "oauth"
        ? "sign-in"
        : view.authMethod || "api-key"
      : newMethod;
  const signIn = root?.signIn;
  const providerClis: HostCLIStatus[] =
    root?.hostClis?.filter((c) => c.provider === provider) || [];
  const cliStatus: HostCLIStatus | undefined = view?.ref
    ? providerClis.find((c) => !view.binary || c.binary === view.binary) ||
      providerClis[0]
    : (cliBinary
        ? providerClis.find((c) => c.binary === cliBinary)
        : undefined) ||
      providerClis.find((c) => c.installed && c.authenticated) ||
      providerClis.find((c) => c.installed) ||
      providerClis[0];
  const cliAuth: CLIAuthView | null = root?.cliAuth ?? null;
  const cliAuthLabel = cliAuth
    ? root?.hostClis?.find(
        (c) => c.provider === cliAuth.provider && c.binary === cliAuth.binary,
      )?.label ||
      root?.hostClis?.find((c) => c.provider === cliAuth.provider)?.label ||
      cliAuth.binary
    : "";
  const signInAvailable =
    provider === "google"
      ? root?.googleSignInAvailable === true
      : root?.signInAvailable === true;
  const pausedFailure = view?.planPaused
    ? planFailureDetails(view.planFailure || "")
    : null;
  const hasSignIn = root?.catalog?.providers
    .find((p) => p.id === provider)
    ?.methods.some((m) => m.id === "sign-in");
  const servers =
    root?.catalog?.providers.find((p) => p.id === "local")?.servers ?? [];
  const server = servers.find((s) => s.endpoint === endpoint);
  const apiMode =
    view?.credentialModes?.includes(provider as CredentialMode) ?? false;
  const needsPassphrase = view?.ref
    ? !view.osProtected
    : !root?.osVaultAvailable;
  const signInReady = signIn
    ? signIn.remote
      ? !!callback.trim()
      : signIn.state === "callback-ready"
    : false;
  const statusText = view?.ref
    ? `${uiText(view.disabled ? "Disabled" : view.planPaused ? "Paused" : view.locked ? (view.taskAccess ? "Chat locked" : "Locked") : "Unlocked")} · ${providerName}`
    : uiText(
        root && !view && selected && selected !== "new"
          ? "Unavailable"
          : "No connection saved",
      );

  async function pasteCallback() {
    try {
      const text = await navigator.clipboard?.readText?.();
      if (text) setCallback(text);
    } catch {
      // Clipboard read not granted; user can paste directly into Redirect URL.
    }
  }

  async function change(action: string) {
    if (!view) {
      setError(uiText("Previous connection unavailable"));
      return;
    }
    selectionChosen.current = true;
    const hadConnections = !!(root?.ref || root?.connections?.length);
    setBusy(true);
    setError("");
    const secret = passphrase,
      credential = key;
    setKey("");
    setPassphrase("");
    try {
      const command = {
        action: action === "save" && selected === "new" ? "add" : action,
        ref: view?.ref ?? "",
        ...(["save", "unlock", "remove"].includes(action) && needsPassphrase
          ? { passphrase: secret }
          : {}),
        ...(action === "save"
          ? {
              provider,
              ...(provider === "anthropic" && workspace ? { workspace } : {}),
              ...(isLocal
                ? {
                    endpoint,
                    server: !view.ref ? server?.id : undefined,
                    ...(credential ? { key: credential } : {}),
                  }
                : { key: credential }),
              credentialMode: isLocal ? "local" : apiMode ? provider : "test",
            }
          : {}),
      };
      const signInCommand =
        action === "sign-in-start"
          ? {
              action,
              provider,
              ...(provider === "google" ? { googleProject } : {}),
              ref: view.ref,
              remote: core.connection?.location === "remote",
            }
          : action === "sign-in-complete"
            ? {
                action,
                signInId: signIn?.id,
                callback: callback.trim() || undefined,
                ...(needsPassphrase ? { passphrase: secret } : {}),
              }
            : action === "sign-in-cancel"
              ? { action, signInId: signIn?.id }
              : {
                  action,
                  ref: view.ref,
                  ...(needsPassphrase ? { passphrase: secret } : {}),
                };
      const selectedBinary =
        cliAuth?.binary || (providerClis.length > 1 ? cliStatus?.binary : "");
      const cliCommand =
        action === "cli-probe"
          ? { action: "cli-probe" }
          : action === "cli-auth-start"
            ? {
                action: "cli-auth-start",
                provider: cliAuth?.provider || provider,
                ...(selectedBinary ? { binary: selectedBinary } : {}),
                ...(view.ref ? { ref: view.ref } : {}),
              }
            : action === "cli-auth-cancel"
              ? { action: "cli-auth-cancel", signInId: cliAuth?.id }
              : {
                  action: "cli-connect",
                  provider: cliAuth?.provider || provider,
                  ...(selectedBinary ? { binary: selectedBinary } : {}),
                  ...(view.ref ? { ref: view.ref } : {}),
                  ...(cliName.trim() ? { name: cliName.trim() } : {}),
                  ...(cliBinaryPath.trim()
                    ? { binaryPath: cliBinaryPath.trim() }
                    : {}),
                  ...(cliConfigDir.trim()
                    ? { configDir: cliConfigDir.trim() }
                    : {}),
                  ...(needsPassphrase ? { passphrase: secret } : {}),
                };
      const next = await core.request<Connection>(
        "ai/connection",
        "POST",
        action.startsWith("sign-in-")
          ? signInCommand
          : action.startsWith("cli-")
            ? cliCommand
            : command,
      );
      if (
        action !== "sign-in-start" &&
        action !== "sign-in-cancel" &&
        action !== "cli-probe" &&
        action !== "cli-auth-start" &&
        action !== "cli-auth-cancel"
      ) {
        setSelected(next.ref);
        setCallback("");
      }
      if (
        action === "save" ||
        action === "remove" ||
        action === "sign-in-disconnect" ||
        action === "sign-in-start" ||
        action === "cli-connect"
      ) {
        setEditOpen(false);
        setCliName("");
        setCliBinaryPath("");
        setCliConfigDir("");
      }
      const refreshed = next.connections
        ? next
        : await core.request<Connection>("ai/connection");
      setView(refreshed);
      window.dispatchEvent(new Event("ai-connection-changed"));
      if (
        !hadConnections &&
        (action === "save" ||
          action === "sign-in-complete" ||
          action === "cli-connect")
      ) {
        const activeProfile = agents.view?.profiles.find(
          (p) => p.id === agents.view?.profileId,
        );
        const added =
          refreshed.connections?.find((a) => a.ref === next.ref) ??
          (refreshed.ref === next.ref ? refreshed : next);
        if (
          activeProfile &&
          activeProfile.provider === "none" &&
          agents.view &&
          added?.ref &&
          added.provider &&
          !added.locked
        ) {
          const models =
            added.catalog?.models ?? refreshed.catalog?.models ?? [];
          const firstModel =
            models.find(
              (m) =>
                m.connectionRef === added.ref && m.selectable && m.recommended,
            )?.id ??
            models.find((m) => m.connectionRef === added.ref && m.selectable)
              ?.id ??
            models.find((m) => m.provider === added.provider && m.recommended)
              ?.id ??
            "";
          const revision = agents.view.revision;
          void agents
            .change({
              action: "profile-update",
              revision,
              name: activeProfile.name,
              text: activeProfile.persona,
              provider: added.provider as Provider,
              accountRef: added.ref,
              model: firstModel,
            })
            .catch(() => {});
        }
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : "Credential operation failed");
      if (action === "sign-in-complete") {
        try {
          setView(await core.request<Connection>("ai/connection"));
        } catch {}
      }
    } finally {
      setBusy(false);
    }
  }

  const autoCompletedSignIn = useRef("");
  const autoAttemptedCallback = useRef("");
  const autoConnectedCliAuth = useRef("");
  const autoCompleteFn = useRef(change);
  autoCompleteFn.current = change;
  useEffect(() => {
    if (!signIn || signIn.remote || signIn.state !== "waiting") return;
    const timer = window.setInterval(() => {
      void refresh();
    }, 1000);
    return () => clearInterval(timer);
  }, [signIn?.id, signIn?.remote, signIn?.state, refresh]);
  useEffect(() => {
    if (
      !cliAuth ||
      (cliAuth.state !== "starting" && cliAuth.state !== "awaiting-code")
    ) {
      return;
    }
    const timer = window.setInterval(() => {
      void refresh();
    }, 2000);
    return () => clearInterval(timer);
  }, [cliAuth?.id, cliAuth?.state, refresh]);
  useEffect(() => {
    if (!cliAuth) {
      autoConnectedCliAuth.current = "";
      return;
    }
    if (
      cliAuth.state === "authenticated" &&
      !needsPassphrase &&
      !busy &&
      autoConnectedCliAuth.current !== cliAuth.id
    ) {
      autoConnectedCliAuth.current = cliAuth.id;
      void autoCompleteFn.current("cli-connect");
    }
  }, [cliAuth?.id, cliAuth?.state, needsPassphrase, busy]);
  useEffect(() => {
    if (!signIn) {
      autoCompletedSignIn.current = "";
      autoAttemptedCallback.current = "";
      return;
    }
    if (needsPassphrase || busy) return;
    if (
      !signIn.remote &&
      signIn.state === "callback-ready" &&
      autoCompletedSignIn.current !== signIn.id
    ) {
      autoCompletedSignIn.current = signIn.id;
      void autoCompleteFn.current("sign-in-complete");
      return;
    }
    const trimmed = callback.trim();
    const attemptKey = `${signIn.id}:${trimmed}`;
    if (
      signIn.remote &&
      trimmed &&
      isCompleteSignInCallback(signIn.provider, trimmed) &&
      autoAttemptedCallback.current !== attemptKey
    ) {
      autoAttemptedCallback.current = attemptKey;
      void autoCompleteFn.current("sign-in-complete");
    }
  }, [
    signIn?.id,
    signIn?.provider,
    signIn?.remote,
    signIn?.state,
    callback,
    needsPassphrase,
    busy,
  ]);

  function chooseProvider(id: string) {
    selectionChosen.current = true;
    methodChosen.current = false;
    setSelected("new");
    setNewProvider(id);
    setGoogleProject("");
    setCliName("");
    setNewMethod(
      id === "local"
        ? "local"
        : id === "openai" && root?.signInAvailable
          ? "sign-in"
          : "api-key",
    );
    setServerPreset(defaultLocalEndpoint);
    setEndpoint(defaultLocalEndpoint);
    setKey("");
    setPassphrase("");
    setCallback("");
    setCliBinary("");
    setCliBinaryPath("");
    setCliConfigDir("");
  }

  async function toggleAccountEnabled(ref: string, disabled: boolean) {
    setBusy(true);
    try {
      await toggleConnectionEnabled(
        core,
        root,
        setView,
        setError,
        ref,
        disabled,
      );
    } finally {
      setBusy(false);
    }
  }

  function renderCredentialForm() {
    return (
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (method === "cli") {
            if (cliStatus?.installed && cliStatus.authenticated) {
              void change("cli-connect");
            }
            return;
          }
          void change(method === "sign-in" ? "sign-in-complete" : "save");
        }}
        autoComplete="off"
      >
        {isLocal && (
          <label>
            {uiText("Endpoint")}
            <input
              type="url"
              value={endpoint}
              maxLength={256}
              spellCheck={false}
              onChange={(e) => setEndpoint(e.target.value)}
            />
          </label>
        )}
        {method === "cli" && (
          <>
            {providerClis.length > 1 && !view?.ref && (
              <div
                className="segmented-control"
                role="group"
                aria-label={uiText("Runtime")}
              >
                {providerClis.map((c) => (
                  <button
                    key={c.binary}
                    type="button"
                    aria-pressed={cliStatus?.binary === c.binary}
                    disabled={busy || !!signIn || !!cliAuth}
                    onClick={() => setCliBinary(c.binary)}
                  >
                    {c.authenticated && (
                      <span
                        className="cli-status-dot"
                        data-state="ready"
                        aria-hidden="true"
                      />
                    )}
                    {c.label}
                  </button>
                ))}
              </div>
            )}
            <div className="cli-status-card">
              <div className="cli-status-head">
                <TerminalSquare size={15} />
                <strong>{cliStatus?.label || providerName}</strong>
                {cliStatus?.version && (
                  <span className="provider-model-pill cli-version-pill">
                    {`v${cliStatus.version}`}
                  </span>
                )}
                <span
                  className="provider-model-pill"
                  data-state={cliStatus?.authenticated ? "ready" : "stale"}
                >
                  {uiText(
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
            {cliStatus && !cliStatus.installed && (
              <div className="cli-hint-row">
                <code>{cliStatus.installHint}</code>
                <button
                  type="button"
                  className="icon-button"
                  aria-label={
                    copiedText === cliStatus.installHint
                      ? uiText("Copied")
                      : uiText("Copy")
                  }
                  title={
                    copiedText === cliStatus.installHint
                      ? uiText("Copied")
                      : uiText("Copy")
                  }
                  onClick={() => void copyText(cliStatus.installHint)}
                >
                  <Copy size={14} />
                </button>
              </div>
            )}
            {cliStatus && cliStatus.installed && !cliStatus.authenticated && (
              <div className="cli-hint-row">
                <code>{cliStatus.loginCommand}</code>
                <button
                  type="button"
                  className="icon-button"
                  aria-label={
                    copiedText === cliStatus.loginCommand
                      ? uiText("Copied")
                      : uiText("Copy")
                  }
                  title={
                    copiedText === cliStatus.loginCommand
                      ? uiText("Copied")
                      : uiText("Copy")
                  }
                  onClick={() => void copyText(cliStatus.loginCommand)}
                >
                  <Copy size={14} />
                </button>
              </div>
            )}
            {cliStatus?.installed && cliStatus.authenticated && !view?.ref && (
              <>
                <label>
                  {uiText("Name")}
                  <input
                    value={cliName}
                    maxLength={80}
                    autoComplete="off"
                    placeholder={cliStatus.label}
                    onChange={(e) => setCliName(e.target.value)}
                  />
                </label>
                <label>
                  {uiText("Binary path")}
                  <input
                    value={cliBinaryPath}
                    maxLength={260}
                    autoComplete="off"
                    spellCheck={false}
                    placeholder={cliStatus.binaryPath || cliStatus.binary}
                    onChange={(e) => setCliBinaryPath(e.target.value)}
                  />
                </label>
                <label>
                  {uiText("Config directory")}
                  <input
                    value={cliConfigDir}
                    maxLength={260}
                    autoComplete="off"
                    spellCheck={false}
                    onChange={(e) => setCliConfigDir(e.target.value)}
                  />
                </label>
              </>
            )}
          </>
        )}
        {method !== "sign-in" && method !== "cli" && (
          <label>
            {uiText(isLocal ? "API key (optional)" : "API key")}
            <input
              type="password"
              autoComplete="off"
              value={key}
              maxLength={256}
              onChange={(e) => setKey(e.target.value)}
            />
          </label>
        )}
        {provider === "anthropic" && method === "api-key" && (
          <label>
            {uiText("Workspace (optional)")}
            <input
              value={workspace}
              maxLength={71}
              autoComplete="off"
              spellCheck={false}
              onChange={(e) => setWorkspace(e.target.value)}
              placeholder="wrkspc_…"
            />
          </label>
        )}
        {provider === "google" &&
          method === "sign-in" &&
          !signIn &&
          !view?.ref && (
            <label>
              {uiText("Project")}
              <input
                aria-label={uiText("Project")}
                title={uiText("Google Cloud project")}
                value={googleProject}
                maxLength={30}
                autoComplete="off"
                onChange={(e) => setGoogleProject(e.target.value)}
                required
              />
            </label>
          )}
        {needsPassphrase &&
          (method !== "cli" || !!cliStatus?.authenticated) && (
            <label>
              {uiText("Vault passphrase")}
              <input
                type="password"
                autoComplete="off"
                value={passphrase}
                maxLength={1024}
                onChange={(e) => setPassphrase(e.target.value)}
              />
            </label>
          )}
        <div className="agent-actions">
          {method === "sign-in" ? (
            <>
              <button
                type="button"
                className="primary"
                disabled={
                  busy ||
                  !view ||
                  !signInAvailable ||
                  (provider === "google" &&
                    !view.ref &&
                    !/^[a-z][a-z0-9-]{4,28}[a-z0-9]$/.test(googleProject))
                }
                onClick={() => void change("sign-in-start")}
              >
                {uiText(
                  view?.ref
                    ? "Reconnect"
                    : provider === "google"
                      ? "Continue with Google"
                      : provider === "openrouter"
                        ? "Continue"
                        : "Continue with ChatGPT",
                )}
              </button>
              {view?.ref && !signIn && (
                <>
                  <button
                    type="button"
                    className="secondary"
                    disabled={
                      busy || (needsPassphrase && passphrase.length < 12)
                    }
                    onClick={() => void change("sign-in-refresh")}
                  >
                    {uiText("Refresh")}
                  </button>
                  <button
                    type="button"
                    className="secondary"
                    disabled={
                      busy || (needsPassphrase && passphrase.length < 12)
                    }
                    onClick={() => void change("sign-in-disconnect")}
                  >
                    {uiText("Disconnect")}
                  </button>
                </>
              )}
            </>
          ) : method === "cli" ? (
            <>
              {cliStatus && !cliStatus.installed ? (
                <button
                  type="button"
                  className="secondary"
                  disabled={busy}
                  onClick={() => void change("cli-probe")}
                >
                  <RefreshCw size={14} className={busy ? "spin" : ""} />
                  <span>{uiText("Check again")}</span>
                </button>
              ) : cliStatus && !cliStatus.authenticated ? (
                <>
                  {(cliStatus.supportsDevice || provider === "anthropic") && (
                    <button
                      type="button"
                      className="primary"
                      disabled={busy || !!cliAuth}
                      onClick={() => void change("cli-auth-start")}
                    >
                      {uiText("Sign in via CLI")}
                    </button>
                  )}
                  <button
                    type="button"
                    className="secondary"
                    disabled={busy}
                    onClick={() => void change("cli-probe")}
                  >
                    <RefreshCw size={14} className={busy ? "spin" : ""} />
                    <span>{uiText("Check again")}</span>
                  </button>
                </>
              ) : (
                <>
                  <button
                    className="primary"
                    disabled={
                      busy ||
                      !view ||
                      !cliStatus?.authenticated ||
                      (needsPassphrase && passphrase.length < 12)
                    }
                  >
                    {uiText(view?.ref ? "Reconnect" : "Connect CLI")}
                  </button>
                  <button
                    type="button"
                    className="secondary"
                    disabled={busy}
                    onClick={() => void change("cli-probe")}
                  >
                    <RefreshCw size={14} className={busy ? "spin" : ""} />
                    <span>{uiText("Check again")}</span>
                  </button>
                </>
              )}
            </>
          ) : (
            <>
              <button
                className="primary"
                disabled={
                  busy ||
                  !view ||
                  (isLocal ? !endpoint : !key) ||
                  (needsPassphrase && passphrase.length < 12) ||
                  (view.pilotEnrollment && !!view.ref)
                }
              >
                {view?.ref ? uiText("Replace") : uiText("Save")}
              </button>
              {method === "api-key" && PROVIDER_API_KEY_URLS[provider] && (
                <a
                  className="secondary"
                  href={PROVIDER_API_KEY_URLS[provider]}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  {uiText("Get API key")}
                </a>
              )}
            </>
          )}
          {(view?.ref || (root && !view && selected && selected !== "new")) && (
            <>
              {(!view?.osProtected || view?.planPaused) && (
                <button
                  type="button"
                  className="secondary"
                  disabled={
                    busy ||
                    !view?.ref ||
                    (needsPassphrase && passphrase.length < 12)
                  }
                  onClick={() => void change("unlock")}
                >
                  {uiText(view?.planPaused ? "Review access" : "Unlock")}
                </button>
              )}
              {!view?.osProtected && (
                <button
                  type="button"
                  className="secondary"
                  disabled={busy || !view?.ref}
                  onClick={() => void change("lock")}
                >
                  {uiText("Lock")}
                </button>
              )}
              {method !== "sign-in" && (
                <button
                  type="button"
                  className="secondary"
                  disabled={
                    busy ||
                    !view?.ref ||
                    (needsPassphrase && passphrase.length < 12)
                  }
                  onClick={() => void change("remove")}
                >
                  {uiText("Remove")}
                </button>
              )}
            </>
          )}
        </div>
      </form>
    );
  }

  const visibleProviders = (
    root?.catalog?.providers.filter(
      (p) =>
        (!p.family || p.family === p.id) &&
        (p.name.toLowerCase().includes(search.toLowerCase()) ||
          p.servers?.some((s) =>
            s.name.toLowerCase().includes(search.toLowerCase()),
          ) ||
          root.connections?.some(
            (a) =>
              (a.provider === p.id ||
                root?.catalog?.providers.find((x) => x.id === a.provider)
                  ?.family === p.id) &&
              a.name?.toLowerCase().includes(search.toLowerCase()),
          )),
    ) || []
  )
    .map((p, index) => ({
      provider: p,
      index,
      connected:
        root?.connections?.some(
          (a) =>
            a.provider === p.id ||
            root?.catalog?.providers.find((x) => x.id === a.provider)
              ?.family === p.id,
        ) ?? false,
    }))
    .sort((a, b) => {
      if (a.provider.id === "local" && b.provider.id !== "local") return 1;
      if (b.provider.id === "local" && a.provider.id !== "local") return -1;
      return a.connected === b.connected
        ? a.index - b.index
        : a.connected
          ? -1
          : 1;
    })
    .map((x) => x.provider);

  return (
    <section className="providers-page" aria-label={uiText("Providers")}>
      <header className="providers-heading">
        <button
          type="button"
          className="shell-icon"
          aria-label={uiText("Back")}
          data-tooltip={uiText("Back")}
          onClick={() => setProvidersOpen(false)}
        >
          <ArrowLeft size={18} />
        </button>
        <h1 ref={pageHeading} tabIndex={-1}>
          {uiText("Providers")}
        </h1>
      </header>
      <div className="providers-layout">
        <aside
          className="panel providers-list"
          aria-label={uiText("Providers")}
        >
          <label className="providers-search">
            <Search size={16} />
            <input
              aria-label={uiText("Search providers")}
              placeholder={uiText("Search")}
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </label>
          {!root && (
            <div
              className="agent-record-body"
              role={error ? "alert" : "status"}
            >
              {error ? uiText(error) : uiText("Loading…")}
              {error && (
                <button className="text-button" onClick={() => void refresh()}>
                  {uiText("Reload")}
                </button>
              )}
            </div>
          )}
          {visibleProviders.map((p) => (
            <div key={p.id} className="provider-brand" data-provider={p.id}>
              <button
                type="button"
                className="provider-brand-button"
                aria-pressed={providerFamily === p.id && !view?.ref}
                disabled={busy || !!signIn}
                onClick={() => chooseProvider(p.id)}
              >
                <span>{uiText(p.name)}</span>
                <Plus size={14} />
              </button>
              {p.id === "local" &&
                p.servers
                  ?.filter(
                    (s) =>
                      !search ||
                      p.name.toLowerCase().includes(search.toLowerCase()) ||
                      s.name.toLowerCase().includes(search.toLowerCase()),
                  )
                  .map((s) => (
                    <button
                      key={s.id}
                      type="button"
                      className="provider-account"
                      aria-label={`${uiText("Connect")} ${s.name}`}
                      aria-pressed={
                        selected === "new" && isLocal && server?.id === s.id
                      }
                      disabled={busy || !!signIn}
                      onClick={() => {
                        if (selected !== "new" || !isLocal)
                          chooseProvider("local");
                        else setKey("");
                        setServerPreset(s.endpoint);
                        setEndpoint(s.endpoint);
                      }}
                    >
                      <span>{s.name}</span>
                    </button>
                  ))}
              {root?.connections
                ?.filter(
                  (a) =>
                    a.provider === p.id ||
                    root?.catalog?.providers.find((x) => x.id === a.provider)
                      ?.family === p.id,
                )
                .map((a) => (
                  <button
                    key={a.ref}
                    data-account={a.ref}
                    data-disabled={a.disabled ? "true" : undefined}
                    type="button"
                    className="provider-account"
                    aria-pressed={view?.ref === a.ref}
                    disabled={busy || !!signIn}
                    onClick={() => {
                      selectionChosen.current = true;
                      setSelected(a.ref);
                      setKey("");
                      setPassphrase("");
                      setCallback("");
                    }}
                  >
                    {a.accentColor && (
                      <i
                        className="provider-accent-dot"
                        style={{ backgroundColor: a.accentColor }}
                        aria-hidden="true"
                      />
                    )}
                    <span className="provider-account-copy">
                      {a.name || p.name}
                      <small>
                        {p.family &&
                          `${uiText(root?.catalog?.providers.find((x) => x.id === a.provider)?.route || "")} · `}
                        {uiText(
                          a.authMethod === "oauth" || a.authMethod === "sign-in"
                            ? "Sign in"
                            : a.authMethod === "cli"
                              ? "CLI"
                              : a.authMethod === "local"
                                ? "Server"
                                : "API key",
                        )}
                        {a.cliVersion ? ` · v${a.cliVersion}` : ""}
                        {a.disabled ? ` · ${uiText("Disabled")}` : ""}
                      </small>
                    </span>
                    {a.locked && <LockKeyhole size={14} />}
                  </button>
                ))}
            </div>
          ))}
          {root && (
            <button
              type="button"
              className="secondary providers-guided-btn"
              disabled={busy || !!signIn || root.pilotEnrollment}
              onClick={() => setWizardOpen(true)}
            >
              {uiText("Guided setup")}
            </button>
          )}
        </aside>
        <section
          className="panel ai-connection provider-editor"
          aria-label={uiText("Connection")}
        >
          <div className="panel-heading">
            <h3>{uiText(providerName)}</h3>
            {view?.authMethod === "oauth" && view.googleProject && (
              <span className="provider-route">{view.googleProject}</span>
            )}
            {catalogProvider?.route && (
              <span className="provider-route">
                {uiText(catalogProvider.route)}
              </span>
            )}
            <div className="provider-heading-actions">
              {view?.ref && (
                <button
                  className="shell-icon"
                  type="button"
                  aria-label={uiText("Add provider")}
                  title={uiText("Add provider")}
                  disabled={busy || !!signIn || root?.pilotEnrollment}
                  onClick={() => {
                    methodChosen.current = false;
                    setSelected("new");
                    setNewProvider(provider);
                    setCliName("");
                    setCliBinary("");
                    setCliBinaryPath("");
                    setCliConfigDir("");
                    setNewMethod(
                      provider === "local"
                        ? "local"
                        : provider === "openai" && root?.signInAvailable
                          ? "sign-in"
                          : "api-key",
                    );
                    setCallback("");
                    setKey("");
                    setServerPreset(defaultLocalEndpoint);
                    setEndpoint(defaultLocalEndpoint);
                    setPassphrase("");
                  }}
                >
                  <Plus size={16} />
                </button>
              )}
              <button
                type="button"
                className="shell-icon"
                aria-label={uiText("Refresh models")}
                title={uiText("Refresh models")}
                disabled={busy || !view?.ref || view.locked}
                onClick={() => void change("models-refresh")}
              >
                <RefreshCw size={16} className={busy ? "spin" : ""} />
              </button>
            </div>
          </div>
          <div className="agent-record-body">
            {!view?.ref && regionalProviders.length > 1 && (
              <div
                className="segmented-control"
                role="group"
                aria-label={uiText("Region")}
              >
                {regionalProviders.map((p) => (
                  <button
                    key={p.id}
                    type="button"
                    aria-pressed={provider === p.id}
                    disabled={busy || !!signIn}
                    onClick={() => chooseProvider(p.id)}
                  >
                    {uiText(p.route || p.name)}
                  </button>
                ))}
              </div>
            )}
            {!view?.ref &&
              (hasSignIn || !!cliStatus) &&
              (provider !== "google" || signInAvailable || !!cliStatus) && (
                <div
                  className="segmented-control"
                  role="group"
                  aria-label={uiText("Connection")}
                >
                  {hasSignIn && (provider !== "google" || signInAvailable) && (
                    <button
                      type="button"
                      aria-pressed={newMethod === "sign-in"}
                      disabled={busy || !!signIn || !signInAvailable}
                      onClick={() => {
                        methodChosen.current = true;
                        setNewMethod("sign-in");
                        setKey("");
                        setPassphrase("");
                      }}
                    >
                      {uiText("Sign in")}
                    </button>
                  )}
                  <button
                    type="button"
                    aria-pressed={newMethod === "api-key"}
                    disabled={busy || !!signIn}
                    onClick={() => {
                      methodChosen.current = true;
                      setNewMethod("api-key");
                      setKey("");
                      setPassphrase("");
                      setCallback("");
                    }}
                  >
                    {uiText("API key")}
                  </button>
                  {cliStatus && (
                    <button
                      type="button"
                      aria-pressed={newMethod === "cli"}
                      disabled={busy || !!signIn || !!cliAuth}
                      onClick={() => {
                        methodChosen.current = true;
                        setNewMethod("cli");
                        setKey("");
                        setPassphrase("");
                        setCallback("");
                      }}
                    >
                      {providerClis.some((c) => c.authenticated) && (
                        <span
                          className="cli-status-dot"
                          data-state="ready"
                          aria-hidden="true"
                        />
                      )}
                      {uiText("CLI")}
                    </button>
                  )}
                </div>
              )}
            <p
              className="connection-status"
              role={editOpen || signIn ? undefined : "status"}
            >
              {statusText}
            </p>
            {view?.planPaused && (
              <p role="status">
                {uiText(pausedFailure?.summary || "Provider unavailable")}
              </p>
            )}
            {error && !editOpen && !signIn && (
              <p role="alert">{uiText(error)}</p>
            )}
            {root && selected && selected !== "new" && !view && (
              <p role="alert">{uiText("Previous connection unavailable")}</p>
            )}
            {view?.catalog?.state === "failed" && (
              <p role="alert">{uiText("Models unavailable")}</p>
            )}
            {view?.ref && (
              <div className="provider-action-bar">
                <button
                  type="button"
                  className="provider-action-btn"
                  aria-expanded={editOpen}
                  disabled={busy || !!signIn}
                  onClick={() => setEditOpen(true)}
                >
                  <Pencil size={14} />
                  <span>{uiText("Edit")}</span>
                </button>
                {view.provider !== "kimi-code" && (
                  <TaskAccessSettings
                    key={`${view.ref}:${core.instanceId}`}
                    connection={view}
                    onChanged={() =>
                      window.dispatchEvent(new Event("ai-connection-changed"))
                    }
                  />
                )}
                <OpenAIPilotSettings connection={view} />
                <ToggleSwitch
                  checked={!view.disabled}
                  disabled={busy || !!signIn}
                  label={uiText("Enabled")}
                  onChange={(checked) =>
                    void toggleAccountEnabled(view.ref, !checked)
                  }
                />
              </div>
            )}
            {view?.planFailure && !editOpen && !signIn && (
              <p role="alert">{pausedFailure?.details || view.planFailure}</p>
            )}
            {!view?.ref && !signIn && renderCredentialForm()}
          </div>
          {view?.ref && editOpen && !signIn && (
            <Dialog title={uiText("Edit")} onClose={() => setEditOpen(false)}>
              <div className="dialog-body">
                <p className="connection-status" role="status">
                  {statusText}
                </p>
                {error && <p role="alert">{uiText(error)}</p>}
                {renderCredentialForm()}
                {view.planFailure && (
                  <p role="alert">
                    {pausedFailure?.details || view.planFailure}
                  </p>
                )}
              </div>
            </Dialog>
          )}
          {signIn && (
            <Dialog
              title={uiText(providerName)}
              onClose={() => void change("sign-in-cancel")}
            >
              <form
                className="dialog-body signin-modal"
                onSubmit={(e) => {
                  e.preventDefault();
                  void change("sign-in-complete");
                }}
                autoComplete="off"
              >
                <p role="status">
                  {uiText(
                    signIn.state === "callback-ready"
                      ? "Ready"
                      : "Waiting for sign-in",
                  )}
                </p>
                {error && <p role="alert">{uiText(error)}</p>}
                {signIn.remote ? (
                  <>
                    <ol className="signin-steps">
                      <li className="signin-step">
                        <span className="signin-step-num" aria-hidden="true">
                          1
                        </span>
                        <a
                          className={signInReady ? "secondary" : "primary"}
                          href={signIn.url}
                          target="_blank"
                          rel="noopener noreferrer"
                        >
                          <ExternalLink size={14} />
                          <span>{uiText("Open sign-in")}</span>
                        </a>
                      </li>
                      <li className="signin-step">
                        <span className="signin-step-num" aria-hidden="true">
                          2
                        </span>
                        <p className="signin-callout">
                          {uiText(
                            signIn.provider === "openrouter"
                              ? "Copy the code from the finished tab"
                              : "Copy the 127.0.0.1 URL from the finished tab — connection error is normal",
                          )}
                        </p>
                      </li>
                      <li className="signin-step">
                        <span className="signin-step-num" aria-hidden="true">
                          3
                        </span>
                        <label>
                          {uiText(
                            signIn.provider === "openrouter"
                              ? "Code"
                              : "Redirect URL",
                          )}
                          <div className="signin-paste-row">
                            <input
                              aria-label={uiText(
                                signIn.provider === "openrouter"
                                  ? "Code"
                                  : "Redirect URL",
                              )}
                              placeholder={
                                signIn.provider === "openrouter"
                                  ? undefined
                                  : "http://127.0.0.1:.../auth/callback?..."
                              }
                              value={callback}
                              maxLength={4096}
                              autoComplete="off"
                              spellCheck={false}
                              onChange={(e) => setCallback(e.target.value)}
                            />
                            <button
                              type="button"
                              className="icon-button"
                              aria-label={uiText("Paste")}
                              title={uiText("Paste")}
                              disabled={busy}
                              onClick={() => void pasteCallback()}
                            >
                              <ClipboardPaste size={15} />
                            </button>
                          </div>
                        </label>
                      </li>
                    </ol>
                    <div className="cli-hint-row">
                      <code>{`fased-app ai-connection --remote --sign-in ${signIn.provider}`}</code>
                      <button
                        type="button"
                        className="icon-button"
                        aria-label={
                          copiedText ===
                          `fased-app ai-connection --remote --sign-in ${signIn.provider}`
                            ? uiText("Copied")
                            : uiText("Copy")
                        }
                        title={
                          copiedText ===
                          `fased-app ai-connection --remote --sign-in ${signIn.provider}`
                            ? uiText("Copied")
                            : uiText("Copy")
                        }
                        onClick={() =>
                          void copyText(
                            `fased-app ai-connection --remote --sign-in ${signIn.provider}`,
                          )
                        }
                      >
                        <Copy size={14} />
                      </button>
                    </div>
                  </>
                ) : (
                  <a
                    className={signInReady ? "secondary" : "primary"}
                    href={signIn.url}
                    target="_blank"
                    rel="noopener noreferrer"
                  >
                    <ExternalLink size={14} />
                    <span>{uiText("Open sign-in")}</span>
                  </a>
                )}
                {needsPassphrase && (
                  <label>
                    {uiText("Vault passphrase")}
                    <input
                      type="password"
                      autoComplete="off"
                      value={passphrase}
                      maxLength={1024}
                      onChange={(e) => setPassphrase(e.target.value)}
                    />
                  </label>
                )}
                <div className="agent-actions">
                  {(signIn.remote || needsPassphrase || busy) && (
                    <button
                      className="primary"
                      disabled={
                        busy ||
                        (needsPassphrase && passphrase.length < 12) ||
                        !signInReady
                      }
                    >
                      {uiText("Finish")}
                    </button>
                  )}
                  <button
                    type="button"
                    className="secondary"
                    disabled={busy}
                    onClick={() => void change("sign-in-cancel")}
                  >
                    {uiText("Cancel")}
                  </button>
                </div>
              </form>
            </Dialog>
          )}
          {cliAuth && !signIn && (
            <Dialog
              title={cliAuthLabel}
              onClose={() => void change("cli-auth-cancel")}
            >
              <form
                className="dialog-body signin-modal"
                onSubmit={(e) => {
                  e.preventDefault();
                  void change("cli-connect");
                }}
                autoComplete="off"
              >
                <p role="status">
                  {uiText(
                    cliAuth.state === "failed"
                      ? "Unavailable"
                      : cliAuth.state === "authenticated"
                        ? "Connected"
                        : "Waiting for sign-in",
                  )}
                </p>
                {cliAuth.state === "failed" && cliAuth.message && (
                  <p role="alert">{cliAuth.message}</p>
                )}
                {cliAuth.verificationUrl && (
                  <a
                    className="primary"
                    href={cliAuth.verificationUrl}
                    target="_blank"
                    rel="noopener noreferrer"
                  >
                    <ExternalLink size={14} />
                    <span>{uiText("Open sign-in")}</span>
                  </a>
                )}
                {cliAuth.userCode && (
                  <div
                    className="cli-code-box"
                    aria-label={uiText("Device code")}
                  >
                    <code>{cliAuth.userCode}</code>
                    <button
                      type="button"
                      className="icon-button"
                      aria-label={
                        copiedText === cliAuth.userCode
                          ? uiText("Copied")
                          : uiText("Copy")
                      }
                      title={
                        copiedText === cliAuth.userCode
                          ? uiText("Copied")
                          : uiText("Copy")
                      }
                      onClick={() => void copyText(cliAuth.userCode || "")}
                    >
                      <Copy size={15} />
                    </button>
                  </div>
                )}
                {cliAuth.state === "authenticated" && needsPassphrase && (
                  <label>
                    {uiText("Vault passphrase")}
                    <input
                      type="password"
                      autoComplete="off"
                      value={passphrase}
                      maxLength={1024}
                      onChange={(e) => setPassphrase(e.target.value)}
                    />
                  </label>
                )}
                <div className="agent-actions">
                  {cliAuth.state === "authenticated" && (
                    <button
                      type="submit"
                      className="primary"
                      disabled={
                        busy || (needsPassphrase && passphrase.length < 12)
                      }
                    >
                      {uiText("Connect CLI")}
                    </button>
                  )}
                  <button
                    type="button"
                    className="secondary"
                    disabled={busy}
                    onClick={() => void change("cli-auth-cancel")}
                  >
                    {uiText("Cancel")}
                  </button>
                </div>
              </form>
            </Dialog>
          )}
          {view?.ref && (
            <ProviderModelsPanel
              view={view}
              provider={provider}
              busy={busy}
              onRefresh={() => void change("models-refresh")}
            />
          )}
        </section>
      </div>
      {wizardOpen && (
        <AddProviderWizard
          initialProvider={provider}
          onAdded={(ref) => {
            if (ref) {
              selectionChosen.current = true;
              setSelected(ref);
            }
          }}
          onClose={() => setWizardOpen(false)}
        />
      )}
    </section>
  );
}
