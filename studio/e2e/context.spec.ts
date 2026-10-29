/**
 * "Where it appears" end to end (RFC 0004 §3.4): CI uploads a usages
 * document and a capture upload for a seeded project, exactly as
 * `glossa extract --upload` and `glossa capture --upload` do — a real
 * multipart body with real PNG bytes, re-encoded and stored by the
 * server — and the translator workspace shows them: the usages grouped
 * application → route → component, the screenshot cropped around the
 * message with a full-page lightbox and a locale toggle, the `unused`
 * and `not captured` states, and the message-list filters.
 */
import { expect, test, type Page } from "@playwright/test";
import { pageShot, sha256 } from "./png";
import { api, expectAccessible, expectAccessibleInBothThemes, signIn } from "./support";

const COMMIT = "9f2c1e7a".repeat(5);
const VIEWPORT = { width: 800, height: 600 };
/** A full-page screenshot: 800 CSS pixels wide, 1200 tall, with a band where the message sits. */
const BAND = { y: 700, height: 40 };
const IMAGE_EN = pageShot(VIEWPORT.width, 1200, BAND);
const IMAGE_DE = pageShot(VIEWPORT.width, 1200, { y: BAND.y + 4, height: BAND.height });
/** Where checkout.pay rendered, in CSS pixels from the page's top left. */
const REGION = { x: 300, y: BAND.y, width: 160, height: BAND.height };

const usagesDocument = {
  schema: "glossa.usages/v1",
  application: "web",
  commit: COMMIT,
  branch: "main",
  tool: { name: "@klarlabs-studio/glossa", version: "0.1.0" },
  usages: [
    { key: "checkout.pay", file: "src/checkout/PaymentFooter.vue", line: 42, column: 9, component: "PaymentFooter", route: "/checkout/payment", kind: "component" },
    { key: "checkout.pay", file: "src/ui/PrimaryButton.vue", line: 7, column: 5, component: "PrimaryButton", route: "/checkout/payment", kind: "t" },
    { key: "checkout.total", file: "src/checkout/Summary.vue", line: 18, column: 3, component: "Summary", route: "/checkout/payment", kind: "t" },
  ],
};

const capture = (locale: string, image: Buffer) => ({
  route: "/checkout/payment",
  url: "http://localhost:4173/checkout/payment",
  viewport: { ...VIEWPORT, deviceScaleFactor: 1 },
  locale,
  image: { sha256: sha256(image), width: VIEWPORT.width, height: 1200 },
  renders: [{ index: 0, key: "checkout.pay", locale }],
  regions: [{ index: 0, kind: "text", box: REGION, visible: true }],
});

const capturesManifest = {
  schema: "glossa.captures/v1",
  application: "web",
  commit: COMMIT,
  branch: "main",
  tool: { name: "glossa", version: "0.9.0" },
  captures: [capture("en", IMAGE_EN), capture("de", IMAGE_DE)],
};

/** The pane for the message the workspace shows. */
const pane = (page: Page) => page.getByTestId("where-pane");

async function select(page: Page, key: string): Promise<void> {
  await page.getByRole("listbox", { name: "Messages" }).getByRole("option", { name: new RegExp(key.replace(".", "\\.")) }).click();
  await expect(page.getByRole("heading", { name: key })).toBeVisible();
}

