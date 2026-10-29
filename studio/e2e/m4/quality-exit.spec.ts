/**
 * RFC 0005 §12.8 — the M4 exit test's dashboard criterion: a Playwright
 * test opens Studio's `quality` view against the same server and
 * asserts the seven numbers match the API.
 *
 * "The same server" is the point. This spec does not provision a stack;
 * `platform/internal/systemtest/m4` hands it a running glossa-server
 * (GLOSSA_E2E_API_URL), that server's log to read the sign-in link out
 * of (GLOSSA_E2E_SERVER_LOG), and the tenant and project it seeded
 * (GLOSSA_M4_TENANT, GLOSSA_M4_PROJECT, GLOSSA_M4_EMAIL). Without those
 * the spec is not collected at all (playwright.config.ts's testIgnore).
 *
 * The expected renderings below are computed here, from the API's own
 * JSON, with plain `Intl` — not by importing Studio's formatters. The
 * point of the criterion is that the screen and the API agree, and a
 * comparison that ran the screen's own code on the screen's own numbers
 * would agree with itself.
 */
import { expect, test } from "@playwright/test";
import { api, signIn } from "../support";

const tenant = process.env.GLOSSA_M4_TENANT!;
const project = process.env.GLOSSA_M4_PROJECT!;
const email = process.env.GLOSSA_M4_EMAIL ?? "lena@brotwerk.example";

/** A percentage the way CLDR writes it, which is what the view renders. */
const percent = (v: number): string => new Intl.NumberFormat(undefined, { style: "percent", maximumFractionDigits: 0 }).format(v);

const UNITS = [
  { unit: "day", seconds: 86_400 },
  { unit: "hour", seconds: 3_600 },
  { unit: "minute", seconds: 60 },
  { unit: "second", seconds: 1 },
] as const;

/** A span of seconds in the largest unit that leaves a number worth reading. */
const duration = (seconds: number): string => {
  const u = UNITS.find((x) => seconds >= x.seconds) ?? UNITS[UNITS.length - 1]!;
  const n = seconds / u.seconds;
  return new Intl.NumberFormat(undefined, { style: "unit", unit: u.unit, unitDisplay: "long", maximumFractionDigits: n < 10 ? 1 : 0 }).format(n);
};

const count = (n: number, one: string, many: string): string => (n === 1 ? `1 ${one}` : `${n.toLocaleString()} ${many}`);

/** What the header must render for one number, given what the API said. Undefined means "Not measured". */
function expected(summary: any): Record<string, string | undefined> {
  const h = summary.project;
  const c = h.coverage;
  const f = h.findings;
  const ai = h.ai;
  const q = h.queue;
  const ctx = h.context;
  const lt = h.lead_time;
  const ck = h.checks;
  const pct = (part?: number, whole?: number) => (part === undefined || !whole ? undefined : percent(part / whole));
  return {
    "health-coverage": pct(c?.translated, c?.messages),
    "health-findings": f ? `${count(f.errors, "error", "errors")}, ${count(f.warnings, "warning", "warnings")}` : undefined,
    "health-ai": !ai || ai.decisions === 0 ? undefined : percent(ai.acceptance_rate),
    "health-queue": !q ? undefined : q.depth === 0 ? "Nothing waiting" : count(q.depth, "waiting", "waiting"),
    "health-context": pct(ctx?.with_usage, ctx?.active_messages) === undefined ? undefined : `${pct(ctx.with_usage, ctx.active_messages)} have a usage`,
    "health-lead-time": !lt || lt.samples === 0 ? undefined : `Half within ${duration(lt.p50_seconds)}`,
    "health-checks": !ck || ck.runs === 0 || ck.pass_rate === undefined ? undefined : `${percent(ck.pass_rate)} of checks pass`,
  };
}

test("Studio's quality view shows the same seven numbers the API reports", async ({ page }) => {
  await signIn(page, email);
  const client = await api(page);

  const base = `/v1/tenants/${tenant}/projects/${project}`;
  const summary = await client.get(`${base}/quality-summary`);
  expect(summary.schema, "the summary document").toContain("quality-summary");

  await page.goto(`/t/${tenant}/p/${project}/quality`);
  const header = page.getByTestId("health-header");
  await expect(header).toBeVisible();
  await expect.poll(async () => header.getAttribute("data-state")).not.toBe("loading");

  // Seven, and no more (RFC 0005 §8: "seven numbers, and no more").
  await expect(header.locator(".stat")).toHaveCount(7);

  const want = expected(summary);
  const unmeasured: string[] = (summary.unmeasured ?? []).map((u: { number: string }) => u.number);
  for (const [testid, value] of Object.entries(want)) {
    const stat = header.getByTestId(testid);
    await expect(stat, `${testid} is on the screen`).toBeVisible();
    const rendered = (await stat.getByTestId("stat-value").innerText()).trim();
    if (value === undefined) {
      expect(rendered, `${testid}: the API did not measure it, so the screen may not invent a number`).toBe("Not measured");
      expect(await stat.getAttribute("data-measured")).toBe("false");
      continue;
    }
    expect(rendered, `${testid}: the screen and the API disagree`).toBe(value);
    expect(await stat.getAttribute("data-measured")).toBe("true");
  }

  // The number the navigation carries is the same errors the API
  // reported, not a second count of the findings list.
  const findings = summary.project.findings;
  if (findings) {
    const detail = (await header.getByTestId("health-findings").getByTestId("stat-detail").innerText()).trim();
    expect(detail, "the waived count is carried on its own").toContain(count(findings.waived, "waived", "waived"));
  }

  // A number the API left out is named as unmeasured on the screen too.
  for (const number of unmeasured) {
    const testid = `health-${number.replace(/_/g, "-")}`;
    if (!(testid in want)) continue;
    await expect(header.getByTestId(testid)).toHaveAttribute("data-measured", "false");
  }
});
