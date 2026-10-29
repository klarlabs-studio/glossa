/**
 * The overlay in Chromium (RFC 0004 §5): a preview page served under the
 * CSP the README documents, the overlay script loaded from the Studio
 * origin, and the fake API answering cross-origin with CORS. Covers Alt+click
 * targeting with real layout (nested markers, duplicate text, attributes,
 * component hosts), live override on save, keyboard use, axe, and that
 * nothing violates the CSP.
 */
import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";
import type { Page, Route } from "@playwright/test";
import { build } from "esbuild";

import { openapiValidator } from "../src/testing/contract.js";
import { FakeApi, PROJECT, TENANT, TOKEN } from "../src/testing/fake-api.js";
import { PAGE_HTML } from "../src/testing/page.js";
import { fixture, seed } from "../src/testing/release.js";

const APP = "https://app.glossa.test";
const STUDIO = "https://studio.glossa.test";
const API = "https://api.glossa.test";
const OVERLAY_SRC = `${STUDIO}/overlay/v1/overlay.js`;

/** The CSP the README asks preview deployments for: no unsafe-inline, no eval, no frames. */
const CSP = [
  "default-src 'none'",
  `script-src 'self' ${STUDIO}`,
  "style-src 'self'",
  `connect-src ${API}`,
  "img-src 'self'",
  "base-uri 'none'",
  "form-action 'none'",
  "frame-ancestors 'none'",
].join("; ");

const HTML = `<!doctype html>
<html lang="de"><head><meta charset="utf-8"><title>Overlay fixture</title>
<link rel="stylesheet" href="/page.css">
<script src="/app.js" defer></script>
</head><body>${PAGE_HTML}</body></html>`;

const PAGE_CSS = `body { margin: 0; font: 16px/1.4 Arial, Helvetica, sans-serif; color: #1a1a1a; background: #fff; }
main { width: 640px; padding: 16px; }
button { font: inherit; }`;

let appJs = "";
let overlayJs = "";
const validator = openapiValidator();

test.beforeAll(async () => {
  const bundle = async (entry: string, extra: Record<string, unknown> = {}) =>
    (
      await build({
        entryPoints: [entry],
        bundle: true,
        format: "iife",
        write: false,
        platform: "browser",
        logLevel: "silent",
        ...extra,
      })
    ).outputFiles[0]!.text;
  const config = {
    apiBase: API,
    overlaySrc: OVERLAY_SRC,
    token: TOKEN,
    tenant: TENANT,
    project: PROJECT,
  };
  appJs =
    `var RELEASE = ${JSON.stringify(fixture)};\nvar CONFIG = ${JSON.stringify(config)};\n` +
    (await bundle(new URL("./app.ts", import.meta.url).pathname));
  overlayJs = await bundle(new URL("../src/index.ts", import.meta.url).pathname, {
    globalName: "GlossaOverlay",
  });
});

const cors = {
  "Access-Control-Allow-Origin": APP,
  "Access-Control-Allow-Methods": "GET, POST, PUT",
  "Access-Control-Allow-Headers": "Authorization, Content-Type, If-Match, Idempotency-Key",
  "Access-Control-Expose-Headers": "ETag",
  Vary: "Origin",
};

async function serve(page: Page, fake: FakeApi): Promise<void> {
  await page.route(`${APP}/**`, (route: Route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/app.js") return route.fulfill({ contentType: "text/javascript", body: appJs });
    if (path === "/page.css") return route.fulfill({ contentType: "text/css", body: PAGE_CSS });
    return route.fulfill({
      contentType: "text/html",
      headers: { "Content-Security-Policy": CSP },
      body: HTML,
    });
  });
  await page.route(OVERLAY_SRC, (route) =>
    route.fulfill({ contentType: "text/javascript", body: overlayJs }),
  );
  await page.route(`${API}/**`, async (route) => {
    const req = route.request();
    if (req.method() === "OPTIONS") return route.fulfill({ status: 204, headers: cors });
    const res = fake.handle({
      method: req.method(),
      url: req.url(),
      headers: await req.allHeaders(),
      body: req.postData() ?? undefined,
    });
    return route.fulfill({
      status: res.status,
      headers: { ...res.headers, ...cors },
      body: res.body === undefined ? "" : JSON.stringify(res.body),
    });
  });
}

