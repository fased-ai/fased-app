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
  const {
    state,
    core,
    agents,
    dispatch,
    setupOpen,
    setSetupOpen,
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
  return (
    <>
      {setupOpen &&
        (connected ? (
          <Dialog title="Instance" onClose={() => setSetupOpen(false)}>
            <div className="settings-body">
              <InstanceSummary />
              <RemoteConnection />
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
                Open Chat <ArrowRight size={14} />
              </button>
            </div>
          </Dialog>
        ) : (
          <Dialog
            title="Make this workspace yours"
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
                  ? "Preferences are saved on this instance."
                  : "Setup simulation · no services or accounts are created"}
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
                    <h3>Where will your instance run?</h3>
                    <p>
                      Use one instance from a desktop window, browser or CLI.
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
                            {host === "local" ? "Local" : "Remote"}
                          </strong>
                          <span>
                            {host === "local"
                              ? "Simpler setup. New decisions require your computer to be awake."
                              : "Always-on host. Private access and application authentication are required."}
                          </span>
                        </button>
                      ))}
                    </div>
                    {state.setup.host === "vps" && (
                      <div className="info-banner">
                        <Globe2 size={17} />
                        <div>
                          <strong>Private access, before remote use.</strong>
                          <span>
                            The working installer will guide Tailscale
                            authorization or an SSH tunnel. This preview changes
                            no network settings.
                          </span>
                        </div>
                      </div>
                    )}
                  </>
                ))}
              {state.setup.step === 1 && (
                <>
                  <h3>Choose your wallet capability</h3>
                  <p>You can explore first and connect an account later.</p>
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
                        <strong>Manual unlock is the default.</strong>
                        <span>
                          Keys, backup and permission setup are not implemented
                          in P1. No key will be generated here.
                        </span>
                      </div>
                    </div>
                  )}
                </>
              )}
              {state.setup.step === 2 && (
                <>
                  <h3>Add AI when a task needs it</h3>
                  <p>
                    Deterministic tasks can work without an AI account. This
                    step saves a preference only.
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
                            {id === "none" ? "Set up later" : name}
                          </strong>
                          <small>
                            {id === "none"
                              ? "Start without a provider."
                              : "Real authentication belongs to a later phase."}
                          </small>
                        </span>
                        {state.setup.provider === id && <Check size={17} />}
                      </button>
                    ))}
                  </div>
                  <p className="subtle">
                    No API keys, login tokens or subscription credentials are
                    accepted in this prototype.
                  </p>
                </>
              )}
              {state.setup.step === 3 && (
                <>
                  <h3>A clear starting point</h3>
                  <p>
                    Review your preferences before returning to the workspace.
                  </p>
                  <dl className="review-list">
                    <div>
                      <dt>Location</dt>
                      <dd>
                        {state.setup.host === "local" ? "Local" : "Remote"} ·
                        simulated
                      </dd>
                    </div>
                    <div>
                      <dt>Wallet mode</dt>
                      <dd>{walletNames[state.setup.walletMode]}</dd>
                    </div>
                    <div>
                      <dt>AI preference</dt>
                      <dd>{providerNames[state.setup.provider]}</dd>
                    </div>
                    <div>
                      <dt>Execution</dt>
                      <dd>Unavailable · prototype only</dd>
                    </div>
                  </dl>
                  <div className="info-banner">
                    <ShieldCheck size={17} />
                    <div>
                      <strong>No financial authority is created.</strong>
                      <span>
                        Wallet keys, grants, live accounts and provider
                        connections remain unconfigured.
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
                {state.setup.step > 0 ? "Back" : "Explore first"}
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
                  ? "Continue"
                  : connected
                    ? "Save setup preferences"
                    : "Save prototype preferences"}
                <ArrowRight size={15} />
              </button>
            </div>
          </Dialog>
        ))}
      {noticesOpen && (
        <Dialog
          title="Notification inbox"
          onClose={() => setNoticesOpen(false)}
        >
          <div className="notice-tools">
            <p className="subtle">
              Durable prototype events. No external deliveries.
            </p>
            <button
              className="text-button"
              onClick={() => dispatch({ type: "read-notices" })}
            >
              Mark all read <CheckCheck size={14} />
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
          title="Review an action before it runs"
          onClose={() => setActionOpen(false)}
        >
          <div className="setup-body">
            <Badge>SIMULATED · NOT SIGNABLE</Badge>
            <h3>Example allocation review</h3>
            <p>
              Actions become structured records with explicit authority. A chat
              message is not a wallet permission.
            </p>
            <dl className="review-list">
              <div>
                <dt>Profile</dt>
                <dd>{profile.name}</dd>
              </div>
              <div>
                <dt>Requested action</dt>
                <dd>Read and compare a draft strategy</dd>
              </div>
              <div>
                <dt>Funds or destination</dt>
                <dd>None</dd>
              </div>
              <div>
                <dt>Financial permission</dt>
                <dd>Not granted</dd>
              </div>
              <div>
                <dt>Result</dt>
                <dd>Local preview record only</dd>
              </div>
            </dl>
          </div>
          <div className="dialog-footer">
            <button className="secondary" onClick={() => setActionOpen(false)}>
              Cancel
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
              Record preview <Check size={14} />
            </button>
          </div>
        </Dialog>
      )}
      {profileOpen && (
        <Dialog
          title="Create an agent profile"
          onClose={() => setProfileOpen(false)}
        >
          <form
            onSubmit={async (e) => {
              e.preventDefault();
              if (!profileName.trim()) return;
              if (connected) {
                if (
                  await agents.change({
                    action: "profile-create",
                    name: profileName,
                    text: "Custom research profile",
                  })
                ) {
                  setProfileName("");
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
              <label htmlFor="new-profile-name">Profile name</label>
              <input
                id="new-profile-name"
                autoFocus
                required
                maxLength={48}
                value={profileName}
                onChange={(e) => setProfileName(e.target.value)}
                placeholder="e.g. Portfolio Researcher"
              />
              <p className="subtle">
                {agents.error && <span role="alert">{agents.error}</span>}
                Profiles hold their own context and model preference. They do
                not need a separate service or wallet.
              </p>
            </div>
            <div className="dialog-footer">
              <button
                className="secondary"
                type="button"
                onClick={() => setProfileOpen(false)}
              >
                Cancel
              </button>
              <button
                className="primary"
                disabled={
                  !profileName.trim() ||
                  state.profiles.length >= 30 ||
                  agents.busy ||
                  (connected && !agents.view)
                }
              >
                Create profile <Plus size={14} />
              </button>
            </div>
          </form>
        </Dialog>
      )}
      {resetOpen && (
        <Dialog
          title="Reset prototype preferences?"
          onClose={() => setResetOpen(false)}
        >
          <div className="settings-body">
            <p>
              This removes this browser's demo setup, custom profiles, notes and
              inbox events. It affects no wallet, account or external service.
              {connected && " Your setup is preserved."}
            </p>
          </div>
          <div className="dialog-footer">
            <button className="secondary" onClick={() => setResetOpen(false)}>
              Keep preferences
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
              Reset prototype
            </button>
          </div>
        </Dialog>
      )}
    </>
  );
}
