import { createHash } from "node:crypto";
import { expect, test } from "@playwright/test";
import { keyIndexed, servedManifest, signInLink } from "./harness";
import { expectAccessible, expectAccessibleInBothThemes } from "./support";

test("sign in by magic link, set up a project, translate with the keyboard, fix QA, approve, release", async ({ page }) => {
  test.setTimeout(180_000);
  const email = `translator-${Date.now()}@example.com`;

  // ── sign in with the link the dev mailer captured ──────────────────
  await page.goto("/app");
  await expect(page.getByRole("heading", { name: "Sign in to Glossa" })).toBeVisible();
  await expect(page.getByText("Public Beta").first()).toBeVisible();
  await expectAccessible(page, "sign-in");
  await page.getByLabel("Email address").fill(email);
  await page.getByRole("button", { name: "Email me a sign-in link" }).click();
  await expect(page.getByRole("heading", { name: "Check your inbox" })).toBeVisible();

  await page.goto(await signInLink(email));
  await expect(page.getByRole("heading", { name: "Projects" })).toBeVisible();
  expect(page.url()).not.toContain("token=");
  await expectAccessible(page, "projects");

  // ── create a project ───────────────────────────────────────────────
  await page.getByRole("button", { name: "New project" }).click();
  await page.getByLabel("Name", { exact: true }).fill("Demo App");
  await page.getByLabel("Source locale").fill("en");
  await expect(page.getByLabel("Translations need review before release")).toBeChecked();
  await page.getByRole("button", { name: "Create project" }).click();

  // ── add locales (BCP 47, canonicalized, direction shown) ───────────
  await expect(page.getByRole("heading", { name: "Locales", exact: true })).toBeVisible();
  const add = page.getByLabel("Add locale");
  await add.fill("de_de");
  await expect(page.getByText("Adds de-DE — German (Germany), written left to right.")).toBeVisible();
  await add.fill("de");
  await page.getByRole("button", { name: "Add locale" }).click();
  await expect(page.getByRole("cell", { name: "de", exact: true })).toBeVisible();
  await add.fill("ar");
  await page.getByRole("button", { name: "Add locale" }).click();
  await expect(page.getByRole("row", { name: /ar .*Right to left/ })).toBeVisible();
  await add.fill("en-u-ca-gregory");
  await expect(page.getByText("Extensions (-u-, -t-) and private use (-x-) aren't allowed.")).toBeVisible();
  await add.fill("");
  await expectAccessible(page, "locales");

  // ── import a small catalog through the API (as the CLI would) ──────
  const projectId = new URL(page.url()).pathname.split("/")[4]!;
  const tenantId = new URL(page.url()).pathname.split("/")[2]!;
  const me = await (await page.request.get("/v1/me")).json();
  const upsert = await page.request.post(`/v1/tenants/${tenantId}/projects/${projectId}/message-upserts`, {
    headers: { "X-CSRF-Token": me.csrf_token },
    data: {
      items: [
        { key: "app.title", text: "Glossa demo", description: "Browser tab title" },
        { key: "cart.items", text: "{count, plural, one {# item} other {# items}}", description: "Cart badge" },
        { key: "greeting", text: "Hello, {name}!", description: "Shown on the dashboard after sign-in" },
      ],
    },
  });
  expect(upsert.ok()).toBe(true);
  expect((await upsert.json()).results.map((r: { status: string }) => r.status)).toEqual(["created", "created", "created"]);
  // Coverage filters see new messages once Localization has processed their events.
  await expect
    .poll(async () => (await (await page.request.get(`/v1/tenants/${tenantId}/projects/${projectId}/messages?missing_in=de`)).json()).items.length)
    .toBe(3);

  // ── the translator workspace, keyboard first ───────────────────────
  await page.getByRole("link", { name: "Translate" }).click();
  await page.getByLabel("Target locale").selectOption("de");
  const list = page.getByRole("listbox", { name: "Messages" });
  await expect(list.getByRole("option")).toHaveCount(3);
  await expect(page.getByRole("heading", { name: "app.title" })).toBeVisible();
  await expect(list.getByRole("option", { name: /app\.title/ })).toContainText("Missing");

  await page.keyboard.press("j");
  await expect(page.getByRole("heading", { name: "cart.items" })).toBeVisible();
  await page.keyboard.press("j");
  await expect(page.getByRole("heading", { name: "greeting" })).toBeVisible();
  await expect(page.getByText("Shown on the dashboard after sign-in")).toBeVisible();
  await expect(page.getByTestId("preview-source")).toHaveText("Hello, Ada!");

  // A broken placeholder: the live MF1 preview (the server's kernel)
  // flags it before anything is saved…
  await page.keyboard.press("Enter");
  const editor = page.getByTestId("target-editor");
  await expect(editor).toBeFocused();
  await expect(editor).toHaveAttribute("lang", "de");
  await expect(editor).toHaveAttribute("dir", "ltr");
  await editor.fill("Hallo, {name");
  const previewErrors = page.getByTestId("preview-errors");
  await expect(previewErrors).toContainText("mf1-syntax-error");
  await expect(page.getByTestId("preview-target")).toHaveText("Nothing to preview yet.");
  await expectAccessible(page, "workspace with a preview error");
  // …and previews text that parses as the translator types.
  await editor.fill("Hallo, {name}!!");
  await expect(previewErrors).toBeHidden();
  await expect(page.getByTestId("preview-target")).toHaveText("Hallo, Ada!!");

  // A misspelt placeholder parses, but the server's structural QA refuses it.
  await editor.fill("Hallo, {nam}!");
  await page.keyboard.press("ControlOrMeta+Enter");
  const qa = page.getByTestId("qa-findings");
  await expect(qa).toBeVisible();
  await expect(qa).toContainText("missing-argument");
  await expect(editor).toHaveAttribute("aria-invalid", "true");
  await expectAccessible(page, "workspace with QA findings");

  // Fixed, then saved and approved in one chord.
  await editor.fill("Hallo, {name}!");
  await page.keyboard.press("ControlOrMeta+Shift+Enter");
  await expect(page.getByTestId("editor-status")).toHaveText("Approved.");
  await expect(page.getByTestId("translation-state")).toHaveText("Approved");
  await expect(qa).toBeHidden();
  await expect(page.getByTestId("preview-target")).toHaveText("Hallo, Ada!");

  // Save only: back to the list, translate the plural, which waits for review.
  await page.keyboard.press("Escape");
  await expect(list).toBeFocused();
  await page.keyboard.press("k");
  await expect(page.getByRole("heading", { name: "cart.items" })).toBeVisible();
  await page.keyboard.press("Enter");
  await editor.fill("{count, plural, one {# Artikel} other {# Artikel}}");
  await page.keyboard.press("ControlOrMeta+Enter");
  await expect(page.getByTestId("editor-status")).toHaveText("Saved.");
  await expect(page.getByTestId("translation-state")).toHaveText("Needs review");

  // …and a reviewer approves it with the button.
  await page.getByRole("button", { name: "Approve", exact: true }).click();
  await expect(page.getByTestId("translation-state")).toHaveText("Approved");

  // History shows both revisions with provenance.
  await page.getByRole("tab", { name: "History" }).click();
  await expect(page.getByRole("list", { name: "Revisions, newest first" }).getByRole("listitem")).toHaveCount(2);
  await expectAccessible(page, "workspace history");

  // The shortcut sheet.
  await page.getByRole("tab", { name: "Editor" }).focus();
  await page.keyboard.press("?");
  await expect(page.getByRole("dialog", { name: "Keyboard shortcuts" })).toBeVisible();
  await expectAccessible(page, "shortcut sheet");
  await page.keyboard.press("Escape");

  // ── releases: publish, promote, roll back ─────────────────────────
  await page.getByRole("link", { name: "Releases" }).click();
  await expect(page.getByRole("heading", { name: "Releases", level: 1 })).toBeVisible();
  const dev = page.getByTestId("env-development");
  const prod = page.getByTestId("env-production");
  await expect(dev.getByTestId("env-version")).toHaveText("Nothing published here yet");
  await expect(prod).toContainText("Ships: Approved; outdated included");
  await expectAccessibleInBothThemes(page, "releases (empty)");

  const publish = async (env: string) => {
    await page.getByRole("button", { name: "Publish release" }).click();
    const dialog = page.getByRole("dialog", { name: "Publish a release" });
    await dialog.getByLabel("Environment").selectOption(env);
    await expect(dialog.getByTestId("publish-preview")).toContainText(`${env} ships translations that are:`);
    await expect(dialog.getByTestId("dry-run-summary")).toBeVisible();
    return dialog;
  };

  // Publish to development. The dry run shows what would ship before
  // anything is stored: both approved German translations, all added.
  let d = await publish("development");
  const dryRun = d.getByTestId("publish-preview");
  await expect(dryRun).toContainText("Nothing is published to development yet");
  await expect(d.getByTestId("dry-run-summary")).toContainText("3 messages in 3 locales · 3 new artifacts to upload");
  await expect(dryRun.locator("tr[data-locale=de]")).toContainText("2");
  await expect(dryRun.locator("tr[data-locale=de]")).toContainText("+2");
  expect(servedManifest(projectId, "development")).toBeNull();
  await expectAccessibleInBothThemes(page, "publish dialog");
  await d.getByRole("button", { name: "Publish to development" }).click();
  await expect(d.getByRole("status")).toHaveText("Published v1 to development.");
  await expect(d.locator("tr[data-locale=de]")).toContainText("2");
  await d.getByRole("button", { name: "Done" }).click();
  await expect(dev.getByTestId("env-version")).toHaveText("Serving v1");
  await expect(dev).toContainText("Published by you");
  await expect.poll(() => servedManifest(projectId, "development")?.release.version).toBe(1);

  // Development ships drafts, so its releases can't go to production: the
  // way there is staging, which ships approved text like production.
  await prod.getByRole("button", { name: "Promote to production" }).click();
  d = page.getByRole("dialog", { name: "Promote a release" });
  await d.getByLabel("Release").selectOption({ label: "v1 · development" });
  await expect(d.getByTestId("promote-ineligible")).toContainText("v1 can't go to production");
  await expect(d.getByTestId("promote-ineligible")).toContainText("Publish to staging");
  await expect(d.getByRole("button", { name: "Promote v1 to production" })).toBeDisabled();
  await d.getByRole("button", { name: "Cancel" }).click();

  // The policy dialog (staging keeps its default: approved only).
  await page.getByTestId("env-staging").getByRole("button", { name: "Policy of staging" }).click();
  d = page.getByRole("dialog", { name: "What ships to staging" });
  await expect(d.getByLabel("Approved")).toBeChecked();
  await expect(d.getByLabel("Draft")).not.toBeChecked();
  await expectAccessible(page, "policy dialog");
  await d.getByRole("button", { name: "Cancel" }).click();

  d = await publish("staging");
  await expect(d.getByTestId("dry-run-summary")).toContainText("3 messages in 3 locales");
  await d.getByRole("button", { name: "Publish to staging" }).click();
  await expect(d.getByRole("status")).toHaveText("Published v2 to staging.");
  await d.getByRole("button", { name: "Done" }).click();

  await prod.getByRole("button", { name: "Promote to production" }).click();
  d = page.getByRole("dialog", { name: "Promote a release" });
  await d.getByLabel("Release").selectOption({ label: "v2 · staging" });
  await expect(d.getByTestId("promote-summary")).toContainText("production: nothing → v2");
  await d.getByRole("button", { name: "Promote v2 to production" }).click();
  await expect(page.getByTestId("releases-status")).toHaveText("production now serves v2.");
  await expect(prod.getByTestId("env-version")).toHaveText("Serving v2");
  await expect.poll(() => servedManifest(projectId, "production")?.release.version).toBe(2);

  // Approve one more translation, publish it and promote it.
  await page.getByRole("link", { name: "Translate" }).click();
  await page.getByLabel("Target locale").selectOption("de");
  await list.getByRole("option", { name: /app\.title/ }).click();
  await expect(page.getByRole("heading", { name: "app.title" })).toBeVisible();
  await page.keyboard.press("Enter");
  await expect(editor).toBeFocused();
  await editor.fill("Glossa-Demo");
  await page.keyboard.press("ControlOrMeta+Shift+Enter");
  await expect(page.getByTestId("translation-state")).toHaveText("Approved");

  await page.getByRole("link", { name: "Releases" }).click();
  d = await publish("staging");
  // Against what staging serves: the one new German text.
  await expect(d.getByTestId("publish-preview")).toContainText("Compared with v2, which staging serves now:");
  await expect(d.getByTestId("publish-preview").locator("tr[data-locale=de]")).toContainText("+1");
  await d.getByLabel("Note (optional)").fill("German title");
  await d.getByRole("button", { name: "Publish to staging" }).click();
  await expect(d.getByRole("status")).toHaveText("Published v3 to staging.");
  await expect(d.getByRole("heading", { name: "Changes since v2, per locale" })).toBeVisible();
  await expect(d.locator("tr[data-locale=de]")).toContainText("+1");
  await expectAccessible(page, "publish result");
  await d.getByRole("button", { name: "Done" }).click();

  await prod.getByRole("button", { name: "Promote to production" }).click();
  d = page.getByRole("dialog", { name: "Promote a release" });
  await d.getByLabel("Release").selectOption({ label: "v3 · staging · German title" });
  await expect(d.getByTestId("promote-summary")).toContainText("production: v2 → v3");
  await expect(d.locator("tr[data-locale=de]")).toContainText("+1");
  await expectAccessibleInBothThemes(page, "promote dialog");
  await d.getByRole("button", { name: "Promote v3 to production" }).click();
  await expect(prod.getByTestId("env-version")).toHaveText("Serving v3");
  await expect.poll(() => servedManifest(projectId, "production")?.release.version).toBe(3);

  // Roll production back: the dialog names the move and what goes away.
  await prod.getByRole("button", { name: "Roll back production" }).click();
  d = page.getByRole("dialog", { name: "Roll back production" });
  await expect(d.getByTestId("rollback-summary")).toContainText("production: v3 → v2");
  await expect(d.locator("tr[data-locale=de]")).toContainText("−1");
  await expectAccessible(page, "rollback dialog");
  await d.getByRole("button", { name: "Roll production back to v2" }).click();
  await expect(page.getByTestId("releases-status")).toHaveText("production is back on v2.");
  await expect(prod.getByTestId("env-version")).toHaveText("Serving v2");
  await expect(prod).toContainText("Rolled back by you");
  await expect.poll(() => servedManifest(projectId, "production")?.release.version).toBe(2);
  await expectAccessibleInBothThemes(page, "releases");

  // The deployment history, newest first.
  await prod.getByRole("button", { name: "History of production" }).click();
  d = page.getByRole("dialog", { name: "Deployments to production" });
  await expect(d.getByTestId("deployments").locator("tbody tr")).toHaveCount(3);
  await expect(d.getByTestId("deployments").locator("tbody tr").first()).toContainText("Rolled back");
  await d.getByRole("button", { name: "Close" }).click();

  // Release detail: per-locale counts and the diff to its parent.
  await page.getByTestId("release-list").getByRole("link", { name: "v3" }).click();
  await expect(page.getByRole("heading", { name: "Release v3" })).toBeVisible();
  await expect(page.getByText("German title")).toBeVisible();
  await expect(page.getByTestId("release-locales").locator("tr[data-locale=de]")).toContainText("+1");
  await expectAccessibleInBothThemes(page, "release detail");

  // ── delivery keys ──────────────────────────────────────────────────
  await page.getByRole("link", { name: "Settings" }).click();
  await page.locator("#dk-name").fill("web");
  await page.getByRole("button", { name: "Create delivery key" }).click();
  d = page.getByRole("dialog", { name: "Delivery key “web”" });
  const key = await d.getByTestId("created-key").inputValue();
  expect(key).toMatch(/^glossa_pk_[A-Za-z0-9_-]{32}$/);
  await expect(d.getByRole("tab", { name: "JavaScript" })).toHaveAttribute("aria-selected", "true");
  await expect(d.getByTestId("snippet-runtime")).toContainText(`deliveryKey: "${key}"`);
  await d.getByRole("tab", { name: "JavaScript" }).press("End");
  await expect(d.getByRole("tab", { name: "Go" })).toBeFocused();
  await expect(d.getByTestId("snippet-go")).toContainText(`DeliveryKey: "${key}"`);
  await expectAccessibleInBothThemes(page, "delivery key created");
  await d.getByRole("button", { name: "Done" }).click();
  await expect.poll(() => keyIndexed(key)).toBe(true);
  await expect(page.getByTestId("delivery-keys")).toContainText("Active");
  await expect(page.getByTestId("delivery-keys")).not.toContainText(key);
  // A new key reads production only (RFC 0004 §4.3).
  const reads = (name: string) => page.getByTestId("delivery-keys").locator("tbody tr").filter({ hasText: name }).getByTestId("key-scope");
  await expect(reads("web")).toHaveText("production");

  // A preview key reads branch previews, and nothing in production.
  await page.locator("#dk-name").fill("previews");
  const scope = page.getByTestId("new-key-scope");
  await scope.getByRole("checkbox", { name: "production", exact: true }).uncheck();
  await scope.getByRole("checkbox", { name: "preview", exact: true }).check();
  await page.getByTestId("new-key-preview").check();
  await page.getByRole("button", { name: "Create delivery key" }).click();
  d = page.getByRole("dialog", { name: "Delivery key “previews”" });
  const previewKey = await d.getByTestId("created-key").inputValue();
  await d.getByRole("button", { name: "Done" }).click();
  await expect.poll(() => keyIndexed(previewKey)).toBe(true);
  await expect(reads("previews")).toHaveText("preview + branch previews");

  // What a key reads can change without changing the key.
  await page.getByRole("button", { name: "Change scope web" }).click();
  d = page.getByRole("dialog", { name: "What “web” reads" });
  await d.getByTestId("scope-edit-preview").check();
  await expectAccessibleInBothThemes(page, "delivery key scope");
  await d.getByRole("button", { name: "Save" }).click();
  await expect(page.getByRole("status")).toHaveText("web now reads production + branch previews.");
  await expect(reads("web")).toHaveText("production + branch previews");

  await page.getByRole("button", { name: "Revoke web" }).click();
  d = page.getByRole("dialog", { name: "Revoke “web”?" });
  await d.getByRole("button", { name: "Revoke key" }).click();
  await expect(page.getByTestId("delivery-keys")).toContainText("Revoked");
  await expect.poll(() => keyIndexed(key)).toBe(false);
});

test("dark theme stays accessible", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("kl-theme", "dark"));
  await page.goto("/auth/sign-in");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expectAccessible(page, "sign-in (dark)");
});

test("serves the in-product editor with the hash a build pins", async ({ request }) => {
  // RFC 0004 §5.1: preview deployments load /overlay/v1/overlay.js with the
  // integrity from overlay.json, so the two must agree byte for byte.
  const script = await request.get("/overlay/v1/overlay.js");
  expect(script.ok()).toBe(true);
  const body = await script.body();
  const release = await (await request.get("/overlay/v1/overlay.json")).json();
  expect(release.integrity).toBe(`sha384-${createHash("sha384").update(body).digest("base64")}`);
  expect(release.version).toMatch(/^\d+\.\d+\.\d+/);
  expect(body.toString("utf8")).toContain("glossa-overlay");
});
