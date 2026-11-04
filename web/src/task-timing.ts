export function taskInterval(seconds: number, locale: string) {
  const [value, unit] =
    seconds % 86400 === 0
      ? [seconds / 86400, "day"]
      : seconds % 3600 === 0
        ? [seconds / 3600, "hour"]
        : seconds % 60 === 0
          ? [seconds / 60, "minute"]
          : [seconds, "second"];
  return new Intl.NumberFormat(locale, {
    style: "unit",
    unit,
    unitDisplay: "long",
  }).format(value as number);
}

export function intervalWithUnit(seconds: number, unit: number, next: number) {
  return Math.min(604800, Math.max(60, Math.round((seconds / unit) * next)));
}
