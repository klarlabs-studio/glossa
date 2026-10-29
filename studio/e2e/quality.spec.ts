/**
 * The quality view end to end (RFC 0005 §8, §9): the project section,
 * the honest empty state before anything has been checked, and the
 * waiver half of the flow — a waiver the API stores shows up with its
 * reason and is revocable from the screen, and revoking it takes
 * nothing away.
 *
 * What is *not* here, and why: nothing can create a check run through
 * /v1 yet (`RecordCheckRun` has no HTTP operation — wave 2 shipped
 * `listCheckRuns`, `getCheckRun` and `listFindings`, not a create), so
 * a browser test cannot seed findings to group, filter or crop. Those
 * paths are covered in src/views/project/QualityView.test.ts against
 * the in-memory port until a create operation exists.
 */
import { expect, test } from "@playwright/test";
import { api, expectAccessible, expectAccessibleInBothThemes, signIn } from "./support";

/** `^f_[0-9a-f]{16}$` — a fingerprint no stored finding carries, which is the unexamined waiver a dashboard should show. */
const FINGERPRINT = "f_7c1a3e9b40d2f815";
const REASON = "“Login” is the German term our brand guide prescribes.";

test("the quality view shows what a project accepts, and lets it be taken back", async ({ page }) => {
  test.setTimeout(180_000);
  await signIn(page, `quality-${Date.now()}@example.com`);

  const v1 = await api(page);
  const t = v1.tenant;
  const project = await v1.post(`/v1/tenants/${t}/projects`, { name: "Quality Demo", slug: `quality-${Date.now()}`, source_locale: "en" });
  const P = `/v1/tenants/${t}/projects/${project.id}`;
  await v1.post(`${P}/locales`, { code: "de" });
  await v1.post(`${P}/message-upserts`, { items: [{ key: "auth.login", text: "Log in" }] });
  const base = `/t/${t}/p/${project.id}`;

  // ── the section is in the project navigation, and reachable ──
  await page.goto(`${base}/translate`);
  await page.getByRole("navigation", { name: "Project sections" }).getByRole("link", { name: "Quality" }).click();
  await expect(page).toHaveURL(new RegExp(`${base}/quality$`));
  await expect(page.getByRole("heading", { name: "Quality", level: 1 })).toBeVisible();

  // ── nothing checked is not a clean run, and the screen says which ──
  await expect(page.getByTestId("quality-never-checked")).toContainText("Nothing has been checked yet.");
  await expect(page.getByTestId("quality-never-checked")).toContainText("glossa check");
  await expect(page.getByTestId("no-waivers")).toContainText("This project accepts nothing yet.");
  await expectAccessibleInBothThemes(page, "quality with nothing checked");

  // ── a waiver the API stored: its reason is the thing shown ──
  const waiver = await v1.post(`${P}/waivers`, { fingerprint: FINGERPRINT, reason: REASON, scope: "project" });
  expect(waiver.reason).toBe(REASON);
  expect(waiver.active).toBe(true);
  // The reason is required: a blank one is refused by the API, as it is by the form.
  const blank = await page.request.post(`${P}/waivers`, {
    headers: { "X-CSRF-Token": (await (await page.request.get("/v1/me")).json()).csrf_token },
    data: { fingerprint: FINGERPRINT, reason: "   " },
  });
  expect(blank.status()).toBe(400);
  expect((await blank.json()).code).toBe("waiver_reason_required");

  await page.reload();
  const row = page.getByTestId("waiver");
  await expect(row).toHaveCount(1);
  await expect(row).toContainText(REASON);
  await expect(row).toContainText("Everywhere in this project");
  await expect(row).toContainText("Stands");
  // No stored finding carries this fingerprint, and the list says so rather than hiding it.
  await expect(page.getByTestId("waiver-unexamined")).toBeVisible();
  await expectAccessibleInBothThemes(page, "quality with a waiver");

  // ── taking it back: reachable from the keyboard, and it deletes nothing ──
  const revoke = row.getByRole("button", { name: /Take the waiver back/ });
  await revoke.focus();
  await expect(revoke).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.getByTestId("quality-status")).toContainText("is taken back");
  await expect(row).toHaveCount(1);
  await expect(row).toContainText("Revoked");
  await expect(row).toContainText(REASON);
  await expect(row.getByRole("button", { name: /Take the waiver back/ })).toHaveCount(0);
  await expectAccessible(page, "quality with a revoked waiver");
});
