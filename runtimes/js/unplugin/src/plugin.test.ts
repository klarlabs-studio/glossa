import { existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { build } from "vite";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { glossa, scannable, TOOL_NAME, TOOL_VERSION } from "./plugin.js";
import type { GlossaPluginOptions } from "./options.js";

const SHA = "0123456789abcdef0123456789abcdef01234567";
const CI = ["GITHUB_SHA", "GITHUB_HEAD_REF", "GITHUB_REF_NAME", "GITHUB_REF_TYPE"] as const;

/** A project outside any git checkout, so nothing but options and env name the build. */
function project(): string {
  const root = mkdtempSync(join(tmpdir(), "glossa-unplugin-"));
  mkdirSync(join(root, "src"));
  writeFileSync(join(root, "package.json"), JSON.stringify({ name: "@acme/shop" }));
  writeFileSync(join(root, "src", "main.ts"), `declare const t: (id: string) => string;\nexport const a = t("cart.title");\n`);
  return root;
}

async function viteBuild(root: string, options: GlossaPluginOptions) {
  await build({
    root,
    configFile: false,
    logLevel: "silent",
    plugins: [glossa.vite(options)],
    build: { outDir: "dist", lib: { entry: "src/main.ts", formats: ["es"] }, minify: false },
  });
}

describe("the plugin", () => {
  const saved: Partial<Record<(typeof CI)[number], string>> = {};
  beforeEach(() => {
    for (const k of CI) {
      saved[k] = process.env[k];
      delete process.env[k];
    }
  });
  afterEach(() => {
    for (const k of CI) if (saved[k] === undefined) delete process.env[k];
    else process.env[k] = saved[k];
    vi.restoreAllMocks();
  });

  it("writes .glossa/usages.json beside the build output, naming the build from CI variables", async () => {
    const root = project();
    process.env.GITHUB_SHA = SHA;
    process.env.GITHUB_HEAD_REF = "feat/cart";
    await viteBuild(root, {});
    const doc = JSON.parse(readFileSync(join(root, ".glossa", "usages.json"), "utf8"));
    expect(doc).toEqual({
      schema: "glossa.usages/v1",
      application: "shop",
      commit: SHA,
      branch: "feat/cart",
      tool: { name: TOOL_NAME, version: TOOL_VERSION },
      usages: [{ key: "cart.title", file: "src/main.ts", line: 2, column: 21, kind: "t" }],
    });
    expect(existsSync(join(root, "dist", ".glossa"))).toBe(false);
  });

  it("honours outDir and the options over the environment", async () => {
    const root = project();
    process.env.GITHUB_SHA = "f".repeat(40);
    await viteBuild(root, { outDir: "reports", application: "web", commit: SHA, branch: "main" });
    const doc = JSON.parse(readFileSync(join(root, "reports", "usages.json"), "utf8"));
    expect(doc).toMatchObject({ application: "web", commit: SHA, branch: "main" });
  });

  it("warns and writes nothing when the build can't be named", async () => {
    const root = project();
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    await viteBuild(root, { application: "web" });
    expect(existsSync(join(root, ".glossa"))).toBe(false);
    expect(warn).toHaveBeenCalledWith(expect.stringMatching(/usages\.json not written: no commit .*; no branch/));
  });
});

describe("scannable", () => {
  it("takes the project's scripts and SFC script/template modules, nothing else", () => {
    expect(scannable("/app/src/a.ts")).toBe(true);
    expect(scannable("/app/src/A.tsx")).toBe(true);
    expect(scannable("/app/src/A.vue?vue&type=script&setup=true&lang.ts")).toBe(true);
    expect(scannable("/app/src/page.astro")).toBe(true);
    expect(scannable("/app/src/A.vue?vue&type=style&index=0&lang.css")).toBe(false);
    expect(scannable("/app/src/a.ts?raw")).toBe(false);
    expect(scannable("/app/node_modules/x/index.js")).toBe(false);
    expect(scannable("\0virtual:thing.js")).toBe(false);
    expect(scannable("/app/src/a.css")).toBe(false);
  });
});
