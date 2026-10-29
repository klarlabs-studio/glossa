/**
 * The unplugin factory: one core for Vite, Rollup, Rolldown, webpack and
 * esbuild. It never makes network calls and never changes the code; it
 * only reads each module after the framework transforms and writes
 * `usages.json` when the bundle is written.
 */
import { mkdirSync, writeFileSync } from "node:fs";
import { readFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { dirname, extname, isAbsolute, join, resolve } from "node:path";
import { Parser } from "acorn";
import type { SourceMapInput } from "@jridgewell/trace-mapping";
import { createUnplugin } from "unplugin";
import type { UnpluginContextMeta, UnpluginFactory, UnpluginOptions } from "unplugin";

import { detectBuild, problems } from "./build-info.js";
import type { BuildInfo } from "./build-info.js";
import { Collector } from "./collector.js";
import { stripQuery } from "./locate.js";
import { accessorPaths } from "./keys.js";
import type { GlossaPluginOptions } from "./options.js";
import { overlayConfig, overlayPlugin } from "./overlay.js";
import type { Node } from "./scan.js";
import type { UsagesDocument } from "./usage.js";

export const PLUGIN_NAME = "@klarlabs-studio/glossa-unplugin";
/** What usages documents name as their producer: the npm package, which ships this plugin as its `unplugin` subpath. */
export const TOOL_NAME = "@klarlabs-studio/glossa";
export const TOOL_VERSION = (createRequire(import.meta.url)("../package.json") as { version: string }).version;

const SCRIPT = /\.(?:[cm]?[jt]sx?|vue|astro)$/;
/** Sub-modules that aren't code: styles and asset imports. */
const NOT_CODE = /[?&](?:type=style|raw|url|inline|worker|sharedworker)(?:[&=]|$)/;

/** Modules worth scanning: the project's scripts, Vue SFCs and Astro components. */
export function scannable(id: string): boolean {
  if (id.startsWith("\0") || id.includes("/node_modules/") || id.includes("\\node_modules\\")) return false;
  const q = id.indexOf("?");
  if (q >= 0 && NOT_CODE.test(id.slice(q))) return false;
  return SCRIPT.test(stripQuery(id));
}

/** acorn, for bundlers whose plugin context has no parser of its own (webpack, esbuild). */
function acornParse(code: string): Node {
  const options = { ecmaVersion: "latest", allowHashBang: true, allowAwaitOutsideFunction: true } as const;
  try {
    return Parser.parse(code, { ...options, sourceType: "module" }) as unknown as Node;
  } catch {
    return Parser.parse(code, { ...options, sourceType: "script", allowReturnOutsideFunction: true }) as unknown as Node;
  }
}

function warn(message: string): void {
  console.warn(`[${PLUGIN_NAME}] ${message}`);
}

interface Context {
  parse?: (code: string) => unknown;
  getCombinedSourcemap?: () => SourceMapInput;
  getNativeBuildContext?: () => { framework: string; inputSourceMap?: SourceMapInput };
  environment?: { name?: string };
}

const ESBUILD_LOADERS: Record<string, "js" | "jsx" | "ts" | "tsx"> = {
  ".js": "js",
  ".mjs": "js",
  ".cjs": "js",
  ".jsx": "jsx",
  ".ts": "ts",
  ".mts": "ts",
  ".cts": "ts",
  ".tsx": "tsx",
};

function usagesPlugin(options: GlossaPluginOptions, meta: UnpluginContextMeta): UnpluginOptions {
  const collector = new Collector(process.cwd(), options.routes);
  let frameworkRoot: string | undefined;
  let buildOutput: string | undefined;
  let info: BuildInfo | undefined;
  let warned = false;

  const root = () => resolve(options.root ?? frameworkRoot ?? process.cwd());

  const outDir = () => {
    if (options.outDir) return resolve(root(), options.outDir);
    return buildOutput ? join(dirname(buildOutput), ".glossa") : join(root(), ".glossa");
  };

  const buildStart = async () => {
    collector.root = root();
    collector.reset();
    const keys = typeof options.keys === "function" ? await options.keys() : options.keys;
    collector.accessors = accessorPaths(keys ?? []);
    const detected = detectBuild(collector.root);
    info = {
      application: options.application ?? detected.application,
      commit: options.commit ?? detected.commit,
      branch: options.branch ?? detected.branch,
    };
  };

  const write = (output?: { dir?: string; file?: string }) => {
    const dir = output?.dir ?? (output?.file ? dirname(output.file) : undefined);
    // Rollup names its output only here; the other bundlers did at config time.
    if (dir && !buildOutput) buildOutput = resolve(root(), dir);
    const build = info ?? {};
    const wrong = problems(build);
    if (wrong.length > 0) {
      if (!warned) warn(`usages.json not written: ${wrong.join("; ")}`);
      warned = true;
      return;
    }
    const doc: UsagesDocument = {
      schema: "glossa.usages/v1",
      application: build.application!,
      commit: build.commit!,
      branch: build.branch!,
      tool: { name: TOOL_NAME, version: TOOL_VERSION },
      usages: collector.usages(),
    };
    const out = outDir();
    mkdirSync(out, { recursive: true });
    writeFileSync(join(out, "usages.json"), `${JSON.stringify(doc, null, 2)}\n`);
  };

  const scanCode = (scope: string, id: string, code: string, map: SourceMapInput | null | undefined, parse: (code: string) => unknown) => {
    let program: Node;
    try {
      program = parse(code) as Node;
    } catch (err) {
      warn(`can't parse ${stripQuery(id)} after the transforms, skipped: ${(err as Error).message}`);
      return;
    }
    collector.module(scope, id, code, map, program);
  };

  const plugin: UnpluginOptions = {
    name: PLUGIN_NAME,
    // After the framework plugins: Vue templates compiled, JSX and TS gone.
    enforce: "post",
    buildStart,
    writeBundle(...args: unknown[]) {
      write(args[0] as { dir?: string; file?: string } | undefined);
    },
    watchChange(id, change) {
      if (change.event === "delete") collector.forget(id);
    },
    vite: {
      apply: "build",
      configResolved(config) {
        frameworkRoot = config.root;
        buildOutput = resolve(config.root, config.build.outDir);
        // @vitejs/plugin-vue only maps SFC sub-modules back to the .vue file
        // when build.sourcemap is on. Ask it for maps anyway: they only feed
        // the combined map this plugin reads, not the bundle's output.
        for (const p of config.plugins) {
          const api = (p as { api?: { options?: Record<string, unknown> } }).api;
          if (p.name === "vite:vue" && api?.options) api.options = { ...api.options, sourceMap: true };
        }
      },
      transformIndexHtml: {
        order: "pre",
        handler(html, ctx) {
          collector.html("html", ctx.filename, html);
        },
      },
    },
    webpack(compiler) {
      frameworkRoot = compiler.options.context;
      if (compiler.options.output.path) buildOutput = compiler.options.output.path;
    },
    esbuild: {
      setup(build) {
        const o = build.initialOptions;
        frameworkRoot = o.absWorkingDir;
        const out = o.outdir ?? (o.outfile ? dirname(o.outfile) : undefined);
        if (out) buildOutput = isAbsolute(out) ? out : resolve(o.absWorkingDir ?? process.cwd(), out);
        // esbuild plugins see files before esbuild compiles them, so strip
        // TS and JSX here with esbuild's own transform and read its map.
        build.onLoad({ filter: /\.[cm]?[jt]sx?$/ }, async (args) => {
          if (args.namespace !== "file" || !scannable(args.path)) return undefined;
          const source = await readFile(args.path, "utf8");
          const loader = ESBUILD_LOADERS[extname(args.path)] ?? "js";
          const out = await build.esbuild.transform(source, {
            loader,
            sourcemap: "external",
            sourcefile: args.path,
            jsx: o.jsx === "preserve" ? "transform" : o.jsx,
            jsxFactory: o.jsxFactory,
            jsxFragment: o.jsxFragment,
            jsxImportSource: o.jsxImportSource,
            tsconfigRaw: o.tsconfigRaw,
          });
          scanCode("esbuild", args.path, out.code, JSON.parse(out.map) as SourceMapInput, acornParse);
          return undefined;
        });
      },
    },
  };

  if (meta.framework !== "esbuild") {
    plugin.transformInclude = scannable;
    plugin.transform = function transform(code, id) {
      const ctx = this as unknown as Context;
      const native = meta.framework === "vite" || meta.framework === "rollup" || meta.framework === "rolldown";
      const map = native ? ctx.getCombinedSourcemap?.() : ctx.getNativeBuildContext?.().inputSourceMap;
      const parse = native && ctx.parse ? (c: string) => ctx.parse!(c) : acornParse;
      scanCode(ctx.environment?.name ?? meta.framework, id, code, map, parse);
      return null;
    };
  }
  return plugin;
}

/**
 * The factory behind every bundler's plugin. Features are separate
 * plugins: the usage collector (unless `usages: false`) and the in-product
 * editor's overlay loader, which only a build for a non-production Glossa
 * `environment` gets (RFC 0004 §5.1). Options that ask for the overlay in a
 * production build throw here, so the build fails before it starts.
 */
export const unpluginFactory: UnpluginFactory<GlossaPluginOptions | undefined> = (options = {}, meta) => {
  const overlay = overlayConfig(options);
  const plugins: UnpluginOptions[] = [];
  if (options.usages !== false) plugins.push(usagesPlugin(options, meta));
  if (overlay) plugins.push(overlayPlugin(overlay, meta));
  return plugins.length === 1 ? plugins[0]! : plugins;
};

export const glossa = createUnplugin(unpluginFactory);
