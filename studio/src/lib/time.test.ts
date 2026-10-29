import { expect, it } from "vitest";
import { relativeTime } from "./time";

const now = Date.parse("2026-09-19T12:00:00Z");

it.each([
  ["2026-09-19T11:59:58Z", "now"],
  ["2026-09-19T11:59:00Z", "1 minute ago"],
  ["2026-09-19T09:00:00Z", "3 hours ago"],
  ["2026-09-18T12:00:00Z", "yesterday"],
  ["2026-09-01T12:00:00Z", "Sep 1, 2026"],
])("%s → %s", (iso, want) => {
  expect(relativeTime(iso, now, "en-US")).toBe(want);
});

it("passes through what it can't parse", () => {
  expect(relativeTime("soon", now)).toBe("soon");
});
