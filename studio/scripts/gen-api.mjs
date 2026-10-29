#!/usr/bin/env node
// Generates src/api/schema.d.ts from the /v1 contract (platform/api/openapi.yaml).
//
//   node scripts/gen-api.mjs          write the file
//   node scripts/gen-api.mjs --check  fail if the committed file is stale (runs in `lint`)
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import openapiTS, { astToString } from "openapi-typescript";

const studio = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const spec = resolve(studio, "../platform/api/openapi.yaml");
const out = join(studio, "src/api/schema.d.ts");

const header = `/**
 * Generated from platform/api/openapi.yaml by scripts/gen-api.mjs
 * (openapi-typescript). Do not edit; run \`pnpm --filter @glossa/studio gen:api\`.
 */

`;
const ast = await openapiTS(pathToFileURL(spec), { alphabetize: false, exportType: false });
const generated = header + astToString(ast);

if (process.argv.includes("--check")) {
  let current = "";
  try {
    current = readFileSync(out, "utf8");
  } catch {
    // missing counts as stale
  }
  if (current !== generated) {
    console.error("src/api/schema.d.ts is stale: run `pnpm --filter @glossa/studio gen:api`.");
    process.exit(1);
  }
} else {
  writeFileSync(out, generated);
  console.log(`wrote ${out}`);
}
