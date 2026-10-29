import { describe, expect, it } from "vitest";
import type { LocaleHealth, SummaryLayer } from "../api/quality-summary-schemas";
import {
  availableLayers,
  coverageShare,
  duration,
  healthLocales,
  layerState,
  localeLayers,
  openErrors,
  percent,
  percentiles,
  share,
  unmeasured,
} from "./health";
import { LAYERS } from "./quality";

const layer = (over: Partial<SummaryLayer> & Pick<SummaryLayer, "layer">): SummaryLayer => ({
  available: true,
  checked: true,
  ...over,
});

const localeHealth = (over: Partial<LocaleHealth> = {}): LocaleHealth => ({
  code: "de",
  direction: "ltr",
  is_source: false,
  layers: [],
  ...over,
});

describe("layerState", () => {
  it("answers 'unavailable' before it answers anything else", () => {
    // A layer that cannot run here is never graded, even if stale counts came with it.
    const l = layer({ layer: "linguistic", available: false, unavailable: "unsupported_locale", checked: true, findings: { errors: 0, warnings: 0, waived: 0 } });
    expect(layerState(l)).toBe("unavailable");
  });

  it("separates a layer that ran and found nothing from one that never ran", () => {
    expect(layerState(layer({ layer: "style", checked: true, findings: { errors: 0, warnings: 0, waived: 0 } }))).toBe("clean");
    expect(layerState(layer({ layer: "style", checked: false }))).toBe("not-checked");
    // Checked but with no counts is still not a clean bill: nothing was reported.
    expect(layerState(layer({ layer: "style", checked: true }))).toBe("not-checked");
  });

  it("grades a layer by the worst thing it found", () => {
    expect(layerState(layer({ layer: "terminology", findings: { errors: 2, warnings: 9, waived: 1 } }))).toBe("errors");
    expect(layerState(layer({ layer: "terminology", findings: { errors: 0, warnings: 9, waived: 1 } }))).toBe("warnings");
    // Waived findings never make a layer look bad: they are counted on their own (RFC 0005 §2.3).
    expect(layerState(layer({ layer: "terminology", findings: { errors: 0, warnings: 0, waived: 4 } }))).toBe("clean");
  });
});

describe("localeLayers", () => {
  it("lists every layer the RFC defines, in its order", () => {
    const got = localeLayers(localeHealth({ layers: [layer({ layer: "visual", findings: { errors: 1, warnings: 0, waived: 0 } })] }));
    expect(got.map((l) => l.layer)).toEqual([...LAYERS]);
  });

  it("calls a layer the summary never mentioned 'unknown', not clean", () => {
    const got = localeLayers(localeHealth({ layers: [layer({ layer: "structure", findings: { errors: 0, warnings: 0, waived: 0 } })] }));
    expect(got.find((l) => l.layer === "structure")?.state).toBe("clean");
    // Everything else was not reported. Guessing "available and clean" here is the exact bug this slice prevents.
    expect(got.filter((l) => l.layer !== "structure").every((l) => l.state === "unknown")).toBe(true);
  });

  it("counts how many layers can run at all", () => {
    const got = localeLayers(
      localeHealth({
        layers: [
          layer({ layer: "linguistic", available: false, unavailable: "unsupported_locale", checked: false }),
          layer({ layer: "style", available: false, unavailable: "not_configured", checked: false }),
        ],
      }),
    );
    expect(availableLayers(got)).toEqual({ available: LAYERS.length - 2, total: LAYERS.length });
  });
});

describe("numbers that were never measured", () => {
  it("never turns nothing into a zero", () => {
    expect(coverageShare(undefined)).toBeUndefined();
    expect(coverageShare({ messages: 0, translated: 0, outdated: 0, missing: 0 })).toBeUndefined();
    expect(share(0, 0)).toBeUndefined();
    expect(share(undefined, 10)).toBeUndefined();
    expect(percent(undefined)).toBeUndefined();
    expect(openErrors(undefined)).toBeUndefined();
    // A percentile pair over an empty sample is not a pair of zeroes.
    expect(percentiles({ p50_seconds: 0, p90_seconds: 0, samples: 0 })).toBeUndefined();
  });

  it("keeps a measured zero, which is a different answer", () => {
    expect(coverageShare({ messages: 10, translated: 0, outdated: 0, missing: 10 })).toBe(0);
    expect(percent(0)).toBe("0%");
    expect(openErrors({ errors: 0 })).toBe(0);
  });
});

describe("duration", () => {
  it("picks the largest unit that still reads as a number", () => {
    expect(duration(45)).toBe("45 seconds");
    expect(duration(90)).toBe("1.5 minutes");
    expect(duration(7_200)).toBe("2 hours");
    expect(duration(2 * 86_400)).toBe("2 days");
    expect(duration(30 * 86_400)).toBe("30 days");
  });
});

describe("healthLocales and unmeasured", () => {
  it("reads the source locale first, then the rest by code", () => {
    const got = healthLocales([localeHealth({ code: "fr" }), localeHealth({ code: "en", is_source: true }), localeHealth({ code: "de" })]);
    expect(got.map((l) => l.code)).toEqual(["en", "de", "fr"]);
  });

  it("turns a project's locales into rows with nothing measured", () => {
    const got = unmeasured([{ code: "ja", direction: "ltr", is_source: false }]);
    expect(got).toEqual([{ code: "ja", direction: "ltr", is_source: false, layers: [] }]);
    // Every layer of such a row is unknown: a server that told us nothing told us nothing about Japanese either.
    expect(localeLayers(got[0]!).every((l) => l.state === "unknown")).toBe(true);
  });
});
