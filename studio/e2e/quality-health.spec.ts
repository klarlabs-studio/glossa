/**
 * The health surfaces end to end (RFC 0005 §8): the project health
 * header, the navigation's one number, the per-locale row on the
 * projects list, and per-locale layer availability (intent §41).
 *
 * Two halves, on purpose:
 *
 * 1. **Against the real server, on a project nobody has measured.** The
 *    summary endpoint answers, and answers honestly: a brand-new project
 *    has no messages, no check run, no build and no published
 *    translation, so six of the seven numbers read "Not measured" with
 *    the reason each is missing, and the seventh — an empty review queue
 *    — reads as the measured fact it is. This is the case that ships
 *    first for every project, and the one where showing a reassuring
 *    zero would be a lie.
 *
 * 2. **With the summary served by the browser.** Nothing can create the
 *    numbers through /v1 yet, so the measured rendering is exercised by
 *    fulfilling that one request in the page — the same technique as
 *    e2e/fake-github.ts, one layer up. It is what lets axe see the
 *    rendered chips, the tones and the tables at all. Replace it with
 *    real data once the summary API lands.
 */
import { expect, test, type Page } from "@playwright/test";
import { api, expectAccessible, expectAccessibleInBothThemes, signIn } from "./support";

const SUMMARY = "**/v1/tenants/*/projects/*/quality-summary*";

const DAY = 86_400;

/** The shape the summary slice will send (RFC 0005 §8): the seven numbers, per project and per locale. */
const summary = {
  schema: "glossa.quality-summary/v1",
  // The document's own fields, which the contract requires and the
  // server always sends. A fixture that omits them is not the response
  // the UI will actually be given.
  project_id: "prj_health",
  environment: "production",
  since: new Date(Date.now() - 30 * DAY * 1000).toISOString(),
  computed_at: new Date().toISOString(),
  expires_at: new Date(Date.now() + 60_000).toISOString(),
  cached: false,
  unmeasured: [],
  project: {
    coverage: { messages: 1200, translated: 1100, outdated: 40, missing: 60 },
    findings: { errors: 3, warnings: 12, waived: 5 },
    ai: { decisions: 240, acceptance_rate: 0.68, mean_edit_distance: 12.4 },
    queue: { depth: 14, age: { p50_seconds: 2 * DAY, p90_seconds: 6 * DAY, samples: 14 } },
    context: { active_messages: 1200, with_usage: 940, with_region: 610 },
    lead_time: { p50_seconds: DAY, p90_seconds: 4 * DAY, samples: 320 },
    checks: { runs: 88, pass_rate: 0.94, median_seconds: 180 },
    run: { id: "run1", ref: "main", policy_version: 3, started_at: new Date().toISOString() },
  },
  locales: [
    {
      code: "en",
      direction: "ltr",
      is_source: true,
      coverage: { messages: 1200, translated: 1200, outdated: 0, missing: 0 },
      findings: { errors: 0, warnings: 0, waived: 0 },
      queue: { depth: 0 },
      layers: [{ layer: "source", available: true, checked: true, findings: { errors: 0, warnings: 0, waived: 0 } }],
    },
    {
      code: "de",
      direction: "ltr",
      is_source: false,
      coverage: { messages: 1200, translated: 1180, outdated: 10, missing: 10 },
      findings: { errors: 3, warnings: 4, waived: 5 },
      queue: { depth: 9, age: { p50_seconds: 3 * DAY, p90_seconds: 11 * DAY, samples: 9 } },
      layers: [
        { layer: "parity", available: true, checked: true, findings: { errors: 0, warnings: 0, waived: 0 } },
        { layer: "terminology", available: true, checked: true, findings: { errors: 3, warnings: 1, waived: 5 } },
        { layer: "visual", available: true, checked: false },
      ],
    },
    {
      // Japanese: coverage is known, nothing has been checked, and three layers cannot run for it at all.
      code: "ja",
      direction: "ltr",
      is_source: false,
      coverage: { messages: 1200, translated: 300, outdated: 0, missing: 900 },
      layers: [
        { layer: "linguistic", available: false, unavailable: "unsupported_locale", checked: false },
        { layer: "style", available: false, unavailable: "not_configured", checked: false },
        { layer: "visual", available: false, unavailable: "no_evidence", checked: false },
      ],
    },
  ],
};

async function serveSummary(page: Page): Promise<void> {
  await page.route(SUMMARY, (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(summary) }));
}

