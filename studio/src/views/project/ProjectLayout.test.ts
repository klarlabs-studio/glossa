/**
 * The one number the project navigation carries (RFC 0005 §8): open
 * errors, and nothing else.
 *
 * The rule the tests below pin: **no badge means nobody measured.** A
 * project with a clean run shows "0", so the absence of a badge can only
 * ever mean "not checked" or "not readable" — never "you are fine". A
 * navigation that is silent in both cases is the dashboard RFC 0005 §8
 * warns about: one nobody can trust, which is worse than none.
 */
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it, vi } from "vitest";
import { defineComponent } from "vue";
import { createMemoryHistory, createRouter } from "vue-router";
import { ApiError } from "../../api/errors";
import { QUALITY_SUMMARY, type QualitySummaryPort } from "../../api/quality-summary";
import type { QualitySummary } from "../../api/quality-summary-schemas";
import { refreshSession } from "../../session/session";
import { qualitySummary } from "../../test/fake-quality-summary";
import ProjectLayout from "./ProjectLayout.vue";

let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

const NOW = "2026-09-19T08:00:00Z";
const Empty = defineComponent({ template: "<div />" });
const ME = {
  person: { id: "me", email: "me@example.com", email_verified: true, totp_enabled: false, individual_tenant_id: "t", created_at: NOW },
  csrf_token: "csrf",
  memberships: [],
};
const PROJECT = {
  id: "p",
  slug: "demo",
  name: "Demo",
  source_locale: "en",
  settings: { default_syntax: "mf1", review_required: true },
  created_at: NOW,
  updated_at: NOW,
};

const port = (answer: QualitySummary | ApiError): QualitySummaryPort => ({
  async summary() {
    if (answer instanceof ApiError) throw answer;
    return answer;
  },
});

/** The membership the session holds in tenant `t`; none: the person is no member there. */
const member = (roles: string[]) => ({
  member_id: "m",
  tenant: { id: "t", kind: "organization", slug: "acme", name: "Acme", created_at: NOW },
  roles,
  locales: [],
});

async function layout(answer: QualitySummary | ApiError, roles?: string[]) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      const body =
        path === "/v1/me" ? { ...ME, memberships: roles ? [member(roles)] : [] } : path === "/v1/tenants/t/projects/p" ? PROJECT : { items: [{ code: "en", direction: "ltr", is_source: true, created_at: NOW }] };
      return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
    }),
  );
  await refreshSession();
  const router = createRouter({
    history: createMemoryHistory(),
    routes: ["translate", "review", "terms", "style", "locales", "quality", "releases", "project-workflow", "files", "ai", "settings"]
      .map((p) => ({ path: `/t/:tenant/p/:project/${p}`, name: p, component: Empty }))
      .concat([{ path: "/t/:tenant", name: "projects", component: Empty }]),
  });
  await router.push("/t/t/p/p/quality");
  wrapper = mount(ProjectLayout, { attachTo: document.body, global: { plugins: [router], provide: { [QUALITY_SUMMARY as symbol]: port(answer) } } });
  await flushPromises();
  await flushPromises();
  return wrapper;
}

const badge = (w: VueWrapper) => w.find("[data-testid=nav-open-errors]");
const quality = (w: VueWrapper) => w.findAll("a.tab").find((a) => a.text().startsWith("Quality"));

describe("the project navigation's open-errors badge", () => {
  it("carries the count, with the word a screen reader needs", async () => {
    const w = await layout(qualitySummary({ project: { findings: { errors: 3, warnings: 12, waived: 5 } } }));
    expect(badge(w).text()).toContain("3 open errors");
    expect(badge(w).classes()).toContain("pill-err");
    // The whole tab, as a link, reads "Quality 3 open errors".
    expect(quality(w)?.text()).toContain("3 open errors");
  });

  it("shows a measured zero, so a missing badge can only mean 'not measured'", async () => {
    const w = await layout(qualitySummary({ project: { findings: { errors: 0, warnings: 0, waived: 0 } } }));
    expect(badge(w).exists()).toBe(true);
    expect(badge(w).text()).toContain("0 open errors");
    expect(badge(w).classes()).toContain("pill-ok");
  });

  it("carries no badge when nothing has been checked", async () => {
    const w = await layout(qualitySummary({ project: {} }));
    expect(badge(w).exists()).toBe(false);
    expect(quality(w)?.text().trim()).toBe("Quality");
  });

  it("carries no badge, and no broken project, when the summary cannot be read", async () => {
    const w = await layout(new ApiError(404, "not_found", "No such operation."));
    expect(badge(w).exists()).toBe(false);
    // The project itself is unharmed: the summary is a side dish, never the meal.
    expect(w.find(".crumbs .current").text()).toBe("Demo");
    expect(w.findAll("a.tab")).toHaveLength(10);
  });

  it("shows exactly one number, on exactly one tab", async () => {
    const w = await layout(qualitySummary({ project: { findings: { errors: 3, warnings: 12, waived: 5 } } }));
    expect(w.findAll("[data-testid=nav-open-errors]")).toHaveLength(1);
    // Not warnings, not waived, not coverage. §8 is explicit: one number.
    expect(w.get(".tabs").text()).not.toContain("12");
  });
});

describe("the project navigation's permission-guarded tabs", () => {
  const tabNames = (w: VueWrapper) => w.findAll("a.tab").map((a) => a.text().replace(/\s+\d+ open errors$/, "").trim());
  const summary = qualitySummary({ project: {} });

  it("offers Workflow to whoever may read workflows, as the API asks", async () => {
    const w = await layout(summary, ["developer"]);
    expect(tabNames(w)).toContain("Workflow");
    expect(w.findAll("a.tab")).toHaveLength(11);
  });

  it("offers it to a translator too: reading workflows is part of every role", async () => {
    const w = await layout(summary, ["translator"]);
    expect(tabNames(w)).toContain("Workflow");
  });

  it("leaves it out for a person with no role here, rather than offering a tab that answers 403", async () => {
    const w = await layout(summary);
    expect(tabNames(w)).not.toContain("Workflow");
    expect(w.findAll("a.tab")).toHaveLength(10);
    // The tabs that need no permission are still all there, in order.
    expect(tabNames(w)).toEqual(["Translate", "Review", "Termbase", "Style", "Locales", "Quality", "Releases", "Import & export", "AI", "Settings"]);
  });
});
