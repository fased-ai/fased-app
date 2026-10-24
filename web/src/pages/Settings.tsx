import {
  InstanceSummary,
  RemoteConnection,
} from "../components/InstanceConnection";
import { ArrowRight } from "../icons";
import { UpdateStatusPanel } from "../components/UpdateStatusPanel";
import { UpdatePreferencesPanel } from "../components/UpdatePreferencesPanel";
import { providerNames } from "../state";
import { Badge, SectionHead } from "../components/shared";
import { useWorkspace } from "../workspace";
export function Settings() {
  const { core, state, setSetupOpen, setResetOpen, notify, navigate } =
    useWorkspace();
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
              <InstanceSummary />
              <RemoteConnection />
              {core.mode === "ready" && (
                <details className="instance-diagnostics">
                  <summary>Diagnostics</summary>
                  <dl>
                    <dt>Saved deployment preference</dt>
                    <dd data-testid="saved-deployment-preference">
                      {state.setup.host === "vps" ? "Remote" : "Local"}
                    </dd>
                  </dl>
                </details>
              )}
              <button className="secondary" onClick={() => setSetupOpen(true)}>
                {core.mode === "ready" ? "Setup" : "Review setup"}{" "}
                <ArrowRight size={14} />
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
        <details className="panel settings-advanced update-settings">
          <summary>Updates and recovery</summary>
          <UpdateStatusPanel />
          <UpdatePreferencesPanel />
        </details>
      </div>
    </>
  );
}
