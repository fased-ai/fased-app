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
              Browser storage is unavailable. Changes last only until this page
              is closed.
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
                <SectionHead title="Activity" />
                <section
                  className="panel activity-workspace"
                  aria-label="Activity records"
                >
                  <div className="panel-heading">
                    <h2>
                      {core.mode === "ready"
                        ? "Profile activity"
                        : "Recent prototype activity"}
                    </h2>
                    <Badge>
                      {core.mode === "ready" ? "PROFILE ONLY" : "LOCAL ONLY"}
                    </Badge>
                  </div>
                  {core.mode === "ready" && (agents.error || !agents.view) ? (
                    <p
                      className="agent-record-body"
                      role={agents.error ? "alert" : "status"}
                    >
                      {agents.error || "Loading agent activity…"}
                    </p>
                  ) : state.notices.length ? (
                    <ul className="activity-list" aria-label="Activity events">
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
                                ? "READ"
                                : "UNREAD"
                              : "DEMO"}
                          </Badge>
                        </li>
                      ))}
                    </ul>
                  ) : (
                    <p className="agent-record-body">
                      {core.mode === "ready"
                        ? "No activity for this profile."
                        : "No prototype activity."}
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
              <h2>Workspace context</h2>
              <button
                className="icon-button"
                aria-label="Close workspace context"
                onClick={() => setContextOpen(false)}
              >
                <X size={17} />
              </button>
            </div>
            <div className="context-inner">
              <Badge tone="lime">SELECTED PROFILE</Badge>
              <Avatar profile={profile} />
              <h2>{profile.name}</h2>
              <p>{profile.role}</p>
              <div className="context-detail">
                <small>AI connection</small>
                <strong>{providerNames[profile.provider]}</strong>
                <small>Wallet authority</small>
                <strong>None assigned</strong>
                <small>Current workspace</small>
                <strong>{section}</strong>
              </div>
              <button
                className="secondary"
                onClick={() => navigate("agents/chat")}
              >
                <MessageSquare size={15} /> Open agent chat
              </button>
              <button
                className="text-button"
                onClick={() => navigate("agents/memory")}
              >
                <BookOpen size={14} /> Inspect memory
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
            aria-label="Dismiss notification"
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