async function open(page: Page): Promise<FakeApi> {
  const fake = new FakeApi(validator);
  seed(fake);
  await page.addInitScript(() => {
    const w = window as unknown as { violations: string[] };
    w.violations = [];
    document.addEventListener("securitypolicyviolation", (e) =>
      w.violations.push(`${e.violatedDirective} ${e.blockedURI}`),
    );
  });
  await serve(page, fake);
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto(`${APP}/checkout?glossa=edit`);
  await page.waitForFunction(() => document.documentElement.dataset.overlay === "ready");
  await page.waitForFunction(() =>
    document.querySelector("glossa-text")?.hasAttribute("data-glossa-id"),
  );
  return fake;
}

/** What an element shows, without markers and bidi isolates. */
const shown = (page: Page, selector: string) =>
  page.evaluate(
    (sel) => document.querySelector(sel)!.textContent!.replace(/[\u2061-\u2069]/g, ""),
    selector,
  );

async function altClickAt(page: Page, at: { x: number; y: number }): Promise<void> {
  await page.keyboard.down("Alt");
  await page.mouse.click(at.x, at.y);
  await page.keyboard.up("Alt");
}

/** Alt+click on the text `<glossa-text>` renders (its host is `display: contents`, with no box). */
async function altClickComponent(page: Page): Promise<void> {
  const at = await page.evaluate(() => {
    const range = document.createRange();
    range.selectNodeContents(document.querySelector("glossa-text")!.shadowRoot!);
    const r = range.getBoundingClientRect();
    return { x: r.x + r.width / 2, y: r.y + r.height / 2 };
  });
  await altClickAt(page, at);
}

/** Alt+click at the middle of `visible` inside the element `selector`. */
async function altClickText(page: Page, selector: string, visible: string): Promise<void> {
  const box = await page.evaluate(
    ([sel, text]) => {
      const node = document.querySelector(sel!)!.firstChild as Text;
      const start = node.data.indexOf(text!);
      const range = document.createRange();
      range.setStart(node, start);
      range.setEnd(node, start + text!.length);
      const r = range.getBoundingClientRect();
      return { x: r.x + r.width / 2, y: r.y + r.height / 2 };
    },
    [selector, visible],
  );
  await altClickAt(page, box);
}

const panel = (page: Page) => page.locator("glossa-overlay [role=dialog]");
const title = (page: Page) => page.locator("glossa-overlay h2");
const draft = (page: Page) => page.locator("glossa-overlay #draft");

test.afterEach(async ({ page }) => {
  expect(
    await page.evaluate(() => (window as unknown as { violations: string[] }).violations),
  ).toEqual([]);
});

test("Alt+click finds the message under the pointer", async ({ page }) => {
  const fake = await open(page);

  // A marked value inside another message: the pointer decides which one.
  await altClickText(page, "#greeting", "Lina");
  await expect(title(page)).toContainText("user.name");
  await altClickText(page, "#greeting", "Hallo");
  await expect(title(page)).toContainText("cart.greeting");
  await expect(draft(page)).toHaveValue("Hallo {$name}!");

  // Three buttons read the same; each opens its own message.
  await page.locator("#save-2").click({ modifiers: ["Alt"] });
  await expect(title(page)).toContainText("settings.save");
  await page.locator("#save-1").click({ modifiers: ["Alt"] });
  await expect(title(page)).toContainText("profile.save");

  // An attribute and a component host.
  await page.locator("#search").click({ modifiers: ["Alt"] });
  await expect(title(page)).toContainText("search.placeholder");
  await altClickComponent(page);
  await expect(title(page)).toContainText("cart.checkout");
  await expect(draft(page)).toHaveValue("Zur Kasse");

  // A plain click is the page's own.
  await page.keyboard.press("Escape");
  await expect(panel(page)).toHaveCount(0);
  await page.locator("#pay").click();
  await expect(panel(page)).toHaveCount(0);
  expect(fake.violations).toEqual([]);
});

