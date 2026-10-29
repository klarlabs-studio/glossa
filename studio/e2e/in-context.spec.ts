/**
 * In-product editing, against the real server (RFC 0004 §5.2): registering
 * where the editor may run, and the popup that authorizes it.
 *
 * The popup half runs with a real opener — a tiny page on a loopback port
 * standing in for a product's preview deployment — so `window.opener`,
 * the cross-origin `postMessage` and the target-origin check are the
 * browser's, not a stub's.
 */
import { createServer, type Server } from "node:http";
import { AddressInfo } from "node:net";
import { expect, test } from "@playwright/test";
import { api, expectAccessibleInBothThemes, signIn } from "./support";

/** A page that opens the authorize popup and keeps what comes back. */
function opener(): Promise<{ origin: string; close: () => Promise<void> }> {
  const server = createServer((req, res) => {
    res.writeHead(200, { "content-type": "text/html" });
    res.end(
      `<!doctype html><meta charset="utf-8"><title>Preview deployment</title>
       <script>
         window.received = [];
         window.addEventListener("message", (e) => {
           window.received.push({ origin: e.origin, data: e.data });
         });
         window.authorize = (url) => window.open(url, "_blank", "popup=yes,width=460,height=620");
       </script>`,
    );
  });
  return new Promise((resolve) => {
    server.listen(0, "127.0.0.1", () => {
      const { port } = server.address() as AddressInfo;
      resolve({
        origin: `http://127.0.0.1:${port}`,
        close: () => new Promise<void>((done) => (server as Server).close(() => done())),
      });
    });
  });
}

