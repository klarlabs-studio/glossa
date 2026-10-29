/**
 * `@klarlabs-studio/glossa-astro/vue`, the Vue app entrypoint for islands:
 * `vue({ appEntrypoint: "@klarlabs-studio/glossa-astro/vue" })`. Every island app gets the
 * page's runtime (the request's on the server), so all islands and elements
 * render the same release in the same locale. With your own entrypoint, call
 * this from it.
 */
import type { App } from "vue";
import { createGlossa } from "@klarlabs-studio/glossa-vue";

import { getRuntime } from "./client.js";

export default function setup(app: App): void {
  app.use(createGlossa({ runtime: getRuntime() }));
}
