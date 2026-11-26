import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type PointerEvent,
} from "react";
import { useUI } from "../ui-preferences";
import {
  ArrowDown,
  ArrowRight,
  ArrowUp,
  Bell,
  Bot,
  Clock3,
  FlaskConical,
  Globe2,
  History,
  LayoutDashboard,
  Maximize2,
  Menu,
  MoreHorizontal,
  Newspaper,
  Plus,
  Trash2,
  Wallet,
  Pickaxe,
  ArrowLeftRight,
  TrendingUp,
} from "../icons";
import { Avatar, Dialog } from "../components/shared";
import { useWorkspace } from "../workspace";
import {
  defaultDashboard,
  moveWidget,
  parseDashboard,
  resizeWidget,
  widgetNames,
  type DashboardLayout,
  type WidgetID,
  type WidgetSize,
} from "../dashboard";
import {
  fetchIntelEvents,
  fetchPredictions,
  formatProbPct,
  type IntelEvent,
  type PredictionList,
} from "../world-intel";
import {
  automationStateLabel,
  getAutomationState,
  listNotifications,
  setAutomationState,
  type AutomationControlState,
  type NotificationItem,
} from "../automation";
import {
  deskStatusLabel,
  formatCurrencyUSD,
  getDeskState,
  type DeskSnapshot,
} from "../quantlab-desk";

const icons = {
  agents: Bot,
  tasks: Clock3,
  news: Newspaper,
  intel: Globe2,
  predictions: TrendingUp,
  alerts: Bell,
  desk: FlaskConical,
  wallets: Wallet,
  activity: History,
  mining: Pickaxe,
  onchain: ArrowLeftRight,
  hyperliquid: TrendingUp,
};
const paths = {
  agents: "agents/settings",
  tasks: "agents/tasks",
  news: "agents/news",
  intel: "intel",
  predictions: "intel",
  alerts: "agents/tasks",
  desk: "lab",
  wallets: "wallets/overview",
  activity: "activity",
  mining: "terminals/satcoin",
  onchain: "terminals/jupiter",
  hyperliquid: "terminals/hyperliquid",
};
const readLayout = (key: string) => {
  try {
    return parseDashboard(localStorage.getItem(key));
  } catch {
    return defaultDashboard();
  }
};

