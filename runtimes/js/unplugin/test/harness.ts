/**
 * Runs the shared usage fixtures (runtimes/testdata/usages, see its README)
 * through real bundler builds with the plugin from src/, and reads back the
 * `usages.json` each build writes.
 */
import { cpSync, existsSync, mkdirSync, readFileSync, readdirSync, rmSync, statSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, extname, isAbsolute, join, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";
import vue from "@vitejs/plugin-vue";
import Ajv2020Module from "ajv/dist/2020.js";
import * as esbuild from "esbuild";
import { rollup } from "rollup";
import type { Plugin as RollupPlugin } from "rollup";
import { build as viteBuild } from "vite";
import type { Plugin as VitePlugin } from "vite";
import webpack from "webpack";

import { glossa } from "../src/plugin.js";
import type { GlossaPluginOptions } from "../src/options.js";
import type { UsagesDocument } from "../src/usage.js";

const here = dirname(fileURLToPath(import.meta.url));
export const TESTDATA = resolve(here, "../../../testdata");
const CASES = join(TESTDATA, "usages");
/** Builds run in copies under the package (so Astro resolves its runtime from our node_modules). */
const WORK = resolve(here, "../.fixtures");

export interface FixtureCase {
  name: string;
  project: string;
  description: string;
  implementations: string[];
  keys: string[];
  routes?: Record<string, string[]>;
  expected: UsagesDocument;
  files: string[];
}

export const RUNNER: Pick<GlossaPluginOptions, "application" | "commit" | "branch"> = {
  application: "fixture",
  commit: "0123456789abcdef0123456789abcdef01234567",
  branch: "main",
};

function walk(dir: string, root = dir): string[] {
  return readdirSync(dir).flatMap((f) => {
    const p = join(dir, f);
    return statSync(p).isDirectory() ? walk(p, root) : [relative(root, p).split(sep).join("/")];
  });
}

export function fixtureCases(implementation = "unplugin"): FixtureCase[] {
  return readdirSync(CASES)
    .filter((d) => existsSync(join(CASES, d, "case.json")))
    .sort()
    .map((name) => {
      const c = JSON.parse(readFileSync(join(CASES, name, "case.json"), "utf8")) as Omit<FixtureCase, "name">;
      const project = join(CASES, name, "project");
      const expected = JSON.parse(readFileSync(join(CASES, name, "expected.json"), "utf8")) as UsagesDocument;
      return { ...c, name, project, expected, files: walk(project) };
    })
    .filter((c) => c.implementations.includes(implementation));
}

const schema = JSON.parse(readFileSync(join(TESTDATA, "schemas", "usages.v1.schema.json"), "utf8")) as object;
const Ajv2020 = ((Ajv2020Module as unknown as { default?: unknown }).default ?? Ajv2020Module) as typeof import("ajv/dist/2020.js").default;
const validateSchema = new Ajv2020({ allErrors: true, strict: false }).compile(schema);

/** Schema errors of a usages document, empty when it's valid. */
export function schemaErrors(doc: unknown): string[] {
  return validateSchema(doc) ? [] : (validateSchema.errors ?? []).map((e) => `${e.instancePath} ${e.message}`);
}

/** A fresh copy of the case's project: `<work>/project`, with the build output beside it. */
function workspace(c: FixtureCase, runner: string): { work: string; root: string } {
  const work = join(WORK, `${c.name}-${runner}`);
  rmSync(work, { recursive: true, force: true });
  mkdirSync(work, { recursive: true });
  const root = join(work, "project");
  cpSync(c.project, root, { recursive: true });
  return { work, root };
}

function options(c: FixtureCase, extra: GlossaPluginOptions = {}): GlossaPluginOptions {
  return { ...RUNNER, keys: c.keys, routes: c.routes, ...extra };
}

function read(path: string): UsagesDocument {
  return JSON.parse(readFileSync(path, "utf8")) as UsagesDocument;
}

const bare = (id: string) => !id.startsWith(".") && !id.startsWith("/") && !isAbsolute(id) && !id.startsWith("\0");
const EXTENSIONS = ["", ".ts", ".tsx", ".js", ".jsx", ".vue"];

function resolveRelative(source: string, importer: string): string | undefined {
  const base = resolve(dirname(importer.replace(/[?#].*$/, "")), source);
  return EXTENSIONS.map((e) => base + e).find((p) => existsSync(p) && statSync(p).isFile());
}

/** Imports the fixture doesn't contain (packages, the generated Vue registration) stay external. */
function externals(): VitePlugin & RollupPlugin {
  return {
    name: "fixture-externals",
    enforce: "pre",
    resolveId(source: string, importer: string | undefined) {
      if (!importer) return null;
      if (bare(source)) return { id: source, external: true };
      if (source.startsWith(".")) return resolveRelative(source, importer) ?? { id: source, external: true };
      return null;
    },
  };
}

/**
 * Vite: every file of the project is an entry; `.astro` projects go through
 * `astro build`. `ssr` builds the server bundle instead (Vue's SSR
 * compiler emits markup strings, not vnodes), without the HTML entries.
 */
export async function viteRun(c: FixtureCase, { ssr = false } = {}): Promise<{ doc: UsagesDocument; path: string }> {
  const { work, root } = workspace(c, ssr ? "vite-ssr" : "vite");
  const input = c.files
    .filter((f) => !f.endsWith(".astro") && !(ssr && f.endsWith(".html")))
    .map((f) => join(root, f));
  await viteBuild({
    root,
    configFile: false,
    logLevel: "silent",
    cacheDir: join(work, ".vite"),
    plugins: [
      vue({ template: { compilerOptions: { isCustomElement: (tag) => tag.startsWith("glossa-") } } }),
      glossa.vite(options(c)),
      externals(),
    ],
    build: { outDir: join(work, "dist"), emptyOutDir: true, minify: false, ssr, rollupOptions: { input } },
  });
  // Default location: .glossa/ beside the build output (work/dist → work/.glossa).
  const path = join(work, ".glossa", "usages.json");
  return { doc: read(path), path };
}

/** Astro: a real `astro build` of the project, with the plugin added to its Vite config. */
export async function astroRun(c: FixtureCase): Promise<{ doc: UsagesDocument; path: string }> {
  const { work, root } = workspace(c, "astro");
  const { build } = await import("astro");
  process.env.ASTRO_TELEMETRY_DISABLED = "1";
  await build({
    root,
    outDir: join(work, "dist"),
    logLevel: "silent",
    vite: {
      resolve: {
        alias: [{ find: "@klarlabs-studio/glossa/astro/server", replacement: join(here, "support", "glossa-astro-server.mjs") }],
      },
      plugins: [glossa.vite(options(c, { outDir: join(work, ".glossa") }))],
    },
  });
  const path = join(work, ".glossa", "usages.json");
  return { doc: read(path), path };
}

const LOADERS: Record<string, esbuild.Loader> = { ".ts": "ts", ".tsx": "tsx", ".js": "js", ".jsx": "jsx" };

/** Rollup: esbuild strips TS and JSX first (with a source map), then the plugin reads the result. */
export async function rollupRun(c: FixtureCase): Promise<UsagesDocument> {
  const { work, root } = workspace(c, "rollup");
  const strip: RollupPlugin = {
    name: "fixture-esbuild",
    async transform(code, id) {
      const loader = LOADERS[extname(id)];
      if (!loader) return null;
      const out = await esbuild.transform(code, { loader, sourcemap: "external", sourcefile: id, jsx: "automatic" });
      return { code: out.code, map: out.map };
    },
  };
  const bundle = await rollup({
    input: c.files.map((f) => join(root, f)),
    // Rollup has no project root of its own: pass it (the default is the working directory).
    plugins: [externals(), strip, glossa.rollup(options(c, { root }))],
    onwarn: () => {},
  });
  await bundle.write({ dir: join(work, "dist"), format: "es" });
  await bundle.close();
  return read(join(work, ".glossa", "usages.json"));
}

/** esbuild: the plugin compiles TS/JSX itself with esbuild's transform and reads that map. */
export async function esbuildRun(c: FixtureCase): Promise<UsagesDocument> {
  const { work, root } = workspace(c, "esbuild");
  await esbuild.build({
    absWorkingDir: root,
    entryPoints: c.files.map((f) => join(root, f)),
    bundle: true,
    format: "esm",
    packages: "external",
    outdir: join(work, "dist"),
    jsx: "automatic",
    logLevel: "silent",
    plugins: [glossa.esbuild(options(c))],
  });
  return read(join(work, ".glossa", "usages.json"));
}

const require = createRequire(import.meta.url);

/** webpack: a TS/JSX loader (esbuild, with a source map) runs first; the plugin is a post loader. */
export async function webpackRun(c: FixtureCase): Promise<UsagesDocument> {
  const { work, root } = workspace(c, "webpack");
  const compiler = webpack({
    mode: "none",
    context: root,
    entry: Object.fromEntries(c.files.map((f) => [f.replace(/[/.]/g, "_"), `./${f}`])),
    output: { path: join(work, "dist") },
    externalsType: "commonjs",
    externals: [({ request }, callback) => (request && bare(request) ? callback(undefined, request) : callback())],
    resolve: { extensions: [".ts", ".tsx", ".js", ".jsx"] },
    module: { rules: [{ test: /\.[jt]sx?$/, loader: require.resolve("./support/esbuild-loader.cjs") }] },
    optimization: { minimize: false },
    devtool: false,
    plugins: [glossa.webpack(options(c))],
  });
  await new Promise<void>((done, fail) =>
    compiler.run((err, stats) => {
      if (err || stats?.hasErrors()) fail(err ?? new Error(stats!.toString("errors-only")));
      else compiler.close(() => done());
    }),
  );
  return read(join(work, ".glossa", "usages.json"));
}

/** The document minus `tool`, which the fixture comparison ignores. */
export function comparable(doc: UsagesDocument): Omit<UsagesDocument, "tool"> {
  const { tool: _tool, ...rest } = doc;
  return rest;
}
