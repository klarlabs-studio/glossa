/**
 * The browser side of the overlay e2e tests: a preview deployment's page on
 * `@klarlabs-studio/glossa-runtime` and `@klarlabs-studio/glossa-elements`, bundled by esbuild and served
 * from the app origin under a strict CSP. `RELEASE` and `CONFIG` are
 * prepended by the test. On `?glossa=edit` it loads the overlay from the
 * Studio origin and starts a session with a token, which is what the panel
 * tests need; the real loader and its guards are e2e/loader.spec.ts.
 */
import { createRuntime } from "@klarlabs-studio/glossa-runtime";
import type { BundledRelease, Runtime } from "@klarlabs-studio/glossa-runtime";
import "@klarlabs-studio/glossa-elements";

import type { Overlay, activate } from "../src/index.js";
import { renderPage } from "../src/testing/page.js";

declare const RELEASE: BundledRelease;
declare const CONFIG: {
  apiBase: string;
  overlaySrc: string;
  token: string;
  tenant: string;
  project: string;
};

declare global {
  interface Window {
    app: { runtime: Runtime; overlay?: Overlay };
    GlossaOverlay?: { activate: typeof activate };
  }
}

const runtime = createRuntime({
  bundled: RELEASE,
  locales: "de",
  environment: "preview",
  storage: null,
});
runtime.subscribe(() => renderPage(document, runtime));
renderPage(document, runtime);
window.app = { runtime };

if (new URLSearchParams(location.search).get("glossa") === "edit") {
  const script = document.createElement("script");
  script.src = CONFIG.overlaySrc;
  script.addEventListener("load", () => {
    window.app.overlay = window.GlossaOverlay!.activate({
      apiBase: CONFIG.apiBase,
      token: () => CONFIG.token,
      tenant: CONFIG.tenant,
      project: CONFIG.project,
      locale: "de",
      runtimes: runtime,
      route: "/checkout",
    });
    document.documentElement.dataset.overlay = "ready";
  });
  document.head.append(script);
}
