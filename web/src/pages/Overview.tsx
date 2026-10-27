import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type PointerEvent,
} from "react";
import { useUI } from "../ui-preferences";
import {
  ArrowRight,
  Bot,
  Clock3,
  History,
  LayoutDashboard,
  Menu,
  MoreHorizontal,
  Newspaper,
  Plus,
  Wallet,
  Pickaxe,
  ArrowLeftRight,
  TrendingUp,
} from "../icons";
import { Avatar, Badge, Dialog } from "../components/shared";
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

const icons = {
  agents: Bot,
  tasks: Clock3,
  news: Newspaper,
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
  const board = useRef<HTMLDivElement>(null);
  const dragging = useRef<WidgetID | undefined>(undefined);
  function closeMenus() {
    board.current
      ?.querySelectorAll<HTMLDetailsElement>(".widget-menu[open]")
      .forEach((d) => {
        d.open = false;
      });
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
                onClick={() =>
                  n.runId
                    ? task(
                        view.runs.find((r) => r.id === n.runId)?.taskId || "",
                      )
                    : open("activity")
                }
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
    return (
      <div className="dashboard-empty">
        <Badge>{t("Unavailable")}</Badge>
        <button className="text-button" onClick={() => open(paths[id])}>
          {t("Details")}
          <ArrowRight size={14} />
        </button>
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
      {storageError && (
        <p role="status">{t("Layout cannot be saved in this browser.")}</p>
      )}
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
                <details className="widget-menu">
                  <summary
                    aria-label={t("{name} options", {
                      name: t(widgetNames[widget.id]),
                    })}
                  >
                    <MoreHorizontal size={17} />
                  </summary>
                  <div className="widget-menu-panel">
                    <button
                      onClick={() => {
                        closeMenus();
                        setExpanded(widget.id);
                      }}
                    >
                      {t("Expand")}
                    </button>
                    <label>
                      {t("Size")}
                      <select
                        aria-label={t("{name} size", {
                          name: t(widgetNames[widget.id]),
                        })}
                        value={widget.size}
                        onChange={(e) =>
                          save(
                            resizeWidget(
                              layout,
                              widget.id,
                              e.target.value as WidgetSize,
                            ),
                          )
                        }
                      >
                        {(["small", "wide", "tall"] as const).map((size) => (
                          <option key={size} value={size}>
                            {t(size[0].toUpperCase() + size.slice(1))}
                          </option>
                        ))}
                      </select>
                    </label>
                    <button
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
                      {t("Move up")}
                    </button>
                    <button
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
                      {t("Move down")}
                    </button>
                    <button
                      onClick={() =>
                        save({
                          ...layout,
                          widgets: layout.widgets.filter(
                            (w) => w.id !== widget.id,
                          ),
                        })
                      }
                    >
                      {t("Remove")}
                    </button>
                  </div>
                </details>
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
            {(Object.keys(widgetNames) as WidgetID[]).map((id) => (
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
