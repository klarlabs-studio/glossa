import { describe, expect, it } from "vitest";

import { END, START, decodeIndex, hasMarkers, mark, ranges, strip } from "./markers.js";

describe("markers", () => {
  it("wrap text in a start mark, the index in invisible binary digits and an end mark", () => {
    expect(mark(0, "Save")).toBe(`${START}\u2061Save${END}`);
    expect(mark(5, "Save")).toBe(`${START}\u2062\u2061\u2062Save${END}`);
    for (const n of [0, 1, 2, 7, 8, 255, 1024, 99_999]) {
      const s = mark(n, "x");
      expect(ranges(s)).toEqual([{ index: n, from: s.indexOf("x"), to: s.indexOf("x") + 1 }]);
    }
  });

  it("use only default-ignorable invisible operators, never ZWJ, ZWNJ or ZWSP", () => {
    const chars = new Set(mark(123_456, ""));
    for (const c of chars) {
      expect(c.codePointAt(0)).toBeGreaterThanOrEqual(0x2061);
      expect(c.codePointAt(0)).toBeLessThanOrEqual(0x2064);
    }
  });

  it("strip back to the original string, nested or not", () => {
    const inner = mark(1, "Lina");
    const outer = mark(0, `Hallo, ${inner}!`);
    expect(strip(outer)).toBe("Hallo, Lina!");
    expect(hasMarkers(outer)).toBe(true);
    expect(hasMarkers("Hallo")).toBe(false);
  });

  it("find nested ranges innermost first, and skip unbalanced marks", () => {
    const s = `a${mark(0, `b${mark(3, "c")}d`)}e`;
    const found = ranges(s);
    expect(found.map((r) => [r.index, strip(s.slice(r.from, r.to))])).toEqual([
      [3, "c"],
      [0, "bcd"],
    ]);
    expect(ranges(mark(2, "x").slice(0, -1))).toEqual([]);
    expect(ranges(`${END}x`)).toEqual([]);
    expect(ranges(`${START}x${END}`)).toEqual([]);
    expect(decodeIndex("")).toBe(-1);
  });
});
