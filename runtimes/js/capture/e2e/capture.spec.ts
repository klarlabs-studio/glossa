/**
 * Capture mode in a real browser (RFC 0004 §3.1): marker geometry, regions
 * against captures.v1, and that markers don't move the layout.
 */
import { expect, test } from "@playwright/test";
import type { Page } from "@playwright/test";
import { build } from "esbuild";

import type { Box, Capture, Region } from "../src/index.js";
import { hasMarkers } from "../src/markers.js";
import { fixture } from "../src/testing/release.js";
import { findingErrors, schemaErrors } from "../src/testing/schema.js";
import type { Probe } from "./fixture.js";

const HTML = `<!doctype html>
<html lang="de"><head><meta charset="utf-8"><title>Capture fixture</title>
<style>
  body { margin: 0; font: 16px/1.4 Arial, Helvetica, sans-serif; }
  main { width: 600px; padding: 16px; }
  .row { display: flex; gap: 8px; margin-bottom: 8px; }
  p { margin: 0 0 8px; }
  #long { width: 160px; }
  #offscreen { position: absolute; left: -9999px; top: 0; }
  #clipped { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; }
  #hidden-display { display: none; }
  #hidden-visibility { visibility: hidden; }
  #rtl { width: 300px; }
  #rtl-long { width: 160px; }
  .spacer { height: 1500px; }
</style></head>
<body><main>
  <div class="row">
    <button id="save-1" data-probe></button><button id="save-2" data-probe></button><button id="save-3" data-probe></button>
  </div>
  <p id="total" data-probe></p>
  <div class="row">
    <input id="search" data-probe><span id="hint" data-probe>?</span>
    <img id="logo" width="40" height="20" data-probe><input id="submit" type="submit" data-probe>
  </div>
  <p id="hidden-display"></p><p id="hidden-visibility" data-probe></p>
  <p id="offscreen"></p><p id="clipped"></p>
  <p id="greeting" data-probe></p>
  <p id="long" data-probe></p>
  <div id="rtl" dir="rtl" lang="ar"><p id="rtl-save" data-probe></p><p id="rtl-long" data-probe></p></div>
  <p id="inline" data-probe>x</p>
  <glossa-provider id="provider"><p id="component" data-probe><glossa-text key="cart.checkout">Zur Kasse</glossa-text></p></glossa-provider>
  <div class="spacer"></div>
</main></body></html>`;

let script = "";

test.beforeAll(async () => {
  const out = await build({
    entryPoints: [new URL("./fixture.ts", import.meta.url).pathname],
    bundle: true,
    format: "iife",
    write: false,
    platform: "browser",
    logLevel: "silent",
  });
  script = out.outputFiles[0]!.text;
});

async function open(page: Page): Promise<void> {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.setContent(HTML);
  await page.evaluate((release) => (window.release = release), fixture);
  await page.addScriptTag({ content: script });
  await page.evaluate(() => window.fixture.render());
  await settled(page);
}

/** Wait for Lit to render `<glossa-text>` (with its current attributes). */
async function settled(page: Page): Promise<void> {
  await page.waitForFunction(
    () => document.querySelector("glossa-text")?.shadowRoot?.textContent?.includes("Zur Kasse"),
  );
  await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => r(null))));
}

const rect = (page: Page, selector: string, text = false): Promise<Box> =>
  page.evaluate(
    ([sel, contents]) => {
      const el = document.querySelector(sel as string)!;
      let r: DOMRect;
      if (contents) {
        const range = document.createRange();
        range.selectNodeContents(el.shadowRoot ?? el);
        r = range.getBoundingClientRect();
      } else r = el.getBoundingClientRect();
      return { x: r.x + scrollX, y: r.y + scrollY, width: r.width, height: r.height };
    },
    [selector, text] as const,
  );

