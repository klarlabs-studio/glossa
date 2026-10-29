/** `@klarlabs-studio/glossa-unplugin` for esbuild: `import glossa from "@klarlabs-studio/glossa-unplugin/esbuild"`. List it first, before plugins that load files. */
import { glossa } from "./plugin.js";

export type { GlossaPluginOptions } from "./options.js";
export default glossa.esbuild;
