import { ArrowRight } from "../icons";
import { UpdateStatusPanel } from "../components/UpdateStatusPanel";
import { UpdatePreferencesPanel } from "../components/UpdatePreferencesPanel";
import { providerNames } from "../state";
import { Badge, SectionHead } from "../components/shared";
import { useWorkspace } from "../workspace";
export function Settings() {
  const { core, state, dispatch, setSetupOpen, setResetOpen, notify } =
    useWorkspace();
  return (
    <>
      <SectionHead
        eyebrow="WORKSPACE PREFERENCES"
        title="Settings"
        description="Connections when you need them. A small, explicit configuration surface."
      />
      <div className="two-col">
        <section className="panel">
          <div className="panel-heading">
            <h2>Instance</h2>
            <Badge>
              {core.mode === "ready" ? "PRIVATE CORE" : "SIMULATED"}
            </Badge>
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
              <option value="local">Local computer</option>
              <option value="vps">Private VPS</option>
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
        <UpdateStatusPanel />
      </div>
      <UpdatePreferencesPanel />
      <section className="panel">
        <div className="panel-heading">
          <h2>AI connections</h2>
          <Badge>OPTIONAL</Badge>
        </div>
        {Object.entries(providerNames)
          .filter(([id]) => id !== "none")
          .map(([id, name]) => (
            <div className="provider-row" key={id}>
              <span className="provider-mark">{name[0]}</span>
              <div>
                <strong>{name}</strong>
                <p>
                  {id === "xai"
                    ? "Optional X / web research through supported API tools"
                    : id === "openai"
                      ? "API first; optional official subscription connector later"
                      : "API or a separately verified official subscription connector"}
                </p>
              </div>
              <Badge>NOT CONNECTED</Badge>
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
          <h2>Prototype data</h2>
          <Badge>THIS BROWSER ONLY</Badge>
        </div>
        <div className="panel-footer">
          <p>
            {core.mode === "ready"
              ? "Profiles, notes and inbox events remain browser demo data. Setup is saved separately in your private core."
              : "Setup choices, profiles, notes and inbox events are saved locally."}
            Chat messages are temporary. Do not enter sensitive information.
          </p>
          <button
            className="secondary danger"
            onClick={() => setResetOpen(true)}
          >
            Reset prototype
          </button>
        </div>
      </section>
    </>
  );
}