test("project health: seven numbers, one badge, and a check that cannot run never reads as one that passed", async ({ page }) => {
  test.setTimeout(180_000);
  await signIn(page, `health-${Date.now()}@example.com`);

  const v1 = await api(page);
  const t = v1.tenant;
  const project = await v1.post(`/v1/tenants/${t}/projects`, { name: "Health Demo", slug: `health-${Date.now()}`, source_locale: "en" });
  const P = `/v1/tenants/${t}/projects/${project.id}`;
  await v1.post(`${P}/locales`, { code: "de" });
  await v1.post(`${P}/locales`, { code: "ja" });
  const base = `/t/${t}/p/${project.id}`;

  // ── 1. a project nobody has measured: nothing is claimed ──
  await page.goto(`${base}/quality`);
  await expect(page.getByTestId("health-header")).toHaveAttribute("data-state", "ready");

  // Six numbers have nothing behind them, and each says why rather than
  // showing a zero. The reasons are the point: "nothing has been checked"
  // and "no build has been uploaded" are different facts, and a person
  // deciding what to do next needs to know which.
  for (const [id, why] of [
    ["health-coverage", "no active messages to cover"],
    ["health-findings", "Nothing has been checked yet"],
    ["health-ai", "No AI suggestion has been accepted"],
    ["health-context", "No build has been uploaded"],
    ["health-lead-time", "Nothing has gone from a source change to a published translation"],
    ["health-checks", "No check has run against a pull request"],
  ] as const) {
    await expect(page.getByTestId(id).getByTestId("stat-value"), id).toHaveText("Not measured");
    await expect(page.getByTestId(id), id).toContainText(why);
  }
  // The seventh is measured: an empty queue is a fact, not an absence,
  // and it must not read as "Not measured".
  await expect(page.getByTestId("health-queue").getByTestId("stat-value")).toHaveText("Nothing waiting");
  await expect(page.getByTestId("health-queue")).not.toContainText("Not measured");
  // No badge at all, rather than a reassuring zero on a project nobody has measured.
  await expect(page.getByTestId("nav-open-errors")).toHaveCount(0);
  // The locales are still listed, each saying that nothing about it is known.
  await expect(page.getByTestId("locale-row")).toHaveCount(3);
  await expect(page.locator("[data-testid=locale-row][data-locale=ja] [data-testid=locale-coverage] [data-testid=stat-value]")).toHaveText("Not measured");
  // The layer is named and not graded. Which of the not-green states it
  // is matters less than the invariant: it must never read as "Clean",
  // because nothing has checked this locale.
  const jaLinguistic = page.locator("[data-testid=locale-row][data-locale=ja] [data-chip-layer=linguistic]");
  await expect(jaLinguistic).toContainText("Not checked");
  await expect(jaLinguistic).not.toContainText("Clean");
  await expectAccessibleInBothThemes(page, "project health with nothing measured");

  // ── 2. with the numbers: seven, and exactly one in the navigation ──
  await serveSummary(page);
  await page.reload();
  await expect(page.getByTestId("health-header")).toHaveAttribute("data-state", "ready");
  await expect(page.getByTestId("health-coverage").getByTestId("stat-value")).toHaveText("92%");
  await expect(page.getByTestId("health-findings").getByTestId("stat-value")).toHaveText("3 errors, 12 warnings");
  await expect(page.getByTestId("health-ai").getByTestId("stat-value")).toHaveText("68%");
  await expect(page.getByTestId("health-queue").getByTestId("stat-value")).toHaveText("14 waiting");
  await expect(page.getByTestId("health-context").getByTestId("stat-value")).toHaveText("78% have a usage");
  await expect(page.getByTestId("health-lead-time").getByTestId("stat-value")).toHaveText("Half within 1 day");
  await expect(page.getByTestId("health-checks").getByTestId("stat-value")).toHaveText("94% of checks pass");
  await expect(page.locator("[data-testid=health-header] .stat")).toHaveCount(7);

  // One number in the navigation, and it reads as a sentence to a screen reader.
  await expect(page.getByTestId("nav-open-errors")).toHaveCount(1);
  await expect(page.getByRole("navigation", { name: "Project sections" }).getByRole("link", { name: /Quality.*3 open errors/ })).toBeVisible();

  // ── 3. availability: "cannot run here" is not "ran and found nothing" ──
  const de = page.locator("[data-testid=locale-row][data-locale=de]");
  const ja = page.locator("[data-testid=locale-row][data-locale=ja]");
  await expect(de.locator("[data-chip-layer=parity]")).toContainText("Clean");
  await expect(de.locator("[data-chip-layer=terminology]")).toContainText("3 errors");
  await expect(de.locator("[data-chip-layer=visual]")).toContainText("Not checked");
  await expect(ja.locator("[data-chip-layer=linguistic]")).toContainText("Not available");
  await expect(ja.locator("[data-chip-layer=linguistic]")).not.toContainText("Clean");
  await expect(ja.getByTestId("layers-summary")).toContainText("7 of 10 checks can run for this locale");
  await expect(ja.getByTestId("layers-unavailable")).toContainText("No rules or data exist for this language.");
  // Japanese has never been checked, so it shows no error count at all — not a zero.
  await expect(ja.locator("[data-testid=locale-errors] [data-testid=stat-value]")).toHaveText("Not measured");
  await expect(page.locator("[data-testid=locale-row][data-locale=en] [data-testid=locale-errors] [data-testid=stat-value]")).toHaveText("No open errors");
  await expectAccessibleInBothThemes(page, "project health measured");

  // ── 4. the projects list's per-locale row ─────────────────────────
  await page.goto(`/t/${t}`);
  const rows = page.locator(`[data-testid=project-locales-${project.id}]`);
  await expect(rows.locator("[data-locale=de] [data-stat=coverage]")).toHaveText("98%");
  await expect(rows.locator("[data-locale=de] [data-stat=errors]")).toHaveText("3 open errors");
  await expect(rows.locator("[data-locale=de] [data-stat=queue]")).toHaveText("Half wait under 3 days");
  await expect(rows.locator("[data-locale=ja] [data-stat=errors]")).toHaveText("Not measured");
  await expectAccessibleInBothThemes(page, "projects list with per-locale health");

  // ── 5. and when one project's numbers fail, the list still works ──
  await page.unroute(SUMMARY);
  await page.route(SUMMARY, (route) => route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ code: "internal" }) }));
  await page.reload();
  await expect(page.getByTestId(`project-locales-${project.id}`)).toHaveAttribute("data-state", "unavailable");
  await expect(page.getByRole("heading", { name: "Projects" })).toBeVisible();
  await expectAccessible(page, "projects list with health unavailable");
});