export function Overview() {
  const { t } = useUI();
  const {
    state,
    wallets,
    core,
    agents,
    navigate,
    dispatch,
    setSetupOpen,
    setFocusedTask,
  } = useWorkspace();
  const view = agents.view;
  const key = `fased.dashboard.v1:${core.instanceId || "demo"}`;
  const [layout, setLayout] = useState<DashboardLayout>(() => readLayout(key));
  const [storageError, setStorageError] = useState(false);
  const [adding, setAdding] = useState(false);
  const [expanded, setExpanded] = useState<WidgetID>();
  const [openMenu, setOpenMenu] = useState<WidgetID | null>(null);
  const [intelEvents, setIntelEvents] = useState<IntelEvent[]>([]);
  const [predList, setPredList] = useState<PredictionList | null>(null);
  const [autoState, setAutoState] = useState<AutomationControlState | null>(
    null,
  );
  const [alertItems, setAlertItems] = useState<NotificationItem[]>([]);
  const [alertUnread, setAlertUnread] = useState(0);
  const [deskSnap, setDeskSnap] = useState<DeskSnapshot | null>(null);
  const hasIntelWidget = layout.widgets.some((w) => w.id === "intel");
  const hasPredWidget = layout.widgets.some((w) => w.id === "predictions");
  const hasAlertsWidget = layout.widgets.some((w) => w.id === "alerts");
  const hasDeskWidget = layout.widgets.some((w) => w.id === "desk");
  useEffect(() => {
    if (core.mode !== "ready" || !hasAlertsWidget) return;
    let cancelled = false;
    void Promise.all([
      getAutomationState(core),
      listNotifications(core, { limit: 3 }),
    ])
      .then(([st, ntfs]) => {
        if (cancelled) return;
        setAutoState(st);
        setAlertItems(ntfs.notifications ?? []);
        setAlertUnread(ntfs.unreadCount ?? 0);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [core, hasAlertsWidget]);
  useEffect(() => {
    if (core.mode !== "ready" || !hasIntelWidget) return;
    let cancelled = false;
    void fetchIntelEvents(core, { limit: 5 })
      .then((res) => {
        if (!cancelled) setIntelEvents(res.events ?? []);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [core, hasIntelWidget]);
  useEffect(() => {
    if (core.mode !== "ready" || !hasPredWidget) return;
    let cancelled = false;
    void fetchPredictions(core, { limit: 5 })
      .then((res) => {
        if (!cancelled) setPredList(res);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [core, hasPredWidget]);
  useEffect(() => {
    if (core.mode !== "ready" || !hasDeskWidget) return;
    let cancelled = false;
    void getDeskState(core)
      .then((res) => {
        if (!cancelled) setDeskSnap(res);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [core, hasDeskWidget]);
  const board = useRef<HTMLDivElement>(null);
  const dragging = useRef<WidgetID | undefined>(undefined);
  function closeMenus() {
    setOpenMenu(null);
  }
  useEffect(() => {
    const click = (e: Event) => {
      if (!(e.target as Element).closest(".widget-menu")) closeMenus();
    };
    const escape = (e: KeyboardEvent) => {
      if (e.key === "Escape") closeMenus();
    };
    document.addEventListener("pointerdown", click);
    document.addEventListener("keydown", escape);
    return () => {
      document.removeEventListener("pointerdown", click);
      document.removeEventListener("keydown", escape);
    };
  }, []);
  const resizing = useRef<
    | {
        id: WidgetID;
        x: number;
        y: number;
        width: number;
        height: number;
        size: WidgetSize;
      }
    | undefined
  >(undefined);
  useLayoutEffect(() => {
    setLayout(readLayout(key));
    setStorageError(false);
  }, [key]);
  useEffect(() => {
    const sync = (e: StorageEvent) => {
      if (e.key === key || e.key === null) setLayout(readLayout(key));
    };
    window.addEventListener("storage", sync);
    return () => window.removeEventListener("storage", sync);
  }, [key]);
  function save(next: DashboardLayout) {
    setLayout(next);
    try {
      localStorage.setItem(key, JSON.stringify(next));
      setStorageError(false);
    } catch {
      setStorageError(true);
    }
  }
  function open(path: string) {
    setExpanded(undefined);
    navigate(path);
  }
  function task(id: string) {
    setFocusedTask(id);
    open("agents/tasks");
  }
  function body(id: WidgetID) {
    if (
      core.mode === "ready" &&
      !view &&
      ["agents", "tasks", "news", "activity"].includes(id)
    )
      return null;
    if (id === "agents")
      return (
        <div className="dashboard-rows">
          {state.profiles.map((p) => (
            <button
              className="dashboard-row"
              key={p.id}
              onClick={async () => {
                if (core.mode === "ready") await agents.select(p.id);
                else dispatch({ type: "profile", id: p.id });
                open("agents/chat");
              }}
            >
              <Avatar profile={p} small />
              <strong>{p.name}</strong>
              <ArrowRight size={14} />
            </button>
          ))}
        </div>
      );
    if (id === "tasks")
      return (
        <div className="dashboard-rows">
          {view?.tasks
            .filter((x) => !x.wen && x.state !== "cancelled")
            .slice(-5)
            .reverse()
            .map((x) => (
              <button
                className="dashboard-row"
                key={x.id}
                onClick={() => task(x.id)}
              >
                <span className="dashboard-copy">
                  <strong>{x.name}</strong>
                  <small>
                    {t(x.state)}
                    {x.nextAt > 0 && x.state === "scheduled"
                      ? ` · ${new Date(x.nextAt * 1000).toLocaleString(document.documentElement.lang, { dateStyle: "short", timeStyle: "short" })}`
                      : ""}
                  </small>
                </span>
                <span>
                  {x.runs}/{x.maxRuns}
                </span>
                <ArrowRight size={14} />
              </button>
            ))}
          {!view?.tasks.some((x) => !x.wen && x.state !== "cancelled") && (
            <div className="dashboard-empty">
              {t("No tasks")}
              <button className="text-button" onClick={() => task("new")}>
                {t("New task")}
                <Plus size={14} />
              </button>
            </div>
          )}
        </div>
      );
    if (id === "news") {
      const latest = new Map<
        string,
        NonNullable<typeof view>["newsFetches"][number]
      >();
      for (const f of view?.newsFetches || [])
        if (f.state === "completed" && f.fresh) latest.set(f.sourceId, f);
      const headlines = [...latest.values()]
        .flatMap((f) =>
          f.items.map((item) => ({
            item,
            source:
              view?.newsSources.find((s) => s.id === f.sourceId)?.name ||
              new URL(f.url).hostname,
            at: f.fetchedAt,
          })),
        )
        .sort((a, b) => b.item.publishedAt - a.item.publishedAt)
        .slice(0, 6);
      return (
        <div className="dashboard-rows">
          {headlines.map(({ item, source, at }) => (
            <button
              key={item.id + source}
              className="dashboard-row headline"
              onClick={() => open(paths.news)}
            >
              <span className="dashboard-copy">
                <strong>{item.title}</strong>
                <small>
                  {source} ·{" "}
                  {new Date(at * 1000).toLocaleTimeString(
                    document.documentElement.lang,
                    { hour: "2-digit", minute: "2-digit" },
                  )}
                </small>
              </span>
              <ArrowRight size={14} />
            </button>
          ))}
          {!headlines.length && (
            <div className="dashboard-empty">
              {t("No headlines")}
              <button className="text-button" onClick={() => open(paths.news)}>
                {t("Sources")}
                <ArrowRight size={14} />
              </button>
            </div>
          )}
        </div>
      );
    }
    if (id === "activity")
      return (
        <div className="dashboard-rows">
          {view?.notices
            .slice(-5)
            .reverse()
            .map((n) => (
              <button
                className="dashboard-row"
                key={n.id}
                onClick={() => {
                  const run = view.runs.find((r) => r.id === n.runId);
                  if (run) task(run.taskId);
                  else open("activity");
                }}
              >
                <span className="dashboard-copy">
                  <strong>{n.text}</strong>
                  <small>
                    {new Date(n.at * 1000).toLocaleTimeString(
                      document.documentElement.lang,
                      { hour: "2-digit", minute: "2-digit" },
                    )}
                  </small>
                </span>
                {!n.read && <span className="dot" />}
              </button>
            ))}
          {!view?.notices.length && (
            <div className="dashboard-empty">{t("No activity")}</div>
          )}
        </div>
      );
    if (id === "wallets" || id === "onchain") {
      const accounts = wallets.view?.record.accounts || [];
      const supported =
        id === "onchain"
          ? accounts.filter(
              (a) =>
                [2, 3].includes(a.version) &&
                ["devnet", "mainnet-beta"].includes(a.network),
            )
          : accounts;
      return (
        <div className="dashboard-rows">
          {wallets.loading && (
            <div className="dashboard-empty" role="status">
              {t("Loading…")}
            </div>
          )}
          {wallets.error && (
            <div className="dashboard-empty" role="alert">
              {t("Wallets unavailable")}
              <button className="text-button" onClick={() => wallets.refresh()}>
                {t("Retry")}
              </button>
            </div>
          )}
          {supported.slice(0, 5).map((account) => (
            <button
              key={account.id}
              className="dashboard-row"
              onClick={() => open(paths[id])}
            >
              <Wallet size={17} />
              <span className="dashboard-copy">
                <strong>{account.name}</strong>
                <small>
                  {t(
                    account.network === "mainnet-beta"
                      ? "Mainnet"
                      : account.network === "devnet"
                        ? "Devnet"
                        : "Paper",
                  )}{" "}
                  · {t(account.rpc ? "RPC configured" : "Set up RPC")}
                </small>
              </span>
              <ArrowRight size={14} />
            </button>
          ))}
          {!wallets.loading && !wallets.error && !supported.length && (
            <div className="dashboard-empty">
              {t("No wallets")}
              <button
                className="text-button"
                onClick={() => open("wallets/overview")}
              >
                {t("Add wallet")}
                <ArrowRight size={14} />
              </button>
            </div>
          )}
          {id === "onchain" && supported.length > 0 && (
            <div className="dashboard-footer-status">
              {t("Live execution disabled")}
            </div>
          )}
        </div>
      );
    }
    if (id === "intel")
      return (
        <div className="dashboard-rows">
          {intelEvents.slice(0, 5).map((ev) => (
            <button
              key={ev.id}
              className="dashboard-row headline"
              onClick={() => open(paths.intel)}
            >
              <span className="dashboard-copy">
                <strong>{ev.title}</strong>
                <small>{`${ev.sourceName} · ${ev.region.toUpperCase()}`}</small>
              </span>
              <span className="mono">{`${Math.round(ev.urgency * 100)}%`}</span>
            </button>
          ))}
          {!intelEvents.length && (
            <div className="dashboard-empty">
              <button className="text-button" onClick={() => open(paths.intel)}>
                {t("World Intel")}
                <ArrowRight size={14} />
              </button>
            </div>
          )}
        </div>
      );
    if (id === "predictions")
      return (
        <div className="dashboard-rows">
          {(predList?.arbitrage ?? []).slice(0, 1).map((arb) => (
            <button
              key={arb.id}
              className="dashboard-row"
              onClick={() => open(paths.predictions)}
            >
              <span className="dashboard-copy">
                <strong>{`${t("Arbitrage")}: ${arb.topic}`}</strong>
                <small>{`Poly ${formatProbPct(arb.polymarketProb)} · Kalshi ${formatProbPct(arb.kalshiProb)}`}</small>
              </span>
              <span className="mono">{`Δ ${arb.spreadPct.toFixed(1)}%`}</span>
            </button>
          ))}
          {(predList?.markets ?? []).slice(0, 4).map((mkt) => (
            <button
              key={mkt.id}
              className="dashboard-row"
              onClick={() => open(paths.predictions)}
            >
              <span className="dashboard-copy">
                <strong>{mkt.question}</strong>
                <small>{mkt.venue}</small>
              </span>
              <span className="mono">{formatProbPct(mkt.yesProb)}</span>
            </button>
          ))}
          {!predList?.markets?.length && (
            <div className="dashboard-empty">
              <button
                className="text-button"
                onClick={() => open(paths.predictions)}
              >
                {t("Predictions")}
                <ArrowRight size={14} />
              </button>
            </div>
          )}
        </div>
      );
    if (id === "alerts") {
      const stateText = automationStateLabel(autoState);
      return (
        <div className="dashboard-rows" data-testid="overview-alerts-widget">
          <div className="dashboard-row">
            <span className="dashboard-copy">
              <strong>{t(stateText)}</strong>
              <small>{t("{count} unread", { count: alertUnread })}</small>
            </span>
            <button
              type="button"
              className="secondary"
              disabled={core.mode !== "ready"}
              onClick={() => {
                if (core.mode !== "ready") return;
                const action = autoState?.killSwitchActive
                  ? "unkill"
                  : autoState?.paused
                    ? "resume"
                    : "pause";
                void setAutomationState(core, action)
                  .then((next) => setAutoState(next))
                  .catch(() => {});
              }}
            >
              {t(
                autoState?.killSwitchActive
                  ? "Clear kill switch"
                  : autoState?.paused
                    ? "Resume"
                    : "Pause",
              )}
            </button>
          </div>
          {alertItems.slice(0, 3).map((item) => (
            <button
              key={item.id}
              className="dashboard-row"
              onClick={() => open(paths.alerts)}
            >
              <span className="dashboard-copy">
                <strong>{item.title}</strong>
                <small>{item.severity}</small>
              </span>
              <ArrowRight size={14} />
            </button>
          ))}
          {alertItems.length === 0 && (
            <div className="dashboard-empty">
              <button
                className="text-button"
                onClick={() => open(paths.alerts)}
              >
                {t("Event triggers")}
                <ArrowRight size={14} />
              </button>
            </div>
          )}
        </div>
      );
    }
    if (id === "desk") {
      const status = deskStatusLabel(deskSnap?.control);
      return (
        <div className="dashboard-rows" data-testid="overview-desk-widget">
          <div className="dashboard-row">
            <span className="dashboard-copy">
              <strong>{t(status)}</strong>
              <small>
                {`${deskSnap?.limits.mode ?? "shadow"} · ${formatCurrencyUSD(deskSnap?.account.equityUsd ?? 100000)}`}
              </small>
            </span>
            <span className="mono">{`${deskSnap?.account.openPositions ?? 0}/${deskSnap?.account.pendingTickets ?? 0}`}</span>
          </div>
          <div className="dashboard-empty">
            <button className="text-button" onClick={() => open(paths.desk)}>
              {t("Quant Lab")}
              <ArrowRight size={14} />
            </button>
          </div>
        </div>
      );
    }
    return (
      <div className="dashboard-empty">
        <span>
          {t(
            id === "mining"
              ? "Mining is not available yet"
              : "Hyperliquid is not available yet",
          )}
        </span>
      </div>
    );
  }
  function pointerMove(e: PointerEvent<HTMLButtonElement>) {
    const start = resizing.current;
    if (!start) return;
    const dx = e.clientX - start.x,
      dy = e.clientY - start.y;
    start.size =
      dy > start.height * 0.25
        ? "tall"
        : dx > start.width * 0.25
          ? "wide"
          : dx < -start.width * 0.25 || dy < -start.height * 0.25
            ? "small"
            : start.size;
  }
  return (
    <div className="dashboard-screen" ref={board}>
      <div className="dashboard-toolbar">
        <h1>{t("Dashboard")}</h1>
        <div className="dashboard-actions">
          {core.mode === "ready" && view && (
            <select
              aria-label={t("Agent")}
              value={view.profileId}
              disabled={agents.busy}
              onChange={(e) => void agents.select(e.target.value)}
            >
              {view.profiles.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          )}
          <button className="secondary" onClick={() => setAdding(true)}>
            <Plus size={15} />
            {t("Add widgets")}
          </button>
          <button
            className="icon-button"
            aria-label={t("Setup")}
            title={t("Setup")}
            onClick={() => setSetupOpen(true)}
          >
            <LayoutDashboard size={16} />
          </button>
        </div>
      </div>
      {core.mode === "ready" && (agents.error || !view) && (
        <div
          className="dashboard-status"
          role={agents.error ? "alert" : "status"}
        >
          <span>{agents.error || t("Loading…")}</span>
          {agents.error && (
            <button
              className="secondary"
              onClick={() => void agents.refresh().catch(() => {})}
            >
              {t("Reload")}
            </button>
          )}
        </div>
      )}
      {storageError && (
        <p role="status">{t("Layout cannot be saved in this browser.")}</p>
      )}
      <div className="dashboard-kpi-strip" aria-label={t("Status")}>
        <div className="dashboard-kpi">
          <span>{t("Agents")}</span>
          <strong>
            {core.mode === "ready" && view
              ? view.profiles.length
              : state.profiles.length}
          </strong>
        </div>
        <div className="dashboard-kpi">
          <span>{t("Tasks")}</span>
          <strong>
            {view
              ? view.tasks.filter((x) => !x.wen && x.state !== "cancelled")
                  .length
              : 0}
          </strong>
        </div>
        <div className="dashboard-kpi">
          <span>{t("Alerts")}</span>
          <strong>
            {hasAlertsWidget
              ? alertUnread
              : view
                ? view.notices.filter((n) => !n.read).length
                : state.notices.filter((n) => !n.read).length}
          </strong>
        </div>
        <div className="dashboard-kpi">
          <span>{t("Wallets")}</span>
          <strong>{wallets.view?.record.accounts.length ?? 0}</strong>
        </div>
      </div>
      <div className="dashboard-grid" aria-label={t("Dashboard widgets")}>
        {layout.widgets.map((widget, index) => {
          const Icon = icons[widget.id];
          return (
            <section
              key={widget.id}
              className={`dashboard-widget size-${widget.size}`}
              data-widget={widget.id}
              aria-label={t(widgetNames[widget.id])}
              onDragOver={(e) => e.preventDefault()}
              onDrop={(e) => {
                e.preventDefault();
                if (dragging.current)
                  save(moveWidget(layout, dragging.current, widget.id));
                dragging.current = undefined;
              }}
            >
              <header className="dashboard-widget-header">
                <button
                  className="icon-button widget-grip"
                  draggable
                  onDragStart={(e) => {
                    dragging.current = widget.id;
                    e.dataTransfer.effectAllowed = "move";
                    e.dataTransfer.setData("text/plain", widget.id);
                  }}
                  onDragEnd={() => {
                    dragging.current = undefined;
                  }}
                  aria-label={t("Move {name}", {
                    name: t(widgetNames[widget.id]),
                  })}
                  title={t("Move")}
                >
                  <Menu size={15} />
                </button>
                <button
                  className="dashboard-title"
                  onClick={() => open(paths[widget.id])}
                >
                  <Icon size={16} />
                  <h2>{t(widgetNames[widget.id])}</h2>
                </button>
                <div className="widget-menu">
                  <button
                    type="button"
                    className="icon-button widget-menu-trigger"
                    aria-label={t("{name} options", {
                      name: t(widgetNames[widget.id]),
                    })}
                    aria-expanded={openMenu === widget.id}
                    onClick={() =>
                      setOpenMenu((prev) =>
                        prev === widget.id ? null : widget.id,
                      )
                    }
                  >
                    <MoreHorizontal size={17} aria-hidden="true" />
                  </button>
                  {openMenu === widget.id && (
                    <div className="widget-menu-panel">
                      <div className="widget-size-row">
                        <span className="widget-size-label">{t("Size")}</span>
                        <div
                          className="segmented-control widget-size-control"
                          role="group"
                          aria-label={t("{name} size", {
                            name: t(widgetNames[widget.id]),
                          })}
                        >
                          {(["small", "wide", "tall"] as const).map((size) => (
                            <button
                              key={size}
                              type="button"
                              aria-pressed={widget.size === size}
                              onClick={() =>
                                save(resizeWidget(layout, widget.id, size))
                              }
                            >
                              {t(size[0].toUpperCase() + size.slice(1))}
                            </button>
                          ))}
                        </div>
                      </div>
                      <button
                        type="button"
                        onClick={() => {
                          setOpenMenu(null);
                          setExpanded(widget.id);
                        }}
                      >
                        <Maximize2 size={14} aria-hidden="true" />
                        <span>{t("Expand")}</span>
                      </button>
                      <button
                        type="button"
                        disabled={index === 0}
                        onClick={() =>
                          save(
                            moveWidget(
                              layout,
                              widget.id,
                              layout.widgets[index - 1]?.id,
                            ),
                          )
                        }
                      >
                        <ArrowUp size={14} aria-hidden="true" />
                        <span>{t("Move up")}</span>
                      </button>
                      <button
                        type="button"
                        disabled={index === layout.widgets.length - 1}
                        onClick={() =>
                          save(
                            moveWidget(
                              layout,
                              layout.widgets[index + 1].id,
                              widget.id,
                            ),
                          )
                        }
                      >
                        <ArrowDown size={14} aria-hidden="true" />
                        <span>{t("Move down")}</span>
                      </button>
                      <button
                        type="button"
                        className="widget-menu-danger"
                        onClick={() => {
                          setOpenMenu(null);
                          save({
                            ...layout,
                            widgets: layout.widgets.filter(
                              (w) => w.id !== widget.id,
                            ),
                          });
                        }}
                      >
                        <Trash2 size={14} aria-hidden="true" />
                        <span>{t("Remove")}</span>
                      </button>
                    </div>
                  )}
                </div>
              </header>
              <div className="dashboard-widget-body">{body(widget.id)}</div>
              <button
                className="widget-resize"
                aria-label={t("Resize {name}", {
                  name: t(widgetNames[widget.id]),
                })}
                title={t("Resize")}
                onPointerDown={(e) => {
                  if (e.button !== 0) return;
                  const r = e.currentTarget
                    .closest("section")!
                    .getBoundingClientRect();
                  resizing.current = {
                    id: widget.id,
                    x: e.clientX,
                    y: e.clientY,
                    width: r.width,
                    height: r.height,
                    size: widget.size,
                  };
                  e.currentTarget.setPointerCapture(e.pointerId);
                }}
                onPointerMove={pointerMove}
                onPointerUp={(e) => {
                  const start = resizing.current;
                  if (start) {
                    save(resizeWidget(layout, start.id, start.size));
                    resizing.current = undefined;
                  }
                  e.currentTarget.releasePointerCapture(e.pointerId);
                }}
                onPointerCancel={() => {
                  resizing.current = undefined;
                }}
                onKeyDown={(e) => {
                  if (e.key === "Enter" || e.key === " ") {
                    e.preventDefault();
                    const sizes: WidgetSize[] = ["small", "wide", "tall"];
                    save(
                      resizeWidget(
                        layout,
                        widget.id,
                        sizes[(sizes.indexOf(widget.size) + 1) % 3],
                      ),
                    );
                  }
                }}
              >
                <span>⌟</span>
              </button>
            </section>
          );
        })}
      </div>
      {!layout.widgets.length && (
        <button
          className="dashboard-empty-board"
          onClick={() => setAdding(true)}
        >
          <Plus size={22} />
          {t("Add widgets")}
        </button>
      )}
      {adding && (
        <Dialog title={t("Widgets")} onClose={() => setAdding(false)}>
          <div className="widget-catalog">
            {(Object.keys(widgetNames) as WidgetID[])
              .filter(
                (id) =>
                  id !== "mining" ||
                  core.mode !== "ready" ||
                  Boolean(agents.view?.wenMining),
              )
              .map((id) => (
                <button
                  className="secondary"
                  key={id}
                  disabled={layout.widgets.some((w) => w.id === id)}
                  onClick={() =>
                    save({
                      ...layout,
                      widgets: [...layout.widgets, { id, size: "small" }],
                    })
                  }
                >
                  {t(widgetNames[id])}
                  <Plus size={14} />
                </button>
              ))}
          </div>
          <div className="dialog-footer">
            <button
              className="text-button"
              onClick={() => save(defaultDashboard())}
            >
              {t("Reset layout")}
            </button>
            <button className="primary" onClick={() => setAdding(false)}>
              {t("Done")}
            </button>
          </div>
        </Dialog>
      )}
      {expanded && (
        <Dialog
          title={t(widgetNames[expanded])}
          onClose={() => setExpanded(undefined)}
        >
          <div className="dashboard-expanded">{body(expanded)}</div>
          <div className="dialog-footer">
            <button className="primary" onClick={() => open(paths[expanded])}>
              {t("Open")}
              <ArrowRight size={14} />
            </button>
          </div>
        </Dialog>
      )}
    </div>
  );
}
