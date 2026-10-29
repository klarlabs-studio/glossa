/**
 * Pure helpers for the health surfaces (RFC 0005 §8): the project
 * header's seven numbers, the per-locale row, and the per-locale layer
 * availability of intent §41.
 *
 * The rule the whole file exists to keep: **a number nobody measured is
 * not zero, and a layer nobody could run is not clean.** Every helper
 * here returns `undefined` — never a fallback `0` — when the summary
 * did not carry the number, and `layerState` names the six answers a
 * layer can give so that "could not run here" and "ran, found nothing"
 * can never share a rendering.
 */
import type { FindingLayer } from "../api/quality-schemas";
import type { LocaleHealth, SummaryCoverage, SummaryLayer, SummaryPercentiles } from "../api/quality-summary-schemas";
import { LAYERS } from "./quality";

/**
 * What one layer says about one locale. Each of these renders with its
 * own word, not only its own colour:
 * - `unavailable` — it cannot run for this locale at all (intent §41).
 * - `unknown` — the summary did not mention it, so nothing is known.
 * - `not-checked` — it could run here, and the newest run did not.
 * - `errors` / `warnings` — it ran and found these.
 * - `clean` — it ran here and found nothing. The only green one.
 */
export type LayerState = "unavailable" | "unknown" | "not-checked" | "errors" | "warnings" | "clean";

/** A layer's state for a locale. Availability is asked first: an unavailable layer is never graded. */
export function layerState(l: SummaryLayer): LayerState {
  if (!l.available) return "unavailable";
  if (!l.checked || !l.findings) return "not-checked";
  if (l.findings.errors > 0) return "errors";
  if (l.findings.warnings > 0) return "warnings";
  return "clean";
}

/** A layer as the summary reported it, or nothing when it left the layer out. */
export interface LocaleLayer {
  layer: FindingLayer;
  state: LayerState;
  reported: SummaryLayer | undefined;
}

/**
 * Every layer RFC 0005 §3 defines, in its order, with what this locale's
 * summary said about it. A layer the summary never mentioned is
 * `unknown` rather than assumed available and clean: guessing here is
 * exactly the failure this slice is meant to prevent.
 */
export function localeLayers(l: LocaleHealth): LocaleLayer[] {
  const by = new Map(l.layers.map((x) => [x.layer, x]));
  return LAYERS.map((layer) => {
    const reported = by.get(layer);
    return { layer, state: reported ? layerState(reported) : "unknown", reported };
  });
}

/** How many of a locale's layers can run for it at all, and how many there are. */
export function availableLayers(layers: readonly LocaleLayer[]): { available: number; total: number } {
  return { available: layers.filter((l) => l.state !== "unavailable").length, total: layers.length };
}

/** Translated over active messages, or nothing when there is no coverage to take a share of. */
export function coverageShare(c: SummaryCoverage | undefined): number | undefined {
  if (!c || c.messages <= 0) return undefined;
  return c.translated / c.messages;
}

/** A share of a whole, or nothing when the whole is zero — 0/0 is not 0 %. */
export function share(part: number | undefined, whole: number | undefined): number | undefined {
  if (part === undefined || whole === undefined || whole <= 0) return undefined;
  return part / whole;
}

const UNITS = [
  { unit: "day", seconds: 86_400 },
  { unit: "hour", seconds: 3_600 },
  { unit: "minute", seconds: 60 },
  { unit: "second", seconds: 1 },
] as const;

/**
 * A span of seconds in the largest unit that leaves a number worth
 * reading: "2.5 days", "40 minutes". CLDR through `Intl`, so it is a
 * real localized unit and not a hand-built suffix.
 */
export function duration(seconds: number, locale?: string): string {
  const u = UNITS.find((x) => seconds >= x.seconds) ?? UNITS[UNITS.length - 1]!;
  const n = seconds / u.seconds;
  return new Intl.NumberFormat(locale, {
    style: "unit",
    unit: u.unit,
    unitDisplay: "long",
    maximumFractionDigits: n < 10 ? 1 : 0,
  }).format(n);
}

/** A percentage, or nothing when there was no share to take. */
export function percent(value: number | undefined, locale?: string): string | undefined {
  if (value === undefined) return undefined;
  return new Intl.NumberFormat(locale, { style: "percent", maximumFractionDigits: 0 }).format(value);
}

/** p50 and p90, when there is a sample behind them. A percentile over nothing is not a zero. */
export function percentiles(p: SummaryPercentiles | undefined): SummaryPercentiles | undefined {
  return p && p.samples > 0 ? p : undefined;
}

/**
 * The one number the project navigation carries (RFC 0005 §8): open
 * errors. `undefined` when nothing has ever been checked — the tab then
 * carries no badge at all, rather than a reassuring zero.
 */
export function openErrors(findings: { errors: number } | undefined): number | undefined {
  return findings?.errors;
}

/**
 * The project's locales as health rows with nothing measured, for when
 * the summary could not be read at all. Every number is absent and
 * every layer is `unknown`, which is the truth: a server that does not
 * report the summary has told us nothing about any locale, and a row of
 * zeroes would be a claim we cannot make.
 */
export function unmeasured(locales: readonly { code: string; direction: "ltr" | "rtl"; is_source: boolean }[]): LocaleHealth[] {
  return locales.map((l) => ({ code: l.code, direction: l.direction, is_source: l.is_source, layers: [] }));
}

/** The locales worth a health row, source first, then by code — the order a person reads them in. */
export function healthLocales(locales: readonly LocaleHealth[]): LocaleHealth[] {
  return [...locales].sort((a, b) => Number(b.is_source) - Number(a.is_source) || a.code.localeCompare(b.code));
}
