/** "3 minutes ago" for recent moments, a date for older ones. */
const UNITS: Array<[Intl.RelativeTimeFormatUnit, number]> = [
  ["second", 60],
  ["minute", 60],
  ["hour", 24],
  ["day", 7],
];

export function relativeTime(iso: string, now: number = Date.now(), locale?: string): string {
  const then = Date.parse(iso);
  if (Number.isNaN(then)) return iso;
  let delta = Math.round((then - now) / 1000);
  const rtf = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
  for (const [unit, size] of UNITS) {
    if (Math.abs(delta) < size) return rtf.format(unit === "second" && Math.abs(delta) < 10 ? 0 : delta, unit);
    delta = Math.round(delta / size);
  }
  return new Date(then).toLocaleDateString(locale, { year: "numeric", month: "short", day: "numeric" });
}

export const absoluteTime = (iso: string, locale?: string): string =>
  new Date(iso).toLocaleString(locale, { dateStyle: "medium", timeStyle: "short" });
