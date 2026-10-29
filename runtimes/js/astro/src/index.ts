/**
 * `glossa()`, the Astro integration:
 *
 * ```js
 * // astro.config.mjs
 * import vue from "@astrojs/vue";
 * import glossa from "@klarlabs-studio/glossa-astro";
 *
 * export default defineConfig({
 *   i18n: { locales: ["de", "en"], defaultLocale: "de" },
 *   integrations: [
 *     vue({ appEntrypoint: "@klarlabs-studio/glossa-astro/vue" }),
 *     glossa({ edge: "https://edge.example.com", deliveryKey: "pk_…", elements: true }),
 *   ],
 * });
 * ```
 *
 * At build start it loads one release (a `glossa pull --release` directory,
 * or the edge's current release) and every page renders with it: `.astro`
 * components through `getGlossa(Astro)`, `<glossa-*>` elements prerendered by
 * the middleware, Vue islands through the shared runtime. Static pages ship
 * translated HTML; islands and elements hydrate from the inlined slice of the
 * same release, then refresh from the edge.
 */
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import type { AstroConfig, AstroIntegration } from "astro";
import type { BundledRelease } from "@klarlabs-studio/glossa-runtime";
import { LOADER_ID, overlayConfig } from "@klarlabs-studio/glossa-unplugin";
import type { GlossaPluginOptions } from "@klarlabs-studio/glossa-unplugin";
import glossaUsages from "@klarlabs-studio/glossa-unplugin/vite";

import type { GlossaAstroOptions, PublicConfig } from "./config.js";
import { loadRelease } from "./release.js";
import { siteLocales } from "./routing.js";
import type { Routing } from "./routing.js";

type VitePlugins = NonNullable<NonNullable<AstroConfig["vite"]>["plugins"]>;

export type { GlossaAstroOptions, PublicConfig } from "./config.js";
export { fetchRelease, loadRelease, readRelease } from "./release.js";
export type { ReleaseSource } from "./release.js";
export type { Routing, SiteLocale } from "./routing.js";

/** The site's locales and routing: the integration's options, else Astro's i18n, else the release's. */
export function resolveRouting(
  o: GlossaAstroOptions,
  config: Pick<AstroConfig, "i18n" | "base">,
  release: BundledRelease | undefined,
): Routing {
  const i18n = config.i18n;
  const locales = siteLocales(
    o.locales ?? i18n?.locales ?? release?.manifest.locales.map((l) => l.code) ?? [],
  );
  const defaultLocale =
    o.defaultLocale ??
    i18n?.defaultLocale ??
    release?.manifest.sourceLocale ??
    locales[0]?.code ??
    "en";
  const r = i18n?.routing;
  const prefixDefaultLocale = typeof r === "object" && !!r.prefixDefaultLocale;
  return { locales, defaultLocale, prefixDefaultLocale, base: config.base };
}

/** Every message key in the release: what the typed accessors are named after. */
function releaseKeys(release: BundledRelease | undefined): string[] | undefined {
  if (!release) return undefined;
  return [...new Set(Object.values(release.artifacts).flatMap((a) => Object.keys(a.messages)))];
}

/**
 * `@klarlabs-studio/glossa-unplugin` for this site: file paths relative to Astro's root,
 * `.glossa/usages.json` beside `outDir` (never inside what gets deployed).
 */
export function usagesPlugin(
  options: GlossaAstroOptions["usages"],
  config: Pick<AstroConfig, "root" | "outDir">,
  release: BundledRelease | undefined,
  overlay: Pick<GlossaPluginOptions, "environment" | "overlay" | "studio"> = {},
): VitePlugins {
  if (options === false && !overlayConfig(overlay)) return [];
  return [
    glossaUsages({
      root: fileURLToPath(config.root),
      outDir: join(dirname(fileURLToPath(config.outDir)), ".glossa"),
      keys: releaseKeys(release),
      ...(options === false ? { usages: false } : options),
      ...overlay,
    }),
  ];
}

/** Serves `virtual:glossa/config` (public) and `virtual:glossa/release` (server-only). */
function virtualModules(values: Record<string, unknown>) {
  return {
    name: "@klarlabs-studio/glossa-astro:virtual",
    resolveId: (id: string) => (id in values ? `\0${id}` : undefined),
    load: (id: string) =>
      id.startsWith("\0") && id.slice(1) in values
        ? `export default ${JSON.stringify(values[id.slice(1)])};`
        : undefined,
  };
}

/** `requireRelease`, else the `GLOSSA_REQUIRE_RELEASE` environment variable (`1` or `true`), else off. */
export function requireRelease(options: Pick<GlossaAstroOptions, "requireRelease">): boolean {
  if (options.requireRelease !== undefined) return options.requireRelease;
  return ["1", "true"].includes(process.env.GLOSSA_REQUIRE_RELEASE?.toLowerCase() ?? "");
}

export default function glossa(options: GlossaAstroOptions = {}): AstroIntegration {
  return {
    name: "@klarlabs-studio/glossa-astro",
    hooks: {
      "astro:config:setup": async ({
        config,
        updateConfig,
        addMiddleware,
        injectScript,
        logger,
      }) => {
        const environment = options.environment ?? "production";
        // The overlay's build-time guard first: in production it throws.
        const editor = { environment, overlay: options.overlay, studio: options.studio };
        const loader = overlayConfig(editor);
        const src =
          typeof options.release === "string"
            ? resolve(fileURLToPath(config.root), options.release)
            : options.release;
        const release = await loadRelease(src, {
          edge: options.edge,
          deliveryKey: options.deliveryKey,
          environment,
          publicKeys: options.publicKeys,
        });
        if (release) {
          const { id, version } = release.manifest.release;
          logger.info(`rendering with release ${id} (version ${version})`);
        } else if (requireRelease(options)) {
          throw new Error(
            "@klarlabs-studio/glossa-astro: requireRelease is on but no release was loaded: set `release`, or `edge` and `deliveryKey` (is GLOSSA_DELIVERY_KEY unset in this build?). Pages would ship their inline defaults.",
          );
        } else {
          logger.warn(
            "no release configured (release, or edge + deliveryKey): pages render their inline defaults",
          );
        }
        const pub: PublicConfig = {
          edge: options.edge,
          deliveryKey: options.deliveryKey,
          environment,
          publicKeys: options.publicKeys,
          routing: resolveRouting(options, config, release),
          prerender: options.prerender ?? "static",
          inline: options.inline ?? "auto",
        };
        updateConfig({
          vite: {
            plugins: [
              virtualModules({
                "virtual:glossa/config": pub,
                "virtual:glossa/release": release ?? null,
              }),
              ...usagesPlugin(options.usages, config, release, editor),
            ],
            // The package imports virtual modules, so Vite has to process it, not pre-bundle or externalize it.
            optimizeDeps: { exclude: ["@klarlabs-studio/glossa-astro"] },
            ssr: { noExternal: ["@klarlabs-studio/glossa-astro"] },
          },
        });
        addMiddleware({ entrypoint: new URL("./middleware.js", import.meta.url), order: "pre" });
        if (options.elements) {
          const elements = fileURLToPath(new URL("./elements.js", import.meta.url));
          injectScript("page", `import ${JSON.stringify(elements)};`);
        }
        if (loader) injectScript("page", `import ${JSON.stringify(LOADER_ID)};`);
      },
    },
  };
}
