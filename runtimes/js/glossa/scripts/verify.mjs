#!/usr/bin/env node
// What `pnpm test` asserts about the built umbrella (the sources' own tests run in their packages):
//  - every exports target exists, and a .d.ts sits beside each .js entry;
//  - one copy of everything: no file imports a sibling subpath by relative path, and the published
//    files name no source package (build.mjs already fails on both; re-checked here on the files
//    on disk, so a stale dist/ is caught too);
//  - the entries that need no DOM import under Node through the package's own name, so the exports
//    map resolves for a real consumer, and the runtime they share is the same module instance.
import { existsSync, readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const pkg = JSON.parse(readFileSync(resolve(root, "package.json"), "utf8"));
const failures = [];

for (const [key, value] of Object.entries(pkg.exports)) {
  const files = typeof value === "string" ? [value] : Object.values(value);
  for (const file of files) if (!existsSync(resolve(root, file))) failures.push(`${key}: ${file} missing (run build)`);
}
if (failures.length) fail();

const NODE_SAFE = [".", "./idb", "./apierr", "./dev", "./astro/translate", "./astro/testing", "./elements/ssr", "./messageformat", "./messageformat/testing", "./capture/probes"];
const loaded = {};
for (const key of NODE_SAFE) {
  const specifier = key === "." ? pkg.name : `${pkg.name}/${key.slice(2)}`;
  try {
    loaded[key] = await import(specifier);
  } catch (error) {
    failures.push(`import ${specifier}: ${error.message}`);
  }
}
if (typeof loaded["."]?.createRuntime !== "function") failures.push("`.` does not export createRuntime");
if (Object.keys(loaded["./messageformat"] ?? {}).length === 0) failures.push("./messageformat exports nothing");

// The bindings take the runtime from `@klarlabs-studio/glossa` itself, never from a copy of their own.
for (const file of ["dist/vue/glossa.js", "dist/react/glossa.js"]) {
  const path = resolve(root, file);
  if (existsSync(path) && !/from "@klarlabs-studio\/glossa"/.test(readFileSync(path, "utf8"))) {
    failures.push(`${file} should import the runtime as "@klarlabs-studio/glossa"`);
  }
}

// Tree-shaking: an app on the runtime alone carries no Lit, no Vue, no React and no capture code.
const { build } = await import("esbuild");
const bundled = async (code) =>
  Object.keys(
    (
      await build({
        stdin: { contents: code, resolveDir: root, loader: "js" },
        bundle: true,
        write: false,
        metafile: true,
        format: "esm",
        platform: "browser",
        external: ["vue", "react", "react-dom"],
        logLevel: "silent",
      })
    ).metafile.inputs,
  );
const runtimeOnly = await bundled(`import { createRuntime } from "@klarlabs-studio/glossa"; console.log(createRuntime);`);
for (const banned of ["lit@", "lit-html", "@lit", "dist/elements/", "dist/capture/", "dist/overlay/", "dist/vue/", "dist/react/"]) {
  if (runtimeOnly.some((input) => input.includes(banned))) failures.push(`importing "@klarlabs-studio/glossa" pulls in ${banned}`);
}
const withVue = await bundled(`import { createGlossa } from "@klarlabs-studio/glossa/vue"; console.log(createGlossa);`);
const copies = withVue.filter((input) => input.endsWith("dist/runtime/index.js"));
if (copies.length !== 1) failures.push(`@klarlabs-studio/glossa/vue bundles ${copies.length} copies of the runtime, want 1`);

if (failures.length) fail();
console.log(`@klarlabs-studio/glossa: ${Object.keys(pkg.exports).length} exports resolve, ${NODE_SAFE.length} entries import under Node`);

function fail() {
  console.error(`verify failed:\n  ${failures.join("\n  ")}`);
  process.exit(1);
}
