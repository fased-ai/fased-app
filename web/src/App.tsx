import { ShellNavigation } from "./components/ShellNavigation";
import { UIProvider, useUI } from "./ui-preferences";
import { BookOpen, Check, CheckCheck, MessageSquare, X } from "./icons";
import { providerNames } from "./state";
import { Badge, Avatar, SectionHead } from "./components/shared";
import { useWorkspace, WorkspaceProvider } from "./workspace";
import { WorkspaceDialogs } from "./components/WorkspaceDialogs";
import { Overview } from "./pages/Overview";
import { Wallets } from "./pages/Wallets";
import { Agents } from "./pages/Agents";
import { LocalAgents } from "./pages/LocalAgents";
import { Terminals } from "./pages/Terminals";
import { Settings } from "./pages/Settings";
import { CoreProvider } from "./core";
import { CoreGate } from "./components/CoreGate";
function WorkspaceShell() {
  const { t: uiText } = useUI();
  const { preferences, t } = useUI();
  const {
    state,
    core,
    agents,
    contextOpen,
    setContextOpen,
    storageError,
    toast,
    setToast,
    profile,
    section,
    navigate,
  } = useWorkspace();
  return (
    <>
      <a
        className="skip-link"
        href="#main-content"
        onClick={(e) => {
          e.preventDefault();
          document.getElementById("main-content")?.focus();
        }}
      >
        {t("Skip to workspace")}
      </a>
      <div
        className={`workspace-shell ${preferences.rail ? "rail-visible" : "rail-hidden"} ${contextOpen ? "context-visible" : ""}`}
      >
        <ShellNavigation />
        <div className="shell-body">
          {core.error && core.mode === "ready" && (
            <div className="storage-warning" role="alert">
              {core.error}
            </div>
          )}
          {storageError && (
            <div className="storage-warning" role="alert">
              {uiText(
                "Browser storage is unavailable. Changes last only until this page is closed.",
              )}
            </div>
          )}
          <main id="main-content" tabIndex={-1} className="main-content">
            {core.mode === "ready" && (
              <div className="agent-page" hidden={section !== "agents"}>
                <LocalAgents />
              </div>
            )}
            {section === "overview" ? (
              <Overview />
            ) : section === "wallets" ? (
              <Wallets />
            ) : section === "agents" ? (
              core.mode === "ready" ? null : (
                <Agents />
              )
            ) : section === "terminals" ? (
              <Terminals />
            ) : section === "settings" ? (
              <Settings />
            ) : (
              <>
                <SectionHead title={uiText("Activity")} />
                <section
                  className="panel activity-workspace"
                  aria-label={uiText("Activity records")}
                >
                  <div className="panel-heading">
                    <h2>
                      {core.mode === "ready"
                        ? uiText("Profile activity")
                        : uiText("Recent prototype activity")}
                    </h2>
                    <Badge>
                      {core.mode === "ready"
                        ? uiText("PROFILE ONLY")
                        : uiText("LOCAL ONLY")}
                    </Badge>
                  </div>
                  {core.mode === "ready" && (agents.error || !agents.view) ? (
                    <p
                      className="agent-record-body"
                      role={agents.error ? "alert" : "status"}
                    >
                      {agents.error || uiText("Loading agent activity…")}
                    </p>
                  ) : state.notices.length ? (
                    <ul
                      className="activity-list"
                      aria-label={uiText("Activity events")}
                    >
                      {state.notices.map((n) => (
                        <li className="activity-row" key={n.id}>
                          <span className="activity-icon" aria-hidden="true">
                            <CheckCheck size={17} />
                          </span>
                          <div>
                            <strong>{n.title}</strong>
                            <p>{n.detail}</p>
                          </div>
                          <Badge>
                            {core.mode === "ready"
                              ? n.read
                                ? uiText("READ")
                                : uiText("UNREAD")
                              : uiText("DEMO")}
                          </Badge>
                        </li>
                      ))}
                    </ul>
                  ) : (
                    <p className="agent-record-body">
                      {core.mode === "ready"
                        ? uiText("No activity for this profile.")
                        : uiText("No prototype activity.")}
                    </p>
                  )}
                </section>
              </>
            )}
          </main>
        </div>
        {contextOpen && (
          <aside className="context-panel">
            <div className="panel-heading">
              <h2>{uiText("Workspace context")}</h2>
              <button
                className="icon-button"
                aria-label={uiText("Close workspace context")}
                onClick={() => setContextOpen(false)}
              >
                <X size={17} />
              </button>
            </div>
            <div className="context-inner">
              <Badge tone="lime">{uiText("SELECTED PROFILE")}</Badge>
              <Avatar profile={profile} />
              <h2>{profile.name}</h2>
              <p>{profile.role}</p>
              <div className="context-detail">
                <small>{uiText("AI connection")}</small>
                <strong>{providerNames[profile.provider]}</strong>
                <small>{uiText("Wallet authority")}</small>
                <strong>{uiText("None assigned")}</strong>
                <small>{uiText("Current workspace")}</small>
                <strong>{section}</strong>
              </div>
              <button
                className="secondary"
                onClick={() => navigate("agents/chat")}
              >
                <MessageSquare size={15} />
                {uiText(" Open agent chat")}
              </button>
              <button
                className="text-button"
                onClick={() => navigate("agents/memory")}
              >
                <BookOpen size={14} />
                {uiText(" Inspect memory")}
              </button>
            </div>
          </aside>
        )}
      </div>
      <WorkspaceDialogs />
      {toast && (
        <div className="toast" role="status">
          <Check size={16} />
          {toast}
          <button
            className="icon-button"
            aria-label={uiText("Dismiss notification")}
            onClick={() => setToast("")}
          >
            <X size={14} />
          </button>
        </div>
      )}
    </>
  );
}

export default function App() {
  return (
    <UIProvider>
      <CoreProvider>
        <WorkspaceProvider>
          <CoreGate>
            <WorkspaceShell />
          </CoreGate>
        </WorkspaceProvider>
      </CoreProvider>
    </UIProvider>
  );
}