const near = (a: Box, b: Box, tolerance = 1) =>
  expect(
    [a.x - b.x, a.y - b.y, a.width - b.width, a.height - b.height].every(
      (d) => Math.abs(d) <= tolerance,
    ),
    `${JSON.stringify(a)} ≈ ${JSON.stringify(b)}`,
  ).toBe(true);

const inside = (a: Box, b: Box) =>
  expect(
    a.x >= b.x - 0.5 &&
      a.y >= b.y - 0.5 &&
      a.x + a.width <= b.x + b.width + 0.5 &&
      a.y + a.height <= b.y + b.height + 0.5,
    `${JSON.stringify(a)} inside ${JSON.stringify(b)}`,
  ).toBe(true);

/** The regions for message `key` (by render or host), and the locale it rendered in. */
function regionsOf(c: Capture, key: string, locale?: string): Region[] {
  const indexes = new Set(
    c.renders.filter((r) => r.key === key && (!locale || r.locale === locale)).map((r) => r.index),
  );
  return c.regions.filter((r) => r.key === key || (r.index !== undefined && indexes.has(r.index)));
}

test("markers don't move anything, and stopping restores the page", async ({ page }) => {
  await open(page);
  const before: Probe[] = await page.evaluate(() => window.fixture.probes());
  const plain = await page.evaluate(() => document.body.innerHTML);

  await page.evaluate(() => window.fixture.start());
  await settled(page);
  expect(hasMarkers(await page.evaluate(() => document.body.innerHTML))).toBe(true);
  const during: Probe[] = await page.evaluate(() => window.fixture.probes());
  expect(during.map((p) => p.id)).toEqual(before.map((p) => p.id));
  for (const [i, p] of during.entries()) near(p, before[i]!, 0.01);

  await page.evaluate(() => window.fixture.stop());
  await settled(page);
  expect(await page.evaluate(() => window.fixture.hooked())).toEqual([false, false]);
  expect(await page.evaluate(() => document.body.innerHTML)).toBe(plain);
  const after: Probe[] = await page.evaluate(() => window.fixture.probes());
  for (const [i, p] of after.entries()) near(p, before[i]!, 0.01);
});

