import { expect, it } from "vitest";
import {
  calendarFromInput,
  calendarInput,
  calendarLabel,
} from "./task-calendar";

it("calendar UTC input is exact and rejects normalized or incomplete dates", () => {
  const c = calendarFromInput("2026-11-01T06:30", "UTC");
  expect(c).toEqual({ version: "once-at-v1", at: 1793514600, timeZone: "UTC" });
  expect(calendarInput(c.at, "UTC")).toBe("2026-11-01T06:30");
  for (const invalid of [
    "",
    "2026-02-30T12:00",
    "2026-11-01",
    "2026-11-01T25:00",
    "2026-11-01T06:30Z",
  ])
    expect(() => calendarFromInput(invalid, "UTC")).toThrow();
  expect(() => calendarFromInput("2026-11-01T06:30", "missing/zone")).toThrow();
});

it("calendar review distinguishes both repeated daylight-saving instants", () => {
  const first = {
    version: "once-at-v1" as const,
    at: 1793514600,
    timeZone: "America/Chicago",
  };
  expect(calendarLabel(first)).toContain("GMT-5");
  expect(calendarLabel({ ...first, at: first.at + 3600 })).toContain("GMT-6");
  expect(calendarLabel(first)).toContain("America/Chicago");
});
