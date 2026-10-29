/**
 * `@klarlabs-studio/glossa-astro/server`: messages in `.astro` components, rendered on the
 * server (at build time for static pages) with the build's release.
 *
 * ```astro
 * ---
 * import { getGlossa, alternates } from "@klarlabs-studio/glossa-astro/server";
 * const { t, locale, dir } = getGlossa(Astro);
 * ---
 * <html lang={locale} dir={dir}>
 *   <head>{alternates(Astro).map((a) => <link rel="alternate" hreflang={a.hreflang} href={a.href} />)}</head>
 *   <h1>{t("home.title", {}, { default: "Willkommen" })}</h1>
 * </html>
 * ```
 */
import { AsyncLocalStorage } from "node:async_hooks";
import { createRuntime } from "@klarlabs-studio/glossa-runtime";
import type { Explanation, Part, Runtime } from "@klarlabs-studio/glossa-runtime";
import config from "virtual:glossa/config";
import release from "virtual:glossa/release";

import * as routing from "./routing.js";

const runtimes = new Map<string, Runtime>();

/** The server's runtime for `locale`: the build's release, no network, one per locale. */
export function runtimeFor(locale: string): Runtime {
  let rt = runtimes.get(locale);
  if (!rt) {
    rt = createRuntime({
      bundled: release ?? undefined,
      locales: locale,
      environment: config.environment,
      storage: null,
      refreshInterval: 0,
    });
    runtimes.set(locale, rt);
  }
  return rt;
}

/** The request being rendered; islands rendered on the server read their runtime from it. */
export const requestContext = new AsyncLocalStorage<{ locale: string; runtime: Runtime }>();

// `@klarlabs-studio/glossa-astro/client`'s getRuntime() looks here first, so a Vue island
// rendered on the server uses the page's runtime without importing Node code.
(globalThis as Record<symbol, unknown>)[Symbol.for("glossa.astro.server")] = () =>
  requestContext.getStore()?.runtime ?? runtimeFor(config.routing.defaultLocale);

export interface PageGlossa {
  /** The page's locale (the active one, after negotiation against the release). */
  locale: string;
  dir: "ltr" | "rtl";
  t(id: string, values?: Record<string, unknown>, opts?: { default?: string }): string;
  parts(id: string, values?: Record<string, unknown>, opts?: { default?: string }): Part[];
  explain(id: string): Explanation;
  release: { id: string; version: number } | undefined;
  runtime: Runtime;
}

/** Messages for the page being rendered, in its locale (`Astro.currentLocale`, else the default). */
export function getGlossa(astro: { currentLocale?: string | undefined }): PageGlossa {
  const locale = routing.pageLocale(astro.currentLocale, config.routing);
  const runtime = runtimeFor(locale);
  return {
    locale: runtime.locale ?? locale,
    dir: runtime.dir,
    t: runtime.t,
    parts: runtime.parts,
    explain: (id) => runtime.explain(id),
    release: runtime.release,
    runtime,
  };
}

/** The site's locales and routing, from Astro's i18n config or the integration's. */
export const siteRouting: routing.Routing = config.routing;

/** `path` in `locale`, following Astro's i18n routing. */
export const localizePath = (path: string, locale: string) =>
  routing.localizePath(path, locale, config.routing);

/** hreflang alternates of the current page, with `x-default`. */
export const alternates = (astro: { url: URL }) =>
  routing.alternates(astro.url.pathname, config.routing);

/** `getStaticPaths()` entries for a `[...locale]` route. */
export const localeParams = () => routing.localeParams(config.routing);
