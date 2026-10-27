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

it("named zones resolve exact wall time and refuse gaps or repeated times", () => {
  const c = calendarFromInput("2026-10-25T09:00", "Asia/Seoul");
  expect(c.at).toBe(Date.parse("2026-10-25T00:00:00Z") / 1000);
  expect(calendarInput(c.at, "Asia/Seoul")).toBe("2026-10-25T09:00");
  expect(() =>
    calendarFromInput("2026-11-01T01:30", "America/New_York"),
  ).toThrow("twice");
  expect(() =>
    calendarFromInput("2027-03-14T02:30", "America/Chicago"),
  ).toThrow("does not exist");
  expect(() =>
    calendarFromInput("2027-04-04T01:45", "Australia/Lord_Howe"),
  ).toThrow("twice");
});
