import type { TaskCalendar } from "./agents";

export function calendarInput(at: number, zone: string) {
  const d = new Date(at * 1000);
  if (zone === "UTC") return d.toISOString().slice(0, 16);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// The UI offers the browser's own zone and UTC. Arbitrary named zones are
// supported by the shared CLI using an explicit Unix instant, without guessing
// a server-local time or adding a second timezone conversion implementation.
export function calendarFromInput(value: string, zone: string): TaskCalendar {
  const local = Intl.DateTimeFormat().resolvedOptions().timeZone;
  if (zone !== "UTC" && zone !== local) throw Error("Choose Local time or UTC");
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(value))
    throw Error("Choose a date and time");
  const at =
    new Date(value + (zone === "UTC" ? ":00Z" : ":00")).getTime() / 1000;
  if (!Number.isFinite(at) || calendarInput(at, zone) !== value)
    throw Error("That local time does not exist. Choose another time or UTC.");
  if (zone !== "UTC") {
    const offset = new Date(at * 1000).getTimezoneOffset();
    for (const day of [-2, 2]) {
      const other = new Date((at + day * 86400) * 1000).getTimezoneOffset();
      const candidate = at + (other - offset) * 60;
      if (candidate !== at && calendarInput(candidate, zone) === value)
        throw Error(
          "That local time occurs twice. Choose UTC for the exact time.",
        );
    }
  }
  return { version: "once-at-v1", at, timeZone: zone };
}

export function calendarLabel(c: TaskCalendar) {
  return `${new Intl.DateTimeFormat(undefined, {
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
