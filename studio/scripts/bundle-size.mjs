#!/usr/bin/env node
// Reports Studio's initial JS (the entry chunk and everything it imports
// statically: what the browser must download before the first screen) and
// the lazy chunks, gzipped, from the Vite manifest. Fails over budget.
//
//   node scripts/bundle-size.mjs [--budget-kb 250]
import { readFileSync } from "node:fs";
import { join, resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { gzipSync } from "node:zlib";

const studio = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const dist = join(studio, "dist");
const budgetArg = process.argv.indexOf("--budget-kb");
const budgetKb = budgetArg > 0 ? Number(process.argv[budgetArg + 1]) : 250;

const manifest = JSON.parse(readFileSync(join(dist, ".vite", "manifest.json"), "utf8"));
const gz = (file) => gzipSync(readFileSync(join(dist, file)), { level: 9 }).length;
const kb = (n) => `${(n / 1024).toFixed(1)} kB`;

const entryKey = Object.keys(manifest).find((k) => manifest[k].isEntry);
const initial = new Set();
(function walk(key) {
  const chunk = manifest[key];
  if (!chunk || initial.has(chunk.file)) return;
  initial.add(chunk.file);
  for (const dep of chunk.imports ?? []) walk(dep);
})(entryKey);

const initialCss = [...new Set(Object.values(manifest).filter((c) => initial.has(c.file)).flatMap((c) => c.css ?? []))];
const initialJs = [...initial].reduce((sum, f) => sum + gz(f), 0);
const lazy = Object.values(manifest)
  .filter((c) => c.file.endsWith(".js") && !initial.has(c.file))
  .map((c) => ({ file: c.file, size: gz(c.file) }))
  .sort((a, b) => b.size - a.size);

console.log(`initial JS (gzip): ${kb(initialJs)}  [${[...initial].join(", ")}]`);
console.log(`initial CSS (gzip): ${kb(initialCss.reduce((s, f) => s + gz(f), 0))}`);
console.log("largest lazy chunks (gzip):");
for (const c of lazy.slice(0, 5)) console.log(`  ${kb(c.size).padStart(9)}  ${c.file}`);
if (initialJs > budgetKb * 1024) {
  console.error(`initial JS is over the ${budgetKb} kB budget`);
  process.exit(1);
}
