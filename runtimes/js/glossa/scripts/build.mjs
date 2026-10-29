#!/usr/bin/env node
// Assembles @klarlabs-studio/glossa from the nine private source packages (RFC 0002
// Amendment 1): each package's compiled output is copied to dist/<subpath>/, and every
// reference to a sibling package is rewritten to the umbrella's public specifier, so the
// published files import each other only as `@klarlabs-studio/glossa[/subpath]`. A consumer
// therefore resolves exactly one copy of the runtime, and the custom elements and the
// Lit context are defined once.
//
//   pnpm --filter @klarlabs-studio/glossa build   (the nine sources build first, by workspace order)
//
// The build fails when the package.json exports map stops mirroring the sources' maps 1:1,
// when a source package has not been built, or when a sibling reference survives the rewrite.
import { cpSync, existsSync, mkdirSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const UMBRELLA = "@klarlabs-studio/glossa";

/** Source package directory (relative to this package) → its dist folder and public subpath prefix. */
export const SOURCES = [
  { dir: "../runtime", name: "runtime", pkg: "@klarlabs-studio/glossa-runtime", prefix: "" },
  { dir: "../elements", name: "elements", pkg: "@klarlabs-studio/glossa-elements", prefix: "/elements" },
  { dir: "../vue", name: "vue", pkg: "@klarlabs-studio/glossa-vue", prefix: "/vue" },
  { dir: "../react", name: "react", pkg: "@klarlabs-studio/glossa-react", prefix: "/react" },
  { dir: "../astro", name: "astro", pkg: "@klarlabs-studio/glossa-astro", prefix: "/astro" },
  { dir: "../unplugin", name: "unplugin", pkg: "@klarlabs-studio/glossa-unplugin", prefix: "/unplugin" },
  { dir: "../capture", name: "capture", pkg: "@klarlabs-studio/glossa-capture", prefix: "/capture" },
  { dir: "../overlay", name: "overlay", pkg: "@klarlabs-studio/glossa-overlay", prefix: "/overlay" },
  { dir: "../../../messageformat/js", name: "messageformat", pkg: "@klarlabs-studio/glossa-messageformat", prefix: "/messageformat" },
];

/** `@klarlabs-studio/glossa-<name>` plus an optional subpath, anywhere in a text. */
const SIBLING = new RegExp(
  `@klarlabs-studio/glossa-(${SOURCES.map((s) => s.name).join("|")})((?:/package\\.json)|(?:/[\\w-]+)*)(?![\\w-])`,
  "g",
);

/** The public specifier for a sibling reference. */
export function publicSpecifier(name, rest) {
  const source = SOURCES.find((s) => s.name === name);
  return `${UMBRELLA}${source.prefix}${rest}`;
}

export function rewrite(text) {
  return text.replace(SIBLING, (_all, name, rest) => publicSpecifier(name, rest));
}

/**
 * Vite is told to process the Astro integration (it imports virtual modules) by package name.
 * The package is now the umbrella, and `@klarlabs-studio/glossa/astro` is not a package name,
 * so these two lists must say `@klarlabs-studio/glossa`.
 */
const VITE_PACKAGE_LISTS = /(optimizeDeps: \{ exclude: |ssr: \{ noExternal: )\["@klarlabs-studio\/glossa-astro"\]/g;

/**
 * Places where a source file reaches for its own package folder, which the umbrella's layout moves
 * one level up. Each patch must match exactly once.
 */
const PATCHES = [
  {
    file: "unplugin/plugin.js",
    from: '("../package.json")',
    to: '("../../package.json")', // the tool's version is the umbrella's
  },
];

function listFiles(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) =>
    e.isDirectory() ? listFiles(join(dir, e.name)) : [join(dir, e.name)],
  );
}

const SHIPPED = /\.(?:js|d\.ts)$/;

function copyPackage(source, out) {
  const dist = resolve(root, source.dir, "dist");
  if (!existsSync(dist)) throw new Error(`${source.pkg} is not built (${dist})`);
  const target = join(out, source.name);
  for (const file of listFiles(dist)) {
    if (!SHIPPED.test(file)) continue; // no source maps: their sources aren't in the tarball
    const to = join(target, relative(dist, file));
    mkdirSync(dirname(to), { recursive: true });
    let text = readFileSync(file, "utf8").replace(/\n\/\/# sourceMappingURL=\S*\s*$/, "\n");
    if (source.name === "astro" && file.endsWith("/index.js")) {
      let hits = 0;
      text = text.replace(VITE_PACKAGE_LISTS, (_m, head) => (hits++, `${head}["${UMBRELLA}"]`));
      if (hits !== 2) throw new Error(`astro/index.js: expected 2 Vite package lists, found ${hits}`);
    }
    for (const patch of PATCHES.filter((p) => join(out, p.file) === to)) {
      if (text.split(patch.from).length !== 2) throw new Error(`${patch.file}: ${patch.from} must occur exactly once`);
      text = text.replace(patch.from, patch.to);
    }
    writeFileSync(to, rewrite(text));
  }
}

/** The exports the sources declare, as the umbrella must declare them: 1:1, under their prefix. */
export function expectedExports() {
  const expected = {};
  for (const source of SOURCES) {
    const pkg = JSON.parse(readFileSync(resolve(root, source.dir, "package.json"), "utf8"));
    for (const [key, value] of Object.entries(pkg.exports)) {
      if (key === "./package.json") continue; // the umbrella has its own
      const sub = key === "." ? source.prefix || "." : `${source.prefix}${key.slice(1)}`;
      const subpath = sub === "." ? "." : `.${sub}`;
      const place = (file) => file.replace(/^\.\/dist\//, `./dist/${source.name}/`);
      expected[subpath] =
        typeof value === "string"
          ? place(value)
          : Object.fromEntries(Object.entries(value).map(([cond, file]) => [cond, place(file)]));
    }
  }
  expected["./package.json"] = "./package.json";
  return expected;
}

function checkExports(pkg) {
  const expected = expectedExports();
  const actual = pkg.exports;
  const problems = [];
  for (const key of new Set([...Object.keys(expected), ...Object.keys(actual)])) {
    if (JSON.stringify(expected[key]) !== JSON.stringify(actual[key])) {
      problems.push(`${key}: expected ${JSON.stringify(expected[key])}, found ${JSON.stringify(actual[key])}`);
    }
  }
  if (problems.length) throw new Error(`package.json exports drifted from the sources:\n  ${problems.join("\n  ")}`);
}

/** Every exports target must exist, with the .d.ts beside each .js. */
function checkTargets(pkg) {
  for (const [key, value] of Object.entries(pkg.exports)) {
    const files = typeof value === "string" ? [value] : Object.values(value);
    for (const file of files) {
      if (!existsSync(resolve(root, file))) throw new Error(`exports ${key}: ${file} was not built`);
    }
  }
}

/** No sibling reference may survive, and no relative import may leave its subpath's folder. */
function checkRewritten(out) {
  const stale = [];
  for (const file of listFiles(out)) {
    const text = readFileSync(file, "utf8");
    if (new RegExp(`@klarlabs-studio/glossa-(${SOURCES.map((s) => s.name).join("|")})\\b`).test(text)) {
      stale.push(`${relative(root, file)}: names a source package`);
    }
    const area = relative(out, file).split("/")[0];
    for (const m of text.matchAll(/(?:from\s*|import\s*\(?\s*|import\s+)["'](\.\.[^"']*)["']/g)) {
      const resolved = resolve(dirname(file), m[1]);
      if (relative(join(out, area), resolved).startsWith("..")) {
        stale.push(`${relative(root, file)}: relative import ${m[1]} leaves ${area}/`);
      }
    }
  }
  if (stale.length) throw new Error(`cross-subpath references survived:\n  ${stale.join("\n  ")}`);
}

function main() {
  const pkg = JSON.parse(readFileSync(join(root, "package.json"), "utf8"));
  checkExports(pkg);
  const out = join(root, "dist");
  rmSync(out, { recursive: true, force: true });
  for (const source of SOURCES) copyPackage(source, out);
  const migration = resolve(root, "../elements/MIGRATION.md");
  if (existsSync(migration)) cpSync(migration, join(out, "elements/MIGRATION.md"));
  checkTargets(pkg);
  checkRewritten(out);
  const files = listFiles(out);
  const bytes = files.reduce((n, f) => n + statSync(f).size, 0);
  console.log(`@klarlabs-studio/glossa: ${files.length} files, ${(bytes / 1024).toFixed(0)} KiB in dist/`);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) main();
