import { describe, expect, it } from "vitest";
import {
  defaultDashboard,
  parseDashboard,
  moveWidget,
  resizeWidget,
} from "./dashboard";

describe("Dashboard layout", () => {
  it("preserves an empty board, removed widgets, order and sizes", () => {
    expect(parseDashboard('{"version":1,"widgets":[]}').widgets).toEqual([]);
    const layout = resizeWidget(
      moveWidget(defaultDashboard(), "news", "agents"),
      "news",
      "wide",
    );
    const restored = parseDashboard(JSON.stringify(layout));
    expect(restored.widgets[0]).toEqual({ id: "news", size: "wide" });
    expect(restored.widgets.filter((w) => w.id === "news")).toHaveLength(1);
    expect(
      parseDashboard(
        '{"version":1,"widgets":[{"id":"tasks","size":"small"},{"id":"tasks","size":"wide"},{"id":"foreign","size":"wide"}]}',
      ).widgets,
    ).toEqual([{ id: "tasks", size: "small" }]);
    expect(
      parseDashboard(
        '{"version":1,"widgets":[{"id":"intel","size":"wide"},{"id":"predictions","size":"small"},{"id":"alerts","size":"small"},{"id":"desk","size":"small"},{"id":"trade","size":"wide"},{"id":"watchlists","size":"small"}]}',
      ).widgets,
    ).toEqual([
      { id: "intel", size: "wide" },
      { id: "predictions", size: "small" },
      { id: "alerts", size: "small" },
      { id: "desk", size: "small" },
      { id: "trade", size: "wide" },
      { id: "watchlists", size: "small" },
    ]);
  });
  it("recovers malformed or unsupported preferences without changing defaults", () => {
    const baseline = defaultDashboard();
    expect(parseDashboard("broken")).toEqual(baseline);
    expect(parseDashboard('{"version":99,"widgets":[]}')).toEqual(baseline);
    moveWidget(baseline, "tasks", "agents");
    expect(baseline).toEqual(defaultDashboard());
    expect(
      parseDashboard(
        '{"version":1,"widgets":[{"id":"intel","size":"wide"},{"id":"predictions","size":"small"},{"id":"alerts","size":"small"},{"id":"desk","size":"small"},{"id":"trade","size":"wide"},{"id":"watchlists","size":"small"}]}',
      ).widgets,
    ).toEqual([
      { id: "intel", size: "wide" },
      { id: "predictions", size: "small" },
      { id: "alerts", size: "small" },
      { id: "desk", size: "small" },
      { id: "trade", size: "wide" },
      { id: "watchlists", size: "small" },
    ]);
  });
});
