/**
 * The check-policy editor and the waiver screen end to end
 * (RFC 0005 §4, §9), against a real glossa-server.
 *
 * The two properties worth a browser here are the ones a unit test
 * cannot prove:
 * - **no save without a preview**: there is no button that stores a
 *   policy until the impact of that exact document has been asked for
 *   with `dry_run: true`, and the version the server reports afterwards
 *   is the one the screen said it saved;
 * - **the order is reorderable from the keyboard**, with the move
 *   announced, and both screens pass axe in both themes.
 *
 * What is *not* here, and why: nothing can create a check run through
 * /v1 yet, so a preview in a browser is measured against no stored
 * findings and every impact number is zero. The arithmetic is covered
 * in src/views/project/CheckPolicyView.test.ts against the in-memory
 * port, which decides findings the way the kernel does.
 */
import { expect, test } from "@playwright/test";
import { api, expectAccessible, expectAccessibleInBothThemes, signIn } from "./support";

test("the policy editor previews before it saves, and its order is keyboard-reorderable", async ({ page }) => {
  test.setTimeout(180_000);
  await signIn(page, `policy-${Date.now()}@example.com`);

  const v1 = await api(page);
  const t = v1.tenant;
  const project = await v1.post(`/v1/tenants/${t}/projects`, { name: "Policy Demo", slug: `policy-${Date.now()}`, source_locale: "en" });
  const P = `/v1/tenants/${t}/projects/${project.id}`;
  await v1.post(`${P}/locales`, { code: "de" });
  const base = `/t/${t}/p/${project.id}`;

  // ── reachable from the quality section, as its own page ──
  await page.goto(`${base}/quality`);
  await page.getByRole("navigation", { name: "Quality sections" }).getByRole("link", { name: "Check policy" }).click();
  await expect(page).toHaveURL(new RegExp(`${base}/quality/policy$`));
  await expect(page.getByRole("heading", { name: "Check policy", level: 1 })).toBeVisible();

  // A project that never saved one grades by the documented default, and says so.
  await expect(page.getByTestId("policy-version")).toContainText("No policy saved yet");
  await expect(page.getByTestId("policy-no-grace")).toContainText("No pull request is pinned");
  await expectAccessibleInBothThemes(page, "check policy, never saved");

  // ── a rule, and its position spelled out ──
  await page.getByTestId("policy-add-rule").click();
  const rules = page.getByTestId("policy-rule");
  await expect(rules).toHaveCount(1);
  await expect(rules.first()).toContainText("Rule 1 of 1");
  await expect(rules.first().getByTestId("rule-specificity")).toContainText("Names no field");
  await rules.first().getByTestId("rule-layer").selectOption("terminology");
  await rules.first().getByTestId("rule-severity-select").selectOption("error");
  await expect(rules.first().getByTestId("rule-selects")).toContainText("Layer: terminology");
  await expect(rules.first().getByTestId("rule-specificity")).toContainText("Names 1 field");

  // ── warn mode is a control, and says what it means ──
  await page.getByTestId("policy-add-rule").click();
  await expect(rules).toHaveCount(2);
  await rules.nth(1).getByTestId("rule-layer").selectOption("visual");
  await rules.nth(1).getByTestId("rule-mode").selectOption("warn");
  await expect(rules.nth(1).getByTestId("rule-warn-hint")).toContainText("cannot change any verdict");

  // ── reordering, from the keyboard, announced ──
  const up = rules.nth(1).getByTestId("rule-up");
  await up.focus();
  await expect(up).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.getByTestId("policy-status")).toHaveText("Moved from position 2 to position 1 of 2.");
  await expect(rules.first().getByTestId("rule-selects")).toContainText("Layer: visual");
  // The keyboard went with the rule rather than being dropped at the top of the list.
  await expect(rules.first().getByTestId("rule-down")).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(rules.first().getByTestId("rule-selects")).toContainText("Layer: terminology");

  // ── nothing is stored until the impact of this document is asked for ──
  await expect(page.getByTestId("policy-save")).toHaveCount(0);
  await expect(page.getByTestId("policy-preview-needed")).toContainText("Preview the impact first");
  expect((await v1.get(`${P}/check-policy`)).version).toBe(0);

  await page.getByTestId("policy-preview").click();
  await expect(page.getByTestId("policy-impact")).toBeVisible();
  // No check run exists yet, so the preview says it measured nothing rather than showing a reassuring zero.
  await expect(page.getByTestId("impact-nothing-measured")).toContainText("nothing to measure");
  await expect(page.getByTestId("impact-open-pull-requests")).toContainText("Nothing changes");
  // A dry run stores nothing: the server still has no policy.
  expect((await v1.get(`${P}/check-policy`)).version).toBe(0);
  await expectAccessibleInBothThemes(page, "check policy with an impact preview");

  // ── editing takes the save away again ──
  await page.getByTestId("policy-fail-on").selectOption("warning");
  await expect(page.getByTestId("policy-save")).toHaveCount(0);
  await expect(page.getByTestId("impact-stale")).toContainText("Preview it again before saving");

  // ── preview, then save ──
  await page.getByTestId("policy-grace-days").fill("30");
  await page.getByTestId("policy-preview").click();
  await expect(page.getByTestId("policy-save")).toHaveText("Save version 1");
  await page.getByTestId("policy-save").click();
  await expect(page.getByTestId("policy-status")).toContainText("Version 1 is the policy now.");

  const saved = await v1.get(`${P}/check-policy`);
  expect(saved.version).toBe(1);
  expect(saved.document.fail_on).toBe("warning");
  // Order survives the round trip, because order is part of the meaning.
  expect(saved.document.rules.map((r: { layer: string }) => r.layer)).toEqual(["terminology", "visual"]);
  expect(saved.document.rules[1].mode).toBe("warn");
  // The save pinned the pull requests that predate it, which is what the grace is for.
  expect(saved.grace_until).toBeTruthy();
  expect(saved.pinned_version).toBe(0);

  await expect(page.getByTestId("policy-grace")).toContainText("keep grading against version 0");
  await expect(page.getByTestId("policy-history").getByTestId("policy-version-row")).toHaveCount(1);
  await expectAccessibleInBothThemes(page, "check policy after a save");
});

