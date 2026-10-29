import { describe, expect, it } from "vitest";
import { SLUG_PATTERN, slugify } from "./slug";

describe("slugify", () => {
  it.each([
    ["Checkout App!", "checkout-app"],
    ["  Crème brûlée  ", "creme-brulee"],
    ["Ünïcödé -- Team", "unicode-team"],
    ["---", ""],
    ["a".repeat(80), "a".repeat(63)],
  ])("%j → %j", (name, want) => {
    expect(slugify(name)).toBe(want);
  });

  it("always yields a slug the contract accepts", () => {
    const re = new RegExp(`^${SLUG_PATTERN}$`);
    for (const n of ["Web", "x", "Hello World 2026", "a-" + "b".repeat(70)]) expect(slugify(n)).toMatch(re);
  });
});
