/**
 * Import and export end to end (RFC 0003 §6): a dry run of a small
 * XLIFF file that would change an approved translation shows that
 * conflict — where it is in the file — and changes nothing; applying it
 * imports the rest and keeps the approval; a JSON export of de and en,
 * narrowed to a namespace chosen from the project's, downloads as a zip
 * that carries the imported string. A second test imports an XLIFF file
 * whose trgLang the project lacks as a locale chosen in the wizard, and
 * takes the workspace's translation memory in and out on its own screen.
 * Everything runs in the real server's import/export workers against the
 * `dir` object storage.
 */
import { readFileSync } from "node:fs";
import { expect, test } from "@playwright/test";
import { api, expectAccessible, expectAccessibleInBothThemes, signIn } from "./support";
import { unzip } from "./zip";

const XLIFF = `<?xml version="1.0" encoding="UTF-8"?>
<xliff xmlns="urn:oasis:names:tc:xliff:document:2.0" version="2.1" srcLang="en" trgLang="de"><file id="f1">
<unit id="checkout.title" name="checkout.title"><segment state="final"><source>Checkout</source><target>Zur Kasse</target></segment></unit>
<unit id="cart.empty" name="cart.empty"><segment state="final"><source>Your cart is empty</source><target>Dein Warenkorb ist leer</target></segment></unit>
</file></xliff>
`;