test("registers a preview origin and authorizes the editor for it", async ({ page, context }) => {
  test.setTimeout(240_000);
  await signIn(page, `in-context-${Date.now()}@example.com`);
  const v1 = await api(page);
  const tenant = v1.tenant;
  const project = await v1.post(`/v1/tenants/${tenant}/projects`, {
    slug: `editor-${Date.now()}`,
    name: "Editor",
    source_locale: "en",
  });

  const app = await opener();

  // ── project settings ──────────────────────────────────────────────
  await page.goto(`/t/${tenant}/p/${project.id}/settings`);
  const card = page.getByRole("region", { name: "In-product editing" });
  await expect(card.getByText("cannot run anywhere")).toBeVisible();

  // An origin the editor may not run on is refused with the server's words.
  await card.getByLabel("Origin").fill("http://preview.example.com");
  await card.getByRole("button", { name: "Allow the editor here" }).click();
  await expect(card.getByRole("alert")).toContainText("scheme, host and port");

  // The developer's own machine is allowed, and marked as such.
  await card.getByLabel("Origin").fill(app.origin);
  await card.getByLabel("Label").fill("the test's opener");
  await card.getByRole("button", { name: "Allow the editor here" }).click();
  await expect(card.getByRole("status")).toContainText("may now run on");
  const row = card.getByRole("row").filter({ hasText: app.origin });
  await expect(row).toContainText("Development");

  // ── the check policy (RFC 0004 §6.4) ──────────────────────────────
  // The same settings screen owns what a pull request fails on, and the
  // card explains the settings in the sentence people came for.
  await v1.post(`/v1/tenants/${tenant}/projects/${project.id}/locales`, { code: "de" });
  await page.reload();
  const check = page.getByRole("region", { name: "Pull request check" });
  await expect(check.getByText("A pull request fails when the check finds an error.")).toBeVisible();
  await expect(check.getByText("A new key with no translation in any locale is an error.")).toBeVisible();

  await check.getByLabel("Locales that must be complete").selectOption("listed");
  await check.getByLabel("de", { exact: true }).check();
  await check.getByLabel("An untranslated key is").selectOption("warning");
  await expect(check.getByText("A new key with no translation in de is a warning")).toBeVisible();
  await check.getByRole("button", { name: "Save" }).click();
  await expect(check.getByRole("status")).toContainText("Check policy saved.");

  // Stored on the project, which is where the pull-request check reads it.
  const saved = await v1.get(`/v1/tenants/${tenant}/projects/${project.id}`);
  expect(saved.settings.check_policy).toEqual({
    require_complete: "listed",
    locales: ["de"],
    fail_on: "error",
    missing_translations: "warning",
  });
  // axe's target-size measures against the viewport, so a page left
  // half-scrolled by the form above reports controls that are simply
  // above the fold as "partially obscured". Scan from the top.
  await page.evaluate(() => window.scrollTo(0, 0));
  await expectAccessibleInBothThemes(page, "project settings, in-product editing");

  // ── the popup ─────────────────────────────────────────────────────
  const preview = await context.newPage();
  await preview.goto(app.origin);
  const authorize =
    `/in-context/authorize?tenant=${tenant}&project=${project.id}` +
    `&origin=${encodeURIComponent(app.origin)}&channel=nonce-1`;
  const url = new URL(authorize, page.url()).href;

  const popupPromise = preview.waitForEvent("popup");
  await preview.evaluate((u) => (window as unknown as { authorize: (u: string) => void }).authorize(u), url);
  const popup = await popupPromise;

  await expect(popup.getByRole("heading", { name: "Allow in-product editing" })).toBeVisible();
  await expect(popup.getByText(app.origin)).toBeVisible();
  await expect(popup.getByText("write translations")).toBeVisible();
  await expect(popup.getByText("cannot publish a release")).toBeVisible();
  await expectAccessibleInBothThemes(popup, "in-context authorize");

  // Nothing is minted until the person says so.
  expect(await preview.evaluate(() => (window as unknown as { received: unknown[] }).received.length)).toBe(0);

  await popup.getByRole("button", { name: "Allow editing" }).click();

  await expect
    .poll(
      async () => preview.evaluate(() => (window as unknown as { received: unknown[] }).received.length),
      { timeout: 30_000 },
    )
    .toBeGreaterThan(0);

  const [message] = await preview.evaluate(
    () => (window as unknown as { received: Array<{ origin: string; data: Record<string, unknown> }> }).received,
  );
  // The event's origin is the sender's — Studio — which is what a real
  // opener checks before believing anything. The grant's own `origin` is
  // the page it was minted for.
  expect(message!.origin).toBe(new URL(page.url()).origin);
  expect(message!.data.type).toBe("glossa.in-context-grant");
  expect(message!.data.channel).toBe("nonce-1");
  expect(message!.data.ok).toBe(true);
  expect(String(message!.data.token)).toMatch(/^glossa_ctx_/);
  expect(message!.data.project_id).toBe(project.id);
  expect(message!.data.origin).toBe(app.origin);

  // The grant works from that origin, on the editor's endpoints only.
  const token = String(message!.data.token);
  const from = { Origin: app.origin, Authorization: `Bearer ${token}` };
  const message404 = await page.request.get(
    `/v1/tenants/${tenant}/projects/${project.id}/messages/01a0be1c-0000-7000-8000-00000000dead`,
    { headers: from },
  );
  expect(message404.status(), await message404.text()).toBe(404); // authenticated, just missing
  expect(message404.headers()["access-control-allow-origin"]).toBe(app.origin);

  const tokens = await page.request.get(`/v1/tenants/${tenant}/tokens`, { headers: from });
  expect(tokens.status()).toBe(401); // not one of the editor's endpoints

  const elsewhere = await page.request.get(
    `/v1/tenants/${tenant}/projects/${project.id}/messages/01a0be1c-0000-7000-8000-00000000dead`,
    { headers: { Origin: "https://evil.example.com", Authorization: `Bearer ${token}` } },
  );
  expect(elsewhere.status()).toBe(401); // bound to the origin it was minted for

  // Removing the origin ends the session on it now, not in fifteen minutes.
  await page.goto(`/t/${tenant}/p/${project.id}/settings`);
  await card.getByRole("row").filter({ hasText: app.origin }).getByRole("button", { name: "Remove" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Remove" }).click();
  await expect(card.getByRole("status")).toContainText("no longer runs on");

  const after = await page.request.get(
    `/v1/tenants/${tenant}/projects/${project.id}/messages/01a0be1c-0000-7000-8000-00000000dead`,
    { headers: from },
  );
  expect(after.status()).toBe(401);

  await app.close();
});
