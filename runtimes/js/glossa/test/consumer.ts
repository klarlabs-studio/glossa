// A consumer's view of @klarlabs-studio/glossa: every subpath resolves its types through the
// package's own exports map (self-reference), exactly as an installed copy would. `pnpm lint`
// runs `tsc --noEmit` on this file; the tarball smoke test runs the same file against a real install.
import * as runtime from "@klarlabs-studio/glossa";
import * as idb from "@klarlabs-studio/glossa/idb";
import * as dev from "@klarlabs-studio/glossa/dev";
import * as elements from "@klarlabs-studio/glossa/elements";
import * as ssr from "@klarlabs-studio/glossa/elements/ssr";
import * as parts from "@klarlabs-studio/glossa/elements/parts";
import * as vue from "@klarlabs-studio/glossa/vue";
import * as react from "@klarlabs-studio/glossa/react";
import astro from "@klarlabs-studio/glossa/astro";
import * as astroServer from "@klarlabs-studio/glossa/astro/server";
import * as astroClient from "@klarlabs-studio/glossa/astro/client";
import * as astroVue from "@klarlabs-studio/glossa/astro/vue";
import * as astroElements from "@klarlabs-studio/glossa/astro/elements";
import * as astroMiddleware from "@klarlabs-studio/glossa/astro/middleware";
import * as unplugin from "@klarlabs-studio/glossa/unplugin";
import vite from "@klarlabs-studio/glossa/unplugin/vite";
import rollup from "@klarlabs-studio/glossa/unplugin/rollup";
import webpack from "@klarlabs-studio/glossa/unplugin/webpack";
import esbuild from "@klarlabs-studio/glossa/unplugin/esbuild";
import * as capture from "@klarlabs-studio/glossa/capture";
import * as probes from "@klarlabs-studio/glossa/capture/probes";
import * as overlay from "@klarlabs-studio/glossa/overlay";
import * as messageformat from "@klarlabs-studio/glossa/messageformat";
import * as testing from "@klarlabs-studio/glossa/messageformat/testing";

const rt = runtime.createRuntime({ locales: "de" });
const app: unknown[] = [
  rt.t("cart.checkout", {}, { default: "Zur Kasse" }),
  vue.createGlossa({ runtime: rt }),
  react.createGlossa({ runtime: rt }),
  elements.GlossaText,
  capture.startCapture,
  probes.probe,
  astro(),
  vite(),
  rollup(),
  webpack(),
  esbuild(),
];

export const everySubpath = [
  app,
  idb,
  dev,
  ssr,
  parts,
  astroServer,
  astroClient,
  astroVue,
  astroElements,
  astroMiddleware,
  unplugin,
  overlay,
  messageformat,
  testing,
];
