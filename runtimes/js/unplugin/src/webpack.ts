/** `@klarlabs-studio/glossa-unplugin` for webpack: `import glossa from "@klarlabs-studio/glossa-unplugin/webpack"`. It runs as a post loader. */
import { glossa } from "./plugin.js";

export type { GlossaPluginOptions } from "./options.js";
export default glossa.webpack;
