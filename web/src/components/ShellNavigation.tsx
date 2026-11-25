import { useEffect, useRef, useState } from "react";
import {
  ArrowLeftRight,
  BookOpen,
  Bell,
  Bot,
  CandlestickChart,
  ChevronDown,
  Clock3,
  FlaskConical,
  Globe2,
  History,
  LogOut,
  MessageSquare,
  Monitor,
  Newspaper,
  PanelLeftClose,
  PanelLeftOpen,
  PanelRightOpen,
  Pickaxe,
  Plug,
  Search,
  Server,
  Settings,
  Settings2,
  TrendingUp,
  Wallet,
} from "../icons";
import logo from "../assets/fased-logo.svg";
import { useWorkspace } from "../workspace";
import { useUI } from "../ui-preferences";
import { Dialog } from "./shared";

export const shellPages = [
  { name: "Chat", path: "agents/chat", icon: MessageSquare },
  { name: "Tasks", path: "agents/tasks", icon: Clock3 },
  { name: "Agents", path: "agents/settings", icon: Bot },
  { name: "Trade", path: "trade", icon: CandlestickChart },
  { name: "Intel", path: "intel", icon: Globe2 },
  { name: "Lab", path: "lab", icon: FlaskConical },
  { name: "Memory", path: "agents/memory", icon: BookOpen },
  { name: "News", path: "agents/news", icon: Newspaper },
  { name: "Mining", path: "terminals/satcoin", icon: Pickaxe },
  { name: "Onchain", path: "terminals/jupiter", icon: ArrowLeftRight },
  { name: "Hyperliquid", path: "terminals/hyperliquid", icon: TrendingUp },
] as const;
export function visibleShellPaths(
  coreMode: string,
  capabilities:
    { walletSigner?: boolean; financialExecution?: boolean } | null | undefined,
  hasWENMining: boolean,
) {
  const showCryptoTerminals =
    coreMode !== "ready" ||
    capabilities?.walletSigner === true ||
    capabilities?.financialExecution === true;
  const showMiningTerminal = coreMode !== "ready" || hasWENMining;
  return shellPages.filter((p) => {
    if (p.path === "terminals/satcoin") return showMiningTerminal;
    if (p.path.startsWith("terminals/")) return showCryptoTerminals;
    return true;
  });
}