test("dry-run an XLIFF import with a conflict, apply it, export JSON and download it", async ({ page }) => {
  test.setTimeout(120_000);
  await signIn(page, `files-${Date.now()}@example.com`);

  // ── a project whose German cart text is approved already ──
  const v1 = await api(page);
  const t = v1.tenant;
  const slug = `files-${Date.now()}`;
  const project = await v1.post(`/v1/tenants/${t}/projects`, { name: "Files Demo", slug, source_locale: "en" });
  const P = `/v1/tenants/${t}/projects/${project.id}`;
  await v1.post(`${P}/locales`, { code: "de" });
  await v1.post(`${P}/message-upserts`, {
    items: [
      { key: "checkout.title", text: "Checkout" },
      { key: "cart.empty", text: "Your cart is empty" },
    ],
  });
  // Translations need Localization to have processed the new messages' events.
  await expect.poll(async () => (await v1.get(`${P}/messages?missing_in=de`)).items.length).toBe(2);
  // Messages are addressed by key.
  await v1.put(`${P}/messages/cart.empty/translations/de`, { text: "Ihr Warenkorb ist leer", state: "approved" });
  const base = `/t/${t}/p/${project.id}`;

  // ── import & export, empty ──
  await page.goto(`${base}/files`);
  await expect(page.getByRole("heading", { name: "Import & export", level: 1 })).toBeVisible();
  await expect(page.getByText("No imports yet.")).toBeVisible();
  await expectAccessibleInBothThemes(page, "import and export");

  // ── the wizard: the file is recognized, a dry run is the default ──
  await page.keyboard.press("i");
  await expect(page.getByRole("heading", { name: "Import a file", level: 1 })).toBeVisible();
  await page.getByLabel("File to import").setInputFiles({ name: "shop.de.xlf", mimeType: "application/xml", buffer: Buffer.from(XLIFF) });
  await expect(page.getByLabel("Format", { exact: true })).toHaveValue("xliff");
  await expect(page.getByText("Recognized as XLIFF 2.1.")).toBeVisible();
  await expect(page.getByLabel("Target locale")).toHaveValue("de");
  await expect(page.getByTestId("xliff-target")).toContainText("The file says de (trgLang).");
  await expect(page.getByRole("radio", { name: /^Dry run/ })).toBeChecked();
  await expectAccessibleInBothThemes(page, "import wizard");
  await page.getByRole("button", { name: "Start dry run" }).click();

  // ── the dry run's results: the approved translation is a conflict, nothing changed ──
  await expect(page.getByRole("heading", { name: "Import of shop.de.xlf", level: 1 })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByTestId("import-state")).toHaveText("Done");
  await expect(page.getByTestId("dry-run-notice")).toContainText("Dry run: nothing changed.");
  const summary = page.getByTestId("import-summary");
  await expect(summary.locator("[data-status=conflict] dd")).toHaveText("1");
  await expect(summary.locator("[data-status=created] dd")).toHaveText("1");
  const conflict = page.getByTestId("import-results").locator("tr[data-status=conflict]");
  await expect(conflict).toContainText("cart.empty");
  await expect(conflict).toContainText("An approved translation differs; the approved one is kept.");
  // Every item says where it is in the file, not only a fatal problem.
  // A translation is located at its <target> (the unit is on line 4).
  await expect(conflict).toContainText(/line 4, column \d+/);
  await expect(conflict.locator(".ref")).toHaveText("#/f=f1/u=cart.empty");
  await expect(page.getByTestId("import-results").locator("tr[data-status=created]")).toContainText(/line 3, column \d+/);
  expect((await page.request.get(`${P}/messages/checkout.title/translations/de`)).status()).toBe(404);
  await page.getByLabel("Status", { exact: true }).selectOption("conflict");
  await expect(page.getByTestId("import-results").locator("tbody tr")).toHaveCount(1);
  await expectAccessibleInBothThemes(page, "import results (dry run)");

  // ── apply it: the same file as a merge; the approval stays ──
  await page.getByRole("button", { name: "Apply this import" }).click();
  await expect(page.getByTestId("dry-run-notice")).toBeHidden({ timeout: 30_000 });
  await expect(page.getByTestId("import-state")).toHaveText("Done");
  await expect(page.getByText("Merge", { exact: true })).toBeVisible();
  await expect(summary.locator("[data-status=created] dd")).toHaveText("1");
  await expect(summary.locator("[data-status=conflict] dd")).toHaveText("1");
  const imported = await v1.get(`${P}/messages/checkout.title/translations/de`);
  expect([imported.text, imported.state, imported.origin]).toEqual(["Zur Kasse", "approved", "import"]);
  expect((await v1.get(`${P}/messages/cart.empty/translations/de`)).text).toBe("Ihr Warenkorb ist leer");

  // ── the history lists both ──
  await page.getByRole("link", { name: "Back to import & export" }).click();
  const imports = page.getByTestId("import-jobs").locator("tbody tr");
  await expect(imports).toHaveCount(2);
  await expect(imports.first()).toContainText("Merge");
  await expect(imports.first()).toContainText("1 created, 0 updated, 1 conflicts");
  await expect(imports.nth(1)).toContainText("Dry run");

  // ── export JSON for en and de, download the zip ──
  await page.getByRole("button", { name: "Export", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Export" });
  await expect(dialog.getByLabel("Format", { exact: true })).toHaveValue("json");
  await expect(dialog.getByRole("checkbox", { name: "en (source)" })).toBeChecked();
  await expect(dialog.getByRole("checkbox", { name: "de", exact: true })).toBeChecked();
  // Namespaces come from the project's own, with their message counts.
  const namespace = dialog.getByTestId("export-namespaces").getByRole("checkbox", { name: "default (2 messages)" });
  await expect(namespace).not.toBeChecked();
  await namespace.check();
  await expectAccessible(page, "export dialog");
  await dialog.getByRole("button", { name: "Export", exact: true }).click();
  await expect(dialog.getByTestId("export-ready")).toContainText(`${slug}.json.zip is ready`, { timeout: 30_000 });
  const [download] = await Promise.all([page.waitForEvent("download"), dialog.getByRole("button", { name: "Download" }).click()]);
  expect(download.suggestedFilename()).toBe(`${slug}.json.zip`);
  const files = unzip(readFileSync((await download.path())!));
  expect([...files.keys()].sort()).toEqual(["de.json", "en.json"]);
  expect(JSON.parse(files.get("de.json")!)).toEqual({ "cart.empty": "Ihr Warenkorb ist leer", "checkout.title": "Zur Kasse" });
  expect(JSON.parse(files.get("en.json")!)).toMatchObject({ "checkout.title": "Checkout" });
  await expect(dialog.getByTestId("export-saved")).toHaveText(`Saved ${slug}.json.zip.`);
  await dialog.getByRole("button", { name: "Close" }).click();
  await expect(page.getByTestId("export-jobs").locator("tbody tr").first()).toContainText("Done");
  const exported = await v1.get(`/v1/tenants/${t}/export-jobs?project=${project.id}`);
  expect(exported.items[0].options.namespaces).toEqual(["default"]);
});

const SWISS = `<?xml version="1.0" encoding="UTF-8"?>
<xliff xmlns="urn:oasis:names:tc:xliff:document:2.0" version="2.1" srcLang="en" trgLang="de-CH"><file id="f1">
<unit id="checkout.title" name="checkout.title"><segment state="translated"><source>Checkout</source><target>Zur Kasse</target></segment></unit>
</file></xliff>
`;
const TMX = `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4"><header creationtool="e2e" creationtoolversion="1" segtype="sentence" o-tmf="x" adminlang="en" srclang="en" datatype="plaintext"/>
<body>
<tu tuid="save"><tuv xml:lang="en"><seg>Save changes</seg></tuv><tuv xml:lang="de"><seg>Änderungen speichern</seg></tuv></tu>
</body></tmx>
`;

test("import an XLIFF file as a chosen locale; the workspace's translation memory in and out", async ({ page }) => {
  test.setTimeout(120_000);
  await signIn(page, `knowledge-${Date.now()}@example.com`);
  const v1 = await api(page);
  const t = v1.tenant;
  const project = await v1.post(`/v1/tenants/${t}/projects`, { name: "Locale Demo", slug: `locale-${Date.now()}`, source_locale: "en" });
  const P = `/v1/tenants/${t}/projects/${project.id}`;
  await v1.post(`${P}/locales`, { code: "de" });
  await v1.post(`${P}/message-upserts`, { items: [{ key: "checkout.title", text: "Checkout" }] });
  await expect.poll(async () => (await v1.get(`${P}/messages?missing_in=de`)).items.length).toBe(1);

  // ── the file says de-CH, the project has de: the wizard asks which locale to import it as ──
  await page.goto(`/t/${t}/p/${project.id}/files/import`);
  await page.getByLabel("File to import").setInputFiles({ name: "shop.xlf", mimeType: "application/xml", buffer: Buffer.from(SWISS) });
  await expect(page.getByTestId("xliff-target")).toContainText("The file says de-CH (trgLang), which isn't a locale of this project");
  await expect(page.getByTestId("import-problem")).toHaveText("Choose the locale to import the file's translations as.");
  await expect(page.getByRole("button", { name: "Start dry run" })).toBeDisabled();
  await page.getByLabel("Target locale").selectOption("de");
  await page.getByRole("radio", { name: /^Merge/ }).check();
  await expectAccessible(page, "import wizard (locale choice)");
  await page.getByRole("button", { name: "Import", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Import of shop.xlf", level: 1 })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByTestId("import-state")).toHaveText("Done");
  await expect(page.getByTestId("import-summary").locator("[data-status=created] dd")).toHaveText("1");
  expect((await v1.get(`${P}/messages/checkout.title/translations/de`)).text).toBe("Zur Kasse");

  // ── workspace settings › translation memory & termbase ──
  await page.goto(`/t/${t}`);
  await page.getByRole("link", { name: /Translation memory & termbase/ }).click();
  await expect(page.getByRole("heading", { name: "Translation memory & termbase", level: 1 })).toBeVisible();
  await expect(page.getByText("No imports yet.")).toBeVisible();
  await expectAccessibleInBothThemes(page, "workspace translation memory and termbase");

  await page.getByLabel("TMX or TBX file").setInputFiles({ name: "vendor.tmx", mimeType: "application/xml", buffer: Buffer.from(TMX) });
  await expect(page.getByTestId("knowledge-file")).toContainText("into the workspace's translation memory");
  await page.getByRole("radio", { name: /^Merge/ }).check();
  await page.getByRole("button", { name: "Import", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Import of vendor.tmx", level: 1 })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByTestId("import-state")).toHaveText("Done");
  const unit = page.getByTestId("import-results").locator("tr[data-status=created]");
  await expect(unit).toContainText("line 4");
  await expect(unit.locator(".ref")).toHaveText("tu[1]");
  const jobs = await v1.get(`/v1/tenants/${t}/tm-import-jobs`);
  expect(jobs.items).toHaveLength(1);
  expect(jobs.items[0].project_id).toBeUndefined();

  // ── export the whole memory (source en) and download it ──
  await page.getByRole("link", { name: "Back to translation memory & termbase" }).click();
  await expect(page.getByTestId("knowledge-imports").locator("tbody tr")).toHaveCount(1);
  await page.getByLabel("Source locale").fill("en");
  await page.getByRole("button", { name: "Export", exact: true }).click();
  await expect(page.getByTestId("knowledge-export-ready")).toContainText("is ready", { timeout: 30_000 });
  const [download] = await Promise.all([page.waitForEvent("download"), page.getByTestId("knowledge-export").getByRole("button", { name: "Download" }).click()]);
  expect(readFileSync((await download.path())!, "utf8")).toContain("Änderungen speichern");
  await expect(page.getByTestId("knowledge-exports").locator("tbody tr").first()).toContainText("Done");
  expect((await v1.get(`/v1/tenants/${t}/tm-export-jobs`)).items).toHaveLength(1);
});
