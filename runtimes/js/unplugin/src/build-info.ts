/**
 * Which build a usages document describes: the application, the commit and
 * the branch. Options win; then GitHub Actions' variables; then the local
 * git checkout. Nothing here touches the network.
 */
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { join } from "node:path";

export const APPLICATION = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/;
export const COMMIT = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/;
const BRANCH_PART = String.raw`[^/.\x00-\x20\x7f~^:?*[\\{](?:[^/.\x00-\x20\x7f~^:?*[\\{]|\.[^/.\x00-\x20\x7f~^:?*[\\{])*`;
export const BRANCH = new RegExp(`^${BRANCH_PART}(?:/${BRANCH_PART})*$`);

export interface BuildInfo {
  application?: string;
  commit?: string;
  branch?: string;
}

type Env = Record<string, string | undefined>;
type Git = (args: string[]) => string | undefined;

/** Runs git in `cwd`; undefined when git or the repository isn't there. */
export function gitIn(cwd: string): Git {
  return (args) => {
    try {
      return execFileSync("git", args, { cwd, encoding: "utf8", stdio: ["ignore", "pipe", "ignore"], timeout: 5000 }).trim() || undefined;
    } catch {
      return undefined;
    }
  };
}

/** The root package.json's name as an application slug: `@acme/Web App` → `web-app`. */
export function applicationFromPackage(root: string): string | undefined {
  try {
    const { name } = JSON.parse(readFileSync(join(root, "package.json"), "utf8")) as { name?: unknown };
    if (typeof name !== "string") return undefined;
    const slug = name
      .replace(/^@[^/]*\//, "")
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 63)
      .replace(/-+$/, "");
    return APPLICATION.test(slug) ? slug : undefined;
  } catch {
    return undefined;
  }
}

/**
 * Commit: `GITHUB_SHA`, else `git rev-parse HEAD`. Branch: `GITHUB_HEAD_REF`
 * (pull requests), else `GITHUB_REF_NAME` when it names a branch, else the
 * checked-out branch. A detached HEAD has no branch.
 */
export function detectBuild(root: string, env: Env = process.env, git: Git = gitIn(root)): BuildInfo {
  const commit = env.GITHUB_SHA || git(["rev-parse", "HEAD"]);
  const refBranch = env.GITHUB_REF_TYPE === undefined || env.GITHUB_REF_TYPE === "branch" ? env.GITHUB_REF_NAME : undefined;
  const branch = env.GITHUB_HEAD_REF || refBranch || git(["symbolic-ref", "--quiet", "--short", "HEAD"]);
  return { application: applicationFromPackage(root), commit: commit?.toLowerCase(), branch };
}

/** The fields that make the build invalid, with why. Empty when the document can be written. */
export function problems(info: BuildInfo): string[] {
  const out: string[] = [];
  if (!info.application) out.push("no application (set the `application` option)");
  else if (!APPLICATION.test(info.application)) out.push(`application ${JSON.stringify(info.application)} isn't a slug`);
  if (!info.commit) out.push("no commit (set `commit`, GITHUB_SHA, or build in a git checkout)");
  else if (!COMMIT.test(info.commit)) out.push(`commit ${JSON.stringify(info.commit)} isn't a full lowercase hex commit ID`);
  if (!info.branch) out.push("no branch (set `branch`, GITHUB_HEAD_REF/GITHUB_REF_NAME, or check out a branch)");
  else if (info.branch.length > 255 || !BRANCH.test(info.branch)) out.push(`branch ${JSON.stringify(info.branch)} isn't a valid branch name`);
  return out;
}
