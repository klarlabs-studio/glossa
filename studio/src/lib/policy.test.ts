import { describe, expect, it } from "vitest";
import type { PolicyDocument } from "../api/policy-schemas";
import {
  canonical,
  emptyRule,
  moveRule,
  namedFields,
  overlap,
  problems,
  ruleOrder,
  sameSelector,
  specificity,
  toDocument,
  toDraft,
  toRule,
  type DraftRule,
} from "./policy";

const rule = (over: Partial<DraftRule> = {}): DraftRule => ({ ...emptyRule(), ...over });

describe("specificity", () => {
  it("counts the fields the selector names, which is the first half of precedence", () => {
    expect(specificity(rule())).toBe(0);
    expect(specificity(rule({ layer: "terminology" }))).toBe(1);
    expect(specificity(rule({ layer: "terminology", namespace: "legal", locale: "de" }))).toBe(3);
  });

  it("ignores a field that holds only whitespace: the wire never carries it either", () => {
    expect(specificity(rule({ code: "   " }))).toBe(0);
    expect(toRule(rule({ code: "  " })).code).toBeUndefined();
  });
});

describe("overlap", () => {
  it("is false only when both rules name one field differently", () => {
    expect(overlap(rule({ layer: "length" }), rule({ layer: "terminology" }))).toBe(false);
    expect(overlap(rule({ layer: "length" }), rule({ locale: "ja" }))).toBe(true);
    expect(overlap(rule({ layer: "length" }), rule({ layer: "length", locale: "ja" }))).toBe(true);
    expect(overlap(rule(), rule({ namespace: "legal" }))).toBe(true);
  });

  it("separates a selector that names the same fields with the same values from one that does not", () => {
    expect(sameSelector(rule({ layer: "visual" }), rule({ layer: "visual" }))).toBe(true);
    expect(sameSelector(rule({ layer: "visual" }), rule({ layer: "visual", locale: "ja" }))).toBe(false);
  });
});

describe("ruleOrder", () => {
  it("says a rule's position decides nothing when nothing else is equally specific and overlapping", () => {
    const [first, second] = ruleOrder([rule({ layer: "length" }), rule({ layer: "terminology", locale: "de" })]);
    expect(first?.ties).toEqual([]);
    expect(second?.ties).toEqual([]);
  });

  it("names the later rule that beats an equally specific one, because ties go to the later rule", () => {
    const order = ruleOrder([rule({ layer: "visual", severity: "warning" }), rule({ locale: "ja", severity: "off" })]);
    expect(order[0]?.ties).toEqual([1]);
    expect(order[0]?.beatenBy).toEqual([1]);
    expect(order[1]?.beats).toEqual([0]);
    expect(order[1]?.beatenBy).toEqual([]);
  });

  it("marks a rule an identical later selector shadows entirely", () => {
    const order = ruleOrder([rule({ layer: "source", severity: "off" }), rule({ layer: "source", severity: "error" })]);
    expect(order[0]?.shadowedBy).toBe(1);
    // The later one is not shadowed by the earlier: order is the whole difference.
    expect(order[1]?.shadowedBy).toBeUndefined();
  });

  it("flags a rule raising a model-decided layer to an error, which the server refuses", () => {
    const order = ruleOrder([rule({ layer: "linguistic", severity: "error" }), rule({ layer: "linguistic", severity: "warning" })]);
    expect(order[0]?.advisoryError).toBe(true);
    expect(order[1]?.advisoryError).toBe(false);
  });
});

describe("moveRule", () => {
  it("moves one rule and leaves the rest in order", () => {
    expect(moveRule(["a", "b", "c"], 2, 0)).toEqual(["c", "a", "b"]);
    expect(moveRule(["a", "b", "c"], 0, 1)).toEqual(["b", "a", "c"]);
  });

  it("returns the list unchanged for a move that would leave it", () => {
    expect(moveRule(["a", "b"], 0, -1)).toEqual(["a", "b"]);
    expect(moveRule(["a", "b"], 1, 2)).toEqual(["a", "b"]);
    expect(moveRule(["a", "b"], 1, 1)).toEqual(["a", "b"]);
  });
});

