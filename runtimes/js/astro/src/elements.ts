/**
 * `@klarlabs-studio/glossa-astro/elements` (injected on every page with `elements: true`):
 * defines the `<glossa-*>` elements and makes providers without their own
 * `edge` use the page's runtime, the one the Vue islands use.
 */
import { GlossaProvider } from "@klarlabs-studio/glossa-elements";

import { getRuntime } from "./client.js";

GlossaProvider.defaultRuntime = getRuntime;
