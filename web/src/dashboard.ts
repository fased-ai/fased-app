export const widgetNames = {
  agents: "Agents",
  tasks: "Tasks",
  news: "News",
  wallets: "Wallets",
  activity: "Activity",
  mining: "Mining",
  onchain: "Onchain",
  hyperliquid: "Hyperliquid",
} as const;
export type WidgetID = keyof typeof widgetNames;
export type WidgetSize = "small" | "wide" | "tall";
export type DashboardLayout = {
  version: 1;
  widgets: { id: WidgetID; size: WidgetSize }[];
};
export function defaultDashboard(): DashboardLayout {
  return {
    version: 1,
    widgets: ["agents", "tasks", "wallets", "news", "activity"].map((id) => ({
      id: id as WidgetID,
      size: id === "news" ? "wide" : "small",
    })),
  };
}
export function parseDashboard(raw: string | null): DashboardLayout {
  try {
    const v = JSON.parse(raw || "null");
    if (v?.version !== 1 || !Array.isArray(v.widgets))
      return defaultDashboard();
    const seen = new Set<string>();
    return {
      version: 1,
      widgets: v.widgets.flatMap((w: { id?: string; size?: string }) => {
        if (
          !w ||
          typeof w.id !== "string" ||
          !Object.hasOwn(widgetNames, w.id) ||
          seen.has(w.id)
        )
          return [];
        seen.add(w.id);
        return [
          {
            id: w.id as WidgetID,
            size: (["small", "wide", "tall"].includes(w.size || "")
              ? w.size
              : "small") as WidgetSize,
          },
        ];
      }),
    };
  } catch {
    return defaultDashboard();
  }
}
export function moveWidget(
  layout: DashboardLayout,
  id: WidgetID,
  before?: WidgetID,
): DashboardLayout {
  const item = layout.widgets.find((w) => w.id === id);
  if (!item || before === id) return layout;
  const widgets = layout.widgets.filter((w) => w.id !== id);
  const at = widgets.findIndex((w) => w.id === before);
  widgets.splice(at < 0 ? widgets.length : at, 0, item);
  return { ...layout, widgets };
}
export function resizeWidget(
  layout: DashboardLayout,
  id: WidgetID,
  size: WidgetSize,
): DashboardLayout {
  return {
    ...layout,
    widgets: layout.widgets.map((w) => (w.id === id ? { ...w, size } : w)),
  };
}
