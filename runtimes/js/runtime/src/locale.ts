/**
 * Locale negotiation and the fallback graph (runtimes/SPEC.md §4).
 * Deterministic and side-effect free, so `explain()` can reuse it.
 */
import type { Manifest } from "./manifest.js";

/** One step of a resolver chain: a value, or a function that returns one. */
export type LocaleResolver =
  | string
  | readonly string[]
  | null
  | undefined
  | (() => string | readonly string[] | null | undefined);

/** Canonicalize tags (`en_us` → `en-US`, `iw` → `he`), dropping invalid ones and repeats. */
export function canonicalLocales(tags: readonly string[]): string[] {
  const out: string[] = [];
  for (const tag of tags) {
    try {
      const c = Intl.getCanonicalLocales(tag.replace(/_/g, "-"))[0];
      if (c && !out.includes(c)) out.push(c);
    } catch {
      // Not a BCP 47 tag: skip it.
    }
  }
  return out;
}

/** Drop the last subtag, and a single-character subtag left before it (RFC 4647 §3.4). */
const truncate = (tag: string): string => {
  const s = tag.split("-");
  s.pop();
  while (s.length && s[s.length - 1]!.length < 2) s.pop();
  return s.join("-");
};

/** RFC 4647 §3.4 Lookup: the first requested tag, or truncation of it, that is available. */
export function lookupLocale(
  requested: readonly string[],
  available: readonly string[],
): string | undefined {
  for (let tag of requested)
    for (; tag; tag = truncate(tag)) if (available.includes(tag)) return tag;
  return undefined;
}

/**
 * The fallback chain for the active locale (SPEC §4.2): the locale, its
 * explicit edges depth-first (or, without an entry, its available
 * truncations), the `"*"` chain, then the source locale. Repeats are
 * dropped, which also stops cycles.
 */
export function fallbackChain(
  locale: string,
  m: Pick<Manifest, "fallback" | "sourceLocale" | "locales">,
): string[] {
  const fb = m.fallback ?? {};
  const own = Object.hasOwn(fb, locale);
  const chain: string[] = [];
  const add = (l: string) => !chain.includes(l) && chain.push(l) > 0;
  const expand = (l: string): void => {
    for (const f of (Object.hasOwn(fb, l) && fb[l]) || []) if (add(f)) expand(f);
  };
  add(locale);
  if (own) expand(locale);
  else
    for (let t = truncate(locale); t; t = truncate(t))
      if (m.locales.some((x) => x.code === t)) add(t);
  expand("*");
  add(m.sourceLocale);
  return chain;
}

/**
 * Run a resolver chain (intent §13) in priority order, e.g. explicit → user →
 * organization → request metadata → `navigatorLanguages`, and return the
 * requested locales, canonicalized. The manifest's source locale is the
 * implicit last step. A resolver that throws is skipped.
 */
export function resolveLocales(...resolvers: LocaleResolver[]): string[] {
  const out: string[] = [];
  for (const r of resolvers) {
    try {
      const v = typeof r === "function" ? r() : r;
      if (v) out.push(...(typeof v === "string" ? [v] : v));
    } catch {
      // A broken resolver must not break rendering.
    }
  }
  return canonicalLocales(out);
}

/** The browser's preferred languages (`navigator.languages`), if there is a navigator. */
export const navigatorLanguages = (): readonly string[] | undefined =>
  (globalThis as { navigator?: { languages?: readonly string[] } }).navigator?.languages;

/** The tags of an `Accept-Language` header, by quality, header order breaking ties. */
export function acceptLanguage(header: string | null | undefined): string[] {
  return (header ?? "")
    .split(",")
    .map((item, i) => {
      const [tag = "", ...params] = item.split(";").map((s) => s.trim());
      const q = params.find((p) => p.startsWith("q="));
      return { tag, q: q ? Number(q.slice(2)) : 1, i };
    })
    .filter((x) => x.tag && x.tag !== "*" && x.q > 0)
    .sort((a, b) => b.q - a.q || a.i - b.i)
    .map((x) => x.tag);
}
