#!/usr/bin/env node
// Provides @klarlabs-studio/ui to Studio without the GitHub Packages registry.
//
// The design system is published to npm.pkg.github.com, which needs a token
// even for public packages, so a fresh clone of this open-source repo (and CI)
// can't install it from there. Until it is on a public registry, Studio
// depends on it as `link:.klarlabs-ui` and this script makes `.klarlabs-ui`
// point at a *built* copy of the package:
//
//   KLARLABS_UI_DIR=/path/to/klarlabs/packages/ui   use that checkout (it must be built:
//                                                  `pnpm --filter @klarlabs-studio/ui build`)
//   (unset)                                         clone the public repo at the commit pinned in
//                                                  package.json#klarlabsUi and build it once
//
// Both paths are gitignored. Switching to the registry is one line in
// package.json ("@klarlabs-studio/ui": "0.1.0") plus an .npmrc scope mapping;
// see studio/README.md.
import { execFileSync } from "node:child_process";
import { existsSync, lstatSync, mkdirSync, readFileSync, readlinkSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const studio = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const pkg = JSON.parse(readFileSync(join(studio, "package.json"), "utf8"));
const pin = pkg.klarlabsUi;
const link = join(studio, ".klarlabs-ui");
const built = (dir) => existsSync(join(dir, "dist", "tokens.css")) && existsSync(join(dir, "dist", "components"));

function pointLinkAt(target) {
  const rel = relative(studio, target) || ".";
  let current;
  try {
    current = lstatSync(link).isSymbolicLink() ? readlinkSync(link) : undefined;
  } catch {
    current = undefined;
  }
  if (current === rel) return;
  rmSync(link, { recursive: true, force: true });
  symlinkSync(rel, link, process.platform === "win32" ? "junction" : "dir");
}

function run(cmd, args, cwd) {
  execFileSync(cmd, args, { cwd, stdio: ["ignore", "inherit", "inherit"] });
}

const override = process.env.KLARLABS_UI_DIR;
if (override) {
  const dir = resolve(override);
  if (!built(dir)) {
    console.error(`klarlabs-ui: ${dir} has no dist/. Build it first: pnpm --filter @klarlabs-studio/ui build`);
    process.exit(1);
  }
  pointLinkAt(dir);
  console.log(`klarlabs-ui: using ${dir}`);
  process.exit(0);
}

const src = join(studio, ".klarlabs-ui-src");
const pkgDir = join(src, pin.directory);
const marker = join(src, ".glossa-pin");
if (existsSync(marker) && readFileSync(marker, "utf8").trim() === pin.commit && built(pkgDir)) {
  pointLinkAt(pkgDir);
  process.exit(0);
}

console.log(`klarlabs-ui: fetching ${pin.repository}@${pin.commit.slice(0, 12)} …`);
rmSync(src, { recursive: true, force: true });
mkdirSync(src, { recursive: true });
run("git", ["init", "--quiet"], src);
run("git", ["fetch", "--quiet", "--depth", "1", pin.repository, pin.commit], src);
run("git", ["-c", "advice.detachedHead=false", "checkout", "--quiet", "FETCH_HEAD"], src);
// Only the design system and its build tools; no lifecycle scripts.
run("pnpm", ["install", "--frozen-lockfile", "--ignore-scripts", "--filter", "@klarlabs-studio/ui"], src);
run("pnpm", ["--filter", "@klarlabs-studio/ui", "build"], src);
if (!built(pkgDir)) {
  console.error("klarlabs-ui: the build produced no dist/");
  process.exit(1);
}
writeFileSync(marker, `${pin.commit}\n`);
pointLinkAt(pkgDir);
console.log("klarlabs-ui: ready");
