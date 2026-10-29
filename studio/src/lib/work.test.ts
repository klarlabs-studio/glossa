import { describe, expect, it } from "vitest";
import { approval, assignment } from "../test/fake-work";
import { byUrgency, dueState, eligibility, grantCount, hasDecided, liveUnits, unitKey, unitLocales } from "./work";

const AT = "2026-09-01T00:00:00Z";
const NOW = Date.parse("2026-09-19T08:00:00Z");

describe("work rules", () => {
  it("names an assignment's locales once each, sorted", () => {
    expect(unitLocales(assignment({ units: [{ message_id: "a", locale: "fr" }, { message_id: "b", locale: "de" }, { message_id: "a", locale: "de" }] }))).toEqual(["de", "fr"]);
  });

  it("calls only live work overdue", () => {
    expect(dueState(assignment({ due_at: "2026-09-01T00:00:00Z" }), NOW)).toBe("overdue");
    expect(dueState(assignment({ due_at: "2026-09-01T00:00:00Z", state: "done" }), NOW)).toBe("due");
    expect(dueState(assignment({ due_at: "2026-10-01T00:00:00Z" }), NOW)).toBe("due");
    expect(dueState(assignment(), NOW)).toBe("none");
  });

  it("orders live work first, soonest due first", () => {
    const list = [
      assignment({ id: "done", state: "done", due_at: "2026-01-01T00:00:00Z" }),
      assignment({ id: "later", due_at: "2026-12-01T00:00:00Z" }),
      assignment({ id: "undated" }),
      assignment({ id: "soon", state: "accepted", due_at: "2026-10-01T00:00:00Z" }),
    ];
    expect(list.sort(byUrgency).map((a) => a.id)).toEqual(["soon", "later", "undated", "done"]);
  });

  it("collects the units of live assignments only", () => {
    const units = liveUnits([
      assignment({ state: "accepted", units: [{ message_id: "m1", locale: "de" }] }),
      assignment({ state: "declined", units: [{ message_id: "m2", locale: "de" }] }),
    ]);
    expect([...units]).toEqual([unitKey("m1", "de")]);
  });

  it("decides eligibility by member, role and group, and says 'maybe' when groups are unknown", () => {
    const self = { memberId: "m", roles: ["reviewer"] as const, groups: [{ id: "g", name: "g", members: ["m"], created_at: AT, updated_at: AT }] };
    expect(eligibility(approval({ eligible: { kind: "member", id: "m" } }), self)).toBe("yes");
    expect(eligibility(approval({ eligible: { kind: "member", id: "x" } }), self)).toBe("no");
    expect(eligibility(approval({ eligible: { kind: "role", role: "reviewer" } }), self)).toBe("yes");
    expect(eligibility(approval({ eligible: { kind: "role", role: "admin" } }), self)).toBe("no");
    expect(eligibility(approval({ eligible: { kind: "group", id: "g" } }), self)).toBe("yes");
    expect(eligibility(approval({ eligible: { kind: "group", id: "h" } }), self)).toBe("no");
    expect(eligibility(approval({ eligible: { kind: "group", id: "g" } }), { ...self, groups: undefined })).toBe("maybe");
    expect(eligibility(approval({ eligible: { kind: "vendor", id: "v" } }), self)).toBe("no");
  });

  it("counts each person's grant once", () => {
    const ap = approval({
      decisions: [
        { principal: "person:a", decision: "granted", at: AT },
        { principal: "person:a", decision: "granted", at: AT },
        { principal: "person:b", decision: "granted", at: AT },
      ],
    });
    expect(grantCount(ap)).toBe(2);
    expect(hasDecided(ap, "person:b")).toBe(true);
    expect(hasDecided(ap, "person:c")).toBe(false);
  });
});
