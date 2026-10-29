/**
 * `@klarlabs-studio/glossa-unplugin`: finds where every message is used, in any bundler.
 *
 * ```ts
 * // vite.config.ts
 * import glossa from "@klarlabs-studio/glossa-unplugin/vite";
 *
 * export default defineConfig({
 *   plugins: [vue(), glossa({ application: "web", routes: { "/checkout": ["src/checkout/**"] } })],
 * });
 * ```
 *
 * The build writes `.glossa/usages.json` (`glossa.usages/v1`) beside its
 * output; `glossa context push .glossa/usages.json` uploads it.
 */
export { glossa, glossa as default, unpluginFactory, PLUGIN_NAME } from "./plugin.js";
export type { GlossaPluginOptions, StudioOptions } from "./options.js";
export { LOADER_ID, overlayConfig } from "./overlay.js";
export type { Routes, Usage, UsageKind, UsagesDocument } from "./usage.js";
export { accessorPaths } from "./keys.js";
