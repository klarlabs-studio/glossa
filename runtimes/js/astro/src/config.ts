/** Options of the `glossa()` integration, and what reaches the site's code. */
import type { BundledRelease, PublicKey } from "@klarlabs-studio/glossa-runtime";
import type { GlossaPluginOptions, StudioOptions } from "@klarlabs-studio/glossa-unplugin";

import type { Routing } from "./routing.js";

export interface GlossaAstroOptions {
  /** Edge origin. With `deliveryKey`: the build fetches its release here, and islands refresh from it. */
  edge?: string;
  /** The project's publishable delivery key. */
  deliveryKey?: string;
  /** Default `production`. */
  environment?: string;
  /** Trusted manifest-signing keys. */
  publicKeys?: PublicKey[];
  /**
   * The release to build with: a `glossa pull --release` directory (relative
   * to the project root) or a release object. Default: the edge's current
   * release, fetched once at build start.
   */
  release?: string | BundledRelease;
  /**
   * Fail the build instead of warning when no release could be loaded (no `release`, and no
   * `edge` + `deliveryKey`, e.g. `GLOSSA_DELIVERY_KEY` unset in CI), so a production image can't
   * ship inline defaults by accident. Default: off, so local development keeps warning; when
   * unset, the `GLOSSA_REQUIRE_RELEASE=1` environment variable turns it on, which production
   * Dockerfiles and CI can do without touching the config.
   */
  requireRelease?: boolean;
  /** The site's locales. Default: Astro's `i18n.locales`, else the release's. */
  locales?: Array<string | { path: string; codes: string[] }>;
  /** Default: Astro's `i18n.defaultLocale`, else the release's source locale. */
  defaultLocale?: string;
  /**
   * Render `<glossa-*>` elements into the HTML: on prerendered pages
   * (`"static"`, the default), also on on-demand pages (`"all"`, which buffers
   * them instead of streaming), or never (`false`).
   */
  prerender?: "static" | "all" | false;
  /**
   * Inline the page locale's slice of the release, so islands and elements
   * hydrate with exactly the server's text: on pages with islands or providers
   * (`"auto"`, the default), on every page, or never.
   */
  inline?: "auto" | "always" | "never";
  /** Define the `<glossa-*>` elements on every page, sharing the page's runtime with islands. */
  elements?: boolean;
  /**
   * Where messages are used: `astro build` writes `.glossa/usages.json`
   * beside `outDir` through `@klarlabs-studio/glossa-unplugin` (RFC 0004 §2.1), for
   * `glossa context push`. On by default; these options go to the plugin
   * (`keys` defaults to the release's message keys). `false` turns it off.
   */
  usages?: false | GlossaPluginOptions;
  /**
   * The in-product editor's loader on every page (RFC 0004 §5.1). Default:
   * on when `environment` isn't `production`, which then needs `studio`.
   * `true` with `environment: "production"` fails the build.
   */
  overlay?: boolean;
  /** What the overlay loader loads and edits; see `@klarlabs-studio/glossa-unplugin`. */
  studio?: StudioOptions;
}

/** `virtual:glossa/config`: public, so it's safe in client bundles. */
export interface PublicConfig {
  edge?: string;
  deliveryKey?: string;
  environment: string;
  publicKeys?: PublicKey[];
  routing: Routing;
  prerender: "static" | "all" | false;
  inline: "auto" | "always" | "never";
}