test("usages and screenshots from CI show up in the translator workspace", async ({ page }) => {
  test.setTimeout(180_000);
  await signIn(page, `context-${Date.now()}@example.com`);

  // ── a project with three messages, as the CLI would push them ──
  const v1 = await api(page);
  const t = v1.tenant;
  const project = await v1.post(`/v1/tenants/${t}/projects`, { name: "Context Demo", slug: `context-${Date.now()}`, source_locale: "en" });
  const P = `/v1/tenants/${t}/projects/${project.id}`;
  await v1.post(`${P}/locales`, { code: "de" });
  await v1.post(`${P}/applications`, { slug: "web", name: "Web", platform: "web" });
  await v1.post(`${P}/message-upserts`, {
    items: [
      { key: "checkout.pay", text: "Pay now", description: "The payment button" },
      { key: "checkout.total", text: "Total" },
      { key: "legal.terms", text: "By continuing you accept the terms." },
    ],
  });
  await expect.poll(async () => (await v1.get(`${P}/messages?missing_in=de`)).items.length).toBe(3);
  const base = `/t/${t}/p/${project.id}`;

  // ── before any upload: nothing is known, and the pane says so ──
  await page.goto(`${base}/translate?locale=de&key=checkout.pay`);
  await expect(pane(page).getByRole("heading", { name: "Where it appears" })).toBeVisible();
  await expect(pane(page).getByTestId("where-no-data")).toHaveText("No usage data yet.");
  await expect(pane(page)).toContainText("glossa extract --upload");
  await expectAccessibleInBothThemes(page, "workspace without context");

  // ── CI uploads the usages, then the captures (multipart, real PNGs) ──
  const build = await v1.post(`${P}/context-builds?source=plugin`, usagesDocument);
  expect(build.usages).toBe(3);
  expect(build.unknown_keys).toBe(0);
  const upload = await v1.postMultipart(`${P}/captures`, {
    manifest: { name: "manifest.json", mimeType: "application/json", buffer: Buffer.from(JSON.stringify(capturesManifest)) },
    [sha256(IMAGE_EN)]: { name: "en.png", mimeType: "image/png", buffer: IMAGE_EN },
    [sha256(IMAGE_DE)]: { name: "de.png", mimeType: "image/png", buffer: IMAGE_DE },
  });
  expect(upload.captures).toBe(2);
  expect(upload.images_stored).toBe(2);
  expect(upload.unknown_keys).toEqual([]);

  // ── the usages: application → route → component, with file:line ──
  await page.reload();
  await select(page, "checkout.pay");
  const usages = pane(page).getByTestId("where-usages");
  await expect(usages).toContainText("Web");
  await expect(usages).toContainText("/checkout/payment");
  await expect(usages).toContainText("PaymentFooter");
  await expect(usages).toContainText("PrimaryButton");
  await expect(pane(page).getByTestId("usage").first()).toContainText("src/checkout/PaymentFooter.vue:42");
  // No Git connection yet (RFC 0004 §6), so the places are plain text.
  await expect(usages.getByRole("link")).toHaveCount(0);

  // ── the screenshot: cropped around the message, which is outlined ──
  const shot = pane(page).getByTestId("capture-shot").first();
  await expect(shot).toBeVisible();
  await expect(shot.getByTestId("capture-region")).toHaveCount(1);
  const image = shot.locator("img");
  await expect(image).toHaveAttribute("alt", /outlined/);
  // The image really loads through the API (the session cookie travels with it), and it is cropped:
  // the page is wider than the window that shows it.
  await expect.poll(async () => image.evaluate((img: HTMLImageElement) => img.naturalWidth)).toBe(VIEWPORT.width);
  const scale = await image.evaluate((img) => img.getBoundingClientRect().width / (img.parentElement as HTMLElement).getBoundingClientRect().width);
  expect(scale).toBeGreaterThan(1);

  // ── the locale toggle: the target locale's capture leads, the source's is one choice away ──
  const localeToggle = pane(page).getByTestId("where-locale");
  await expect(localeToggle).toHaveValue("de");
  const deImage = await image.getAttribute("src");
  await localeToggle.selectOption("en");
  await expect.poll(async () => image.getAttribute("src")).not.toBe(deImage);
  await expectAccessibleInBothThemes(page, "workspace with where it appears");

  // ── the lightbox: the full page, keyboard-closeable ──
  await pane(page).getByRole("button", { name: "Full screenshot" }).first().click();
  const lightbox = page.getByRole("dialog");
  await expect(lightbox).toBeVisible();
  await expect(lightbox.getByTestId("capture-region")).toHaveCount(1);
  await expect(lightbox.locator("img")).toHaveAttribute("alt", /outlined/);
  await expectAccessible(page, "where it appears lightbox");
  await page.keyboard.press("Escape");
  await expect(lightbox).toBeHidden();

  // ── the other two states: used but not captured, and unused ──
  await select(page, "checkout.total");
  await expect(pane(page).getByTestId("where-not-captured")).toContainText("No screenshot shows this message yet.");
  await expect(pane(page)).toContainText("glossa capture --upload");
  await expect(pane(page).getByTestId("usage")).toContainText("src/checkout/Summary.vue:18");

  await select(page, "legal.terms");
  await expect(pane(page).getByTestId("where-unused")).toContainText("No current build uses this message.");
  await expect(pane(page).getByTestId("capture-shot")).toHaveCount(0);

  // ── the message list's filters ──
  const list = page.getByRole("listbox", { name: "Messages" });
  await expect(list.getByRole("option")).toHaveCount(3);
  await page.getByTestId("ctx-toggle").click();
  await expect(page.locator("#ws-route")).toBeVisible();

  await page.locator("#ws-route").selectOption("/checkout/payment");
  await expect(list.getByRole("option")).toHaveCount(2);
  await page.locator("#ws-component").selectOption("Summary");
  await expect(list.getByRole("option")).toHaveCount(1);
  await expect(list.getByRole("option").first()).toContainText("checkout.total");
  // The filters are in the URL, so the view is bookmarkable.
  expect(page.url()).toContain("component=Summary");
  await expectAccessibleInBothThemes(page, "workspace with context filters");

  await page.getByTestId("ctx-clear").click();
  await page.locator("#ws-file").selectOption("src/ui/PrimaryButton.vue");
  await expect(list.getByRole("option")).toHaveCount(1);
  await expect(list.getByRole("option").first()).toContainText("checkout.pay");

  await page.getByTestId("ctx-clear").click();
  await page.getByTestId("ctx-coverage").selectOption("unused");
  await expect(list.getByRole("option")).toHaveCount(1);
  await expect(list.getByRole("option").first()).toContainText("legal.terms");

  await page.getByTestId("ctx-coverage").selectOption("uncaptured");
  await expect(list.getByRole("option")).toHaveCount(1);
  await expect(list.getByRole("option").first()).toContainText("checkout.total");

  await page.getByTestId("ctx-clear").click();
  await expect(list.getByRole("option")).toHaveCount(3);

  // ── new on a branch: nothing is proposed here yet, and the filter says so ──
  await page.getByLabel("Message state").selectOption("proposed");
  await expect(page.getByText("No messages match these filters.")).toBeVisible();
});
