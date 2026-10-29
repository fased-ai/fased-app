import "./Providers.css";
import type { Provider } from "../state";
type CredentialMode =
  Exclude<Provider, "none"> | "test" | "openai-signin" | "google-oauth";
import { RefreshCw, Plus, ArrowLeft, Search, LockKeyhole } from "../icons";
import { useUI } from "../ui-preferences";
import type { OpenAIModelPolicy, OpenAIAdmission } from "../agents";
import { isUnmeteredPolicy, planFailureDetails } from "../agents";
import { useCallback, useEffect, useRef, useState } from "react";
import { useWorkspace } from "../workspace";
import { useCore } from "../core";
import { TaskAccessSettings, type TaskAccessAllowance } from "./TaskAccess";
import { OpenAIPilotSettings, type PilotAllowance } from "./OpenAIPilot";

const defaultLocalEndpoint = "http://127.0.0.1:11434/v1";

export type Connection = {
  authMethod?: "api-key" | "sign-in" | "local" | "oauth";
  endpoint?: string;
  googleProject?: string;
  workspace?: string;
  googleSignInAvailable?: boolean;
  planUse?: boolean;
  planPaused?: boolean;
  planFailure?: string;
  signInAvailable?: boolean;
  signIn?: {
    id: string;
    provider: string;
    state: "waiting" | "callback-ready";
    url: string;
    expiresAt: number;
    remote: boolean;
  };
  pilotEnrollment?: boolean;
  pilotAvailable?: boolean;
  pilot?: PilotAllowance;
  pilotReview?: PilotAllowance;
  pilotState?: string;
  provider?: string;
  name?: string;
  connections?: Connection[];
  catalog?: {
    version: number;
    state?: string;
    policy?: string;
    providers: {
      id: string;
      name: string;
      methods: { id: string; name: string }[];
      route?: string;
      family?: string;
      servers?: { id: string; name: string; endpoint: string }[];
    }[];
    models: {
      id: string;
      name: string;
      tier?: string;
      policy?: OpenAIModelPolicy;
      provider: string;
      method: string;
      connectionRef: string;
      recommended: boolean;
      selectable: boolean;
      availability: string;
    }[];
  };
  credentialMode?: CredentialMode;
  credentialModes?: CredentialMode[];
  taskAccess?: TaskAccessAllowance;
  accessReview?: TaskAccessAllowance;
  admissionPolicy: OpenAIModelPolicy;
  ref: string;
  model: string;
  locked: boolean;
  unlockExpires: number;
  testOnly: boolean;
  liveCallsEnabled: boolean;
};
export function useAIConnection() {
  const core = useCore();
  const [view, setView] = useState<Connection | null>(null);
  const [error, setError] = useState("");
  const generation = useRef(0);
  const refresh = useCallback(async () => {
    const epoch = ++generation.current;
    try {
      const next = await core.request<Connection>("ai/connection");
      if (epoch === generation.current) {
        setView(next);
        setError("");
      }
      return next;
    } catch {
      if (epoch === generation.current) {
        setView(null);
        setError("Connection unavailable. Try again; preserve the vault.");
      }
      return null;
    }
  }, [core.request]);
  useEffect(() => {
    setView(null);
    setError("");
    if (core.mode !== "ready") return;
    const load = () => {
      void refresh();
    };
    load();
    window.addEventListener("ai-connection-changed", load);
    window.addEventListener("focus", load);
    const timer = window.setInterval(load, 15000);
    return () => {
      generation.current++;
      clearInterval(timer);
      window.removeEventListener("ai-connection-changed", load);
      window.removeEventListener("focus", load);
    };
  }, [refresh, core.mode, core.instanceId]);
  return { view, setView, error, setError, refresh, core };
}
export function ConnectionPicker({
  value,
  onChange,
}: {
  value: string;
  onChange: (ref: string) => void;
}) {
  const { t: uiText } = useUI();
  const { view, refresh } = useAIConnection();
  return (
    <label>
      <span>{uiText("Provider")}</span>
      <select
        value={value}
        aria-label={uiText("Provider")}
        onChange={(e) => onChange(e.target.value)}
        onFocus={() => void refresh()}
      >
        <option value="">{uiText("No AI")}</option>
        {(view?.connections ?? (view?.ref ? [view] : [])).map((account) => (
          <option
            key={account.ref}
            value={account.ref}
            disabled={account.locked || account.planPaused}
          >
            {account.name ||
              view?.catalog?.providers.find((p) => p.id === account.provider)
                ?.name ||
              account.provider ||
              uiText("Unavailable")}{" "}
            ·{" "}
            {uiText(
              account.authMethod === "oauth"
                ? "Google"
                : account.authMethod === "sign-in"
                  ? "ChatGPT"
                  : account.authMethod === "local"
                    ? "Server"
                    : "API key",
            )}
            {(account.planPaused || account.locked) &&
              ` · ${uiText(account.planPaused ? "Paused" : "Locked")}`}
          </option>
        ))}
        {value &&
          !(view?.connections ?? (view?.ref ? [view] : [])).some(
            (a) => a.ref === value,
          ) && (
            <option value={value} disabled>
              {uiText("Previous connection unavailable")}
            </option>
          )}
      </select>
    </label>
  );
}
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
  const { setProvidersOpen, agents } = useWorkspace();
  const assignedAccount =
    agents.view?.profiles.find((p) => p.id === agents.view?.profileId)
      ?.connectionRef || "";
  const [search, setSearch] = useState("");
  const pageHeading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    pageHeading.current?.focus({ preventScroll: true });
    window.scrollTo(0, 0);
  }, []);

  const [selected, setSelected] = useState(startNew ? "new" : assignedAccount);
  const selectionChosen = useRef(false);
  useEffect(() => {
    if (!selectionChosen.current && assignedAccount)
      setSelected(assignedAccount);
  }, [assignedAccount]);
  const [newProvider, setNewProvider] = useState("openai");
  const [newMethod, setNewMethod] = useState("api-key");
  const [callback, setCallback] = useState("");
  const [googleProject, setGoogleProject] = useState("");
  useEffect(() => {
    if (selected && selected !== "new")
      setGoogleProject(
        root?.connections?.find((a) => a.ref === selected)?.googleProject || "",
      );
  }, [selected, root?.connections]);
  const [name, setName] = useState("");
  const savedName =
    selected && selected !== "new"
      ? root?.connections?.find((a) => a.ref === selected)?.name || ""
      : root?.name || "";
  useEffect(() => {
    setName(selected === "new" ? "" : savedName);
  }, [selected, savedName]);

  const view =
    selected === "new" || (!root?.ref && !root?.connections?.length)
      ? root
        ? {
            ...root,
            provider: newProvider,
            authMethod: newMethod as "api-key" | "sign-in" | "local" | "oauth",
            ref: "",
            locked: true,
            planPaused: false,
            planFailure: "",
            taskAccess: undefined,
          }
        : null
      : selected
        ? (root?.connections?.find((a) => a.ref === selected) ?? null)
        : root;
  const [key, setKey] = useState("");
  const [endpoint, setEndpoint] = useState(defaultLocalEndpoint);
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
        : defaultLocalEndpoint,
    );
  }, [
    core.instanceId,
    selected,
    isLocal,
    view?.ref,
    view?.locked,
    view?.endpoint,
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
  async function change(action: string) {
    if (!view) {
      setError(uiText("Previous connection unavailable"));
      return;
    }
    selectionChosen.current = true;
    setBusy(true);
    setError("");
    const secret = passphrase,
      credential = key;
    setKey("");
    setPassphrase("");
    try {
      const command = {
        action: action === "save" && selected === "new" ? "add" : action,
        name: action === "save" ? name : undefined,
        ref: view?.ref ?? "",
        ...(["save", "unlock", "remove"].includes(action)
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
              ...(provider === "google" ? { googleProject } : {}),
              name,
              remote: core.connection?.location === "remote",
            }
          : action === "sign-in-complete"
            ? {
                action,
                signInId: signIn?.id,
                callback: callback || undefined,
                passphrase: secret,
              }
            : action === "sign-in-cancel"
              ? { action, signInId: signIn?.id }
              : { action, ref: view.ref, passphrase: secret };
      const next = await core.request<Connection>(
        "ai/connection",
        "POST",
        action.startsWith("sign-in-") ? signInCommand : command,
      );
      if (action !== "sign-in-start" && action !== "sign-in-cancel") {
        setSelected(next.ref);
        setCallback("");
      }
      setView(await core.request<Connection>("ai/connection"));
      window.dispatchEvent(new Event("ai-connection-changed"));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Credential operation failed");
    } finally {
      setBusy(false);
    }
  }
  function chooseProvider(id: string) {
    selectionChosen.current = true;
    setSelected("new");
    setNewProvider(id);
    setGoogleProject("");
    setNewMethod(id === "local" ? "local" : "api-key");
    setName("");
    setKey("");
    setPassphrase("");
    setCallback("");
  }
  const visibleProviders =
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
    ) || [];
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
              {error || uiText("Loading…")}
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
                    <span>
                      {a.name || p.name}
                      <small>
                        {p.family &&
                          `${uiText(root?.catalog?.providers.find((x) => x.id === a.provider)?.route || "")} · `}
                        {uiText(
                          a.authMethod === "oauth"
                            ? "Google"
                            : a.authMethod === "sign-in"
                              ? "ChatGPT"
                              : a.authMethod === "local"
                                ? "Server"
                                : "API key",
                        )}
                      </small>
                    </span>
                    {a.locked && <LockKeyhole size={14} />}
                  </button>
                ))}
            </div>
          ))}
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
            <button
              className="shell-icon"
              type="button"
              aria-label={uiText("Add provider")}
              title={uiText("Add provider")}
              disabled={busy || !!signIn || root?.pilotEnrollment}
              onClick={() => {
                setSelected("new");
                setName("");
                setNewMethod(newProvider === "local" ? "local" : "api-key");
                setCallback("");
                setKey("");
                setEndpoint(defaultLocalEndpoint);
                setPassphrase("");
              }}
            >
              <Plus size={16} />
            </button>
            <button
              type="button"
              className="shell-icon"
              aria-label={uiText("Refresh models")}
              title={uiText("Refresh models")}
              disabled={busy || !view?.ref || view.locked}
              onClick={() => void change("models-refresh")}
            >
              <RefreshCw size={16} />
            </button>
          </div>
          <div className="agent-record-body">
            {!view?.ref && regionalProviders.length > 1 && (
              <div className="provider-methods" aria-label={uiText("Region")}>
                {regionalProviders.map((p) => (
                  <button
                    key={p.id}
                    type="button"
                    className="secondary"
                    aria-pressed={provider === p.id}
                    disabled={busy || !!signIn}
                    onClick={() => chooseProvider(p.id)}
                  >
                    {uiText(p.route || p.name)}
                  </button>
                ))}
              </div>
            )}
            {!view?.ref && hasSignIn && (
              <div
                className="provider-methods"
                aria-label={uiText("Connection")}
              >
                <button
                  type="button"
                  className="secondary"
                  aria-pressed={newMethod === "sign-in"}
                  disabled={busy || !!signIn || !signInAvailable}
                  onClick={() => {
                    setNewMethod("sign-in");
                    setKey("");
                    setPassphrase("");
                  }}
                >
                  {uiText("Sign in")}
                </button>
                <button
                  type="button"
                  className="secondary"
                  aria-pressed={newMethod === "api-key"}
                  disabled={busy || !!signIn}
                  onClick={() => {
                    setNewMethod("api-key");
                    setKey("");
                    setPassphrase("");
                    setCallback("");
                  }}
                >
                  {uiText("API key")}
                </button>
              </div>
            )}
            {isLocal && !view?.ref && (
              <div
                className="provider-methods provider-servers"
                aria-label={uiText("Server")}
              >
                {servers.map((s) => (
                  <button
                    key={s.id}
                    type="button"
                    className="secondary"
                    aria-pressed={server?.id === s.id}
                    onClick={() => {
                      setEndpoint(s.endpoint);
                      setKey("");
                    }}
                    disabled={busy}
                  >
                    {s.name}
                  </button>
                ))}
              </div>
            )}
            <p className="connection-status" role="status">
              {view?.ref
                ? `${uiText(view.planPaused ? "Paused" : view.locked ? "Locked" : "Unlocked")} · ${providerName}`
                : uiText(
                    selected && selected !== "new"
                      ? "Unavailable"
                      : "No connection saved",
                  )}
            </p>
            {view?.planPaused && (
              <p role="status">
                {uiText(
                  pausedFailure?.summary ||
                    (view.provider === "local"
                      ? "Local model request failed"
                      : "ChatGPT request failed"),
                )}
              </p>
            )}
            {error && <p role="alert">{error}</p>}
            {root && selected && selected !== "new" && !view && (
              <p role="alert">{uiText("Previous connection unavailable")}</p>
            )}

            {view?.catalog?.state === "failed" && (
              <p role="alert">{uiText("Models unavailable")}</p>
            )}
            {view?.ref && <OpenAIPilotSettings connection={view} />}
            {view?.ref && view.provider !== "kimi-code" && (
              <TaskAccessSettings
                key={`${view.ref}:${core.instanceId}`}
                connection={view}
                onChanged={() =>
                  window.dispatchEvent(new Event("ai-connection-changed"))
                }
              />
            )}
            <details
              className="connection-options"
              key={selected || view?.ref || "new"}
              open={!view?.ref}
            >
              <summary>{uiText("Credentials")}</summary>
              <form
                onSubmit={(e) => {
                  e.preventDefault();
                  void change(
                    method === "sign-in" ? "sign-in-complete" : "save",
                  );
                }}
                autoComplete="off"
              >
                <label>
                  {uiText("Name")}
                  <input
                    value={name}
                    maxLength={80}
                    onChange={(e) => setName(e.target.value)}
                  />
                </label>
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
                {method !== "sign-in" && (
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
                  <details>
                    <summary>{uiText("Options")}</summary>
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
                  </details>
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
                {method === "sign-in" && signIn && (
                  <>
                    <p role="status">
                      {uiText(
                        signIn.state === "callback-ready"
                          ? "Ready"
                          : "Waiting for sign-in",
                      )}
                    </p>
                    <a
                      className="secondary"
                      href={signIn.url}
                      target="_blank"
                      rel="noopener noreferrer"
                    >
                      {uiText("Open sign-in")}
                    </a>
                    {signIn.remote && signIn.provider !== "google" && (
                      <label>
                        {uiText(
                          signIn.provider === "openrouter"
                            ? "Code"
                            : "Redirect URL",
                        )}
                        <input
                          aria-label={uiText(
                            signIn.provider === "openrouter"
                              ? "Code"
                              : "Redirect URL",
                          )}
                          value={callback}
                          maxLength={4096}
                          autoComplete="off"
                          onChange={(e) => setCallback(e.target.value)}
                        />
                      </label>
                    )}
                  </>
                )}
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
                <div className="agent-actions">
                  {method === "sign-in" ? (
                    <>
                      {!signIn ? (
                        <button
                          type="button"
                          className="primary"
                          disabled={
                            busy ||
                            !view ||
                            !signInAvailable ||
                            (provider === "google" &&
                              !view.ref &&
                              !/^[a-z][a-z0-9-]{4,28}[a-z0-9]$/.test(
                                googleProject,
                              ))
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
                      ) : (
                        <>
                          <button
                            className="primary"
                            disabled={
                              busy ||
                              passphrase.length < 12 ||
                              (signIn.remote && signIn.provider !== "google"
                                ? !callback
                                : signIn.state !== "callback-ready")
                            }
                          >
                            {uiText("Finish")}
                          </button>
                          <button
                            type="button"
                            className="secondary"
                            disabled={busy}
                            onClick={() => void change("sign-in-cancel")}
                          >
                            {uiText("Cancel")}
                          </button>
                        </>
                      )}
                      {view?.ref && !signIn && (
                        <>
                          <button
                            type="button"
                            className="secondary"
                            disabled={busy || passphrase.length < 12}
                            onClick={() => void change("sign-in-refresh")}
                          >
                            {uiText("Refresh")}
                          </button>
                          <button
                            type="button"
                            className="secondary"
                            disabled={busy || passphrase.length < 12}
                            onClick={() => void change("sign-in-disconnect")}
                          >
                            {uiText("Disconnect")}
                          </button>
                        </>
                      )}
                    </>
                  ) : (
                    <button
                      className="primary"
                      disabled={
                        busy ||
                        !view ||
                        (isLocal ? !endpoint : !key) ||
                        passphrase.length < 12 ||
                        (view.pilotEnrollment && !!view.ref)
                      }
                    >
                      {view?.ref ? uiText("Replace") : uiText("Save")}
                    </button>
                  )}
                  {(view?.ref || (selected && selected !== "new")) && (
                    <>
                      <button
                        type="button"
                        className="secondary"
                        disabled={busy || !view?.ref || passphrase.length < 12}
                        onClick={() => void change("unlock")}
                      >
                        {uiText(view?.planPaused ? "Review access" : "Unlock")}
                      </button>
                      <button
                        type="button"
                        className="secondary"
                        disabled={busy || !view?.ref}
                        onClick={() => void change("lock")}
                      >
                        {uiText("Lock")}
                      </button>
                      {method !== "sign-in" && (
                        <button
                          type="button"
                          className="secondary"
                          disabled={
                            busy || !view?.ref || passphrase.length < 12
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
              <details className="connection-inspection">
                <summary>{uiText("Recovery")}</summary>
                {view?.planFailure && (
                  <details>
                    <summary>{uiText("Provider response")}</summary>
                    <pre>{pausedFailure?.details || view.planFailure}</pre>
                  </details>
                )}
                <p>
                  {uiText(
                    "Encrypted with your separate vault passphrase. Unlock lasts 15 minutes; lock, sign-out and restart end it. An unlocked key is accessible to the running core.",
                  )}
                </p>
                <p>
                  {uiText(
                    "Metadata backups exclude credentials. Keep the vault and passphrase; owner password recovery cannot recover them. Reconnecting preserves request records and budget holds.",
                  )}
                </p>
                <p>
                  {uiText("Connection: ")}
                  {view?.ref || uiText("None")}
                </p>
              </details>
            </details>
          </div>
          {view?.ref && (
            <section className="provider-models" aria-label={uiText("Models")}>
              <header>
                <h3>{uiText("Models")}</h3>
              </header>
              <ul>
                {view.catalog?.models
                  .filter(
                    (m) =>
                      m.connectionRef === view.ref && m.provider === provider,
                  )
                  .map((m) => (
                    <li key={m.id}>
                      <span>{m.name}</span>
                      <small>
                        {uiText(
                          m.selectable
                            ? m.recommended
                              ? "Recommended"
                              : "Ready"
                            : m.availability === "currency-unsupported"
                              ? "Pricing unavailable"
                              : "Unavailable",
                        )}
                      </small>
                    </li>
                  ))}
              </ul>
            </section>
          )}
        </section>
      </div>
    </section>
  );
}

export function AIModelPicker({
  value,
  onChange,
  connectionRef,
  allowDemo = true,
  disabled = false,
}: {
  value: string;
  onChange: (v: string) => void;
  connectionRef: string;
  allowDemo?: boolean;
  disabled?: boolean;
}) {
  const { t: uiText } = useUI();
  const { view } = useAIConnection();
  const selected = connectionRef
    ? (view?.connections?.find((a) => a.ref === connectionRef) ??
      (view?.ref === connectionRef ? view : null))
    : view;
  const models =
    selected?.catalog?.version === 1 ? selected.catalog.models : [];
  return (
    <label>
      <span>{uiText("Model")}</span>
      <select
        aria-label={uiText("Model")}
        disabled={disabled}
        value={value}
        onChange={(e) => onChange(e.target.value)}
      >
        {allowDemo &&
          (!connectionRef ||
            selected?.provider === "openai" ||
            !selected?.provider) && <option value="">{uiText("No AI")}</option>}
        {!allowDemo && !value && (
          <option value="" disabled>
            {uiText("Model")}
          </option>
        )}
        {value && !models.some((model) => model.id === value) && (
          <option value={value} disabled>
            {uiText("Unavailable model: ")}
            {value}
          </option>
        )}
        {[true, false].map((recommended) => {
          const group = models.filter((m) => m.recommended === recommended);
          if (!group.length) return null;
          return (
            <optgroup
              key={String(recommended)}
              label={uiText(recommended ? "Recommended" : "All models")}
            >
              {group.map((model) => (
                <option
                  key={model.id}
                  value={model.id}
                  disabled={
                    !connectionRef ||
                    !model.selectable ||
                    model.connectionRef !== connectionRef
                  }
                  title={model.selectable ? model.id : uiText("Unavailable")}
                >
                  {model.name}
                </option>
              ))}
            </optgroup>
          );
        })}
      </select>
    </label>
  );
}
export function AIAdmissionReview({
  policy,
  admission,
}: {
  policy?: OpenAIModelPolicy;
  admission?: OpenAIAdmission;
}) {
  const { t: uiText } = useUI();
  if (!policy) return null;
  if (isUnmeteredPolicy(policy))
    return (
      <details className="ai-admission">
        <summary>{uiText("Usage")}</summary>
        <p>
          {uiText(
            policy.model === "gemma4:cloud"
              ? "Ollama Cloud"
              : policy.provider === "local"
                ? "Local"
                : policy.provider === "ollama-cloud" ||
                    policy.model === "gemma4:cloud"
                  ? "Ollama Cloud"
                  : policy.provider === "kimi-code"
                    ? "Kimi Code"
                    : "ChatGPT",
          )}{" "}
          · {policy.model}
        </p>
        {admission && (
          <dl>
            <dt>{uiText("Input bound")}</dt>
            <dd>{admission.inputTokens}</dd>
            <dt>{uiText("Admission: ")}</dt>
            <dd>{admission.state}</dd>
          </dl>
        )}
      </details>
    );
  if (policy.wireVersion === "openai-signin-v1")
    return (
      <details className="ai-admission">
        <summary>{uiText("Model")}</summary>
        <p>{policy.model}</p>
        <p>{uiText("Fixture allowance")}</p>
        {admission && (
          <dl>
            <dt>{uiText("Input bound")}</dt>
            <dd>{admission.inputTokens}</dd>
            <dt>{uiText("Admission: ")}</dt>
            <dd>{admission.state}</dd>
          </dl>
        )}
      </details>
    );
  const price = (value: number) => `$${(value / 1000000).toFixed(2)}`;
  return (
    <details className="ai-admission">
      <summary>{uiText("Model and tariff admission")}</summary>
      <p>
        {uiText("Pinned snapshot: ")}
        {policy.model}
        {uiText(" · Standard tier · Text only · No tools.")}
      </p>
      <p>
        {uiText("USD per million tokens: input ")}
        {price(policy.inputMicrousdPerMillion)}
        {uiText(", cached input ")}
        {price(policy.cachedMicrousdPerMillion)}
        {uiText(", output")} {price(policy.outputMicrousdPerMillion)}
        {uiText(
          ". Reserve the uncached ceiling with upward integer rounding. Accounting is not a provider invoice.",
        )}
      </p>
      {!!policy.billableOutputTokens && (
        <p title={uiText("Includes reasoning tokens")}>
          {uiText("Output reserve")}:{" "}
          {policy.billableOutputTokens.toLocaleString()} {uiText("tokens")}
        </p>
      )}
      <p>
        {uiText("Policy ")}
        {policy.id}
        {uiText(" · Reviewed")}{" "}
        {new Date(policy.reviewedAt * 1000).toLocaleDateString()}
        {uiText(" · Expires")}{" "}
        {new Date(policy.expiresAt * 1000).toLocaleString(
          typeof document === "undefined"
            ? "en"
            : document.documentElement.lang,
        )}
        .
      </p>
      <p>
        {uiText(
          "Owner consent covers model visibility and input-token preflight using the same scoped text, instructions and output schema, then one bounded response. Model visibility does not guarantee billing, quota or response permission. Each run checks again; no cache or fallback.",
        )}
      </p>
      <p>
        {uiText(
          "All three API operations are simulated in this build. Test credentials only; real calls stay disabled. The compiled fixture returns a simulated count of 256 tokens, not a local tokenizer.",
        )}
      </p>
      {admission && (
        <>
          <p>
            {uiText("Admission: ")}
            {admission.state}
            {uiText(" · Counted ")}
            {admission.inputTokens}
            {uiText(" input tokens · Response creation attempted:")}{" "}
            {admission.generationAttempted ? uiText("yes") : uiText("no")}.
          </p>
          <p>
            {uiText("Exact request SHA-256: ")}
            {admission.wireSha256}
          </p>
          <p>
            {uiText("Counted payload SHA-256: ")}
            {admission.countPayloadSha256}
          </p>
        </>
      )}
    </details>
  );
}