test("the waiver screen lists, filters and revokes", async ({ page }) => {
  test.setTimeout(180_000);
  await signIn(page, `waivers-${Date.now()}@example.com`);

  const v1 = await api(page);
  const t = v1.tenant;
  const project = await v1.post(`/v1/tenants/${t}/projects`, { name: "Waiver Demo", slug: `waivers-${Date.now()}`, source_locale: "en" });
  const P = `/v1/tenants/${t}/projects/${project.id}`;
  const base = `/t/${t}/p/${project.id}`;

  const KEPT = "Accepted until the checkout redesign lands.";
  const GONE = "Accepted by mistake.";
  await v1.post(`${P}/waivers`, { fingerprint: "f_7c1a3e9b40d2f815", reason: KEPT, scope: "project" });
  await v1.post(`${P}/waivers`, { fingerprint: "f_0b2d4e6a80c1f937", reason: GONE, scope: "branch", ref: "feat/checkout" });

  await page.goto(`${base}/quality`);
  await page.getByRole("navigation", { name: "Quality sections" }).getByRole("link", { name: "Waivers" }).click();
  await expect(page).toHaveURL(new RegExp(`${base}/quality/waivers$`));
  await expect(page.getByRole("heading", { name: "Waivers", level: 1 })).toBeVisible();

  const rows = page.getByTestId("waiver");
  await expect(rows).toHaveCount(2);
  await expect(page.getByTestId("waiver-totals")).toContainText("2 standing");
  // Neither fingerprint is carried by a stored finding, and the screen says so rather than hiding it.
  await expect(page.getByTestId("waiver-unexamined-count")).toContainText("2 accept nothing that is still found");
  await expectAccessibleInBothThemes(page, "waivers");

  // ── a filter narrows the list and leaves the project's totals alone ──
  await page.getByTestId("waiver-fingerprint").fill("7c1a");
  await page.getByTestId("waiver-fingerprint").blur();
  await expect(rows).toHaveCount(1);
  await expect(rows.first()).toContainText(KEPT);
  await expect(page.getByTestId("waiver-matched")).toHaveText("1 waiver matches.");
  await expect(page.getByTestId("waiver-totals")).toContainText("2 standing");
  // The filter is in the URL, so the view is bookmarkable.
  await expect(page).toHaveURL(/fingerprint=7c1a/);
  await page.getByTestId("waiver-clear").click();
  await expect(rows).toHaveCount(2);

  // ── revoking takes nothing away ──
  const target = page.getByTestId("waiver").filter({ hasText: GONE });
  await target.getByRole("button", { name: /Take the waiver back/ }).click();
  await expect(page.getByTestId("waivers-status")).toContainText("is taken back");
  await expect(rows).toHaveCount(2);
  await expect(target).toContainText("Revoked");
  await expect(target).toContainText(GONE);
  await expect(page.getByTestId("waiver-totals")).toContainText("1 standing");
  await expect(page.getByTestId("waiver-totals")).toContainText("1 revoked or expired");

  await page.getByTestId("waiver-state").selectOption("inactive");
  await expect(rows).toHaveCount(1);
  await expect(rows.first()).toContainText(GONE);
  await expectAccessible(page, "waivers filtered to the revoked");
});