export function ShellNavigation() {
  const {
    core,
    agents,
    route,
    navigate,
    query,
    setQuery,
    searchItems,
    unread,
    setNoticesOpen,
    contextOpen,
    setContextOpen,
    setProvidersOpen,
    openSettingsModal,
  } = useWorkspace();
  const { preferences, update, t } = useUI();
  const [more, setMore] = useState(false);
  const [searchOpen, setSearchOpen] = useState(false);
  const moreButton = useRef<HTMLButtonElement>(null),
    moreBox = useRef<HTMLDivElement>(null);
  useEffect(() => {
    setMore(false);
    setSearchOpen(false);
  }, [route]);
  useEffect(() => {
    if (!more) return;
    const pointer = (e: PointerEvent) => {
      if (!moreBox.current?.contains(e.target as Node)) setMore(false);
    };
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setMore(false);
        moreButton.current?.focus();
      }
    };
    document.addEventListener("pointerdown", pointer);
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("pointerdown", pointer);
      document.removeEventListener("keydown", key);
    };
  }, [more]);
  function closeMore() {
    setMore(false);
    if (more) moreButton.current?.focus();
  }
  const visiblePages = visibleShellPaths(
    core.mode,
    core.capabilities,
    Boolean(agents.view?.wenMining),
  );
  const railPages = visiblePages.filter(
    (p) => p.path !== "agents/news" && p.path !== "agents/memory",
  );
  const links = (items: readonly (typeof shellPages)[number][], rail = false) =>
    items.map((p) => (
      <a
        key={p.path}
        href={`#${p.path}`}
        aria-current={route === p.path ? "page" : undefined}
        aria-label={rail ? t(p.name) : undefined}
        data-tooltip={rail ? t(p.name) : undefined}
        onClick={closeMore}
        className={
          !rail
            ? p.name === "Tasks" || p.name === "Agents"
              ? "shell-overflow-route"
              : ["Trade", "Intel", "Lab"].includes(p.name)
                ? "shell-wide-overflow-route"
                : undefined
            : undefined
        }
      >
        <p.icon size={18} aria-hidden="true" />
        {!rail && <span>{t(p.name)}</span>}
      </a>
    ));
  const utility = (name: string, Icon: typeof Search, action: () => void) => (
    <button
      type="button"
      className="shell-icon"
      aria-label={t(name)}
      aria-expanded={
        name.endsWith("navigation rail") ? preferences.rail : undefined
      }
      aria-controls={
        name.endsWith("navigation rail") ? "shell-rail" : undefined
      }
      data-tooltip={t(name)}
      onClick={action}
    >
      <Icon size={18} aria-hidden="true" />
    </button>
  );
  return (
    <>
      <header className="shell-header">
        {utility(
          preferences.rail ? "Hide navigation rail" : "Show navigation rail",
          preferences.rail ? PanelLeftClose : PanelLeftOpen,
          () => update({ rail: !preferences.rail }),
        )}
        <a className="shell-brand" href="#overview" aria-label="Fased">
          <img src={logo} alt="" width="25" height="25" />
          Fased
        </a>
        <nav className="shell-top-nav" aria-label={t("Main navigation")}>
          {shellPages.slice(0, 6).map((p) => (
            <a
              key={p.path}
              href={`#${p.path}`}
              aria-current={route === p.path ? "page" : undefined}
              className={
                p.name === "Tasks" || p.name === "Agents"
                  ? "shell-top-extra"
                  : ["Trade", "Intel", "Lab"].includes(p.name)
                    ? "shell-top-extra shell-top-wide"
                    : undefined
              }
            >
              {t(p.name)}
            </a>
          ))}
          <div ref={moreBox} className="shell-more-wrap">
            <button
              type="button"
              ref={moreButton}
              aria-expanded={more}
              aria-controls="shell-more"
              className="shell-more-toggle"
              data-active={
                shellPages.slice(6).some((p) => p.path === route) ||
                ["activity", "network", "settings", "providers"].includes(
                  route,
                ) ||
                route.startsWith("wallets/")
              }
              onClick={() => setMore(!more)}
            >
              {t("More")}
              <ChevronDown size={14} aria-hidden="true" />
            </button>
            {more && (
              <nav
                id="shell-more"
                className="shell-more"
                aria-label={t("More pages")}
              >
                {links(visiblePages.slice(1))}
                <hr />
                <a href="#network" onClick={closeMore}>
                  <Globe2 size={18} />
                  {t("Network")}
                </a>
                <a href="#activity" onClick={closeMore}>
                  <History size={18} />
                  {t("Activity")}
                </a>
                <a href="#wallets/overview" onClick={closeMore}>
                  <Wallet size={18} />
                  {t("Wallets")}
                </a>
                <button
                  onClick={() => {
                    closeMore();
                    setProvidersOpen(true);
                  }}
                >
                  <Plug size={18} />
                  {t("Providers")}
                </button>
                <a
                  href="#settings"
                  onClick={(e) => {
                    e.preventDefault();
                    closeMore();
                    openSettingsModal("general");
                  }}
                >
                  <Settings2 size={18} />
                  {t("Settings")}
                </a>
                <button
                  onClick={() => {
                    closeMore();
                    setSearchOpen(true);
                  }}
                >
                  <Search size={18} />
                  {t("Search Fased")}
                </button>
                <button
                  className="shell-mobile-utility"
                  onClick={() => {
                    closeMore();
                    setNoticesOpen(true);
                  }}
                >
                  <Bell size={18} />
                  {`${t("Notifications")}, ${unread} ${t("unread")}`}
                </button>
              </nav>
            )}
          </div>
        </nav>
        <button className="shell-search" onClick={() => setSearchOpen(true)}>
          <Search size={16} />
          {t("Search Fased")}
        </button>
        <div className="shell-tools">
          {(core.mode !== "ready" || agents.view?.aiFixtureAvailable) && (
            <span className="shell-demo">{t("Demo")}</span>
          )}
          <span className="shell-desktop-utility">
            {utility("Wallets", Wallet, () => navigate("wallets/overview"))}
          </span>
          <span className="shell-desktop-utility">
            {utility(
              `${t("Notifications")}, ${unread} ${t("unread")}`,
              Bell,
              () => setNoticesOpen(true),
            )}
            {unread > 0 && (
              <span
                className="shell-unread"
                aria-label={`${unread} ${t("unread")}`}
              />
            )}
          </span>
          {utility("Preferences", Settings2, () =>
            openSettingsModal("general"),
          )}
          <button
            className="shell-instance"
            aria-label={t("Instance location")}
            onClick={() => openSettingsModal("instance")}
          >
            {core.connection?.location === "remote" ? (
              <Server size={14} />
            ) : (
              <Monitor size={14} />
            )}
            <span>
              {t(
                core.connection
                  ? core.connection.location === "local"
                    ? "Local"
                    : "Remote"
                  : "Preview",
              )}
            </span>
          </button>
          {core.mode === "ready" && (
            <button
              className="shell-icon"
              aria-label={t("Sign out")}
              data-tooltip={t("Sign out")}
              disabled={core.saving}
              onClick={() => void core.logout()}
            >
              <LogOut size={16} />
            </button>
          )}
        </div>
      </header>
      <nav
        id="shell-rail"
        className="shell-rail"
        hidden={!preferences.rail}
        aria-label={t("Page shortcuts")}
      >
        {links(railPages, true)}
        <div className="shell-rail-bottom">
          {utility(
            contextOpen ? "Close context panel" : "Open context panel",
            PanelRightOpen,
            () => setContextOpen(!contextOpen),
          )}
          <a
            href="#activity"
            aria-label={t("Activity")}
            data-tooltip={t("Activity")}
          >
            <History size={19} />
          </a>
          {utility("Settings", Settings, () => openSettingsModal("general"))}
        </div>
      </nav>
      {searchOpen && (
        <Dialog title={t("Search Fased")} onClose={() => setSearchOpen(false)}>
          <label className="shell-search-field">
            {t("Search workspace")}
            <input
              autoFocus
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
          </label>
          <div className="shell-search-results">
            {searchItems.length ? (
              searchItems.map((item) => (
                <a
                  key={item.path}
                  href={`#${item.path}`}
                  onClick={() => setSearchOpen(false)}
                >
                  {t(item.title)}
                </a>
              ))
            ) : (
              <p>{t("No matching workspace.")}</p>
            )}
          </div>
        </Dialog>
      )}
    </>
  );
}
