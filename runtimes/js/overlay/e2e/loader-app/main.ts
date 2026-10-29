/**
 * The preview deployment the loader tests build with Vite and
 * `@klarlabs-studio/glossa-unplugin`: a page on `@klarlabs-studio/glossa-runtime` and `@klarlabs-studio/glossa-elements`
 * whose bundled release and Glossa environment the test defines. Nothing
 * here knows about the overlay — the plugin adds its loader.
 */
import "@klarlabs-studio/glossa-elements";
import { createRuntime } from "@klarlabs-studio/glossa-runtime";
import type { BundledRelease, Runtime } from "@klarlabs-studio/glossa-runtime";

/** Both from the test's Vite `define`. */
declare const GLOSSA_ENVIRONMENT: string;
declare const GLOSSA_RELEASE: BundledRelease;

const runtime = createRuntime({
  bundled: GLOSSA_RELEASE,
  environment: GLOSSA_ENVIRONMENT,
  locales: "de",
  storage: null,
});

const $ = (id: string) => document.getElementById(id)!;

function render() {
  $("pay").textContent = runtime.t("checkout.pay");
  $("save-1").textContent = runtime.t("profile.save");
  const provider = $("provider") as HTMLElement & { runtime?: Runtime };
  if (provider.runtime !== runtime) provider.runtime = runtime;
}

runtime.subscribe(render);
render();
// e2e/app.ts declares window.app for the panel tests; this fixture only sets it.
(window as unknown as { app: { runtime: Runtime } }).app = { runtime };
