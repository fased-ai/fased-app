import {
  AIConnectionSettings,
  AIModelPicker,
  useAIConnection,
} from "../pages/AIConnection";
import { useState } from "react";
import { useUI } from "../ui-preferences";
import { InstanceSummary, RemoteConnection } from "./InstanceConnection";
import {
  ArrowRight,
  Check,
  CheckCheck,
  FlaskConical,
  Globe2,
  LockKeyhole,
  Monitor,
  Plus,
  Server,
  ShieldCheck,
} from "../icons";
import {
  providerNames,
  walletNames,
  type Provider,
  type WalletMode,
} from "../state";
import { Badge, Dialog } from "../components/shared";
import { useWorkspace } from "../workspace";
export function WorkspaceDialogs() {
  const { t: uiText } = useUI();
  const {
    state,
    core,
    agents,
    dispatch,
    setupOpen,
    setSetupOpen,
    providersOpen,
    setProvidersOpen,
    noticesOpen,
    setNoticesOpen,
    actionOpen,
    setActionOpen,
    profileOpen,
    setProfileOpen,
    resetOpen,
    setResetOpen,
    setToast,
    setMessages,
    profileName,
    setProfileName,
    profile,
    navigate,
    notify,
  } = useWorkspace();
  const connected = core.mode === "ready";
  const { view: connections, refresh: refreshConnections } = useAIConnection();
  const [newAccount, setNewAccount] = useState("");
  const [newModel, setNewModel] = useState("");
  const [instructions, setInstructions] = useState("");
  const [resumeAgent, setResumeAgent] = useState(false);
  const accounts =
    connections?.connections ?? (connections?.ref ? [connections] : []);
  const account = accounts.find((item) => item.ref === newAccount);
  const accountProvider = account?.provider;
  const creationProvider =
    accountProvider === "openai" ||
    accountProvider === "anthropic" ||
    accountProvider === "xai"
      ? accountProvider
      : undefined;
  const modelReady =
    !!creationProvider &&
    account?.catalog?.models.some(
      (item) =>
        item.id === newModel &&
        item.connectionRef === newAccount &&
        item.selectable,
    );
  const closeProviders = () => {
    setProvidersOpen(false);
    if (resumeAgent) {
      setResumeAgent(false);
      setProfileOpen(true);
      void refreshConnections();
    }
  };
  return (
    <>
      {providersOpen && connected && (
        <Dialog title={uiText("Providers")} onClose={closeProviders}>
          <AIConnectionSettings startNew={resumeAgent} />
        </Dialog>
      )}
      {setupOpen &&
        (connected ? (
          <Dialog
            title={uiText("Instance")}
            onClose={() => setSetupOpen(false)}
          >
            <div className="settings-body">
              <InstanceSummary />
              <RemoteConnection />
              <div className="agent-actions">
                <button
                  className="secondary"
                  onClick={() => {
                    setSetupOpen(false);
                    setProvidersOpen(true);
                  }}
                >
                  {uiText("Providers")}
                </button>
                <button
                  className="secondary"
                  onClick={() => {
                    setSetupOpen(false);
                    navigate("agents/settings");
                  }}
                >
                  {uiText("Agents")}
                </button>
                <button
                  className="secondary"
                  onClick={() => {
                    setSetupOpen(false);
                    navigate("wallets/accounts");
                  }}
                >
                  {uiText("Wallets")}
                </button>
              </div>
              {core.error && <p role="alert">{core.error}</p>}
              <button
                className="primary"
                disabled={core.saving}
                onClick={async () => {
                  if (await core.updateSetup({ complete: true, step: 3 })) {
                    setSetupOpen(false);
                    navigate("agents/chat");
                  }
                }}
              >
                {uiText("Open Chat ")}
                <ArrowRight size={14} />
              </button>
            </div>
          </Dialog>
        ) : (
          <Dialog
            title={uiText("Make this workspace yours")}
            onClose={() => setSetupOpen(false)}
            wide
          >
            <div
              className="setup-progress"
              aria-label={`Setup step ${state.setup.step + 1} of 4`}
            >
              {["Location", "Wallet", "AI", "Review"].map((label, i) => (
                <span
                  className={i <= state.setup.step ? "active" : ""}
                  key={label}
                >
                  <i>{i < state.setup.step ? <Check size={12} /> : i + 1}</i>
                  {label}
                </span>
              ))}
            </div>
            <div className="setup-body">
              <p className="prototype-caption">
                <FlaskConical size={14} />
                {connected
                  ? uiText("Preferences are saved on this instance.")
                  : uiText(
                      "Setup simulation · no services or accounts are created",
                    )}
              </p>
              {core.error && connected && (
                <p className="core-error" role="alert">
                  {core.error}
                </p>
              )}
              {state.setup.step === 0 &&
                (connected ? (
                  <>
                    <InstanceSummary />
                    <RemoteConnection />
                  </>
                ) : (
                  <>
                    <h3>{uiText("Where will your instance run?")}</h3>
                    <p>
                      {uiText(
                        "Use one instance from a desktop window, browser or CLI.",
                      )}
                    </p>
                    <div className="choice-grid">
                      {(["local", "vps"] as const).map((host) => (
                        <button
                          className={`choice ${state.setup.host === host ? "selected" : ""}`}
                          key={host}
                          aria-pressed={state.setup.host === host}
                          onClick={() =>
                            dispatch({ type: "setup", patch: { host } })
                          }
                        >
                          {host === "local" ? (
                            <Monitor size={25} />
                          ) : (
                            <Server size={25} />
                          )}
                          <strong>
                            {host === "local"
                              ? uiText("Local")
                              : uiText("Remote")}
                          </strong>
                          <span>
                            {host === "local"
                              ? uiText(
                                  "Simpler setup. New decisions require your computer to be awake.",
                                )
                              : uiText(
                                  "Always-on host. Private access and application authentication are required.",
                                )}
                          </span>
                        </button>
                      ))}
                    </div>
                    {state.setup.host === "vps" && (
                      <div className="info-banner">
                        <Globe2 size={17} />
                        <div>
                          <strong>
                            {uiText("Private access, before remote use.")}
                          </strong>
                          <span>
                            {uiText(
                              "The working installer will guide Tailscale authorization or an SSH tunnel. This preview changes no network settings.",
                            )}
                          </span>
                        </div>
                      </div>
                    )}
                  </>
                ))}
              {state.setup.step === 1 && (
                <>
                  <h3>{uiText("Choose your wallet capability")}</h3>
                  <p>
                    {uiText(
                      "You can explore first and connect an account later.",
                    )}
                  </p>
                  <div className="wallet-choices">
                    {(
                      [
                        "skip",
                        "watch",
                        "external",
                        "automation",
                      ] as WalletMode[]
                    ).map((mode) => (
                      <button
                        key={mode}
                        className={`choice compact ${state.setup.walletMode === mode ? "selected" : ""}`}
                        aria-pressed={state.setup.walletMode === mode}
                        onClick={() =>
                          dispatch({
                            type: "setup",
                            patch: { walletMode: mode },
                          })
                        }
                      >
                        <span>
                          <strong>{walletNames[mode]}</strong>
                          <small>
                            {
                              {
                                skip: "No wallet required to explore.",
                                watch:
                                  "Read-only. No key or signing authority.",
                                external:
                                  "Approve supported actions in your own wallet.",
                                automation:
                                  "Dedicated operating key in the isolated signer.",
                              }[mode]
                            }
                          </small>
                        </span>
                        {state.setup.walletMode === mode && <Check size={17} />}
                      </button>
                    ))}
                  </div>
                  {state.setup.walletMode === "automation" && (
                    <div className="info-banner amber">
                      <LockKeyhole size={17} />
                      <div>
                        <strong>
                          {uiText("Manual unlock is the default.")}
                        </strong>
                        <span>
                          {uiText(
                            "Keys, backup and permission setup are not implemented in P1. No key will be generated here.",
                          )}
                        </span>
                      </div>
                    </div>
                  )}
                </>
              )}
              {state.setup.step === 2 && (
                <>
                  <h3>{uiText("Add AI when a task needs it")}</h3>
                  <p>
                    {uiText(
                      "Deterministic tasks can work without an AI account. This step saves a preference only.",
                    )}
                  </p>
                  <div className="wallet-choices">
                    {Object.entries(providerNames).map(([id, name]) => (
                      <button
                        key={id}
                        className={`choice compact ${state.setup.provider === id ? "selected" : ""}`}
                        aria-pressed={state.setup.provider === id}
                        onClick={() =>
                          dispatch({
                            type: "setup",
                            patch: { provider: id as Provider },
                          })
                        }
                      >
                        <span>
                          <strong>
                            {id === "none" ? uiText("Set up later") : name}
                          </strong>
                          <small>
                            {id === "none"
                              ? uiText("Start without a provider.")
                              : uiText(
                                  "Real authentication belongs to a later phase.",
                                )}
                          </small>
                        </span>
                        {state.setup.provider === id && <Check size={17} />}
                      </button>
                    ))}
                  </div>
                  <p className="subtle">
                    {uiText(
                      "No API keys, login tokens or subscription credentials are accepted in this prototype.",
                    )}
                  </p>
                </>
              )}
              {state.setup.step === 3 && (
                <>
                  <h3>{uiText("A clear starting point")}</h3>
                  <p>
                    {uiText(
                      "Review your preferences before returning to the workspace.",
                    )}
                  </p>
                  <dl className="review-list">
                    <div>
                      <dt>{uiText("Location")}</dt>
                      <dd>
                        {state.setup.host === "local"
                          ? uiText("Local")
                          : uiText("Remote")}
                        {uiText(" · simulated")}
                      </dd>
                    </div>
                    <div>
                      <dt>{uiText("Wallet mode")}</dt>
                      <dd>{walletNames[state.setup.walletMode]}</dd>
                    </div>
                    <div>
                      <dt>{uiText("AI preference")}</dt>
                      <dd>{providerNames[state.setup.provider]}</dd>
                    </div>
                    <div>
                      <dt>{uiText("Execution")}</dt>
                      <dd>{uiText("Unavailable · prototype only")}</dd>
                    </div>
                  </dl>
                  <div className="info-banner">
                    <ShieldCheck size={17} />
                    <div>
                      <strong>
                        {uiText("No financial authority is created.")}
                      </strong>
                      <span>
                        {uiText(
                          "Wallet keys, grants, live accounts and provider connections remain unconfigured.",
                        )}
                      </span>
                    </div>
                  </div>
                </>
              )}
            </div>
            <div className="dialog-footer">
              <button
                className="secondary"
                disabled={core.saving}
                onClick={() =>
                  state.setup.step > 0
                    ? dispatch({
                        type: "setup",
                        patch: { step: state.setup.step - 1 },
                      })
                    : setSetupOpen(false)
                }
              >
                {state.setup.step > 0
                  ? uiText("Back")
                  : uiText("Explore first")}
              </button>
              <button
                className="primary"
                disabled={core.saving}
                onClick={async () => {
                  if (state.setup.step < 3)
                    dispatch({
                      type: "setup",
                      patch: { step: state.setup.step + 1 },
                    });
                  else {
                    if (connected) {
                      if (!(await core.updateSetup({ complete: true }))) return;
                    } else
                      dispatch({ type: "setup", patch: { complete: true } });
                    setSetupOpen(false);
                    notify(
                      "Setup preferences saved",
                      connected
                        ? "Preferences saved to this instance. No wallet or AI connection was created."
                        : "Only prototype preferences were saved. No wallet, AI connection or service was created.",
                    );
                  }
                }}
              >
                {state.setup.step < 3
                  ? uiText("Continue")
                  : connected
                    ? uiText("Save setup preferences")
                    : uiText("Save prototype preferences")}
                <ArrowRight size={15} />
              </button>
            </div>
          </Dialog>
        ))}
      {noticesOpen && (
        <Dialog
          title={uiText("Notification inbox")}
          onClose={() => setNoticesOpen(false)}
        >
          <div className="notice-tools">
            <p className="subtle">
              {uiText("Durable prototype events. No external deliveries.")}
            </p>
            <button
              className="text-button"
              onClick={() => dispatch({ type: "read-notices" })}
            >
              {uiText("Mark all read ")}
              <CheckCheck size={14} />
            </button>
          </div>
          <div className="notice-list">
            {state.notices.map((n) => (
              <article key={n.id} className={n.read ? "read" : ""}>
                <span className="dot" />
                <div>
                  <strong>{n.title}</strong>
                  <p>{n.detail}</p>
                </div>
              </article>
            ))}
          </div>
        </Dialog>
      )}
      {actionOpen && (
        <Dialog
          title={uiText("Review an action before it runs")}
          onClose={() => setActionOpen(false)}
        >
          <div className="setup-body">
            <Badge>{uiText("SIMULATED · NOT SIGNABLE")}</Badge>
            <h3>{uiText("Example allocation review")}</h3>
            <p>
              {uiText(
                "Actions become structured records with explicit authority. A chat message is not a wallet permission.",
              )}
            </p>
            <dl className="review-list">
              <div>
                <dt>{uiText("Profile")}</dt>
                <dd>{profile.name}</dd>
              </div>
              <div>
                <dt>{uiText("Requested action")}</dt>
                <dd>{uiText("Read and compare a draft strategy")}</dd>
              </div>
              <div>
                <dt>{uiText("Funds or destination")}</dt>
                <dd>{uiText("None")}</dd>
              </div>
              <div>
                <dt>{uiText("Financial permission")}</dt>
                <dd>{uiText("Not granted")}</dd>
              </div>
              <div>
                <dt>{uiText("Result")}</dt>
                <dd>{uiText("Local preview record only")}</dd>
              </div>
            </dl>
          </div>
          <div className="dialog-footer">
            <button className="secondary" onClick={() => setActionOpen(false)}>
              {uiText("Cancel")}
            </button>
            <button
              className="primary"
              onClick={() => {
                setActionOpen(false);
                notify(
                  "Action preview recorded",
                  "An illustrative read-only review was recorded. No permission was granted and no operation was submitted.",
                );
              }}
            >
              {uiText("Record preview ")}
              <Check size={14} />
            </button>
          </div>
        </Dialog>
      )}
      {profileOpen && (
        <Dialog
          title={uiText("New agent")}
          onClose={() => setProfileOpen(false)}
        >
          <form
            onSubmit={async (e) => {
              e.preventDefault();
              if (
                !profileName.trim() ||
                (connected && newAccount && !modelReady)
              )
                return;
              if (connected) {
                if (
                  await agents.change({
                    action: "profile-create",
                    name: profileName,
                    text: instructions,
                    provider: newAccount ? creationProvider : "none",
                    accountRef: newAccount,
                    model: newAccount ? newModel : "",
                  })
                ) {
                  setProfileName("");
                  setNewAccount("");
                  setNewModel("");
                  setInstructions("");
                  setProfileOpen(false);
                  navigate("agents/chat");
                }
                return;
              }
              dispatch({
                type: "add-profile",
                name: profileName,
                role: "Custom research profile",
              });
              setProfileName("");
              setProfileOpen(false);
              navigate("agents/chat");
              notify(
                "Prototype profile created",
                "A local persona was added. No wallet or spending authority was created.",
              );
            }}
          >
            <div className="settings-body">
              <label htmlFor="new-profile-name">{uiText("Name")}</label>
              <input
                id="new-profile-name"
                autoFocus
                required
                maxLength={48}
                value={profileName}
                onChange={(e) => setProfileName(e.target.value)}
                placeholder={uiText("e.g. Portfolio Researcher")}
              />
              {connected && (
                <>
                  <label>
                    {uiText("Account")}
                    <select
                      aria-label={uiText("Account")}
                      value={newAccount}
                      disabled={agents.busy}
                      onFocus={() => void refreshConnections()}
                      onChange={(e) => {
                        const ref = e.target.value;
                        const chosen = accounts.find(
                          (item) => item.ref === ref,
                        );
                        setNewAccount(ref);
                        setNewModel(
                          chosen?.catalog?.models.find(
                            (item) => item.selectable && item.recommended,
                          )?.id || "",
                        );
                      }}
                    >
                      <option value="">{uiText("No AI")}</option>
                      {accounts.map((item) => (
                        <option
                          key={item.ref}
                          value={item.ref}
                          disabled={item.locked}
                        >
                          {item.name ||
                            connections?.catalog?.providers.find(
                              (provider) => provider.id === item.provider,
                            )?.name ||
                            item.provider}{" "}
                          ·{" "}
                          {uiText(
                            item.authMethod === "sign-in"
                              ? "ChatGPT"
                              : "API key",
                          )}
                        </option>
                      ))}
                      {newAccount && !account && (
                        <option value={newAccount} disabled>
                          {uiText("Unavailable")}
                        </option>
                      )}
                    </select>
                  </label>
                  {newAccount && (
                    <AIModelPicker
                      value={newModel}
                      onChange={setNewModel}
                      connectionRef={newAccount}
                      allowDemo={false}
                      disabled={agents.busy}
                    />
                  )}
                  <button
                    type="button"
                    className="text-button"
                    disabled={agents.busy}
                    onClick={() => {
                      setResumeAgent(true);
                      setProfileOpen(false);
                      setProvidersOpen(true);
                    }}
                  >
                    {uiText("Connect provider")}
                  </button>
                  <details>
                    <summary>{uiText("Instructions")}</summary>
                    <textarea
                      aria-label={uiText("Instructions")}
                      maxLength={4000}
                      value={instructions}
                      disabled={agents.busy}
                      onChange={(e) => setInstructions(e.target.value)}
                    />
                  </details>
                </>
              )}
              <p className="subtle">
                {agents.error && <span role="alert">{agents.error}</span>}
              </p>
            </div>
            <div className="dialog-footer">
              <button
                className="secondary"
                type="button"
                onClick={() => setProfileOpen(false)}
              >
                {uiText("Cancel")}
              </button>
              <button
                className="primary"
                disabled={
                  !profileName.trim() ||
                  state.profiles.length >= 30 ||
                  agents.busy ||
                  (connected && (!agents.view || (!!newAccount && !modelReady)))
                }
              >
                {uiText("Create agent")}
                <Plus size={14} />
              </button>
            </div>
          </form>
        </Dialog>
      )}
      {resetOpen && (
        <Dialog
          title={uiText("Reset prototype preferences?")}
          onClose={() => setResetOpen(false)}
        >
          <div className="settings-body">
            <p>
              {uiText(
                "This removes this browser's demo setup, custom profiles, notes and inbox events. It affects no wallet, account or external service.",
              )}
              {connected && uiText(" Your setup is preserved.")}
            </p>
          </div>
          <div className="dialog-footer">
            <button className="secondary" onClick={() => setResetOpen(false)}>
              {uiText("Keep preferences")}
            </button>
            <button
              className="primary"
              onClick={() => {
                dispatch({ type: "reset" });
                setMessages([]);
                setResetOpen(false);
                navigate("overview");
                setToast("Prototype preferences reset");
              }}
            >
              {uiText("Reset prototype")}
            </button>
          </div>
        </Dialog>
      )}
    </>
  );
}
