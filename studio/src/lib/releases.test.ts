import { describe, expect, it } from "vitest";
import type { Deployment, Environment, EnvironmentPolicy, Release } from "../api/schemas";
import { covers, defaultRollbackTarget, diffTotals, rollbackCandidates, servingMap, sortEnvironments, uncovered } from "./releases";

const all: EnvironmentPolicy = { states: ["draft", "needs_review", "approved"], include_outdated: true };
const approved: EnvironmentPolicy = { states: ["approved"], include_outdated: true };
const strict: EnvironmentPolicy = { states: ["approved"], include_outdated: false };

const env = (name: string, current?: string): Environment => ({
  name,
  kind: "standard",
  policy: approved,
  ...(current ? { current_release_id: current } : {}),
  created_at: "t",
  updated_at: "t",
});
const rel = (id: string, version: number): Release => ({
  id,
  version,
  environment: "staging",
  policy: approved,
  manifest_digest: "0".repeat(64),
  source_locale: "en",
  locales: [],
  counts: { messages: 0, artifacts: 0, bytes: 0, new_artifacts: 0, locales: {} },
  author: "person:me",
  created_at: "t",
});
const dep = (n: number, release: string): Deployment => ({
  number: n,
  release_id: release,
  action: "promote",
  author: "person:me",
  created_at: "t",
  forced: false,
});

describe("covers (mirrors Release's Policy.Covers)", () => {
  it.each([
    [all, approved, true],
    [approved, all, false],
    [approved, approved, true],
    [strict, approved, false],
    [approved, strict, true],
  ])("%j covers %j → %s", (target, built, want) => {
    expect(covers(target, built)).toBe(want);
  });

  it("names what isn't covered", () => {
    expect(uncovered(approved, all)).toEqual({ states: ["draft", "needs_review"], outdated: false });
    expect(uncovered(strict, approved)).toEqual({ states: [], outdated: true });
  });
});

describe("environments", () => {
  it("sorts the defaults in pipeline order, then custom ones by name", () => {
    const names = sortEnvironments([env("production"), env("pr-42"), env("development"), env("a-qa"), env("staging"), env("preview")]).map((e) => e.name);
    expect(names).toEqual(["development", "preview", "staging", "production", "a-qa", "pr-42"]);
  });

  it("maps releases to the environments serving them", () => {
    const m = servingMap([env("production", "r1"), env("staging", "r1"), env("development", "r2"), env("preview")]);
    expect(m.get("r1")).toEqual(["staging", "production"]);
    expect(m.get("r2")).toEqual(["development"]);
  });
});

describe("rollback targets", () => {
  const releases = new Map([rel("r1", 1), rel("r2", 2), rel("r3", 3), rel("r5", 5)].map((r) => [r.id, r]));

  it("offers releases served before, newest first, without the current one", () => {
    const c = rollbackCandidates([dep(4, "r3"), dep(3, "r5"), dep(2, "r1"), dep(1, "r3")], releases, "r3");
    expect(c.map((r) => r.id)).toEqual(["r5", "r1"]);
  });

  it("defaults to the newest release older than the current one, as the server does", () => {
    const c = rollbackCandidates([dep(3, "r3"), dep(2, "r5"), dep(1, "r1")], releases, "r3");
    expect(defaultRollbackTarget(c, releases.get("r3"))?.id).toBe("r1");
    expect(defaultRollbackTarget([], releases.get("r3"))).toBeUndefined();
  });
});

it("totals a diff", () => {
  expect(
    diffTotals({
      release_id: "r2",
      locales: [
        { locale: "en", added: ["a"], changed: [], removed: [] },
        { locale: "de", added: ["a", "b"], changed: ["c"], removed: ["d"] },
      ],
    }),
  ).toEqual({ added: 3, changed: 1, removed: 1 });
});
