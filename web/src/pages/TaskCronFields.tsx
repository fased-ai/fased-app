import type { TaskCalendar } from "../agents";
import { calendarFromInput, calendarInput } from "../task-calendar";
import { useUI } from "../ui-preferences";

export type CronSettings = {
  preset: "daily" | "weekdays" | "weekly" | "custom";
  time: string;
  weekday: string;
  expression: string;
  timeZone: string;
  until: string;
};
export function cronCalendar(c: CronSettings): TaskCalendar {
  const [hour, minute] = c.time.split(":").map(Number);
  if (
    c.preset !== "custom" &&
    (!/^\d{2}:\d{2}$/.test(c.time) || hour > 23 || minute > 59)
  )
    throw Error("Choose a time");
  const day =
    c.preset === "weekdays" ? "1-5" : c.preset === "weekly" ? c.weekday : "*";
  const expression =
    c.preset === "custom"
      ? c.expression.trim().replace(/\s+/g, " ")
      : `${minute} ${hour} * * ${day}`;
  return {
    version: "cron-v1",
    at: 0,
    timeZone: c.timeZone,
    expression,
    until: calendarFromInput(c.until, c.timeZone).at,
  };
}
export function timeZones(local: string) {
  return [...new Set([local, "UTC", ...Intl.supportedValuesOf("timeZone")])];
}
export function TaskCronFields({
  value: c,
  onChange,
}: {
  value: CronSettings;
  onChange: (v: CronSettings) => void;
}) {
  const { t } = useUI();
  const patch = (v: Partial<CronSettings>) => onChange({ ...c, ...v });
  return (
    <div className="task-calendar-fields">
      <label>
        {t("Repeat")}
        <select
          aria-label={t("Recurrence")}
          value={c.preset}
          onChange={(e) =>
            patch({ preset: e.target.value as CronSettings["preset"] })
          }
        >
          <option value="daily">{t("Daily")}</option>
          <option value="weekdays">{t("Weekdays")}</option>
          <option value="weekly">{t("Weekly")}</option>
          <option value="custom">{t("Custom cron")}</option>
        </select>
      </label>
      {c.preset === "custom" ? (
        <label>
          {t("Cron rule")}
          <input
            aria-label={t("Cron rule")}
            value={c.expression}
            placeholder="0 9 * * 1-5"
            required
            maxLength={120}
            onChange={(e) => patch({ expression: e.target.value })}
          />
        </label>
      ) : (
        <label>
          {t("Time")}
          <input
            aria-label={t("Time")}
            type="time"
            required
            value={c.time}
            onChange={(e) => patch({ time: e.target.value })}
          />
        </label>
      )}
      {c.preset === "weekly" && (
        <label>
          {t("Day")}
          <select
            aria-label={t("Day")}
            value={c.weekday}
            onChange={(e) => patch({ weekday: e.target.value })}
          >
            {[
              "Sunday",
              "Monday",
              "Tuesday",
              "Wednesday",
              "Thursday",
              "Friday",
              "Saturday",
            ].map((day, i) => (
              <option key={day} value={i}>
                {t(day)}
              </option>
            ))}
          </select>
        </label>
      )}
      <label>
        {t("Time zone")}
        <select
          aria-label={t("Time zone")}
          value={c.timeZone}
          onChange={(e) => {
            const next = e.target.value;
            let until = c.until;
            try {
              until = calendarInput(
                calendarFromInput(until, c.timeZone).at,
                next,
              );
            } catch {
              /* Keep invalid dates visible for correction. */
            }
            patch({ timeZone: next, until });
          }}
        >
          {timeZones(c.timeZone).map((z) => (
            <option key={z} value={z}>
              {z}
            </option>
          ))}
        </select>
      </label>
      <label>
        {t("End date")}
        <input
          type="datetime-local"
          aria-label={t("End date")}
          required
          value={c.until}
          onChange={(e) => patch({ until: e.target.value })}
        />
      </label>
    </div>
  );
}
