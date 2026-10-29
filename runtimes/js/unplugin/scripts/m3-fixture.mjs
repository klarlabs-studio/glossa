// The M3 exit test's fixture application, built for real (RFC 0004 §12).
//
// `pnpm --filter @klarlabs-studio/glossa-unplugin build:m3` runs three Vite builds of
// platform/internal/systemtest/m3/testdata/app — the app `go generate
// ./internal/systemtest/m3/...` writes — and puts what the Go test needs
// beside it:
//
//   built/preview/      the app as a preview deployment: the overlay
//                       loader is in it, and `glossa capture` drives it
//   built/production/   the same app built for production: no loader
//   usages.json         what @klarlabs-studio/glossa-unplugin saw in the preview build
//   usages.branch.json  the same, with the pull request's checkout page
//
// src/m3-fixture.test.ts fails when a checked-in file differs from a
// fresh build, so the exit test never runs on a stale bundle.
import { cpSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import vue from "@vitejs/plugin-vue";
import { build } from "vite";

// The built plugin, so the fixture is built by what ships.
import { glossa } from "../dist/plugin.js";

const here = dirname(fileURLToPath(import.meta.url));

/** The generated fixture app, in the platform module's testdata. */
export const APP = resolve(here, "../../../../platform/internal/systemtest/m3/testdata/app");

/** The commit and branch of the pull request's CI push (fixture.go). */
const BRANCH_COMMIT = "7d0e4b19af6c2538e1b7d940c6a2f85413ce0d7b";
const BRANCH_NAME = "feature/pickup-copy";

/** Every generated file, by its path under the app. */
export const outputs = ["usages.json", "usages.branch.json", "built/preview", "built/production"];

/**
 * The packages the app imports, resolved from this package's own
 * node_modules — for every importer, so one copy of vue and one of react
 * end up in the bundle. The app is outside the pnpm workspace on
 * purpose: it is test data of a Go module, not a workspace package, and
 * `pnpm -r lint` and `pnpm -r test` never see it.
 */
function aliases() {
  // [what the app imports, what resolves it here]. The app names the published package
  // (@klarlabs-studio/glossa/...); this build uses the sources it is assembled from, which name
  // each other by their workspace names, so both spellings land on the same files.
  const names = [
    ["vue"],
    ["react"],
    ["react-dom"],
    ["react-dom/client"],
    ["react/jsx-runtime"],
    ["react/jsx-dev-runtime"],
    ["@klarlabs-studio/glossa", "@klarlabs-studio/glossa-runtime"],
    ["@klarlabs-studio/glossa/vue", "@klarlabs-studio/glossa-vue"],
    ["@klarlabs-studio/glossa/react", "@klarlabs-studio/glossa-react"],
    ["@klarlabs-studio/glossa-runtime"],
    ["@klarlabs-studio/glossa-vue"],
    ["@klarlabs-studio/glossa-react"],
  ];
  return names.map(([name, source = name]) => ({
    find: new RegExp(`^${name.replace(/[/\\^$*+?.()|[\]{}]/g, "\\$&")}$`),
    replacement: fileURLToPath(import.meta.resolve(source)),
  }));
}

/** One build of the app. */
async function one({ root, outDir, usages, environment, commit, branch, work }) {
  const info = JSON.parse(readFileSync(join(root, "build.json"), "utf8"));
  const options = {
    application: info.application,
    commit: commit ?? info.commit,
    branch: branch ?? info.branch,
    routes: info.routes,
    keys: info.keys,
    outDir: join(work, ".glossa"),
    root,
    usages: usages !== false,
    environment,
    ...(environment === "production" ? {} : { studio: info.studio }),
  };
  await build({
    root,
    configFile: false,
    logLevel: "warn",
    cacheDir: join(work, ".vite"),
    esbuild: { jsx: "automatic", jsxImportSource: "react" },
    define: { "process.env.NODE_ENV": '"production"' },
    resolve: { alias: aliases() },
    plugins: [vue(), glossa.vite(options)],
    build: { outDir, emptyOutDir: true, chunkSizeWarningLimit: 4096 },
  });
  if (usages === false) return undefined;
  return readFileSync(join(work, ".glossa", "usages.json"), "utf8");
}

/** Builds everything into `dir` (the app itself, or a copy of it). */
export async function buildFixture(dir) {
  // The real path: the plugin reports files under its `root`, and macOS
  // hands out /var/folders/… for a /private/var/folders/… directory.
  const work = realpathSync(mkdtempSync(join(tmpdir(), "glossa-m3-")));
  try {
    const preview = await one({
      root: APP,
      outDir: join(dir, "built", "preview"),
      environment: "preview",
      work: join(work, "preview"),
    });
    const production = { built: join(dir, "built", "production") };
    await one({ root: APP, outDir: production.built, environment: "production", work: join(work, "production") });

    // The pull request changes one file. Build a copy of the app with it
    // in place, for the branch's usages; its bundle is not needed.
    const branchRoot = join(work, "branch-app");
    cpSync(APP, branchRoot, { recursive: true, filter: (p) => !p.includes(`${"built"}`) });
    cpSync(join(APP, "branch", "src", "pages", "CheckoutPage.vue"), join(branchRoot, "src", "pages", "CheckoutPage.vue"));
    const branch = await one({
      root: branchRoot,
      outDir: join(work, "branch-dist"),
      environment: "preview",
      commit: BRANCH_COMMIT,
      branch: BRANCH_NAME,
      work: join(work, "branch"),
    });
    mkdirSync(dir, { recursive: true });
    writeFileSync(join(dir, "usages.json"), stable(preview));
    writeFileSync(join(dir, "usages.branch.json"), stable(branch, branchRoot));
    return { preview, branch };
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
}

/** Pretty-prints a usages document, the way the committed file holds it. */
function stable(raw) {
  return `${JSON.stringify(JSON.parse(raw), null, 2)}\n`;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  await buildFixture(APP);
  const doc = JSON.parse(readFileSync(join(APP, "usages.json"), "utf8"));
  const branch = JSON.parse(readFileSync(join(APP, "usages.branch.json"), "utf8"));
  console.log(`m3 fixture: ${doc.usages.length} usages on ${doc.branch}, ${branch.usages.length} on ${branch.branch}`);
}
