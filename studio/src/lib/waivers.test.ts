import { describe, expect, it } from "vitest";
import { waiver } from "../test/fake-quality";
import { EMPTY_WAIVER_FILTER, filterWaivers, stands, waiverTotals } from "./waivers";

const NOW = Date.parse("2026-09-19T08:00:00Z");
const inDays = (n: number) => new Date(NOW + n * 24 * 60 * 60 * 1000).toISOString();

const standing = waiver({
  fingerprint: "f_0000000000000001",
  reason: "Brand guide says Login.",
  accepts: { layer: "terminology", code: "term_forbidden", locale: "de" },
});
const revoked = waiver({ fingerprint: "f_0000000000000002", reason: "No longer true.", active: false, revoked_at: "2026-09-18T08:00:00Z" });
const unexamined = waiver({ fingerprint: "f_0000000000000003", reason: "Nobody knows." });
const expiring = waiver({
  fingerprint: "f_0000000000000004",
  reason: "Until the redesign lands.",
  accepts: { layer: "visual", code: "text-clipped" },
  expires_at: inDays(7),
});
const all = [standing, revoked, unexamined, expiring];

describe("stands", () => {
  it("is false for a revoked waiver even when the server still says active", () => {
    expect(stands(standing)).toBe(true);
    expect(stands(revoked)).toBe(false);
    expect(stands({ ...standing, active: true, revoked_at: "2026-09-18T08:00:00Z" })).toBe(false);
  });
});

describe("filterWaivers", () => {
  it("shows the standing and the past together by default: a revoked waiver is history, not noise", () => {
    expect(filterWaivers(all, EMPTY_WAIVER_FILTER)).toHaveLength(4);
  });

  it("selects by state", () => {
    expect(filterWaivers(all, { ...EMPTY_WAIVER_FILTER, state: "active" }).map((w) => w.fingerprint)).toEqual([
      standing.fingerprint,
      unexamined.fingerprint,
      expiring.fingerprint,
    ]);
    expect(filterWaivers(all, { ...EMPTY_WAIVER_FILTER, state: "inactive" })).toEqual([revoked]);
  });

  it("selects by the layer and the code it accepts, and by a part of the fingerprint", () => {
    expect(filterWaivers(all, { ...EMPTY_WAIVER_FILTER, layer: "visual" })).toEqual([expiring]);
    expect(filterWaivers(all, { ...EMPTY_WAIVER_FILTER, code: "TERM_forbidden" })).toEqual([standing]);
    expect(filterWaivers(all, { ...EMPTY_WAIVER_FILTER, fingerprint: "0004" })).toEqual([expiring]);
  });

  it("leaves a waiver that accepts nothing out of a layer filter rather than guessing what it accepts", () => {
    expect(filterWaivers(all, { ...EMPTY_WAIVER_FILTER, layer: "terminology" })).toEqual([standing]);
  });
});

describe("waiverTotals", () => {
  it("counts the standing and the past apart", () => {
    expect(waiverTotals(all, NOW)).toMatchObject({ standing: 3, past: 1 });
  });

  it("counts the standing waivers nobody is watching: no stored finding carries their fingerprint", () => {
    expect(waiverTotals(all, NOW).unexamined).toBe(1);
  });

  it("counts the standing waivers about to expire, because a check is about to get stricter by itself", () => {
    expect(waiverTotals(all, NOW).expiringSoon).toBe(1);
    expect(waiverTotals([{ ...expiring, expires_at: inDays(90) }], NOW).expiringSoon).toBe(0);
  });

  it("never counts a revoked waiver as standing, whatever its expiry says", () => {
    expect(waiverTotals([{ ...revoked, expires_at: inDays(1) }], NOW)).toMatchObject({ standing: 0, past: 1, expiringSoon: 0 });
  });
});