describe("toDraft / toDocument", () => {
  const doc: PolicyDocument = {
    schema: "glossa.check-policy/v1",
    require_complete: "listed",
    locales: ["de", "en"],
    fail_on: "error",
    missing_translations: "warning",
    rules: [
      { layer: "source", severity: "off" },
      { layer: "terminology", namespace: "legal", severity: "error" },
      { layer: "visual", severity: "warning", mode: "warn" },
    ],
    environments: { production: { require_complete: "listed", locales: ["de", "en", "fr"], require_review: "approved" }, staging: {} },
  };

  it("round-trips a document without inventing or losing anything", () => {
    const back = toDocument(toDraft(doc));
    // `schema` is the server's to write, so a save never sends it back.
    expect(back.schema).toBeUndefined();
    expect(back.require_complete).toBe("listed");
    expect(back.locales).toEqual(["de", "en"]);
    expect(back.rules).toEqual(doc.rules);
    expect(back.environments?.production).toEqual({ require_complete: "listed", locales: ["de", "en", "fr"], require_review: "approved" });
  });

  it("keeps an environment that names no requirement inheriting, rather than quietly requiring every locale", () => {
    const back = toDocument(toDraft(doc));
    expect(back.environments?.staging).toEqual({});
    expect(back.environments?.staging?.require_complete).toBeUndefined();
  });

  it("orders the rules the way the document did: order is meaning, not presentation", () => {
    expect(toDraft(doc).rules.map((r) => r.layer)).toEqual(["source", "terminology", "visual"]);
  });

  it("canonicalises two equal documents to the same string, and a reorder to a different one", () => {
    const a = toDraft(doc);
    const b = toDraft(doc);
    expect(canonical(toDocument(a))).toBe(canonical(toDocument(b)));
    b.rules = moveRule(b.rules, 0, 2);
    expect(canonical(toDocument(b))).not.toBe(canonical(toDocument(a)));
  });

  it("spells a named field out and leaves an unnamed one out of the sentence the editor prints", () => {
    expect(namedFields({ layer: "terminology", namespace: "legal", severity: "error" })).toEqual([
      ["layer", "terminology"],
      ["namespace", "legal"],
    ]);
  });
});

describe("problems", () => {
  const draft = (over: Partial<ReturnType<typeof toDraft>> = {}) => ({ ...toDraft({ require_complete: "all", fail_on: "error", missing_translations: "error" }), ...over });

  it("refuses `listed` with no locale, which is `none` said confusingly", () => {
    expect(problems(draft({ requireComplete: "listed", locales: [] }))).toEqual([{ where: "require_complete", detail: "listed-empty" }]);
  });

  it("refuses a rule raising the model-decided layer to an error before anyone waits for the server", () => {
    expect(problems(draft({ rules: [rule({ layer: "linguistic", severity: "error" })] }))).toEqual([{ where: "rule-0", detail: "advisory-error" }]);
  });

  it("refuses a locale the project does not have, and says nothing when the locales are unknown", () => {
    const d = draft({ rules: [rule({ layer: "length", locale: "ja" })] });
    expect(problems(d, ["de", "en"])).toEqual([{ where: "rule-0", detail: "unknown-locale" }]);
    expect(problems(d)).toEqual([]);
  });

  it("refuses an unnamed environment and two blocks naming the same one", () => {
    const d = draft({
      environments: [
        { key: "e1", name: "", requireComplete: "" as const, locales: [], requireReview: false },
        { key: "e2", name: "production", requireComplete: "" as const, locales: [], requireReview: false },
        { key: "e3", name: "production", requireComplete: "" as const, locales: [], requireReview: false },
      ],
    });
    expect(problems(d)).toEqual([
      { where: "env-0", detail: "unnamed" },
      { where: "env-2", detail: "duplicate" },
    ]);
  });
});
