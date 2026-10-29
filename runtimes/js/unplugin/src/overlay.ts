/**
 * The in-product editor's loader (RFC 0004 §5.1, the build-time layer): a
 * small module from `@klarlabs-studio/glossa-runtime/dev`, with the Studio origin, the
 * overlay's SRI hash, the tenant and the project inlined, added to builds
 * for a Glossa environment that isn't `production`, and never to others.
 * A production build that asks for it fails.
 *
 * How it gets in, per bundler (order doesn't matter: runtimes list
 * themselves on the page, so the loader finds them whenever it runs):
 * - Vite: a `<script type="module">` for the virtual module in every HTML
 *   entry (`transformIndexHtml`); Astro pages import it through
 *   `@klarlabs-studio/glossa-astro`'s page script.
 * - Rollup and Rolldown: an import appended to every entry module.
 * - webpack: a global entry, added to every entrypoint.
 * - esbuild: `inject`, which adds it to every entry point.
 */
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { isProduction } from "@klarlabs-studio/glossa-runtime/dev";
import type { OverlayLoaderConfig } from "@klarlabs-studio/glossa-runtime/dev";
import type { UnpluginContextMeta, UnpluginOptions } from "unplugin";

import type { GlossaPluginOptions, StudioOptions } from "./options.js";

/** The loader's module ID: what HTML, entries and page scripts import. */
export const LOADER_ID = "virtual:glossa/overlay-loader";
const RESOLVED_ID = `\0${LOADER_ID}`;

const PREFIX = "[@klarlabs-studio/glossa-unplugin]";
const fail = (message: string) => new Error(`${PREFIX} ${message}`);

/** `sha384-` and 48 bytes of base64: the hash Studio's overlay.json publishes. */
const SRI = /^sha384-[A-Za-z0-9+/]{64}$/;
const ID = /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$/;
const LOCAL = new Set(["localhost", "127.0.0.1", "[::1]"]);

/** An origin: https (http only on localhost), no path, query or fragment. */
function origin(name: string, value: unknown): string {
  let url: URL;
  try {
    url = new URL(String(value));
  } catch {
    throw fail(`${name} must be an origin like https://studio.example.com, not ${JSON.stringify(value)}`);
  }
  const secure = url.protocol === "https:" || (url.protocol === "http:" && LOCAL.has(url.hostname));
  const bare = url.pathname === "/" && !url.search && !url.hash && !url.username && !url.password;
  if (!secure || !bare) {
    throw fail(`${name} must be an https origin (http only on localhost) with no path, not ${JSON.stringify(value)}`);
  }
  return url.origin;
}

function studioConfig(environment: string, s: StudioOptions | undefined): OverlayLoaderConfig {
  if (!s || typeof s !== "object") {
    throw fail(
      `the overlay loader is on for environment "${environment}" and needs \`studio\`: ` +
        "{ origin, integrity, tenant, project }. Set overlay: false to build without it.",
    );
  }
  if (typeof s.integrity !== "string" || !SRI.test(s.integrity)) {
    throw fail(
      "studio.integrity must be the overlay's SRI hash (sha384-…), as Studio's /overlay/v1/overlay.json publishes it",
    );
  }
  for (const key of ["tenant", "project"] as const) {
    if (typeof s[key] !== "string" || !ID.test(s[key])) throw fail(`studio.${key} must be a Glossa ID, not ${JSON.stringify(s[key])}`);
  }
  const config: OverlayLoaderConfig = {
    studio: origin("studio.origin", s.origin),
    integrity: s.integrity,
    tenant: s.tenant,
    project: s.project,
  };
  if (s.api !== undefined) config.api = origin("studio.api", s.api);
  return config;
}

/**
 * The build-time guard. The loader's configuration when this build gets
 * it; undefined when it doesn't. Throws when the options ask for the
 * overlay in production, or for one it can't build.
 */
export function overlayConfig(o: GlossaPluginOptions): OverlayLoaderConfig | undefined {
  const env = o.environment;
  if (env !== undefined && (typeof env !== "string" || !env.trim())) {
    throw fail("environment must be the name of the Glossa environment this build is deployed to");
  }
  if (o.overlay !== undefined && typeof o.overlay !== "boolean") throw fail("overlay must be true or false");
  if (env === undefined) {
    if (o.overlay) {
      throw fail("overlay: true needs `environment`, the Glossa environment this build is deployed to (never production)");
    }
    return undefined;
  }
  if (isProduction(env)) {
    if (o.overlay) {
      throw fail(
        `overlay: true with environment "${env}": the in-product editor is never in a production build ` +
          "(RFC 0004 §5.1). Remove overlay, or build for a non-production environment.",
      );
    }
    return undefined;
  }
  if (o.overlay === false) return undefined;
  return studioConfig(env, o.studio);
}

