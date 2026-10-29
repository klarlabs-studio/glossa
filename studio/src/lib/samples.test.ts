import { describe, expect, it } from "vitest";
import type { Argument } from "../api/schemas";
import { defaultSample, pluralExamples, toValues } from "./samples";

const arg = (a: Partial<Argument> & Pick<Argument, "name" | "type">): Argument => a;

describe("defaultSample", () => {
  it.each([
    [arg({ name: "count", type: "number" }), "3"],
    [arg({ name: "n", type: "integer" }), "3"],
    [arg({ name: "rate", type: "percent" }), "0.42"],
    [arg({ name: "amount", type: "currency" }), "1234.5"],
    [arg({ name: "when", type: "date" }), "2026-03-14T15:09:26Z"],
    [arg({ name: "userName", type: "string" }), "Ada"],
    [arg({ name: "product", type: "string" }), "product"],
    [arg({ name: "gender", type: "select", selector: { kind: "string", keys: ["female", "*"] } }), "female"],
    [arg({ name: "gender", type: "select" }), "other"],
  ])("%j → %s", (a, want) => {
    expect(defaultSample(a)).toBe(want);
  });
});

describe("toValues", () => {
  const args = [
    arg({ name: "count", type: "number" }),
    arg({ name: "when", type: "datetime" }),
    arg({ name: "name", type: "string" }),
  ];

  it("types numbers and dates", () => {
    const v = toValues(args, { count: "12", when: "2026-01-02T00:00:00Z", name: "Kim" });
    expect(v.count).toBe(12);
    expect(v.when).toBeInstanceOf(Date);
    expect(v.name).toBe("Kim");
  });

  it("keeps invalid input as text so the formatter can report it", () => {
    const v = toValues(args, { count: "twelve", when: "someday" });
    expect(v.count).toBe("twelve");
    expect(v.when).toBe("someday");
    expect(v.name).toBe("Ada");
  });
});

describe("pluralExamples", () => {
  it("covers English", () => {
    expect(pluralExamples("en")).toEqual([
      { category: "other", value: 0 },
      { category: "one", value: 1 },
    ]);
  });

  it("covers every Arabic category", () => {
    expect(pluralExamples("ar").map((e) => e.category).sort()).toEqual(["few", "many", "one", "other", "two", "zero"]);
  });

  it("supports ordinals", () => {
    expect(pluralExamples("en", "ordinal").map((e) => e.category).sort()).toEqual(["few", "one", "other", "two"]);
  });

  it("returns nothing for an invalid locale", () => {
    expect(pluralExamples("??")).toEqual([]);
  });
});
