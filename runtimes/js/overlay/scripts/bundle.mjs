#!/usr/bin/env node
// The script Studio serves at /overlay/v1/overlay.js: src/standalone.ts with
// Lit and @klarlabs-studio/glossa-capture, one minified ES module, no source map, licence
// notices at the end. Deterministic: the same sources and lockfile give the same bytes,
// so the SRI hash Studio publishes (overlay.json) only changes with the code.
//
//   node scripts/bundle.mjs   → dist/bundle/overlay.js
import { readFileSync } from "node:fs";
import { build } from "esbuild";

// The overlay ships as @klarlabs-studio/glossa/overlay: the umbrella's version is its version.
const pkg = JSON.parse(readFileSync(new URL("../../glossa/package.json", import.meta.url), "utf8"));

await build({
  entryPoints: [new URL("../src/standalone.ts", import.meta.url).pathname],
  outfile: new URL("../dist/bundle/overlay.js", import.meta.url).pathname,
  bundle: true,
  format: "esm",
  platform: "browser",
  target: "es2022",
  minify: true,
  legalComments: "eof", // Lit's licence notices, once, at the end
  charset: "utf8",
  define: { OVERLAY_VERSION: JSON.stringify(pkg.version) },
  logLevel: "warning",
});
