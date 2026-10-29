import { describe, expect, it } from "vitest";
import type { ProjectTranslation } from "../api/schemas";
import { coverageOf, matches, namespacesOf, statusOf } from "./workspace";

describe("coverageOf", () => {
  const t = (message_id: string, state: ProjectTranslation["state"], outdated = false) => ({ message_id, state, outdated }) as ProjectTranslation;
  it("counts usable translations by message ID; rejected ones are missing", () => {
    const c = coverageOf([t("m1", "approved"), t("m2", "draft", true), t("m3", "rejected", true)]);
    expect([...c.translated].sort()).toEqual(["m1", "m2"]);
    expect([...c.outdated]).toEqual(["m2"]);
  });
});

describe("statusOf", () => {
  const coverage = { translated: new Set(["b", "c"]), outdated: new Set(["b"]) };
  it("derives the row status from one listing of the locale", () => {
    expect(statusOf("a", coverage)).toBe("missing");
    expect(statusOf("b", coverage)).toBe("outdated");
    expect(statusOf("c", coverage)).toBe("translated");
    expect(statusOf("c", undefined)).toBe("unknown");
  });
});

describe("matches", () => {
  const m = { key: "checkout.pay", description: "Button on the payment step", source: { text: "Pay {amount}" } } as Parameters<typeof matches>[0];
  it.each([
    ["", true],
    ["CHECKOUT", true],
    ["pay {", true],
    ["payment step", true],
    ["refund", false],
  ])("%j → %s", (q, want) => {
    expect(matches(m, q)).toBe(want);
  });
});

describe("namespacesOf", () => {
  it("dedupes and puts default first", () => {
    expect(namespacesOf([{ namespace: "emails" }, { namespace: "default" }, { namespace: "admin" }, { namespace: "emails" }], ["web"])).toEqual([
      "default",
      "admin",
      "emails",
      "web",
    ]);
  });
});
