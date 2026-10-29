import { afterEach, describe, expect, it, vi } from "vitest";
import type { AstroIntegration } from "astro";

import glossa, { resolveRouting } from "./index.js";
import { release, text } from "./testing/release.js";

const r = release("rel_3", 3, { de: { a: text("A") }, en: { a: text("A") }, ar: { a: text("A") } });

type Setup = NonNullable<AstroIntegration["hooks"]["astro:config:setup"]>;

async function setup(options: Parameters<typeof glossa>[0], i18n?: unknown) {
  const calls = {
    updateConfig: vi.fn(),
    addMiddleware: vi.fn(),
    injectScript: vi.fn(),
    logger: { info: vi.fn(), warn: vi.fn() },
  };
  const hook = glossa(options).hooks["astro:config:setup"] as Setup;
  await hook({
    ...calls,
    config: { root: new URL("file:///site/"), outDir: new URL("file:///site/dist/"), base: "/", i18n },
  } as unknown as Parameters<Setup>[0]);
  return calls;
}

describe("resolveRouting", () => {
  it("prefers the integration's options, then Astro's i18n, then the release", () => {
    expect(resolveRouting({ locales: ["en"], defaultLocale: "en" }, { base: "/" }, r)).toEqual({
      locales: [{ code: "en", path: "en" }],
      defaultLocale: "en",
      prefixDefaultLocale: false,
      base: "/",
    });
    const i18n = {
      locales: ["de", { path: "english", codes: ["en"] }],
      defaultLocale: "de",
      routing: {
        prefixDefaultLocale: true,
        redirectToDefaultLocale: true,
        fallbackType: "redirect",
      },
    } as const;
    expect(resolveRouting({}, { base: "/", i18n } as never, r)).toEqual({
      locales: [
        { code: "de", path: "de" },
        { code: "en", path: "english" },
      ],
      defaultLocale: "de",
      prefixDefaultLocale: true,
      base: "/",
    });
    expect(resolveRouting({}, { base: "/" }, r)).toMatchObject({
      locales: [
        { code: "de", path: "de" },
        { code: "en", path: "en" },
        { code: "ar", path: "ar" },
      ],
      defaultLocale: "de",
    });
  });
});

