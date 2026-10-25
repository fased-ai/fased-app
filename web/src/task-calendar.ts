import { resolveLanguage, translate } from "./ui-language";
import type { TaskCalendar } from "./agents";

export function calendarInput(at: number, zone: string) {
  const parts = new Intl.DateTimeFormat("en-CA", {
    timeZone: zone,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  }).formatToParts(new Date(at * 1000));
  const value = (key: string) => parts.find((p) => p.type === key)!.value;
  return `${value("year")}-${value("month")}-${value("day")}T${value("hour")}:${value("minute")}`;
}

// Resolve an explicit named-zone wall time through Intl. Missing times are
// refused; repeated times require UTC rather than guessing an offset.
export function calendarFromInput(value: string, zone: string): TaskCalendar {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(value))
    throw Error("Choose a date and time");
  const wall = Date.parse(value + ":00Z") / 1000;
  if (!Number.isFinite(wall) || calendarInput(wall, "UTC") !== value)
    throw Error("Choose a valid date and time");
  const offsets = new Set<number>();
  for (const day of [-2, -1, 0, 1, 2]) {
    const sample = wall + day * 86400;
    offsets.add(
      Date.parse(calendarInput(sample, zone) + ":00Z") / 1000 - sample,
    );
  }
  const candidates = [...offsets]
    .map((offset) => wall - offset)
    .filter((at) => calendarInput(at, zone) === value);
  if (candidates.length === 0)
    throw Error("That local time does not exist. Choose another time or UTC.");
  if (candidates.length > 1)
    throw Error("That local time occurs twice. Choose UTC for the exact time.");
  return { version: "once-at-v1", at: candidates[0], timeZone: zone };
}

export function calendarLabel(c: TaskCalendar) {
  const locale =
    typeof document === "undefined" ? undefined : document.documentElement.lang;
  const lang = resolveLanguage("system", [locale ?? "en"]);
  if (c.version === "cron-v1") {
    const f = (c.expression ?? "").split(" ");
    let label = c.expression ?? "";
    if (
      f.length === 5 &&
      /^\d+$/.test(f[0]) &&
      /^\d+$/.test(f[1]) &&
      f[2] === "*" &&
      f[3] === "*"
    ) {
      const time = `${f[1].padStart(2, "0")}:${f[0].padStart(2, "0")}`;
      if (f[4] === "*")
        label = translate(lang, "Daily at {time}").replace("{time}", time);
      else if (f[4] === "1-5")
        label = translate(lang, "Weekdays at {time}").replace("{time}", time);
      else if (/^[0-7]$/.test(f[4]))
        label = `${new Intl.DateTimeFormat(locale, { weekday: "long", timeZone: "UTC" }).format(new Date(Date.UTC(2023, 0, 1 + (Number(f[4]) % 7))))} · ${time}`;
    }
    return `${label} · ${c.timeZone}`;
  }
  return `${new Intl.DateTimeFormat(locale, {
    timeZone: c.timeZone,
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
    timeZoneName: "shortOffset",
  }).format(new Date(c.at * 1000))} · ${c.timeZone}`;
}
