import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ComponentType,
  type PointerEvent,
  type ReactNode,
} from "react";
import {
  ArrowDown,
  ArrowUp,
  Maximize2,
  Menu,
  MoreHorizontal,
  Plus,
  Trash2,
} from "../icons";
import { useUI } from "../ui-preferences";
import { Dialog } from "./shared";
import {
  moveGridWidget,
  parseGridLayout,
  resizeGridWidget,
  type GridLayout,
  type GridWidgetSize,
} from "../widget-layout";

export type WidgetCatalogItem<ID extends string = string> = {
  id: ID;
  title: string;
  icon: ComponentType<{ size?: number; "aria-hidden"?: boolean }>;
  defaultSize?: GridWidgetSize;
};

type WidgetGridProps<ID extends string> = {
  storageKey: string;
  catalog: readonly WidgetCatalogItem<ID>[];
  defaultLayout: GridLayout<ID>;
  renderBody: (id: ID, size: GridWidgetSize) => ReactNode;
  headerActions?: ReactNode;
  renderWidgetHeaderExtra?: (id: ID) => ReactNode;
  addDialogOpen?: boolean;
  onAddDialogOpenChange?: (open: boolean) => void;
};

export function WidgetGrid<ID extends string>({
  storageKey,
  catalog,
  defaultLayout,
  renderBody,
  headerActions,
  renderWidgetHeaderExtra,
  addDialogOpen,
  onAddDialogOpenChange,
}: WidgetGridProps<ID>) {
  const { t } = useUI();
  const validIDs = catalog.map((c) => c.id);
  const metaMap = new Map(catalog.map((c) => [c.id, c]));

  const read = () => {
    try {
      return parseGridLayout(
        localStorage.getItem(storageKey),
        validIDs,
        defaultLayout,
      );
    } catch {
      return defaultLayout;
    }
  };

  const [layout, setLayout] = useState<GridLayout<ID>>(read);
  const [internalAdding, setInternalAdding] = useState(false);
  const adding = addDialogOpen ?? internalAdding;
  const setAdding = (v: boolean) => {
    setInternalAdding(v);
    onAddDialogOpenChange?.(v);
  };
  const [openMenu, setOpenMenu] = useState<ID | null>(null);
  const [expanded, setExpanded] = useState<ID | undefined>();
  const [over, setOver] = useState<ID | undefined>();
  const board = useRef<HTMLDivElement>(null);
  const dragging = useRef<ID | undefined>(undefined);
  const resizing = useRef<
    | {
        id: ID;
        x: number;
        y: number;
        width: number;
        height: number;
        size: GridWidgetSize;
      }
    | undefined
  >(undefined);

  useLayoutEffect(() => {
    setLayout(read());
  }, [storageKey]);

  useEffect(() => {
    const sync = (e: StorageEvent) => {
      if (e.key === storageKey || e.key === null) setLayout(read());
    };
    window.addEventListener("storage", sync);
    return () => window.removeEventListener("storage", sync);
  }, [storageKey]);

  useEffect(() => {
    const click = (e: Event) => {
      if (!(e.target as Element).closest(".widget-menu")) setOpenMenu(null);
    };
    const esc = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpenMenu(null);
    };
    document.addEventListener("pointerdown", click);
    document.addEventListener("keydown", esc);
    return () => {
      document.removeEventListener("pointerdown", click);
      document.removeEventListener("keydown", esc);
    };
  }, []);

  function save(next: GridLayout<ID>) {
    setLayout(next);
    try {
      localStorage.setItem(storageKey, JSON.stringify(next));
    } catch {}
  }

  function pointerMove(e: PointerEvent<HTMLButtonElement>) {
    const start = resizing.current;
    const grid = board.current;
    if (!start || !grid) return;
    const columns = Math.max(
      1,
      getComputedStyle(grid).gridTemplateColumns.split(" ").length,
    );
    const width = start.width + e.clientX - start.x;
    const height = start.height + e.clientY - start.y;
    const size: GridWidgetSize =
      columns > 1 && width >= grid.clientWidth * 0.45
        ? "wide"
        : height >= 360
          ? "tall"
          : "small";
    if (size === start.size) return;
    start.size = size;
    save(resizeGridWidget(layout, start.id, size));
  }

  return (
    <>
      {headerActions && <div className="widget-grid-actions">{headerActions}</div>}
      <div className="dashboard-grid" ref={board}>
        {layout.widgets.map((widget, index) => {
          const meta = metaMap.get(widget.id);
          if (!meta) return null;
          const Icon = meta.icon;
          const content = renderBody(widget.id, widget.size);
          return (
            <section
              key={widget.id}
              data-widget-id={widget.id}
              aria-label={t(meta.title)}
              className={`dashboard-widget size-${widget.size}${over === widget.id ? " is-over" : ""}`}
              onDragOver={(e) => {
                if (!dragging.current || dragging.current === widget.id) return;
                e.preventDefault();
                setOver(widget.id);
              }}
              onDragLeave={() =>
                setOver((v) => (v === widget.id ? undefined : v))
              }
              onDrop={(e) => {
                e.preventDefault();
                setOver(undefined);
                if (dragging.current) {
                  save(moveGridWidget(layout, dragging.current, widget.id));
                }
                dragging.current = undefined;
              }}
            >
              <header className="dashboard-widget-header">
                <button
                  type="button"
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
                  aria-label={t("Move {name}", { name: t(meta.title) })}
                  title={t("Move")}
                >
                  <Menu size={15} />
                </button>
                <div className="dashboard-title">
                  <Icon size={15} aria-hidden />
                  <h2>{t(meta.title)}</h2>
                </div>
                {renderWidgetHeaderExtra?.(widget.id)}
                <div className="widget-menu">
                  <button
                    type="button"
                    className="icon-button widget-menu-trigger"
                    aria-label={t("{name} options", { name: t(meta.title) })}
                    aria-expanded={openMenu === widget.id}
                    onClick={() =>
                      setOpenMenu((prev) =>
                        prev === widget.id ? null : widget.id,
                      )
                    }
                  >
                    <MoreHorizontal size={16} aria-hidden="true" />
                  </button>
                  {openMenu === widget.id && (
                    <div className="widget-menu-panel">
                      <div className="widget-size-row">
                        <span className="widget-size-label">{t("Size")}</span>
                        <div
                          className="segmented-control widget-size-control"
                          role="group"
                          aria-label={t("{name} size", { name: t(meta.title) })}
                        >
                          {(["small", "wide", "tall"] as const).map((sz) => (
                            <button
                              key={sz}
                              type="button"
                              aria-pressed={widget.size === sz}
                              onClick={() =>
                                save(resizeGridWidget(layout, widget.id, sz))
                              }
                            >
                              {t(sz[0].toUpperCase() + sz.slice(1))}
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
                            moveGridWidget(
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
                            moveGridWidget(
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
              <div className="dashboard-widget-body">{content}</div>
              <button
                type="button"
                className="widget-resize"
                aria-label={t("Resize {name}", { name: t(meta.title) })}
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
                    save(resizeGridWidget(layout, start.id, start.size));
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
                    const sizes: GridWidgetSize[] = ["small", "wide", "tall"];
                    save(
                      resizeGridWidget(
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
          type="button"
          className="dashboard-empty-board"
          onClick={() => setAdding(true)}
        >
          <Plus size={20} />
          <span>{t("Add widgets")}</span>
        </button>
      )}

      {adding && (
        <Dialog title={t("Widgets")} onClose={() => setAdding(false)}>
          <div className="widget-catalog">
            {catalog.map((item) => (
              <button
                type="button"
                className="secondary"
                key={item.id}
                disabled={layout.widgets.some((w) => w.id === item.id)}
                onClick={() =>
                  save({
                    ...layout,
                    widgets: [
                      ...layout.widgets,
                      { id: item.id, size: item.defaultSize ?? "small" },
                    ],
                  })
                }
              >
                <span>{t(item.title)}</span>
                <Plus size={14} />
              </button>
            ))}
          </div>
          <div className="dialog-footer">
            <button
              type="button"
              className="text-button"
              onClick={() => save(defaultLayout)}
            >
              {t("Reset layout")}
            </button>
            <button
              type="button"
              className="primary"
              onClick={() => setAdding(false)}
            >
              {t("Done")}
            </button>
          </div>
        </Dialog>
      )}

      {expanded && metaMap.has(expanded) && (
        <Dialog
          wide
          title={t(metaMap.get(expanded)!.title)}
          onClose={() => setExpanded(undefined)}
        >
          <div className="dashboard-expanded">
            {renderBody(expanded, "wide")}
          </div>
        </Dialog>
      )}
    </>
  );
}