describe("glossa()", () => {
  it("serves the config and the release as virtual modules and registers the middleware", async () => {
    const calls = await setup({ release: r, environment: "production", edge: "https://edge.test" });
    const vite = calls.updateConfig.mock.calls[0]![0].vite;
    expect(vite.ssr.noExternal).toContain("@klarlabs-studio/glossa-astro");
    expect(vite.optimizeDeps.exclude).toContain("@klarlabs-studio/glossa-astro");
    const plugin = vite.plugins[0];
    expect(plugin.resolveId("virtual:glossa/config")).toBe("\0virtual:glossa/config");
    expect(plugin.resolveId("./other.js")).toBeUndefined();
    const config = JSON.parse(
      plugin.load("\0virtual:glossa/config").replace("export default ", "").replace(/;$/, ""),
    );
    expect(config).toMatchObject({
      edge: "https://edge.test",
      environment: "production",
      prerender: "static",
      inline: "auto",
      routing: { defaultLocale: "de" },
    });
    expect(plugin.load("\0virtual:glossa/release")).toContain('"rel_3"');
    const [mw] = calls.addMiddleware.mock.calls[0]!;
    expect(mw.order).toBe("pre");
    expect(String(mw.entrypoint)).toMatch(/middleware\.js$/);
    expect(calls.injectScript).not.toHaveBeenCalled();
    expect(calls.logger.info).toHaveBeenCalledWith(expect.stringContaining("rel_3"));
  });

  it("injects the elements on every page with elements: true", async () => {
    const calls = await setup({ release: r, elements: true });
    expect(calls.injectScript).toHaveBeenCalledWith(
      "page",
      expect.stringMatching(/^import ".*elements\.js";$/),
    );
  });

  it("adds @klarlabs-studio/glossa-unplugin to Vite for usages, unless usages: false", async () => {
    const names = (calls: Awaited<ReturnType<typeof setup>>) =>
      (calls.updateConfig.mock.calls[0]![0].vite.plugins as Array<{ name: string }>).map((p) => p.name);
    const on = await setup({ release: r });
    expect(names(on)).toEqual(["@klarlabs-studio/glossa-astro:virtual", "@klarlabs-studio/glossa-unplugin"]);
    const plugin = on.updateConfig.mock.calls[0]![0].vite.plugins[1];
    expect(plugin).toMatchObject({ enforce: "post", apply: "build" });
    const off = await setup({ release: r, usages: false });
    expect(names(off)).toEqual(["@klarlabs-studio/glossa-astro:virtual"]);
  });

  describe("the overlay loader (RFC 0004 §5.1)", () => {
    const studio = {
      origin: "https://studio.glossa.test",
      integrity: `sha384-${"Q".repeat(64)}`,
      tenant: "ten_1",
      project: "prj_1",
    };
    const preview = { ...r, manifest: { ...r.manifest, environment: "preview" } };
    const LOADER = 'import "virtual:glossa/overlay-loader";';

    it("is on every page of a preview site, even with usages: false", async () => {
      const calls = await setup({ release: preview, environment: "preview", studio, usages: false });
      expect(calls.injectScript).toHaveBeenCalledWith("page", LOADER);
      const plugins = calls.updateConfig.mock.calls[0]![0].vite.plugins as unknown[];
      const names = plugins.flat().map((p) => (p as { name: string }).name);
      expect(names).toEqual(["@klarlabs-studio/glossa-astro:virtual", "@klarlabs-studio/glossa-unplugin:overlay"]);
    });

    it("is never on a production site", async () => {
      const calls = await setup({ release: r, studio });
      expect(calls.injectScript).not.toHaveBeenCalled();
    });

    it("fails the build for overlay: true in production, before loading anything", async () => {
      await expect(setup({ release: r, overlay: true, studio })).rejects.toThrow(
        /never in a production build/,
      );
    });

    it("needs studio on a preview site unless overlay: false", async () => {
      await expect(setup({ release: preview, environment: "preview" })).rejects.toThrow(
        /needs `studio`/,
      );
      const off = await setup({ release: preview, environment: "preview", overlay: false });
      expect(off.injectScript).not.toHaveBeenCalled();
    });
  });

  it("warns and renders inline defaults when no release is configured", async () => {
    const calls = await setup({});
    expect(calls.logger.warn).toHaveBeenCalledWith(expect.stringContaining("inline defaults"));
    const plugin = calls.updateConfig.mock.calls[0]![0].vite.plugins[0];
    expect(plugin.load("\0virtual:glossa/release")).toBe("export default null;");
  });

  describe("requireRelease", () => {
    afterEach(() => vi.unstubAllEnvs());

    it("fails the build instead of warning when no release could be loaded", async () => {
      await expect(setup({ requireRelease: true })).rejects.toThrow(/requireRelease.*no release/s);
      await expect(
        setup({ requireRelease: true, edge: "https://edge.test" }), // key missing, as in a keyless CI build
      ).rejects.toThrow(/GLOSSA_DELIVERY_KEY/);
    });

    it("builds when a release is loaded", async () => {
      const calls = await setup({ requireRelease: true, release: r });
      expect(calls.logger.warn).not.toHaveBeenCalled();
    });

    it("is off by default, and GLOSSA_REQUIRE_RELEASE turns it on unless the option says otherwise", async () => {
      vi.stubEnv("GLOSSA_REQUIRE_RELEASE", "");
      await expect(setup({})).resolves.toBeDefined();
      vi.stubEnv("GLOSSA_REQUIRE_RELEASE", "1");
      await expect(setup({})).rejects.toThrow(/requireRelease/);
      await expect(setup({ requireRelease: false })).resolves.toBeDefined();
    });
  });
});
