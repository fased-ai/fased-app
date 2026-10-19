import {
  ArrowUpRight,
  Bell,
  BookOpen,
  Check,
  CheckCheck,
  ChevronRight,
  FlaskConical,
  LockKeyhole,
  Menu,
  MessageSquare,
  Monitor,
  PanelRightClose,
  PanelRightOpen,
  Plus,
  Search,
  Server,
  Settings2,
  ShieldCheck,
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
import { Terminals } from "./pages/Terminals";
import { Settings } from "./pages/Settings";
function WorkspaceShell() {
  const {
    state,
    dispatch,
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
            <span className="brand-mark">
              f<i />
            </span>
            <strong>
              fased<span>app</span>
            </strong>
          </button>
          <div className="workspace-switch">
            <span className="workspace-avatar">F</span>
            <span>
              <strong>Personal workspace</strong>
              <small>Private by design</small>
            </span>
            <LockKeyhole size={13} />
          </div>
          <p className="nav-label">WORKSPACE</p>
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
          <div className="sidebar-agents">
            <div className="nav-label">
              YOUR AGENTS{" "}
              <button
                className="icon-button"
                aria-label="Create agent profile"
                onClick={() => setProfileOpen(true)}
              >
                <Plus size={13} />
              </button>
            </div>
            {state.profiles.slice(0, 5).map((p) => (
              <button
                key={p.id}
                onClick={() => {
                  dispatch({ type: "profile", id: p.id });
                  navigate("agents/chat");
                }}
              >
                <span className={`profile-dot ${p.color}`} />
                {p.name}
              </button>
            ))}
          </div>
          <div className="sidebar-bottom">
            <div className="foundation-note">
              <ShieldCheck size={19} />
              <strong>A smaller foundation.</strong>
              <p>
                Your accounts and decisions.
                <br />
                Clear boundaries throughout.
              </p>
            </div>
            <button
              className={section === "settings" ? "active" : ""}
              onClick={() => navigate("settings")}
            >
              <Settings2 size={17} /> Settings
            </button>
            <div className="sidebar-version">
              <span className="dot" /> Design prototype <span>0.0.0</span>
            </div>
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
              <span className="breadcrumb">
                Workspace <ChevronRight size={13} />{" "}
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
              <div className="instance-pill">
                {state.setup.host === "local" ? (
                  <Monitor size={13} />
                ) : (
                  <Server size={13} />
                )}
                <span>
                  {state.setup.host === "local" ? "Local" : "VPS"} preview
                </span>
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
          <div className="prototype-strip">
            <FlaskConical size={13} />
            <strong>INTERACTIVE PROTOTYPE</strong>
            <span>
              Illustrative data. No connected wallets, AI requests or real
              execution.
            </span>
          </div>
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
              <Agents />
            ) : section === "terminals" ? (
              <Terminals />
            ) : section === "settings" ? (
              <Settings />
            ) : (
              <>
                <SectionHead
                  eyebrow="A DURABLE RECORD"
                  title="Activity"
                  description="Prototype events, setup decisions and notifications. Financial activity is not connected."
                />
                <section className="panel">
                  <div className="panel-heading">
                    <h2>Recent prototype activity</h2>
                    <Badge>LOCAL ONLY</Badge>
                  </div>
                  {state.notices.map((n) => (
                    <div className="activity-row" key={n.id}>
                      <span className="activity-icon">
                        <CheckCheck size={17} />
                      </span>
                      <div>
                        <strong>{n.title}</strong>
                        <p>{n.detail}</p>
                      </div>
                      <Badge>DEMO</Badge>
                    </div>
                  ))}
                </section>
              </>
            )}
          </main>
          <footer className="app-footer">
            <span>
              <LockKeyhole size={12} /> Prototype preferences stay in this
              browser.
            </span>
            <span>Foundation before execution.</span>
          </footer>
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
              <p className="context-note">
                A contextual agent can explain selected state and draft
                reviewable actions. This panel is a design preview.
              </p>
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
    <WorkspaceProvider>
      <WorkspaceShell />
    </WorkspaceProvider>
  );
}
