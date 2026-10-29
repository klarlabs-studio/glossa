/** Options of `@klarlabs-studio/glossa-unplugin`. */
import type { Routes } from "./usage.js";

export interface GlossaPluginOptions {
  /**
   * The application's slug in the Glossa project. Default: the root
   * package.json's name as a slug (`@acme/web` → `web`).
   */
  application?: string;
  /** Full commit ID. Default: `GITHUB_SHA`, else `git rev-parse HEAD`. */
  commit?: string;
  /**
   * Short branch name. Default: `GITHUB_HEAD_REF`, else `GITHUB_REF_NAME`,
   * else the checked-out branch.
   */
  branch?: string;
  /**
   * Where `usages.json` goes. Default: `.glossa/` next to the build output
   * (`dist/` → `.glossa/usages.json` beside it), never inside it, so it is
   * never served. Relative paths resolve against `root`.
   */
  outDir?: string;
  /**
   * The project root that `file` paths are relative to. Default: Vite's
   * `root`, webpack's `context`, esbuild's `absWorkingDir`, else the working
   * directory. Files outside it (and in node_modules) are not reported.
   */
  root?: string;
  /**
   * The catalog's message keys. They name the typed accessors from
   * `glossa generate` (`m.checkout.pay(…)` → `checkout.pay`), so without
   * them accessor calls aren't reported; every other call shape is.
   */
  keys?: Iterable<string> | (() => Iterable<string> | Promise<Iterable<string>>);
  /**
   * Route pattern → root-relative file globs (`**` crosses directories, `*`
   * doesn't). The first entry with a matching glob names a usage's route.
   * Astro's `src/pages/**` routes need no entry.
   */
  routes?: Routes;
  /**
   * Collect usages and write `usages.json`. Default `true`; `false` for a
   * build that only wants the overlay loader.
   */
  usages?: boolean;
  /**
   * The Glossa environment this build is deployed to (`production`,
   * `preview`, `pr-7`…). The in-product editor's loader is injected only
   * when it's set and isn't `production` (RFC 0004 §5.1). It's the Glossa
   * environment, not Vite's `mode`: static previews are built in production
   * mode too.
   */
  environment?: string;
  /**
   * Inject the overlay loader. Default: on when `environment` is set and
   * isn't `production`. `true` with `environment: "production"` fails the
   * build.
   */
  overlay?: boolean;
  /** What the overlay loader loads and edits; required while `overlay` is on. */
  studio?: StudioOptions;
}

/** The Studio the in-product editor comes from, and what it edits. */
export interface StudioOptions {
  /** Studio's origin, which serves the overlay at `/overlay/v1/overlay.js`. */
  origin: string;
  /**
   * The overlay script's SRI hash (`sha384-…`), from Studio's
   * `/overlay/v1/overlay.json`. Pinned in the build, never fetched by it.
   */
  integrity: string;
  /** The tenant ID the project belongs to. */
  tenant: string;
  /** The project ID (the manifest's `project`). */
  project: string;
  /** The API origin. Default: `origin`, since Studio serves `/v1`. */
  api?: string;
}
