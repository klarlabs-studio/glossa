import { describe, expect, it } from "vitest";
import { scrollTopFor, visibleRange } from "./virtual";

describe("visibleRange", () => {
  it("windows a long list", () => {
    expect(visibleRange(0, 400, 40, 10_000, 5)).toEqual({ start: 0, end: 16 });
    expect(visibleRange(4000, 400, 40, 10_000, 5)).toEqual({ start: 95, end: 116 });
  });
  it("clamps to the list", () => {
    expect(visibleRange(399_900, 400, 40, 10_000, 5)).toEqual({ start: 9992, end: 10_000 });
    expect(visibleRange(-50, 400, 40, 3, 5)).toEqual({ start: 0, end: 3 });
    expect(visibleRange(0, 400, 40, 0)).toEqual({ start: 0, end: 0 });
  });
});

describe("scrollTopFor", () => {
  it("scrolls only as far as needed", () => {
    expect(scrollTopFor(5, 0, 400, 40)).toBe(0);
    expect(scrollTopFor(12, 0, 400, 40)).toBe(120);
    expect(scrollTopFor(2, 200, 400, 40)).toBe(80);
  });
});
