/**
 * The fixture app of the `glossa capture` integration tests
 * (platform/internal/cli/capture/browser_integration_test.go and
 * platform/internal/cli/integration_capture_test.go), bundled by
 * `pnpm build:cli` into platform/internal/cli/capture/testdata/app/app.js.
 *
 * It renders a bundled release like an application would: `t()` text, a
 * `<glossa-text>` component host, an attribute, a hidden message, a message
 * only wide viewports show, a dialog a playbook opens, and an element marked
 * `data-glossa-redact`. The query selects the locale (`?lang=`) and the
 * manifest's environment (`?env=production` for the refusal test). Test-only.
 */
import { createRuntime } from "@klarlabs-studio/glossa-runtime";
import type { Artifact, BundledRelease, Manifest, Message } from "@klarlabs-studio/glossa-runtime";
import "@klarlabs-studio/glossa-elements";

const msg = (text: string): Message => ({ type: "message", declarations: [], pattern: [text] });

const catalogs: Record<string, Record<string, string>> = {
  de: {
    "home.title": "Willkommen",
    "cart.checkout": "Zur Kasse",
    "search.placeholder": "Suchen …",
    "hidden.note": "Unsichtbar",
    "desktop.hint": "Nur auf großen Bildschirmen",
    "account.label": "Konto",
    "dialog.body": "Dialoginhalt",
  },
  ja: {
    "home.title": "ようこそ",
    "cart.checkout": "レジへ進む",
  },
};

function release(environment: string): BundledRelease {
  const artifacts: Record<string, Artifact> = {};
  const refs: Manifest["artifacts"] = {};
  for (const [n, [locale, texts]] of Object.entries(catalogs).entries()) {
    // Bundled artifacts are trusted as shipped; the key only has to match the manifest's.
    const sha = String(n + 1).repeat(64);
    const messages = Object.fromEntries(Object.entries(texts).map(([k, v]) => [k, msg(v)]));
    artifacts[sha] = { schema: "glossa.artifact/v1", locale, namespace: "default", messages };
    refs[locale] = { default: { sha256: sha, size: 1 } };
  }
  const manifest: Manifest = {
    schema: "glossa.manifest/v1",
    project: "prj_fixture",
    environment,
    release: { id: "rel_fixture", version: 1, createdAt: "2026-09-19T08:00:00Z" },
    sourceLocale: "de",
    locales: [
      { code: "de", direction: "ltr" },
      { code: "ja", direction: "ltr" },
    ],
    fallback: { "*": ["de"] },
    artifacts: refs,
  };
  return { manifest, artifacts };
}

const query = new URLSearchParams(location.search);
const environment = query.get("env") ?? "preview";
const runtime = createRuntime({
  bundled: release(environment),
  environment,
  locales: query.get("lang") ?? "de",
  storage: null,
});

function render() {
  const $ = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;
  $("title").textContent = runtime.t("home.title");
  $<HTMLInputElement>("search").placeholder = runtime.t("search.placeholder");
  $("hidden").textContent = runtime.t("hidden.note");
  $("desktop").textContent = runtime.t("desktop.hint");
  $("account-label").textContent = runtime.t("account.label");
  $("dialog").textContent = runtime.t("dialog.body");
  const provider = $("provider") as HTMLElement & { runtime?: unknown };
  if (provider.runtime !== runtime) provider.runtime = runtime;
}

function start() {
  render();
  document.getElementById("open")!.addEventListener("click", () => {
    document.getElementById("dialog")!.hidden = false;
  });
}

runtime.subscribe(render);
if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", start);
else start();
