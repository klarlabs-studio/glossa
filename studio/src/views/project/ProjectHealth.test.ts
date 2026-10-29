/**
 * The health surfaces of RFC 0005 §8, through the screen that carries
 * them: the seven-number header, the per-locale row, and — the one that
 * matters most — per-locale layer availability.
 *
 * The assertions below are mostly about what is *not* shown. A health
 * screen is easy to make look right and hard to make honest: the
 * failure mode is a confident zero for a number nobody measured, and a
 * green tick for a check that could never run. Each test here pins one
 * of those apart from its honest twin.
 */
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";
import { checkRun, createFakeQuality, finding } from "../../test/fake-quality";
import {
  cleanLayer,
  createFakeQualitySummary,
  gradedLayer,
  localeHealth,
  qualitySummary,
  unavailableLayer,
  uncheckedLayer,
} from "../../test/fake-quality-summary";
import HealthHeader from "../../components/quality/HealthHeader.vue";
import { locale, mountProjectScreen } from "../../test/project";
import QualityView from "./QualityView.vue";

let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

const LOCALES = [locale("en", true), locale("de"), locale("ja")];

const FULL = qualitySummary({
  computed_at: "2026-09-19T07:59:30Z",
  project: {
    coverage: { messages: 1200, translated: 1100, outdated: 40, missing: 60 },
    findings: { errors: 3, warnings: 12, waived: 5 },
    ai: { decisions: 240, acceptance_rate: 0.68, mean_edit_distance: 12.4 },
    queue: { depth: 14, age: { p50_seconds: 2 * 86_400, p90_seconds: 6 * 86_400, samples: 14 } },
    context: { active_messages: 1200, with_usage: 940, with_region: 610 },
    lead_time: { p50_seconds: 86_400, p90_seconds: 4 * 86_400, samples: 320 },
    checks: { runs: 88, pass_rate: 0.94, median_seconds: 180 },
    run: { id: "run1", ref: "main", policy_version: 3, started_at: "2026-09-19T07:00:00Z" },
  },
  locales: [
    localeHealth("en", {
      is_source: true,
      coverage: { messages: 1200, translated: 1200, outdated: 0, missing: 0 },
      findings: { errors: 0, warnings: 0, waived: 0 },
      layers: [cleanLayer("source")],
    }),
    localeHealth("de", {
      coverage: { messages: 1200, translated: 1180, outdated: 10, missing: 10 },
      findings: { errors: 3, warnings: 4, waived: 5 },
      queue: { depth: 9, age: { p50_seconds: 3 * 86_400, p90_seconds: 11 * 86_400, samples: 9 } },
      layers: [gradedLayer("terminology", 3, 1), cleanLayer("parity"), cleanLayer("length"), uncheckedLayer("visual")],
    }),
    // Japanese: nothing has been checked, and two layers cannot run for it at all.
    localeHealth("ja", {
      coverage: { messages: 1200, translated: 300, outdated: 0, missing: 900 },
      layers: [
        unavailableLayer("linguistic", "unsupported_locale"),
        unavailableLayer("style", "not_configured"),
        unavailableLayer("visual", "no_evidence"),
      ],
    }),
  ],
});

async function screen(health?: typeof FULL) {
  wrapper = await mountProjectScreen(QualityView, {
    quality: createFakeQuality({ runs: [checkRun({ id: "run1" })], findings: { run1: [finding({ layer: "terminology", code: "term_forbidden" })] } }),
    qualitySummary: createFakeQualitySummary(health),
    locales: LOCALES,
    path: "/t/t/p/p/quality",
    ...(health ? { health } : {}),
  });
  await flushPromises();
  return wrapper;
}

const stat = (w: VueWrapper, id: string) => w.get(`[data-testid=${id}]`);
const value = (w: VueWrapper, id: string) => stat(w, id).get("[data-testid=stat-value]").text();
const detail = (w: VueWrapper, id: string) => stat(w, id).find("[data-testid=stat-detail]");
const measured = (w: VueWrapper, id: string) => stat(w, id).attributes("data-measured");
const row = (w: VueWrapper, code: string) => w.get(`[data-testid=locale-row][data-locale=${code}]`);
const chip = (w: VueWrapper, code: string, layer: string) => row(w, code).get(`[data-chip-layer=${layer}]`);

