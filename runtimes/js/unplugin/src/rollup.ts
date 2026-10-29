/** `@klarlabs-studio/glossa-unplugin` for Rollup: `import glossa from "@klarlabs-studio/glossa-unplugin/rollup"`. List it after the plugins that compile TS, JSX or SFCs. */
import { glossa } from "./plugin.js";

export type { GlossaPluginOptions } from "./options.js";
export default glossa.rollup;
