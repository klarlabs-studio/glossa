/**
 * The shared usage fixtures (runtimes/testdata/usages): every case marked
 * for `unplugin` goes through a real Vite build (`astro build` when it has
 * `.astro` files), and the TS/TSX cases also through Rollup, esbuild and
 * webpack. Each build's usages.json must validate against the schema and
 * equal expected.json, order included, ignoring `tool`.
 */
import { describe, expect, it } from "vitest";

import {
  astroRun,
  comparable,
  esbuildRun,
  fixtureCases,
  rollupRun,
  schemaErrors,
  viteRun,
  webpackRun,
} from "./harness.js";

const cases = fixtureCases("unplugin");
const scriptOnly = cases.filter((c) => c.files.every((f) => /\.[jt]sx?$/.test(f)));

describe("usage fixtures", () => {
  it("has cases for the plugin, and some that every bundler can build", () => {
    expect(cases.length).toBeGreaterThanOrEqual(7);
    expect(scriptOnly.map((c) => c.name)).toEqual(
      expect.arrayContaining(["columns-unicode", "react-components", "ts-accessors"]),
    );
  });

  describe.each(cases)("$name", (c) => {
    const astro = c.files.some((f) => f.endsWith(".astro"));
    it(`${astro ? "astro build" : "vite build"} writes the expected usages`, async () => {
      const { doc } = astro ? await astroRun(c) : await viteRun(c);
      expect(schemaErrors(doc)).toEqual([]);
      expect(doc.tool.name).toBe("@klarlabs-studio/glossa");
      expect(comparable(doc)).toEqual(comparable(c.expected));
    });
  });

  describe.each(cases.filter((c) => c.files.some((f) => f.endsWith(".vue"))))("$name", (c) => {
    it("vite build --ssr (Vue's server compiler) writes the same usages for its modules", async () => {
      const { doc } = await viteRun(c, { ssr: true });
      expect(schemaErrors(doc)).toEqual([]);
      const expected = c.expected.usages.filter((u) => !u.file.endsWith(".html"));
      expect(doc.usages).toEqual(expected);
    });
  });

  describe.each(scriptOnly)("$name", (c) => {
    it.each([
      ["rollup", rollupRun],
      ["esbuild", esbuildRun],
      ["webpack", webpackRun],
    ])("%s writes the expected usages", async (_name, run) => {
      const doc = await run(c);
      expect(schemaErrors(doc)).toEqual([]);
      expect(comparable(doc)).toEqual(comparable(c.expected));
    });
  });
});