describe("the project health header", () => {
  it("shows the seven numbers RFC 0005 §8 names, and no eighth", async () => {
    const w = await screen(FULL);
    expect(w.findAll("[data-testid=health-header] .stat")).toHaveLength(7);
    expect(value(w, "health-coverage")).toBe("92%");
    expect(detail(w, "health-coverage").text()).toBe("1,100 translated · 40 outdated · 60 missing");
    expect(value(w, "health-findings")).toBe("3 errors, 12 warnings");
    expect(detail(w, "health-findings").text()).toContain("5 waived");
    expect(value(w, "health-ai")).toBe("68%");
    expect(value(w, "health-queue")).toBe("14 waiting");
    expect(detail(w, "health-queue").text()).toBe("Half have waited under 2 days, nine in ten under 6 days.");
    expect(value(w, "health-context")).toBe("78% have a usage");
    expect(value(w, "health-lead-time")).toBe("Half within 1 day");
    expect(value(w, "health-checks")).toBe("94% of checks pass");
    expect(detail(w, "health-checks").text()).toBe("88 runs · median 3 minutes to a conclusion");
  });

  it("says a number is not measured rather than showing it as zero", async () => {
    // A brand-new project: locales exist, nothing else has happened yet.
    const w = await screen(qualitySummary({ locales: [localeHealth("de")] }));
    for (const id of ["health-coverage", "health-findings", "health-ai", "health-queue", "health-context", "health-lead-time", "health-checks"]) {
      expect(value(w, id), id).toBe("Not measured");
      expect(measured(w, id), id).toBe("false");
    }
    // And it says why, where the summary lets it.
    expect(detail(w, "health-findings").text()).toContain("Nothing has been checked yet");
    expect(detail(w, "health-context").text()).toContain("No build has been uploaded");
    expect(w.text()).not.toContain("0 open errors");
  });

  it("does not invent an explanation when it read no summary at all", async () => {
    const w = await screen();
    expect(w.get("[data-testid=health-header]").attributes("data-state")).toBe("unreported");
    expect(w.get("[data-testid=health-unavailable]").text()).toContain("does not report the quality summary");
    expect(value(w, "health-findings")).toBe("Not measured");
    // "Nothing has been checked yet" would be a claim; nothing was read, so nothing is claimed.
    expect(detail(w, "health-findings").exists()).toBe(false);
  });

  it("tells a server that does not report the numbers from one that could not", () => {
    // Both are "not measured"; they are not the same thing to the person reading.
    const unreported = mount(HealthHeader, { props: { summary: undefined, state: "unreported" as const } });
    const failed = mount(HealthHeader, { props: { summary: undefined, state: "failed" as const } });
    expect(unreported.get("[data-testid=health-unavailable]").text()).toContain("does not report the quality summary");
    expect(failed.get("[data-testid=health-unavailable]").text()).toContain("could not be read");
    expect(failed.get("[data-testid=health-unavailable]").text()).toContain("unknown, not zero");
    unreported.unmount();
    failed.unmount();
  });

  it("keeps a measured zero, which is not the same answer", async () => {
    const w = await screen(
      qualitySummary({
        project: { findings: { errors: 0, warnings: 0, waived: 0 }, run: { id: "r", ref: "main", policy_version: 3, started_at: "2026-09-19T07:00:00Z" } },
      }),
    );
    expect(value(w, "health-findings")).toBe("0 errors, 0 warnings");
    expect(measured(w, "health-findings")).toBe("true");
  });
});

