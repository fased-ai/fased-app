export type GridWidgetSize = "small" | "wide" | "tall";

export type GridWidgetItem<ID extends string = string> = {
  id: ID;
  size: GridWidgetSize;
};

export type GridLayout<ID extends string = string> = {
  version: 1;
  widgets: GridWidgetItem<ID>[];
};

export function parseGridLayout<ID extends string>(
  raw: string | null,
  validIDs: readonly ID[],
  fallback: GridLayout<ID>,
): GridLayout<ID> {
  try {
    const v = JSON.parse(raw || "null");
    if (v?.version !== 1 || !Array.isArray(v.widgets)) return fallback;
    const valid = new Set<string>(validIDs);
    const seen = new Set<string>();
    const widgets: GridWidgetItem<ID>[] = [];
    for (const w of v.widgets) {
      if (!w || typeof w.id !== "string" || !valid.has(w.id) || seen.has(w.id))
        continue;
      seen.add(w.id);
      const size: GridWidgetSize =
        w.size === "wide" || w.size === "tall" ? w.size : "small";
      widgets.push({ id: w.id as ID, size });
    }
    return { version: 1, widgets };
  } catch {
    return fallback;
  }
}

export function moveGridWidget<ID extends string>(
  layout: GridLayout<ID>,
  id: ID,
  before?: ID,
): GridLayout<ID> {
  const item = layout.widgets.find((w) => w.id === id);
  if (!item || before === id) return layout;
  const widgets = layout.widgets.filter((w) => w.id !== id);
  const at = before ? widgets.findIndex((w) => w.id === before) : -1;
  widgets.splice(at < 0 ? widgets.length : at, 0, item);
  return { ...layout, widgets };
}

export function resizeGridWidget<ID extends string>(
  layout: GridLayout<ID>,
  id: ID,
  size: GridWidgetSize,
): GridLayout<ID> {
  return {
    ...layout,
    widgets: layout.widgets.map((w) => (w.id === id ? { ...w, size } : w)),
  };
}
