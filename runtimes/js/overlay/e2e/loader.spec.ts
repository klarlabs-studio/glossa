/**
 * The overlay loader in Chromium (RFC 0004 §5.1, the runtime and delivery
 * layers): a preview deployment built by Vite with `@klarlabs-studio/glossa-unplugin`, the
 * real overlay bundle served from a Studio origin, and the CSP the README
 * asks preview deployments for. It covers what the unit tests can't: the
 * browser's own SRI check, `crossorigin="anonymous"` against CORS, and that
 * a page whose runtime serves a production release stays uneditable even
 * with `?glossa=edit`.
 */
import { createHash } from "node:crypto";
import { readFileSync, readdirSync, rmSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "@playwright/test";
import type { Page, Route } from "@playwright/test";
import { glossa } from "@klarlabs-studio/glossa-unplugin";
import { LOADER_ATTRIBUTE, OVERLAY_PATH } from "@klarlabs-studio/glossa-runtime/dev";
import { build } from "vite";

import { fixture } from "../src/testing/release.js";

const here = dirname(fileURLToPath(import.meta.url));
const APP = "https://app.glossa.test";
const STUDIO = "https://studio.glossa.test";
/** The API origin, separate from Studio's so CORS is real here. */
const API = "https://api.glossa.test";
const OVERLAY_SRC = `${STUDIO}${OVERLAY_PATH}`;

/** The preview CSP from the README: the app's own origin plus Studio for scripts. */
const CSP = [
  "default-src 'none'",
  `script-src 'self' ${STUDIO}`,
  `connect-src ${API}`,
  "style-src 'self'",
  "img-src 'self'",
  "base-uri 'none'",
  "frame-ancestors 'none'",
].join("; ");

/** The bundle Studio publishes at /overlay/v1/overlay.js, and its SRI hash. */
const bundle = readFileSync(join(here, "..", "dist", "bundle", "overlay.js"));
const integrity = `sha384-${createHash("sha384").update(bundle).digest("base64")}`;

const app = join(here, "loader-app");
const out = (name: string) => join(here, "..", ".fixtures", name);

/**
 * Build the app with the plugin for `buildEnv` (which decides whether the
 * loader is in it), running against a release for `releaseEnv`.
 */
async function buildApp(name: string, buildEnv: string, releaseEnv = buildEnv) {
  const dir = out(name);
  rmSync(dir, { recursive: true, force: true });
  const release = { ...fixture, manifest: { ...fixture.manifest, environment: releaseEnv } };
  await build({
    root: app,
    configFile: false,
    logLevel: "silent",
    cacheDir: join(dir, ".vite"),
    define: {
      GLOSSA_ENVIRONMENT: JSON.stringify(releaseEnv),
      GLOSSA_RELEASE: JSON.stringify(release),
    },
    plugins: [
      glossa.vite({
        usages: false,
        environment: buildEnv,
        studio: { origin: STUDIO, integrity, tenant: "ten_1", project: "prj_1", api: API },
      }),
    ],
    build: { outDir: join(dir, "dist"), emptyOutDir: true },
  });
  return join(dir, "dist");
}

const builds: Record<string, string> = {};

test.beforeAll(async () => {
  // Both are built for a preview environment, so both carry the loader.
  // What differs is the release the runtime loads in the browser.
  builds.preview = await buildApp("loader-preview", "preview");
  builds.production = await buildApp("loader-production", "preview", "production");
});

const TYPES: Record<string, string> = {
  ".html": "text/html",
  ".js": "text/javascript",
  ".css": "text/css",
};

/** Serve a build under the app origin, and the overlay (or `served`) from Studio. */
async function serve(page: Page, dist: string, served: Buffer | string = bundle) {
  await page.route(`${APP}/**`, (route: Route) => {
    const path = new URL(route.request().url()).pathname;
    const file = path === "/" ? "index.html" : path.slice(1);
    const type = TYPES[file.slice(file.lastIndexOf("."))] ?? "application/octet-stream";
    return route.fulfill({
      headers: { "content-type": type, "Content-Security-Policy": CSP },
      body: readFileSync(join(dist, file)),
    });
  });
  await page.context().route(`${STUDIO}/**`, (route) => {
    const url = new URL(route.request().url());
    if (url.pathname === AUTHORIZE) return authorize(route, url);
    return route.fulfill({
      headers: {
        "content-type": "text/javascript",
        // What Studio's nginx sends for /overlay/, so crossorigin="anonymous" works.
        "Access-Control-Allow-Origin": "*",
        "Cross-Origin-Resource-Policy": "cross-origin",
        "Cache-Control": "public, max-age=300",
      },
      body: served,
    });
  });
}

/** Studio's authorization popup (RFC 0004 §5.2), as this suite serves it. */
const AUTHORIZE = "/in-context/authorize";

/** The query the popup was opened with, for the test to inspect. */
let popupQuery: URLSearchParams | undefined;

/** Read through a function, so assigning undefined doesn't narrow the type away. */
const askedWith = (): URLSearchParams | undefined => popupQuery;

/**
 * A stand-in for Studio's authorize page: it posts the envelope the real
 * page posts, to the exact origin in the query. It is served with
 * `Cross-Origin-Opener-Policy: unsafe-none`, which is what Studio's nginx
 * does for this one route — under the default `same-origin` a popup a
 * cross-origin page opened gets no `window.opener` at all, and the whole
 * flow is `window.opener.postMessage`.
 */
function authorize(route: Route, url: URL) {
  popupQuery = url.searchParams;
  const envelope = JSON.stringify({
    type: "glossa.in-context-grant",
    channel: url.searchParams.get("channel") ?? "",
    ok: true,
    token: `glossa_ctx_${"t".repeat(43)}`,
    expires_at: new Date(Date.now() + 15 * 60_000).toISOString(),
    project_id: "prj_1",
    origin: url.searchParams.get("origin") ?? "",
  });
  const target = JSON.stringify(url.searchParams.get("origin") ?? "");
  return route.fulfill({
    headers: { "content-type": "text/html", "Cross-Origin-Opener-Policy": "unsafe-none" },
    body:
      "<!doctype html><meta charset=\"utf-8\"><title>Authorize</title>" +
      `<script>window.opener.postMessage(${envelope}, ${target});</script>`,
  });
}

async function open(page: Page, which: string, query = "", served?: Buffer | string) {
  const warnings: string[] = [];
  page.on("console", (m) => m.type() === "warning" && warnings.push(m.text()));
  await page.addInitScript(() => {
    const w = window as unknown as { violations: string[] };
    w.violations = [];
    document.addEventListener("securitypolicyviolation", (e) =>
      w.violations.push(`${e.violatedDirective} ${e.blockedURI}`),
    );
  });
  await serve(page, builds[which]!, served);
  await page.goto(`${APP}/${query}`);
  await page.waitForFunction(() => document.getElementById("pay")?.textContent);
  return {
    warnings,
    violations: () =>
      page.evaluate(() => (window as unknown as { violations: string[] }).violations),
  };
}

const loaderScript = (page: Page) => page.locator(`script[${LOADER_ATTRIBUTE}]`);
const panel = (page: Page) => page.locator("glossa-overlay");

test.describe("the loader", () => {
  test("starts an editor session on ?glossa=edit, from the script it pinned", async ({ page }) => {
    const { violations } = await open(page, "preview", "?glossa=edit");
    await expect(panel(page)).toBeAttached();
    const script = loaderScript(page);
    await expect(script).toHaveAttribute("src", OVERLAY_SRC);
    await expect(script).toHaveAttribute("integrity", integrity);
    await expect(script).toHaveAttribute("crossorigin", "anonymous");
    await expect(script).toHaveAttribute("type", "module");
    expect(await script.evaluate((el) => el.textContent)).toBe("");
    // The session marks what the page renders, so Alt+click can find it.
    await expect(page.locator("glossa-text")).toHaveAttribute("data-glossa-id", "cart.checkout");
    expect(await violations()).toEqual([]);
  });

  test("stays off until Alt+Shift+E without the query", async ({ page }) => {
    await open(page, "preview");
    await expect(loaderScript(page)).toHaveCount(0);
    await page.keyboard.press("Alt+Shift+E");
    await expect(panel(page)).toBeAttached();
    await expect(loaderScript(page)).toHaveCount(1);
  });

  test("refuses a production release, even with ?glossa=edit or the gesture", async ({ page }) => {
    // The loader is in this build; what stops it is the release it finds.
    const assets = join(builds.production!, "assets");
    const bundled = readdirSync(assets).map((f) => readFileSync(join(assets, f), "utf8"));
    expect(bundled.some((code) => code.includes(LOADER_ATTRIBUTE))).toBe(true);
    const { warnings } = await open(page, "production", "?glossa=edit");
    await page.keyboard.press("Alt+Shift+E");
    await expect(page.locator("#pay")).toHaveText("Jetzt zahlen");
    await expect(loaderScript(page)).toHaveCount(0);
    await expect(panel(page)).toHaveCount(0);
    expect(warnings.join("\n")).toContain("the in-product editor stays off");
    expect(await page.evaluate(() => window.app.runtime.environment)).toBe("production");
  });

  // The popup handshake end to end in a real browser: a window Chromium
  // actually opens, a cross-origin `postMessage`, and the grant reaching
  // the API as a bearer — never as a cookie, and never in the URL.
  test("signs in through Studio's popup and uses the grant as a bearer", async ({ page }) => {
    popupQuery = undefined;
    const seen: Array<{ auth: string; cookie: string; url: string }> = [];
    await page.context().route(`${API}/**`, (route) => {
      const h = route.request().headers();
      seen.push({ auth: h.authorization ?? "", cookie: h.cookie ?? "", url: route.request().url() });
      return route.fulfill({
        status: 404,
        headers: {
          "content-type": "application/problem+json",
          "Access-Control-Allow-Origin": APP,
          "Access-Control-Expose-Headers": "ETag",
        },
        body: JSON.stringify({ type: "about:blank", title: "Not Found", status: 404, code: "not_found" }),
      });
    });

    await open(page, "preview", "?glossa=edit");
    await expect(panel(page)).toBeAttached();

    // Opening the panel is what asks for a token, and a popup needs a
    // gesture — which the Alt+click is.
    const popup = page.waitForEvent("popup");
    await page.locator("#pay").click({ modifiers: ["Alt"] });
    await popup;

    await expect.poll(() => seen.length, { timeout: 15_000 }).toBeGreaterThan(0);
    expect(askedWith()?.get("origin")).toBe(APP);
    expect(askedWith()?.get("project")).toBe("prj_1");
    expect(askedWith()?.get("tenant")).toBe("ten_1");
    expect(askedWith()?.get("channel")).toBeTruthy();

    for (const r of seen) {
      expect(r.auth).toMatch(/^Bearer glossa_ctx_/);
      expect(r.cookie).toBe(""); // credentials: "omit"
      expect(r.url).not.toContain("glossa_ctx_"); // never in the URL
    }
  });

  test("won't run a script that doesn't match the pinned hash", async ({ page }) => {
    const tampered = `${bundle.toString("utf8")}\nwindow.tampered = true;\n`;
    await open(page, "preview", "?glossa=edit", tampered);
    await expect(loaderScript(page)).toHaveCount(1);
    await expect(panel(page)).toHaveCount(0);
    expect(await page.evaluate(() => (window as unknown as { tampered?: boolean }).tampered)).toBeUndefined();
  });
});