describe("the per-locale row", () => {
  it("carries coverage, open errors and queue age for each locale, source first", async () => {
    const w = await screen(FULL);
    expect(w.findAll("[data-testid=locale-row]").map((r) => r.attributes("data-locale"))).toEqual(["en", "de", "ja"]);
    const de = row(w, "de");
    expect(de.get("[data-testid=locale-coverage] [data-testid=stat-value]").text()).toBe("98%");
    expect(de.get("[data-testid=locale-errors] [data-testid=stat-value]").text()).toBe("3 open errors");
    expect(de.get("[data-testid=locale-queue] [data-testid=stat-value]").text()).toBe("Half wait under 3 days");
  });

  it("a locale nothing has checked shows no error count at all, rather than none", async () => {
    const w = await screen(FULL);
    const ja = row(w, "ja");
    expect(ja.get("[data-testid=locale-errors] [data-testid=stat-value]").text()).toBe("Not measured");
    expect(ja.get("[data-testid=locale-errors]").attributes("data-measured")).toBe("false");
    // English *was* checked and is clean. The two read differently, on purpose.
    expect(row(w, "en").get("[data-testid=locale-errors] [data-testid=stat-value]").text()).toBe("No open errors");
    expect(row(w, "en").get("[data-testid=locale-errors]").attributes("data-measured")).toBe("true");
    expect(ja.text()).not.toContain("No open errors");
  });

  it("lists the project's locales with nothing measured when there is no summary", async () => {
    const w = await screen();
    expect(w.findAll("[data-testid=locale-row]").map((r) => r.attributes("data-locale"))).toEqual(["en", "de", "ja"]);
    expect(row(w, "ja").get("[data-testid=locale-coverage] [data-testid=stat-value]").text()).toBe("Not measured");
  });
});

describe("per-locale layer availability (intent §41)", () => {
  it("never lets a layer that cannot run here read like one that ran and found nothing", async () => {
    const w = await screen(FULL);
    const clean = chip(w, "de", "parity");
    const cannotRun = chip(w, "ja", "linguistic");

    expect(clean.attributes("data-state")).toBe("clean");
    expect(clean.text()).toContain("Clean");
    expect(clean.classes()).toContain("pill-ok");

    expect(cannotRun.attributes("data-state")).toBe("unavailable");
    expect(cannotRun.text()).toContain("Not available");
    // Words first, then colour: the two never share a tone, a border or a word.
    expect(cannotRun.text()).not.toContain("Clean");
    expect(cannotRun.classes()).not.toContain("pill-ok");
    expect(cannotRun.classes()).toContain("chip-unavailable");
    expect(clean.classes()).not.toContain("chip-unavailable");
  });

  it("keeps 'could not run', 'did not run' and 'was never reported' apart", async () => {
    const w = await screen(FULL);
    expect(chip(w, "de", "visual").attributes("data-state")).toBe("not-checked");
    expect(chip(w, "de", "visual").text()).toContain("Not checked");
    // The summary said nothing about `source` for German. Nothing is known, and it says so.
    expect(chip(w, "de", "source").attributes("data-state")).toBe("unknown");
    expect(chip(w, "de", "source").text()).toContain("Not reported");
    expect(chip(w, "ja", "style").attributes("data-state")).toBe("unavailable");
  });

  it("spells out, in prose everyone can read, why a layer cannot run here", async () => {
    const w = await screen(FULL);
    const why = row(w, "ja").get("[data-testid=layers-unavailable]").text();
    expect(why).toContain("Linguistic: Not available");
    expect(why).toContain("No rules or data exist for this language.");
    expect(why).toContain("Style: Not available");
    expect(why).toContain("Nothing is set up for it yet.");
    expect(row(w, "ja").get("[data-testid=layers-summary]").text()).toBe(
      "7 of 10 checks can run for this locale. The rest are listed as not available, which is not the same as clean.",
    );
  });

  it("shows what a graded layer found, per locale", async () => {
    const w = await screen(FULL);
    const term = chip(w, "de", "terminology");
    expect(term.attributes("data-state")).toBe("errors");
    expect(term.text()).toContain("3 errors");
    expect(term.classes()).toContain("pill-err");
  });
});
