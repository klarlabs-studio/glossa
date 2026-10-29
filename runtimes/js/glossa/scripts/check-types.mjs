#!/usr/bin/env node
// Typechecks a consumer of every subpath (test/consumer.ts) with declaration checking ON, so the
// published .d.ts files are checked too. Third-party declarations (astro pulls in storage drivers
// whose peers aren't installed) are not ours to fix, so errors located in node_modules are
// ignored; anything else fails.
import { spawnSync } from "node:child_process";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const tsc = resolve(root, "node_modules/typescript/bin/tsc");
const run = spawnSync(process.execPath, [tsc, "--noEmit", "-p", "tsconfig.json"], { cwd: root, encoding: "utf8" });
const ours = `${run.stdout}${run.stderr}`
  .split("\n")
  .filter((line) => /error TS\d+/.test(line) && !line.includes("node_modules/"));
if (ours.length) {
  console.error(ours.join("\n"));
  process.exit(1);
}
if (run.error) throw run.error;
console.log("@klarlabs-studio/glossa: every subpath typechecks for a consumer (declarations included)");
