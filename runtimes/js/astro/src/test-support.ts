/**
 * `@klarlabs-studio/glossa/astro/testing`: run code that imports `/astro/client` or `/astro/server`
 * under Vitest, outside an Astro build, where `virtual:glossa/config` and `virtual:glossa/release`
 * don't exist.
 *
 * ```ts
 * // vitest.config.ts
 * import { defineConfig } from "vitest/config";
 * import { glossaAstroTesting } from "@klarlabs-studio/glossa/astro/testing";
 *
 * export default defineConfig({ plugins: [glossaAstroTesting()] });
 * ```
 *
 * The plugin serves both virtual modules (an inert config, no release) and tells Vitest to process
 * the package instead of loading it as an external, which is what lets the virtual imports resolve.
 * Nothing is mocked per test file. Pass overrides to change the stub, or a `release` for code that
 * renders with one.
 */
import type { BundledRelease } from "@klarlabs-studio/glossa-runtime";

import type { PublicConfig } from "./config.js";

/** An inert `virtual:glossa/config`: no edge, no key, English only, nothing prerendered or inlined. */
export const stubConfig: PublicConfig = {
  environment: "test",
  routing: {
    locales: [{ code: "en", path: "en" }],
    defaultLocale: "en",
    prefixDefaultLocale: false,
  },
  prerender: false,
  inline: "never",
};

/** A Vite plugin (structurally; `vite` isn't a dependency) serving the stub virtual modules. */
export function glossaAstroTesting(
  config: Partial<PublicConfig> = {},
  release: BundledRelease | null = null,
) {
  const values: Record<string, unknown> = {
    "virtual:glossa/config": { ...stubConfig, ...config },
    "virtual:glossa/release": release,
  };
  return {
    name: "@klarlabs-studio/glossa-astro:testing",
    enforce: "pre" as const,
    // Vitest externalizes node_modules packages, and an external can't import a virtual module.
    config: () => ({
      ssr: { noExternal: [/@klarlabs-studio[/\\]glossa/] },
      test: { server: { deps: { inline: [/@klarlabs-studio[/\\]glossa/] } } },
    }),
    resolveId: (id: string) => (id in values ? `\0${id}` : undefined),
    load: (id: string) =>
      id.startsWith("\0") && id.slice(1) in values
        ? `export default ${JSON.stringify(values[id.slice(1)])};`
        : undefined,
  };
}
