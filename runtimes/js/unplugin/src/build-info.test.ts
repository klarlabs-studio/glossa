import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

import { applicationFromPackage, detectBuild, problems } from "./build-info.js";

const SHA = "0123456789abcdef0123456789abcdef01234567";
const git = (answers: Record<string, string>) => (args: string[]) => answers[args.join(" ")];

describe("detectBuild", () => {
  const root = mkdtempSync(join(tmpdir(), "glossa-build-info-"));
  writeFileSync(join(root, "package.json"), JSON.stringify({ name: "@acme/Web App" }));

  it("prefers GitHub Actions' variables: the PR head branch, then the ref name", () => {
    const local = git({ "rev-parse HEAD": "f".repeat(40), "symbolic-ref --quiet --short HEAD": "local" });
    expect(detectBuild(root, { GITHUB_SHA: SHA, GITHUB_HEAD_REF: "feat/copy", GITHUB_REF_NAME: "12/merge" }, local)).toEqual({
      application: "web-app",
      commit: SHA,
      branch: "feat/copy",
    });
    expect(detectBuild(root, { GITHUB_SHA: SHA, GITHUB_HEAD_REF: "", GITHUB_REF_NAME: "main", GITHUB_REF_TYPE: "branch" }, local).branch).toBe("main");
    // A tag build names no branch; the checkout's branch (if any) stands in.
    expect(detectBuild(root, { GITHUB_REF_NAME: "v1.0.0", GITHUB_REF_TYPE: "tag" }, local).branch).toBe("local");
  });

  it("falls back to git, and has nothing to say without it", () => {
    const local = git({ "rev-parse HEAD": "A".repeat(40), "symbolic-ref --quiet --short HEAD": "main" });
    expect(detectBuild(root, {}, local)).toMatchObject({ commit: "a".repeat(40), branch: "main" });
    expect(detectBuild(root, {}, git({}))).toMatchObject({ commit: undefined, branch: undefined });
  });
});

describe("problems", () => {
  it("names every field that would make the document invalid", () => {
    expect(problems({ application: "web", commit: SHA, branch: "feat/x" })).toEqual([]);
    expect(problems({})).toHaveLength(3);
    expect(problems({ application: "Web", commit: "9f2c1e7", branch: "feat/../x" })).toEqual([
      expect.stringContaining("isn't a slug"),
      expect.stringContaining("isn't a full lowercase hex commit"),
      expect.stringContaining("isn't a valid branch name"),
    ]);
  });
});

describe("applicationFromPackage", () => {
  it("is undefined without a usable package name", () => {
    expect(applicationFromPackage(join(tmpdir(), "does-not-exist"))).toBeUndefined();
  });
});
