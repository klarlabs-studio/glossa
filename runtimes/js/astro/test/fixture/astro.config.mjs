// A small static site built by test/build.test.ts with the integration from ../../dist.
// GLOSSA_FIXTURE_ENVIRONMENT=preview builds the same site for a preview
// environment (its own release and outDir), which gets the overlay loader.
import { fileURLToPath } from "node:url";
import vue from "@astrojs/vue";
import { defineConfig } from "astro/config";

import glossa from "../../dist/index.js";

const dist = fileURLToPath(new URL("../../dist/", import.meta.url));
const preview = process.env.GLOSSA_FIXTURE_ENVIRONMENT === "preview";

export default defineConfig({
  outDir: preview ? "./dist-preview" : "./dist",
  i18n: { locales: ["de", "en"], defaultLocale: "de" },
  integrations: [
    vue({
      appEntrypoint: "@klarlabs-studio/glossa-astro/vue",
      template: { compilerOptions: { isCustomElement: (tag) => tag.startsWith("glossa-") } },
    }),
    glossa(
      preview
        ? {
            release: "./.glossa-release-preview",
            environment: "preview",
            studio: {
              origin: "https://studio.glossa.test",
              integrity: `sha384-${"Q".repeat(64)}`,
              tenant: "ten_1",
              project: "prj_1",
            },
            elements: true,
            usages: false,
          }
        : { release: "./.glossa-release", elements: true, usages: { application: "astro-fixture" } },
    ),
  ],
  vite: {
    // What installing @klarlabs-studio/glossa-astro from npm would resolve to.
    resolve: {
      alias: [{ find: /^@glossa\/astro\/(.+)$/, replacement: `${dist}$1.js` }],
      dedupe: ["vue"],
    },
  },
});