test("an edit previews live and saves as a revision with in-context provenance", async ({
  page,
}) => {
  const fake = await open(page);
  await page.locator("#pay").click({ modifiers: ["Alt"] });
  await expect(draft(page)).toHaveValue("Jetzt zahlen");

  await draft(page).fill("Sofort bezahlen");
  await expect(page.locator("glossa-overlay #check")).toContainText("Valid message");
  await expect.poll(() => shown(page, "#pay")).toBe("Sofort bezahlen");

  await page.locator("glossa-overlay button[type=submit]").click();
  await expect(
    page.locator("glossa-overlay [role=status].notice, glossa-overlay .notice"),
  ).toContainText("Saved as revision 2");
  const rev = fake.revisions("checkout.pay", "de").at(-1)!;
  expect(rev.origin).toBe("human");
  expect(rev.origin_detail).toEqual({
    in_context: { route: "/checkout", viewport: { width: 1280, height: 800 } },
  });
  const put = fake.requests.find((r) => r.method === "PUT")!;
  expect(put.headers["if-match"]).toBe('"r1"');
  expect(put.headers.cookie).toBeUndefined();

  // The component re-renders too: its override goes through parts().
  await altClickComponent(page);
  await draft(page).fill("Zur Kasse gehen");
  await page.locator("glossa-overlay button[type=submit]").click();
  await expect(page.locator("glossa-overlay .notice")).toContainText("Saved as revision 2");
  await expect
    .poll(() => page.evaluate(() => document.querySelector("glossa-text")!.shadowRoot!.textContent))
    .toBe("Zur Kasse gehen");

  await page.keyboard.press("Escape");
  expect(await shown(page, "#pay")).toBe("Sofort bezahlen");
  expect(fake.violations).toEqual([]);
});

test("keyboard: focus stays in the panel, Esc closes it and returns focus", async ({ page }) => {
  await open(page);
  await page.locator("#search").focus();
  await page.keyboard.press("Alt+Enter");
  await expect(draft(page)).toBeFocused();

  const inPanel = () =>
    page.evaluate(() => document.activeElement?.tagName.toLowerCase() === "glossa-overlay");
  for (let i = 0; i < 12; i++) {
    await page.keyboard.press("Tab");
    expect(await inPanel()).toBe(true);
  }
  for (let i = 0; i < 12; i++) {
    await page.keyboard.press("Shift+Tab");
    expect(await inPanel()).toBe(true);
  }
  await page.keyboard.press("Escape");
  await expect(panel(page)).toHaveCount(0);
  await expect(page.locator("#search")).toBeFocused();
});

test("axe finds no WCAG A/AA violations on the page with the panel open", async ({ page }) => {
  const fake = await open(page);
  fake.termFindings("cart.greeting", "de", [
    {
      code: "term_missing",
      severity: "warning",
      concept_id: "c1",
      term_id: "t1",
      side: "target",
      start: 0,
      end: 5,
      text: "Hallo",
      suggestions: ["Guten Tag"],
      message: "The source uses “Hello”; the termbase says “Guten Tag”.",
    },
  ]);
  await page.locator("#greeting").click({ modifiers: ["Alt"] });
  await expect(draft(page)).toHaveValue("Hallo {$name}!");
  const scan = async () =>
    (
      await new AxeBuilder({ page })
        .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
        .analyze()
    ).violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`);
  expect(await scan()).toEqual([]);

  // Every section open, a parse error shown.
  for (const name of ["History", "Terminology", "Ask AI"]) {
    await page.locator("glossa-overlay button.disclosure", { hasText: name }).click();
  }
  await expect(page.locator("glossa-overlay #section-terms")).toContainText("Guten Tag");
  await expect(page.locator("glossa-overlay #section-history li")).toHaveCount(1);
  // The editor forces, so a current translation still gets an offer
  // (RFC 0004 §5.3) rather than "nothing to translate".
  await expect(page.locator("glossa-overlay #section-ai")).toContainText("Request a suggestion");
  await draft(page).fill("Hallo {$name");
  await expect(page.locator("glossa-overlay #check .error")).toContainText("syntax-error");
  expect(await scan()).toEqual([]);
});

test("styles are constructable stylesheets, not inline", async ({ page }) => {
  await open(page);
  await page.locator("#pay").click({ modifiers: ["Alt"] });
  await expect(panel(page)).toBeVisible();
  const styles = await page.evaluate(() => {
    const root = document.querySelector("glossa-overlay")!.shadowRoot!;
    return {
      adopted: root.adoptedStyleSheets.length,
      styleElements: root.querySelectorAll("style").length,
      styleAttributes: root.querySelectorAll("[style]").length,
    };
  });
  expect(styles).toEqual({ adopted: 1, styleElements: 0, styleAttributes: 0 });
});
