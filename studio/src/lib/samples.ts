/**
 * Sample values for the live preview: one editable value per argument,
 * derived from the message's argument metadata, and representative
 * numbers for each plural category of the *target* locale — so a
 * translator can see every form their language needs, not just the
 * source language's.
 */
import type { Argument } from "../api/schemas";

/** What the sample editor holds: text for inputs, typed on the way out. */
export type SampleInput = string;

const SAMPLE_DATE = "2026-03-14T15:09:26Z";

/** A sensible starting value for an argument. */
export function defaultSample(arg: Argument): SampleInput {
  if (arg.selector && (arg.selector.kind === "exact" || arg.selector.kind === "string")) {
    return arg.selector.keys.find((k) => k !== "*") ?? "other";
  }
  switch (arg.type) {
    case "number":
    case "integer":
      return "3";
    case "percent":
      return "0.42";
    case "currency":
      return "1234.5";
    case "unit":
      return "5";
    case "date":
    case "time":
    case "datetime":
      return SAMPLE_DATE;
    case "select":
      return arg.selector?.keys.find((k) => k !== "*") ?? "other";
    case "string":
      return /name/i.test(arg.name) ? "Ada" : arg.name;
  }
}

/** Is this argument numeric (so its sample is typed as a number)? */
export function isNumeric(arg: Argument): boolean {
  return ["number", "integer", "percent", "currency", "unit"].includes(arg.type);
}

export function isTemporal(arg: Argument): boolean {
  return arg.type === "date" || arg.type === "time" || arg.type === "datetime";
}

/** Convert sample inputs into formatter values. Invalid input stays a string, which the formatter reports. */
export function toValues(args: readonly Argument[], samples: Record<string, SampleInput>): Record<string, unknown> {
  const values: Record<string, unknown> = {};
  for (const arg of args) {
    const raw = samples[arg.name] ?? defaultSample(arg);
    if (isNumeric(arg)) {
      const n = Number(raw);
      values[arg.name] = raw.trim() !== "" && Number.isFinite(n) ? n : raw;
    } else if (isTemporal(arg)) {
      const d = new Date(raw);
      values[arg.name] = Number.isNaN(d.getTime()) ? raw : d;
    } else {
      values[arg.name] = raw;
    }
  }
  return values;
}

export interface PluralExample {
  category: Intl.LDMLPluralRule;
  value: number;
}

/**
 * One representative number per plural category of `locale`, smallest
 * first: English gives one → 1, other → 0; Arabic adds zero, two, few, many.
 */
export function pluralExamples(locale: string, type: Intl.PluralRuleType = "cardinal"): PluralExample[] {
  let rules: Intl.PluralRules;
  try {
    rules = new Intl.PluralRules(locale, { type });
  } catch {
    return [];
  }
  const wanted = new Set(rules.resolvedOptions().pluralCategories);
  const found = new Map<Intl.LDMLPluralRule, number>();
  for (let n = 0; n <= 1000 && found.size < wanted.size; n++) {
    const c = rules.select(n);
    if (!found.has(c)) found.set(c, n);
  }
  // Categories only fractions reach (e.g. "many" in some locales).
  for (const f of [0.5, 1.5, 2.5]) {
    const c = rules.select(f);
    if (!found.has(c)) found.set(c, f);
  }
  return [...found].map(([category, value]) => ({ category, value })).sort((a, b) => a.value - b.value);
}
