/**
 * The projects list's per-locale quality row (RFC 0005 §8): coverage,
 * open errors and queue age per language, on the screen people scan
 * rather than read — which is exactly where a confident zero for an
 * unmeasured number would do the most damage.
 */
import { flushPromises, type VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";
import { ApiError } from "../api/errors";
import type { QualitySummaryPort } from "../api/quality-summary";
import type { QualitySummary } from "../api/quality-summary-schemas";
import type { ProjectRef } from "../api/releases";
import { localeHealth, qualitySummary } from "../test/fake-quality-summary";
import { mountTenantScreen } from "../test/project";
import ProjectsView from "./ProjectsView.vue";

let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

const NOW = "2026-09-19T08:00:00Z";
const project = (id: string, name: string) => ({
  id,
  slug: name.toLowerCase(),
  name,
  source_locale: "en",
  settings: { default_syntax: "mf1", review_required: true },
  created_at: NOW,
  updated_at: NOW,
});

const MEASURED = qualitySummary({
  locales: [
    localeHealth("en", {
      is_source: true,
      coverage: { messages: 100, translated: 100, outdated: 0, missing: 0 },
      findings: { errors: 0, warnings: 0, waived: 0 },
      queue: { depth: 0 },
    }),
    localeHealth("de", {
      coverage: { messages: 100, translated: 80, outdated: 5, missing: 15 },
      findings: { errors: 2, warnings: 1, waived: 0 },
      queue: { depth: 4, age: { p50_seconds: 2 * 86_400, p90_seconds: 5 * 86_400, samples: 4 } },
    }),
    // Nothing has been checked for Japanese, and no queue was measured.
    localeHealth("ja", { coverage: { messages: 100, translated: 10, outdated: 0, missing: 90 } }),
  ],
});

/** Answers per project id, so one project's summary can fail while another's arrives. */
function port(by: Record<string, QualitySummary | ApiError>): QualitySummaryPort {
  return {
    async summary(p: ProjectRef) {
      const answer = by[p.project];
      if (!answer || answer instanceof ApiError) throw answer ?? new ApiError(404, "not_found", "No such operation.");
      return answer;
    },
  };
}

async function screen(summaries: Record<string, QualitySummary | ApiError>, projects = [project("p1", "Alpha")]) {
  wrapper = await mountTenantScreen(ProjectsView, {
    path: "/t/t",
    responses: { "/v1/tenants/t/projects": { items: projects } },
    qualitySummary: port(summaries),
  });
  await flushPromises();
  await flushPromises();
  return wrapper;
}

const cell = (w: VueWrapper, project: string, locale: string, stat: string) =>
  w.get(`[data-testid=project-locales-${project}] [data-locale=${locale}] [data-stat=${stat}]`).text();

describe("ProjectsView per-locale quality row", () => {
  it("shows coverage, open errors and queue age for each locale", async () => {
    const w = await screen({ p1: MEASURED });
    expect(w.findAll("[data-testid=project-locale-row]").map((r) => r.attributes("data-locale"))).toEqual(["en", "de", "ja"]);
    expect(cell(w, "p1", "de", "coverage")).toBe("80%");
    expect(cell(w, "p1", "de", "errors")).toBe("2 open errors");
    expect(cell(w, "p1", "de", "queue")).toBe("Half wait under 2 days");
    expect(cell(w, "p1", "en", "errors")).toBe("No open errors");
    expect(cell(w, "p1", "en", "queue")).toBe("Nothing waiting");
  });

  it("says a locale nobody checked is not measured, rather than showing it as clean", async () => {
    const w = await screen({ p1: MEASURED });
    expect(cell(w, "p1", "ja", "errors")).toBe("Not measured");
    expect(cell(w, "p1", "ja", "queue")).toBe("Not measured");
    // Coverage *was* measured for Japanese, and low coverage is a real number, not an absence.
    expect(cell(w, "p1", "ja", "coverage")).toBe("10%");
  });

  it("lets one project's numbers fail without touching another's", async () => {
    const w = await screen(
      { p1: MEASURED, p2: new ApiError(500, "internal", "boom") },
      [project("p1", "Alpha"), project("p2", "Beta")],
    );
    expect(cell(w, "p1", "de", "coverage")).toBe("80%");
    expect(w.get("[data-testid=project-locales-p2]").attributes("data-state")).toBe("unavailable");
    expect(w.get("[data-testid=project-locales-p2]").text()).toBe("Not measured");
    // The list itself is unharmed: both projects are still there and still reachable.
    expect(w.findAll("[data-testid=project-card]")).toHaveLength(2);
    expect(w.findAll("a.p-name").map((a) => a.text())).toEqual(["Alpha", "Beta"]);
  });
});
