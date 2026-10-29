import { describe, expect, it } from "vitest";
import type { Finding, FindingLayer } from "../api/quality-schemas";
import { finding, waiver } from "../test/fake-quality";
import { EMPTY_FILTER, cropTarget, findingPlace, groupByLayer, isFiltered, notChecked, toFindingFilter, waiversById } from "./quality";

const layersOf = (gs: { layer: FindingLayer }[]) => gs.map((g) => g.layer);

describe("groupByLayer", () => {
  it("orders the groups the way RFC 0005 §3 lists the layers, not the way the findings arrived", () => {
    const groups = groupByLayer([finding({ layer: "visual", code: "text-clipped" }), finding({ layer: "parity", code: "missing-argument" })]);
    expect(layersOf(groups)).toEqual(["parity", "visual"]);
  });

  it("gives every layer the run computed a group, so an empty one reads as clean and not as unchecked", () => {
    const groups = groupByLayer([finding({ layer: "length", code: "max-length-exceeded" })], ["structure", "length"]);
    expect(layersOf(groups)).toEqual(["structure", "length"]);
    expect(groups[0]?.findings).toEqual([]);
    expect(groups[0]?.checked).toBe(true);
  });

  it("counts a waived finding on its own, never as an error or a warning", () => {
    const [group] = groupByLayer([
      finding({ layer: "terminology", code: "term_forbidden", severity: "error" }),
      finding({ layer: "terminology", code: "term_missing", severity: "warning" }),
      finding({ layer: "terminology", code: "term_missing", severity: "waived", waiver: "w1" }),
    ]);
    expect(group).toMatchObject({ errors: 1, warnings: 1, waived: 1 });
    // And it is still in the group: waiving hides nothing.
    expect(group?.findings).toHaveLength(3);
  });

  it("keeps a layer that found something even when the run never listed it", () => {
    expect(layersOf(groupByLayer([finding({ layer: "source", code: "manual-plural" })], ["structure"]))).toEqual(["structure", "source"]);
  });
});

describe("notChecked", () => {
  it("names the layers a run never computed, because nothing known is not the same as clean", () => {
    expect(notChecked(["structure", "parity", "completeness", "terminology", "style", "length", "locale", "source", "visual"])).toEqual(["linguistic"]);
  });

  it("is every layer when a run computed none", () => {
    expect(notChecked([])).toHaveLength(10);
  });
});

describe("waiversById", () => {
  it("indexes waivers so a waived finding can show the reason it was accepted for", () => {
    const w = waiver({ id: "w9", fingerprint: "f_0000000000000001", reason: "“Login” is the German term." });
    expect(waiversById([w]).get("w9")?.reason).toBe("“Login” is the German term.");
  });
});

describe("findingPlace", () => {
  it("is file:line the way a stack trace names a place", () => {
    expect(findingPlace({ file: "src/checkout/PaymentFooter.vue", line: 42 })).toBe("src/checkout/PaymentFooter.vue:42");
  });

  it("is the file alone without a line, and nothing without a file", () => {
    expect(findingPlace({ file: "src/App.vue" })).toBe("src/App.vue");
    expect(findingPlace({ line: 42 })).toBeUndefined();
  });
});

describe("cropTarget", () => {
  const visual = (locus: Finding["locus"]) => finding({ layer: "visual", code: "text-clipped", locus });

  it("is the capture and the key a crop needs", () => {
    expect(cropTarget(visual({ capture: "c1", region: "r_18", key: "checkout.pay" }))).toEqual({ capture: "c1", key: "checkout.pay" });
  });

  it("is nothing without a capture, and nothing without the key the capture is read by", () => {
    expect(cropTarget(visual({ key: "checkout.pay" }))).toBeUndefined();
    expect(cropTarget(visual({ capture: "c1", region: "r_18" }))).toBeUndefined();
  });
});

describe("the filter the URL carries", () => {
  it("is not a filter when nothing is picked; the run alone only says which run", () => {
    expect(isFiltered(EMPTY_FILTER)).toBe(false);
    expect(isFiltered({ ...EMPTY_FILTER, run: "run1" })).toBe(false);
    expect(isFiltered({ ...EMPTY_FILTER, severity: "waived" })).toBe(true);
  });

  it("drops an unknown layer or severity rather than sending it, so a stale bookmark still shows findings", () => {
    expect(toFindingFilter({ ...EMPTY_FILTER, layer: "nonsense", severity: "critical" })).toEqual({
      run: undefined,
      layer: undefined,
      severity: undefined,
      locale: undefined,
      code: undefined,
    });
  });

  it("passes the layer, the severity, the locale and the rule through", () => {
    expect(toFindingFilter({ run: "run1", layer: "visual", severity: "waived", locale: "fr", code: " text-clipped " })).toEqual({
      run: "run1",
      layer: "visual",
      severity: "waived",
      locale: "fr",
      code: "text-clipped",
    });
  });
});
