import {
  InstanceSummary,
  RemoteConnection,
} from "../components/InstanceConnection";
import { ArrowRight } from "../icons";
import { UpdateStatusPanel } from "../components/UpdateStatusPanel";
import { UpdatePreferencesPanel } from "../components/UpdatePreferencesPanel";
import { AIConnectionSettings } from "./AIConnection";
import { Badge, SectionHead } from "../components/shared";
import { useWorkspace } from "../workspace";
export function Settings() {
  const { core, state, setSetupOpen, setResetOpen } = useWorkspace();
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
          {core.mode === "ready" && <AIConnectionSettings />}
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
