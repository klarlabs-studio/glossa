/**
 * Locale routing aligned with Astro's i18n routing (`i18n.locales`,
 * `defaultLocale`, `routing.prefixDefaultLocale`): the default locale lives
 * at the root unless it's prefixed, every other locale under `/{path}/`.
 * Pure functions; `@klarlabs-studio/glossa-astro/server` binds them to the site's config.
 */

/** One locale the site renders: its BCP 47 code and its URL segment (the same unless Astro maps it). */
export interface SiteLocale {
  code: string;
  path: string;
}

export interface Routing {
  locales: SiteLocale[];
  defaultLocale: string;
  prefixDefaultLocale: boolean;
  /** Astro's `base`, e.g. `/docs`; default `/`. */
  base?: string;
}

type AstroLocales = ReadonlyArray<string | { path: string; codes: readonly string[] }>;

/** Astro's `i18n.locales` (strings or `{ path, codes }`) as site locales. */
export const siteLocales = (locales: AstroLocales): SiteLocale[] =>
  locales.map((l) =>
    typeof l === "string" ? { code: l, path: l } : { code: l.codes[0]!, path: l.path },
  );

const find = (r: Routing, locale: string | undefined) => {
  const l = locale?.toLowerCase();
  return r.locales.find((x) => x.code.toLowerCase() === l || x.path.toLowerCase() === l);
};

/** The page's locale: Astro's current locale when the site renders it, else the default. */
export function pageLocale(current: string | undefined, r: Routing): string {
  return find(r, current)?.code ?? r.defaultLocale;
}

const trimBase = (r: Routing) => (r.base ?? "/").replace(/\/+$/, "");

/** `path` without the base and its locale prefix, always starting with `/`. */
export function stripLocale(path: string, r: Routing): string {
  const base = trimBase(r);
  let p = base && path.startsWith(base) ? path.slice(base.length) : path;
  if (!p.startsWith("/")) p = `/${p}`;
  const first = p.split("/")[1];
  return r.locales.some((l) => l.path === first) ? p.slice(first!.length + 1) || "/" : p;
}

/** `path` (without locale prefix) as a URL path in `locale`. */
export function localizePath(path: string, locale: string, r: Routing): string {
  const p = path.startsWith("/") ? path : `/${path}`;
  const l = find(r, locale) ?? find(r, r.defaultLocale);
  const prefix = !l || (l.code === r.defaultLocale && !r.prefixDefaultLocale) ? "" : `/${l.path}`;
  return `${trimBase(r)}${prefix}${p === "/" && prefix ? "/" : p}`;
}

/** `<link rel="alternate" hreflang>` targets for the page at `path`: every locale, plus `x-default`. */
export function alternates(path: string, r: Routing): Array<{ hreflang: string; href: string }> {
  const bare = stripLocale(path, r);
  return [
    ...r.locales.map((l) => ({ hreflang: l.code, href: localizePath(bare, l.code, r) })),
    { hreflang: "x-default", href: localizePath(bare, r.defaultLocale, r) },
  ];
}

/**
 * `getStaticPaths()` entries for a `[...locale]` route: `undefined` for the
 * unprefixed default locale, the locale's path segment otherwise.
 */
export function localeParams(r: Routing): Array<{ params: { locale: string | undefined } }> {
  return r.locales.map((l) => ({
    params: {
      locale: l.code === r.defaultLocale && !r.prefixDefaultLocale ? undefined : l.path,
    },
  }));
}