/** A relative import in the loader, which has to be inlined with it. */
const RELATIVE_IMPORT = /^import\s*\{[^}]*\}\s*from\s*"\.\/([\w.-]+)";?$/gm;

/** Anything left that would need resolving at build time. */
const ANY_IMPORT = /^\s*(?:import|export)\s[^;]*\bfrom\s/m;

function read(file: string): string {
  return readFileSync(file, "utf8").replace(/\n\/\/# sourceMappingURL=\S*\s*$/, "\n");
}

/**
 * The loader module: `@klarlabs-studio/glossa-runtime/dev`, inlined so it needs no
 * resolution, then started.
 *
 * The loader is injected as a virtual module with no place on disk, so a
 * relative import inside it would have nothing to resolve against
 * (`Could not resolve './grant.js'`). Its own files are therefore pulled
 * in with it — one level, which is all the loader has — and anything
 * left that would need resolving fails the build here rather than in
 * every application that uses the plugin.
 */
export function loaderModule(config: OverlayLoaderConfig): string {
  // Where the package that ships the loader keeps it: the file's own folder holds its relative imports.
  const dist = dirname(fileURLToPath(import.meta.resolve("@klarlabs-studio/glossa-runtime/dev")));
  const parts: string[] = [];
  const dev = read(join(dist, "dev.js")).replace(RELATIVE_IMPORT, (_line, file: string) => {
    parts.push(read(join(dist, file)));
    return "";
  });
  const module = `${parts.join("\n")}\n${dev}`;
  if (ANY_IMPORT.test(module)) {
    throw fail(
      "the overlay loader gained an import that can't be inlined; keep @klarlabs-studio/glossa-runtime/dev " +
        "self-contained, or teach loaderModule to resolve it",
    );
  }
  return `${module}\nloadOverlay(${JSON.stringify(config)});\n`;
}

/**
 * The loader as a file, for esbuild's `inject` and webpack's entries, which
 * take paths: under `node_modules/.cache`, never served. Its own
 * package.json says it has side effects, so a project's `"sideEffects":
 * false` can't tree-shake it away.
 */
export function loaderFile(root: string, code: string): string {
  const dir = join(root, "node_modules", ".cache", "glossa");
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, "package.json"), `${JSON.stringify({ type: "module", sideEffects: true })}\n`);
  writeFileSync(join(dir, "overlay-loader.js"), code);
  return join(dir, "overlay-loader.js");
}

interface RollupContext {
  getModuleInfo?: (id: string) => { isEntry?: boolean } | null;
}

export function overlayPlugin(config: OverlayLoaderConfig, meta: UnpluginContextMeta): UnpluginOptions {
  const code = loaderModule(config);
  const appendToEntries = meta.framework === "rollup" || meta.framework === "rolldown";
  const plugin: UnpluginOptions = {
    name: "@klarlabs-studio/glossa-unplugin:overlay",
    resolveId(id) {
      return id === LOADER_ID || id === `/${LOADER_ID}` ? RESOLVED_ID : null;
    },
    loadInclude: (id) => id === RESOLVED_ID,
    load(id) {
      return id === RESOLVED_ID ? code : null;
    },
    vite: {
      transformIndexHtml: {
        order: "pre",
        handler: () => [
          { tag: "script", attrs: { type: "module", src: `/${LOADER_ID}` }, injectTo: "head-prepend" },
        ],
      },
    },
    webpack(compiler) {
      // A global entry (no name): webpack adds it to every entrypoint.
      const file = loaderFile(resolve(compiler.context), code);
      new compiler.webpack.EntryPlugin(compiler.context, file, { name: undefined }).apply(compiler);
    },
    esbuild: {
      setup(build) {
        const o = build.initialOptions;
        const file = loaderFile(resolve(o.absWorkingDir ?? process.cwd()), code);
        o.inject = [...(o.inject ?? []), file];
      },
    },
  };
  if (appendToEntries) {
    plugin.transformInclude = (id) => !id.startsWith("\0");
    plugin.transform = function transform(source, id) {
      if (!(this as unknown as RollupContext).getModuleInfo?.(id)?.isEntry) return null;
      // Appended, so no line moves and the incoming source map stays right.
      return { code: `${source}\nimport ${JSON.stringify(LOADER_ID)};\n`, map: null };
    };
  }
  return plugin;
}
