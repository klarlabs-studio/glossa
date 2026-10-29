// @vitest-environment node
/**
 * Studio's build publishes the overlay (RFC 0004 §5.1): a real `vite build`
 * with the plugin writes `/overlay/v1/overlay.js` and an `overlay.json`
 * whose `integrity` is the hash of exactly those bytes — what a preview
 * deployment pins and the browser checks.
 */
import { createHash } from "node:crypto";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { afterAll, describe, expect, it } from "vitest";
import { build } from "vite";

import { OVERLAY_JS, OVERLAY_JSON, overlayAssets, overlayDelivery } from "./overlay";

// Inside the package, and not under node_modules: Vite's HTML build needs its
// root below the project, and a validation worktree symlinks node_modules out
// of it (warden), which puts the root outside again.
const cache = join(dirname(fileURLToPath(import.meta.url)), "..", ".tmp");
mkdirSync(cache, { recursive: true });
const work = mkdtempSync(join(cache, "overlay-"));
afterAll(() => rmSync(work, { recursive: true, force: true }));

async function studioBuild(): Promise<string> {
  const root = join(work, "app");
  const out = join(work, "dist");
  mkdirSync(root, { recursive: true });
  writeFileSync(
    join(root, "index.html"),
    "<!doctype html><html><head><title>Studio</title></head><body></body></html>",
  );
  await build({
    root,
    configFile: false,
    logLevel: "silent",
    plugins: [overlayDelivery()],
    build: { outDir: out, emptyOutDir: true },
  });
  return out;
}

describe("the overlay Studio serves", () => {
  it("publishes the bundle and an overlay.json whose integrity matches its bytes", async () => {
    const out = await studioBuild();
    const script = readFileSync(join(out, OVERLAY_JS));
    const release = JSON.parse(readFileSync(join(out, OVERLAY_JSON), "utf8")) as {
      version: string;
      integrity: string;
    };
    expect(script.length).toBeGreaterThan(1000);
    expect(script.toString("utf8")).toContain("glossa-overlay");
    expect(release.integrity).toBe(`sha384-${createHash("sha384").update(script).digest("base64")}`);
    expect(release.integrity).toMatch(/^sha384-[A-Za-z0-9+/]{64}$/);
    expect(release.version).toBe(overlayAssets().release.version);
  });

  it("serves the same bytes from the package", () => {
    const { script, release, json } = overlayAssets();
    expect(JSON.parse(json)).toEqual(release);
    expect(`sha384-${createHash("sha384").update(script).digest("base64")}`).toBe(release.integrity);
  });
});
