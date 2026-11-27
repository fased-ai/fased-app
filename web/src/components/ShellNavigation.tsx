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
  Moon,
  Newspaper,
  PanelRightOpen,
  Pickaxe,
  Plug,
  Search,
  Server,
  Settings2,
  Sun,
  TrendingUp,
  Wallet,
} from "../icons";
import logo from "../assets/fased-logo.svg";
import { useWorkspace } from "../workspace";
import { useUI } from "../ui-preferences";
import { Dialog } from "./shared";

export const shellPages = [
  { name: "Mine", path: "mine", icon: Pickaxe },
  { name: "Trade", path: "trade", icon: CandlestickChart },
  { name: "Tasks", path: "tasks", icon: Clock3 },
  { name: "News", path: "intel", icon: Newspaper },
  { name: "Chat", path: "agents/chat", icon: MessageSquare },
  { name: "Dashboard", path: "overview", icon: Globe2 },
  { name: "Lab", path: "lab", icon: FlaskConical },
  { name: "Agents", path: "agents/settings", icon: Bot },
  { name: "Network", path: "network", icon: Globe2 },
  { name: "Memory", path: "agents/memory", icon: BookOpen },
  { name: "Data & Feeds", path: "agents/news", icon: Newspaper },
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
  const { resolvedTheme, update, t } = useUI();
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

  const links = (items: readonly (typeof shellPages)[number][]) =>
    items.map((p) => (
      <a
        key={p.path}
        href={`#${p.path}`}
        aria-current={route === p.path ? "page" : undefined}
        onClick={closeMore}
        className={
          p.path === "tasks"
            ? "shell-overflow-route"
            : ["mine", "trade", "intel"].includes(p.path)
              ? "shell-wide-overflow-route"
              : undefined
        }
      >
        <p.icon size={18} aria-hidden="true" />
        <span>{t(p.name)}</span>
      </a>
    ));

  const utility = (name: string, Icon: typeof Search, action: () => void) => (
    <button
      type="button"
      className="shell-icon"
      aria-label={t(name)}
      data-tooltip={t(name)}
      onClick={action}
    >
      <Icon size={18} aria-hidden="true" />
    </button>
  );

  return (
    <>
      <header className="shell-header">
        <a className="shell-brand" href="#overview" aria-label="Fased">
          <img src={logo} alt="" width="24" height="24" />
          Fased
        </a>
        <nav className="shell-top-nav" aria-label={t("Main navigation")}>
          {shellPages.slice(0, 5).map((p) => (
            <a
              key={p.path}
              href={`#${p.path}`}
              aria-current={route === p.path ? "page" : undefined}
              className={
                p.path === "tasks"
                  ? "shell-top-extra"
                  : ["mine", "trade", "intel"].includes(p.path)
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
                visiblePages.slice(5).some((p) => p.path === route) ||
                ["activity", "settings", "providers"].includes(route) ||
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
                {links(visiblePages.filter((p) => p.path !== "agents/chat"))}
                <hr />
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
        <div className="shell-tools">
          {(core.mode !== "ready" || agents.view?.aiFixtureAvailable) && (
            <span className="shell-demo">{t("Demo")}</span>
          )}
          <span className="shell-desktop-utility">
            <button
              type="button"
              className="shell-wallets-pill"
              aria-label={t("Wallets")}
              onClick={() => navigate("wallets/overview")}
            >
              <Wallet size={15} aria-hidden="true" />
              <span>{t("Wallets")}</span>
            </button>
          </span>
          <span className="shell-desktop-utility">
            {utility("Search Fased", Search, () => setSearchOpen(true))}
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
          {utility(
            resolvedTheme === "dark" ? "Light" : "Dark",
            resolvedTheme === "dark" ? Sun : Moon,
            () =>
              update({ theme: resolvedTheme === "dark" ? "light" : "dark" }),
          )}
          {utility(
            contextOpen ? "Close context panel" : "Open context panel",
            PanelRightOpen,
            () => setContextOpen(!contextOpen),
          )}
          {utility("Preferences", Settings2, () =>
            openSettingsModal("general"),
          )}
          <button
            className="shell-instance"
            aria-label={t("Instance location")}
            onClick={() => openSettingsModal("instance")}
          >
            <span
              className="shell-status-dot"
              data-ready={core.mode === "ready" ? "true" : "false"}
              aria-hidden="true"
            />
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
