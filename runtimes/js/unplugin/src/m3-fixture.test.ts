// @vitest-environment node
/**
 * The M3 exit test's fixture application is built here and committed
 * into the Go tree (scripts/m3-fixture.mjs), so
 * platform/internal/systemtest/m3 needs no Node. This test rebuilds it
 * and fails when the committed output no longer matches the sources
 * `go generate ./internal/systemtest/m3/...` writes — the exit test must
 * never run on a stale bundle or on stale usages.
 *
 * It is the same contract @klarlabs-studio/glossa-capture keeps for the capture agent it
 * bundles into the CLI. The two usages documents are compared exactly;
 * the bundles are compared by what the exit test reads from them — every
 * message of the application, and the loader's markers — because a
 * bundler's output is not byte-identical across the environments that
 * write and check it.
 */
import { existsSync, readFileSync, readdirSync, mkdtempSync, rmSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, relative, sep } from "node:path";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

import { LOADER_ATTRIBUTE, OVERLAY_PATH } from "@klarlabs-studio/glossa-runtime/dev";

import { APP, buildFixture, outputs } from "../scripts/m3-fixture.mjs";
import type { UsagesDocument } from "./usage.js";

/** Every file under dir, as directory-relative slash paths. */
function walk(dir: string, root = dir): string[] {
  if (!existsSync(dir)) return [];
  return readdirSync(dir)
    .flatMap((name) => {
      const p = join(dir, name);
      return statSync(p).isDirectory() ? walk(p, root) : [relative(root, p).split(sep).join("/")];
    })
    .sort();
}

/** Everything one build wrote, as one string. */
function bundleText(dir: string): string {
  return walk(dir)
    .map((f) => readFileSync(join(dir, f), "utf8"))
    .join("\n");
}

const read = (path: string): UsagesDocument => JSON.parse(readFileSync(path, "utf8")) as UsagesDocument;

describe("the M3 exit test's fixture application", () => {
  let work: string;

  beforeAll(async () => {
    work = mkdtempSync(join(tmpdir(), "glossa-m3-check-"));
    await buildFixture(work);
  }, 300_000);

  afterAll(() => {
    if (work) rmSync(work, { recursive: true, force: true });
  });

  it("writes every file the Go test reads", () => {
    expect(outputs.filter((p) => !existsSync(join(APP, p)))).toEqual([]);
  });

  it.each(["usages.json", "usages.branch.json"])(
    "%s is exactly what a fresh build of the committed sources writes",
    (name) => {
      const fresh = read(join(work, name));
      const committed = read(join(APP, name));
      expect(
        committed,
        "out of date: run `go generate ./internal/systemtest/m3/...` in platform/, then `pnpm --filter @klarlabs-studio/glossa-unplugin build:m3`",
      ).toEqual(fresh);
    },
  );

  it("committed both builds of the same application", () => {
    const keys = new Set(read(join(APP, "usages.json")).usages.map((u) => u.key));
    expect(keys.size).toBeGreaterThan(100);
    for (const build of ["built/preview", "built/production"]) {
      const text = bundleText(join(APP, build));
      expect(text.length).toBeGreaterThan(0);
      const missing = [...keys].filter((k) => !text.includes(k));
      expect(missing, `${build} does not render every message of the application`).toEqual([]);
    }
  });

  it("puts the in-product editor's loader in the preview build and in no other", () => {
    const markers = [LOADER_ATTRIBUTE, OVERLAY_PATH, "https://studio.glossa.test", "sha384-"];
    const preview = bundleText(join(APP, "built/preview"));
    const production = bundleText(join(APP, "built/production"));
    expect(markers.filter((m) => !preview.includes(m))).toEqual([]);
    expect(markers.filter((m) => production.includes(m))).toEqual(markers.filter(() => false));
    expect(production).not.toContain("glossa.overlay");
  });
});
