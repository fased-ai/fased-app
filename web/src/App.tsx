import logo from "./assets/fased-logo.svg";
import {
  ArrowUpRight,
  Bell,
  BookOpen,
  Check,
  CheckCheck,
  FlaskConical,
  Menu,
  MessageSquare,
  Monitor,
  PanelRightClose,
  PanelRightOpen,
  Plus,
  Search,
  Server,
  Settings2,
  X,
} from "./icons";
import { providerNames } from "./state";
import { Badge, Avatar, SectionHead } from "./components/shared";
import { navigation } from "./fixtures";
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
  const {
    state,
    core,
    agents,
    setNoticesOpen,
    setProfileOpen,
    contextOpen,
    setContextOpen,
    sidebarOpen,
    setSidebarOpen,
    query,
    setQuery,
    storageError,
    toast,
    setToast,
    profile,
    section,
    unread,
    navigate,
    searchItems,
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
        Skip to workspace
      </a>
      <div className={`app ${contextOpen ? "context-visible" : ""}`}>
        {sidebarOpen && (
          <button
            className="sidebar-backdrop"
            aria-label="Close navigation"
            onClick={() => setSidebarOpen(false)}
          />
        )}
        <aside className={`sidebar ${sidebarOpen ? "open" : ""}`}>
          <button
            className="brand"
            onClick={() => navigate("overview")}
            aria-label="Fased overview"
          >
            <img src={logo} alt="" width="26" height="26" /> Fased
          </button>
          <nav aria-label="Main navigation">
            {navigation.map((n) => (
              <a
                href={`#${n.path}`}
                key={n.title}
                aria-current={
                  section === n.path.split("/")[0] ? "page" : undefined
                }
                className={section === n.path.split("/")[0] ? "active" : ""}
              >
                <n.icon size={18} />
                {n.title}
                {n.title === "Agents" && (
                  <span className="nav-count">{state.profiles.length}</span>
                )}
              </a>
            ))}
          </nav>
          <button
            className="icon-button"
            aria-label="Create agent profile"
            onClick={() => setProfileOpen(true)}
          >
            <Plus size={16} />
          </button>
          <div className="sidebar-bottom">
            <button
              className={section === "settings" ? "active" : ""}
              onClick={() => navigate("settings")}
            >
              <Settings2 size={17} /> Settings
            </button>
          </div>
        </aside>
        <div className="app-body">
          <header className="topbar">
            <div className="flex">
              <button
                className="icon-button menu-button"
                aria-label="Open navigation"
                onClick={() => setSidebarOpen(true)}
              >
                <Menu size={19} />
              </button>
              <span className="mobile-brand">
                <img src={logo} alt="" width="23" height="23" />
                Fased
              </span>
              <span className="breadcrumb">
                <strong>
                  {section === "agents"
                    ? "Agents"
                    : section === "wallets"
                      ? "Wallets"
                      : section === "terminals"
                        ? "Terminals"
                        : section === "settings"
                          ? "Settings"
                          : section === "activity"
                            ? "Activity"
                            : "Overview"}
                </strong>
              </span>
            </div>
            <div className="topbar-actions">
              <span className="fixture-indicator">
                <FlaskConical size={12} /> Fixture
              </span>
              {core.mode === "ready" && (
                <button
                  className="text-button"
                  disabled={core.saving}
                  onClick={() => void core.logout()}
                >
                  Sign out
                </button>
              )}
              <div className="instance-pill" aria-label="Instance location">
                {state.setup.host === "local" ? (
                  <Monitor size={13} />
                ) : (
                  <Server size={13} />
                )}
                <span>{state.setup.host === "local" ? "Local" : "Remote"}</span>
                <span className="dot" />
              </div>
              <div className="search-wrap">
                <Search size={15} />
                <input
                  aria-label="Search workspace"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder="Search workspace"
                />
                {query && (
                  <div className="search-results">
                    {searchItems.length ? (
                      searchItems.map((item) => (
                        <button
                          key={item.path}
                          onClick={() => navigate(item.path)}
                        >
                          {item.title}
                          <ArrowUpRight size={13} />
                        </button>
                      ))
                    ) : (
                      <p>No matching workspace.</p>
                    )}
                  </div>
                )}
              </div>
              <button
                className="icon-button notification-button"
                aria-label={`Notifications, ${unread} unread`}
                onClick={() => setNoticesOpen(true)}
              >
                <Bell size={18} />
                {unread > 0 && <span />}
              </button>
              <button
                className="icon-button context-toggle"
                aria-label={
                  contextOpen ? "Close context panel" : "Open context panel"
                }
                onClick={() => setContextOpen(!contextOpen)}
              >
                {contextOpen ? (
                  <PanelRightClose size={18} />
                ) : (
                  <PanelRightOpen size={18} />
                )}
              </button>
            </div>
          </header>
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
            {section === "overview" ? (
              <Overview />
            ) : section === "wallets" ? (
              <Wallets />
            ) : section === "agents" ? (
              core.mode === "ready" ? (
                <LocalAgents />
              ) : (
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
    <CoreProvider>
      <WorkspaceProvider>
        <CoreGate>
          <WorkspaceShell />
        </CoreGate>
      </WorkspaceProvider>
    </CoreProvider>
  );
}
