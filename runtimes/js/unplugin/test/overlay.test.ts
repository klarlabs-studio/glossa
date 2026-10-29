/**
 * The overlay loader's build-time guard (RFC 0004 §5.1): real builds of a
 * small app on `@klarlabs-studio/glossa-runtime`, scanned for the loader. A build for a
 * preview environment has it (in Vite through an HTML module script, with
 * no inline code); a production build, or one that names no environment,
 * contains neither the loader nor the Studio overlay URL, whatever Vite's
 * `mode`. Asking for the overlay in production fails the build.
 */
import { mkdirSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { LOADER_ATTRIBUTE, OVERLAY_PATH } from "@klarlabs-studio/glossa-runtime/dev";
import * as esbuild from "esbuild";
import { rollup } from "rollup";
import { build as viteBuild } from "vite";
import webpack from "webpack";
import { describe, expect, it } from "vitest";

import { LOADER_ID } from "../src/overlay.js";
import { glossa } from "../src/plugin.js";
import type { GlossaPluginOptions } from "../src/options.js";

const here = dirname(fileURLToPath(import.meta.url));
/** Under the package, so the app resolves @klarlabs-studio/glossa-runtime from our node_modules. */
const WORK = resolve(here, "../.fixtures/overlay");

const STUDIO = "https://studio.glossa.test";
const INTEGRITY = `sha384-${"Q".repeat(64)}`;
const studio = { origin: STUDIO, integrity: INTEGRITY, tenant: "ten_1", project: "prj_1" };
const RUN = { application: "shop", commit: "0".repeat(40), branch: "main", usages: false } as const;

const MAIN = `import { createRuntime } from "@klarlabs-studio/glossa-runtime";
const rt = createRuntime({ environment: "preview", locales: "de", storage: null });
document.querySelector("#title").textContent = rt.t("cart.title", {}, { default: "Warenkorb" });
`;
const HTML = `<!doctype html><html><head><meta charset="utf-8"><title>Shop</title>
<script type="module" src="/src/main.ts"></script></head><body><h1 id="title"></h1></body></html>`;

function app(name: string): { root: string; out: string } {
  const root = join(WORK, name);
  rmSync(root, { recursive: true, force: true });
  mkdirSync(join(root, "src"), { recursive: true });
  writeFileSync(join(root, "index.html"), HTML);
  writeFileSync(join(root, "src", "main.ts"), MAIN);
  return { root, out: join(root, "dist") };
}

function files(dir: string): string[] {
  return readdirSync(dir).flatMap((f) => {
    const p = join(dir, f);
    return statSync(p).isDirectory() ? files(p) : [p];
  });
}

/** Everything a build wrote, one string per file. */
const output = (out: string) => files(out).map((f) => readFileSync(f, "utf8"));

/** What only the loader has: its script attribute, the overlay's URL, the pinned hash. */
function scan(texts: string[]) {
  const all = texts.join("\n");
  return {
    loader: all.includes(LOADER_ATTRIBUTE),
    overlayUrl: all.includes(OVERLAY_PATH),
    studio: all.includes(STUDIO),
    integrity: all.includes(INTEGRITY),
  };
}
const NONE = { loader: false, overlayUrl: false, studio: false, integrity: false };
const ALL = { loader: true, overlayUrl: true, studio: true, integrity: true };

async function vite(name: string, options: GlossaPluginOptions, mode = "production") {
  const { root, out } = app(name);
  await viteBuild({
    root,
    mode,
    configFile: false,
    logLevel: "silent",
    cacheDir: join(root, ".vite"),
    plugins: [glossa.vite({ ...RUN, ...options })],
    build: { outDir: out, emptyOutDir: true },
  });
  return { out, texts: output(out), html: readFileSync(join(out, "index.html"), "utf8") };
}

describe("Vite", () => {
  it("a production build contains no loader and no overlay URL", async () => {
    const { texts } = await vite("vite-production", { environment: "production", studio });
    expect(scan(texts)).toEqual(NONE);
    expect(texts.join("\n")).not.toContain("glossa.overlay");
  });

  it("a build that names no environment contains no loader", async () => {
    const { texts } = await vite("vite-none", { studio });
    expect(scan(texts)).toEqual(NONE);
  });

  it("a preview build has the loader, keyed on the Glossa environment, not Vite's mode", async () => {
    const { texts, html } = await vite("vite-preview", { environment: "preview", studio });
    expect(scan(texts)).toEqual(ALL);
    // Loaded as a module from the build's own origin: no inline script for a CSP to allow.
    const scripts = [...html.matchAll(/<script\b([^>]*)>([\s\S]*?)<\/script>/g)];
    expect(scripts.length).toBeGreaterThan(0);
    for (const [, attrs, body] of scripts) {
      expect(attrs).toMatch(/type="module"/);
      expect(attrs).toMatch(/src="\/assets\//);
      expect(body).toBe("");
    }
    expect(html).not.toContain(LOADER_ID);
  });

  it("overlay: false keeps it out of a preview build", async () => {
    const { texts } = await vite("vite-off", { environment: "preview", overlay: false, studio });
    expect(scan(texts)).toEqual(NONE);
  });
});

describe("the production guard", () => {
  // webpack creates its plugins when the compiler applies them; the others at once.
  const bundlers: Array<[string, (o: GlossaPluginOptions) => unknown]> = [
    ["vite", glossa.vite],
    ["rollup", glossa.rollup],
    ["webpack", (o) => webpack({ context: WORK, plugins: [glossa.webpack(o)] })],
    ["esbuild", glossa.esbuild],
  ];

  it.each(bundlers)("%s: overlay: true with a production environment fails", (_name, make) => {
    for (const environment of ["production", "Production", " production "]) {
      expect(() => make({ environment, overlay: true, studio })).toThrow(
        /overlay: true with environment ".*": the in-product editor is never in a production build/,
      );
    }
  });

  it("a Vite build with that configuration never starts", async () => {
    const { root, out } = app("vite-refused");
    await expect(
      (async () =>
        viteBuild({
          root,
          configFile: false,
          logLevel: "silent",
          plugins: [glossa.vite({ ...RUN, environment: "production", overlay: true, studio })],
          build: { outDir: out },
        }))(),
    ).rejects.toThrow(/never in a production build/);
    expect(() => readdirSync(out)).toThrow();
  });

  it("overlay: true needs an environment", () => {
    expect(() => glossa.vite({ overlay: true, studio })).toThrow(/needs `environment`/);
  });
});

describe("the loader's configuration", () => {
  const on = (s: unknown) => () => glossa.vite({ ...RUN, environment: "preview", studio: s as never });

  it("needs studio while the overlay is on", () => {
    expect(on(undefined)).toThrow(/needs `studio`.*overlay: false/);
  });

  it("checks the pinned hash, the origins and the IDs", () => {
    expect(on({ ...studio, integrity: "sha256-abc" })).toThrow(/SRI hash \(sha384-…\)/);
    expect(on({ ...studio, origin: "http://studio.glossa.test" })).toThrow(/https origin/);
    expect(on({ ...studio, origin: "https://studio.glossa.test/app" })).toThrow(/no path/);
    expect(on({ ...studio, api: "ftp://api.glossa.test" })).toThrow(/studio\.api/);
    expect(on({ ...studio, tenant: "" })).toThrow(/studio\.tenant/);
    expect(on({ ...studio, project: "prj 1" })).toThrow(/studio\.project/);
    expect(on({ ...studio, origin: "http://localhost:5173" })).not.toThrow();
  });
});

/** A script-only app, for the bundlers without HTML entries. */
function scriptApp(name: string) {
  const { root, out } = app(name);
  return { root, out, entry: join(root, "src", "main.ts") };
}

const stripTs: import("rollup").Plugin = {
  name: "strip-ts",
  async transform(code, id) {
    if (!id.endsWith(".ts")) return null;
    return { code: (await esbuild.transform(code, { loader: "ts" })).code, map: null };
  },
};

const nodeResolve: import("rollup").Plugin = {
  name: "resolve-runtime",
  async resolveId(source) {
    if (source === "@klarlabs-studio/glossa-runtime") return fileURLToPath(import.meta.resolve("@klarlabs-studio/glossa-runtime"));
    return null;
  },
};

async function rollupRun(name: string, options: GlossaPluginOptions) {
  const { out, entry } = scriptApp(name);
  const bundle = await rollup({
    input: entry,
    plugins: [nodeResolve, stripTs, glossa.rollup({ ...RUN, ...options })],
    onwarn: () => {},
  });
  await bundle.write({ dir: out, format: "es" });
  await bundle.close();
  return output(out);
}

async function esbuildRun(name: string, options: GlossaPluginOptions) {
  const { root, out, entry } = scriptApp(name);
  await esbuild.build({
    absWorkingDir: root,
    entryPoints: [entry],
    bundle: true,
    format: "esm",
    outdir: out,
    nodePaths: [resolve(here, "../node_modules")],
    logLevel: "silent",
    plugins: [glossa.esbuild({ ...RUN, ...options })],
  });
  return output(out);
}

async function webpackRun(name: string, options: GlossaPluginOptions) {
  const { root, out } = scriptApp(name);
  const compiler = webpack({
    mode: "production",
    context: root,
    entry: { main: "./src/main.ts" },
    output: { path: out },
    resolve: { extensions: [".ts", ".js"], modules: [resolve(here, "../node_modules"), "node_modules"] },
    module: {
      rules: [{ test: /\.ts$/, loader: join(here, "support", "esbuild-loader.cjs") }],
    },
    devtool: false,
    plugins: [glossa.webpack({ ...RUN, ...options })],
  });
  await new Promise<void>((done, fail) =>
    compiler.run((err, stats) => {
      if (err || stats?.hasErrors()) fail(err ?? new Error(stats!.toString("errors-only")));
      else compiler.close(() => done());
    }),
  );
  return output(out);
}

describe.each([
  ["rollup", rollupRun],
  ["esbuild", esbuildRun],
  ["webpack", webpackRun],
])("%s", (name, run) => {
  it("a preview build has the loader in its entry; a production build has none", async () => {
    expect(scan(await run(`${name}-preview`, { environment: "preview", studio }))).toEqual(ALL);
    expect(scan(await run(`${name}-production`, { environment: "production", studio }))).toEqual(NONE);
  });
});
