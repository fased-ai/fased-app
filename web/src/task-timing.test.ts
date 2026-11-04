import { expect, it } from "vitest";
import { intervalWithUnit, taskInterval } from "./task-timing";

it("keeps switched intervals within the core's whole-second bounds", () => {
  expect(intervalWithUnit(86461, 86400, 3600)).toBe(3603);
  expect(intervalWithUnit(3600, 3600, 60)).toBe(60);
  expect(intervalWithUnit(60, 86400, 60)).toBe(60);
  expect(intervalWithUnit(604800, 60, 86400)).toBe(604800);
});

it("shares localized timing between Chat review and Tasks", () => {
  expect(taskInterval(60, "en")).toBe("1 minute");
  expect(taskInterval(3600, "en")).toBe("1 hour");
  expect(taskInterval(86400, "en")).toBe("1 day");
  expect(taskInterval(61, "en")).toBe("61 seconds");
  for (const locale of ["ru", "es", "zh", "ko"])
    expect(taskInterval(3600, locale)).not.toBe("1 hour");
});
