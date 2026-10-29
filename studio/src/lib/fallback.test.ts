import { describe, expect, it } from "vitest";
import { toGraph, toRows, validateRows } from "./fallback";

const locales = ["en", "de", "de-AT", "de-CH", "fr"];

describe("toRows / toGraph", () => {
  it("round-trips and sorts the wildcard last", () => {
    const graph = { "*": ["en"], "de-AT": ["de"], "de-CH": ["de"] };
    const rows = toRows(graph);
    expect(rows.map((r) => r.from)).toEqual(["de-AT", "de-CH", "*"]);
    expect(toGraph(rows)).toEqual(graph);
  });
});

describe("validateRows", () => {
  it("accepts a valid graph", () => {
    expect(validateRows(toRows({ "de-AT": ["de", "en"], "*": ["en"] }), locales)).toEqual([]);
  });

  it("flags unknown locales, self references, duplicates and empty chains", () => {
    const issues = validateRows(
      [
        { from: "it", chain: ["en"] },
        { from: "de", chain: ["de", "en", "en"] },
        { from: "fr", chain: [] },
        { from: "de", chain: ["en"] },
      ],
      locales,
    );
    expect(issues.map((i) => [i.row, i.code, i.locale])).toEqual([
      [0, "fallback_unknown_locale", "it"],
      [1, "fallback_self_reference", "de"],
      [1, "fallback_duplicate", "en"],
      [2, "fallback_empty_chain", "fr"],
      [3, "fallback_duplicate_row", "de"],
    ]);
  });

  it("finds a cycle once, with its path", () => {
    const issues = validateRows(
      [
        { from: "de-AT", chain: ["de"] },
        { from: "de", chain: ["de-CH"] },
        { from: "de-CH", chain: ["de-AT"] },
      ],
      locales,
    );
    const cycles = issues.filter((i) => i.code === "fallback_cycle");
    expect(cycles).toHaveLength(1);
    expect(cycles[0]).toMatchObject({ row: 0, locale: "de-AT", path: ["de-AT", "de", "de-CH", "de-AT"] });
  });

  it("never counts the wildcard as a cycle edge", () => {
    const rows = [
      { from: "*", chain: ["en"] },
      { from: "en", chain: ["de"] },
    ];
    expect(validateRows(rows, locales)).toEqual([]);
  });
});
