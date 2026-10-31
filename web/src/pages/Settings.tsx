import {
  InstanceSummary,
  RemoteConnection,
} from "../components/InstanceConnection";
import { AppearanceSettings, useUI } from "../ui-preferences";
import { ArrowRight } from "../icons";
import { UpdateStatusPanel } from "../components/UpdateStatusPanel";
import { ApplicationUpdatesPanel } from "../components/ApplicationUpdatesPanel";
import { Badge, SectionHead } from "../components/shared";
import { useWorkspace } from "../workspace";
export function Settings() {
  const { t: uiText } = useUI();
  const t = uiText;
  const { core, state, setSetupOpen, setResetOpen, setProvidersOpen } =
    useWorkspace();
  return (
    <>
      <SectionHead title={t("Settings")} />
      <div className="settings-workspace">
        <div className="settings-primary">
          <section className="panel">
            <div className="panel-heading">
              <h2>{t("Preferences")}</h2>
            </div>
            <AppearanceSettings />
          </section>
          <section className="panel">
            <div className="panel-heading">
              <h2>{uiText("Instance")}</h2>
              <Badge>
                {core.mode === "ready"
                  ? uiText("CONNECTED")
                  : uiText("SIMULATED")}
              </Badge>
            </div>
            <div className="settings-body">
              <InstanceSummary />
              <RemoteConnection />
              {core.mode === "ready" && (
                <details className="instance-diagnostics">
                  <summary>{uiText("Diagnostics")}</summary>
                  <dl>
                    <dt>{uiText("Saved deployment preference")}</dt>
                    <dd data-testid="saved-deployment-preference">
                      {state.setup.host === "vps"
                        ? uiText("Remote")
                        : uiText("Local")}
                    </dd>
                  </dl>
                </details>
              )}
              <button className="secondary" onClick={() => setSetupOpen(true)}>
                {core.mode === "ready"
                  ? uiText("Setup")
                  : uiText("Review setup")}{" "}
                <ArrowRight size={14} />
              </button>
            </div>
          </section>
          {core.mode === "ready" && (
            <button
              className="panel provider-entry"
              onClick={() => setProvidersOpen(true)}
            >
              <span>{uiText("Providers")}</span>
              <ArrowRight size={16} />
            </button>
          )}
          {core.mode !== "ready" && (
            <section className="panel">
              <div className="panel-heading">
                <h2>{uiText("Browser demo data")}</h2>
                <Badge>{uiText("THIS BROWSER ONLY")}</Badge>
              </div>
              <div className="panel-footer">
                <p>
                  {uiText(
                    "Reset clears this browser’s prototype setup, profiles, notes and inbox.",
                  )}
                </p>
                <button
                  className="secondary danger"
                  onClick={() => setResetOpen(true)}
                >
                  {uiText("Reset prototype")}
                </button>
              </div>
            </section>
          )}
        </div>
        <ApplicationUpdatesPanel />
        <details className="panel settings-advanced update-settings">
          <summary>{uiText("Diagnostics")}</summary>
          <UpdateStatusPanel />
        </details>
      </div>
    </>
  );
}