test("regions: duplicates, values, attributes, hidden, RTL, wrapping, components", async ({
  page,
}) => {
  await open(page);
  await page.evaluate(() => window.fixture.start());
  await settled(page);
  const c: Capture = await page.evaluate(() => window.fixture.collect());
  expect(schemaErrors(c)).toEqual([]);

  // "Speichern" ×3: three messages with the same text, each found in its own button.
  for (const [n, key] of ["profile.save", "settings.save", "draft.save"].entries()) {
    const [region, ...more] = regionsOf(c, key, "de");
    expect(more).toEqual([]);
    expect(region).toMatchObject({ kind: "text", visible: true });
    near(region!.box, await rect(page, `#save-${n + 1}`, true));
    inside(region!.box, await rect(page, `#save-${n + 1}`));
  }

  // A formatted value: the whole formatted string is the region.
  const [total] = regionsOf(c, "cart.total");
  expect(await page.textContent("#total")).toContain("1.234,5");
  near(total!.box, await rect(page, "#total", true));

  // Attributes: the element's box.
  for (const [key, attribute, sel] of [
    ["search.placeholder", "placeholder", "#search"],
    ["search.hint", "title", "#hint"],
    ["logo.alt", "alt", "#logo"],
    ["form.submit", "value", "#submit"],
  ] as const) {
    const [region, ...more] = regionsOf(c, key);
    expect(more).toEqual([]);
    expect(region).toMatchObject({ kind: "attribute", attribute, visible: true });
    near(region!.box, await rect(page, sel));
  }

  // Rendered but not seen: display none, visibility hidden, off-screen, clipped away.
  const hidden = [...regionsOf(c, "hidden.note"), ...regionsOf(c, "offscreen.note")];
  expect(hidden).toHaveLength(4);
  expect(hidden.every((r) => !r.visible)).toBe(true);

  // Nested: a marked value inside a marked message.
  const [greeting] = regionsOf(c, "greeting");
  const names = regionsOf(c, "user.name");
  near(greeting!.box, await rect(page, "#greeting", true));
  expect(names.some((n) => n.visible && n.box.width < greeting!.box.width)).toBe(true);
  // A marked string inside other text is only its own run.
  const inline = await rect(page, "#inline", true);
  const lina = names.find((n) => Math.abs(n.box.y - inline.y) < 1)!;
  inside(lina.box, inline);
  expect(lina.box.width).toBeLessThan(inline.width / 2);

  // Wrapped text: one region per line.
  const long = regionsOf(c, "long.text", "de");
  expect(long.length).toBeGreaterThanOrEqual(2);
  expect(new Set(long.map((r) => r.box.y)).size).toBe(long.length);
  for (const r of long) inside(r.box, await rect(page, "#long"));

  // RTL: the Arabic island's render resolves from ar and sits at the right edge.
  const [rtlSave] = regionsOf(c, "profile.save", "ar");
  const rtlBox = await rect(page, "#rtl-save");
  expect(rtlSave).toMatchObject({ kind: "text", visible: true });
  inside(rtlSave!.box, rtlBox);
  expect(Math.abs(rtlSave!.box.x + rtlSave!.box.width - (rtlBox.x + rtlBox.width))).toBeLessThan(1);
  const rtlLong = regionsOf(c, "long.text", "ar");
  expect(rtlLong.length).toBeGreaterThanOrEqual(2);
  for (const r of rtlLong) inside(r.box, await rect(page, "#rtl-long"));

  // A component host with display: contents: the box of what it renders.
  const [component, ...moreComponents] = regionsOf(c, "cart.checkout");
  expect(moreComponents).toEqual([]);
  expect(component).toMatchObject({ kind: "element", visible: true });
  near(component!.box, await rect(page, "glossa-text", true));
  expect(await page.getAttribute("glossa-text", "data-glossa-locale")).toBe("de");
});

test("the probe pass measures live layout: the clipped button is found in a real browser", async ({
  page,
}) => {
  await open(page);
  await page.evaluate(() => window.fixture.start());
  await settled(page);
  const c = await page.evaluate(() => window.fixture.collect());

  // #clipped is 1×1 with overflow: hidden and white-space: nowrap, so its
  // text genuinely doesn't fit — the one thing jsdom cannot decide.
  const clipped = c.probes.filter((p) => p.code === "text-clipped");
  expect(clipped.map((p) => p.locus.key)).toEqual(["offscreen.note"]);
  expect(clipped[0]!.locus.region).toMatch(/^r_\d+$/);
  expect(clipped[0]!.evidence).toMatchObject({ box: [1, 1] });

  // #rtl is dir="rtl", so the Arabic island's regions mirrored.
  expect(c.probes.filter((p) => p.code === "rtl-not-mirrored")).toEqual([]);
  // ar falls back to de, so nothing on this screen is out of one chain.
  expect(c.probes.filter((p) => p.code === "mixed-locale")).toEqual([]);

  // Whatever else the browser's fonts decided, every finding is a finding.v1.
  for (const p of c.probes) {
    expect(p).toMatchObject({ schema: "glossa.finding/v1", layer: "visual", severity: "warning" });
    expect(findingErrors(p)).toEqual([]);
  }
  // Wrapped German and Arabic text each cover more than one line box.
  expect(c.metrics["long.text"]).toBeGreaterThanOrEqual(4);
});

test("boxes are page coordinates, whatever the scroll position", async ({ page }) => {
  await open(page);
  await page.evaluate(() => window.fixture.start());
  await settled(page);
  const top: Capture = await page.evaluate(() => window.fixture.collect());
  await page.evaluate(() => scrollTo(0, 600));
  const scrolled: Capture = await page.evaluate(() => window.fixture.collect());
  expect(await page.evaluate(() => scrollY)).toBe(600);
  expect(scrolled).toEqual(top);
});
