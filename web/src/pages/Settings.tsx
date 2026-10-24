import { ArrowRight } from "../icons";
import { UpdateStatusPanel } from "../components/UpdateStatusPanel";
import { UpdatePreferencesPanel } from "../components/UpdatePreferencesPanel";
import { providerNames } from "../state";
import { Badge, SectionHead } from "../components/shared";
import { useWorkspace } from "../workspace";
export function Settings() {
  const {
    core,
    state,
    dispatch,
    setSetupOpen,
    setResetOpen,
    notify,
    navigate,
  } = useWorkspace();
  return (
    <>
      <SectionHead title="Settings" />
      <div className="settings-workspace">
        <div className="settings-primary">
          <section className="panel">
            <div className="panel-heading">
              <h2>Instance</h2>
              <Badge>{core.mode === "ready" ? "CONNECTED" : "SIMULATED"}</Badge>
            </div>
            <div className="settings-body">
              <label htmlFor="instance-host">Deployment preference</label>
              <select
                id="instance-host"
                value={state.setup.host}
                onChange={(e) =>
                  dispatch({
                    type: "setup",
                    patch: { host: e.target.value as "local" | "vps" },
                  })
                }
              >
                <option value="local">Local</option>
                <option value="vps">Remote</option>
              </select>
              <p className="subtle">
                {core.mode === "ready"
                  ? "Saved to this instance. This preference does not change the machine or networking."
                  : "This changes a prototype preference, not the machine or its networking."}
              </p>
              <button className="secondary" onClick={() => setSetupOpen(true)}>
                Review setup <ArrowRight size={14} />
              </button>
            </div>
          </section>
          <section className="panel">
            <div className="panel-heading">
              <h2>AI connections</h2>
              <button
                className="text-button"
                onClick={() => navigate("agents/settings")}
              >
                Manage connections <ArrowRight size={14} />
              </button>
            </div>
            {Object.entries(providerNames)
              .filter(([id]) => id !== "none")
              .map(([id, name]) => (
                <div className="provider-row" key={id}>
                  <span className="provider-mark">{name[0]}</span>
                  <div>
                    <strong>{name}</strong>
                  </div>
                  <Badge>PREVIEW</Badge>
                  <button
                    className="secondary"
                    onClick={() =>
                      notify(
                        `${name} connection preview`,
                        "Real authentication belongs to a later phase. This prototype does not accept API keys or subscription credentials.",
                      )
                    }
                  >
                    View setup <ArrowRight size={13} />
                  </button>
                </div>
              ))}
          </section>
          <section className="panel">
            <div className="panel-heading">
              <h2>Browser demo data</h2>
              <Badge>THIS BROWSER ONLY</Badge>
            </div>
            <div className="panel-footer">
              <p>
                {core.mode === "ready"
                  ? "Reset clears browser demo preferences. Saved core records are preserved."
                  : "Reset clears this browser’s prototype setup, profiles, notes and inbox."}
              </p>
              <button
                className="secondary danger"
                onClick={() => setResetOpen(true)}
              >
                Reset prototype
              </button>
            </div>
          </section>
        </div>
        <UpdateStatusPanel />
        <UpdatePreferencesPanel />
      </div>
    </>
  );
}
