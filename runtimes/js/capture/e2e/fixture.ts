/**
 * The browser side of the capture e2e tests, bundled by esbuild into the
 * fixture page: two runtimes (a German page and an Arabic island), a page
 * that renders with whichever `t()` it's given, and the capture session.
 */
import { createRuntime } from "@klarlabs-studio/glossa-runtime";
import type { BundledRelease, Runtime } from "@klarlabs-studio/glossa-runtime";
import "@klarlabs-studio/glossa-elements";

import { startCapture } from "../src/index.js";
import type { CaptureSession, SessionCapture } from "../src/index.js";
import { probe } from "../src/probes.js";

declare global {
  interface Window {
    release: BundledRelease;
    fixture: Fixture;
  }
}

export interface Probe {
  id: string;
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface Fixture {
  render(): void;
  start(): void;
  stop(): void;
  collect(): SessionCapture;
  /** Every probed element's and text run's box, for the layout comparison. */
  probes(): Probe[];
  hooked(): boolean[];
}

const options = { bundled: window.release, environment: "preview", storage: null };
const de: Runtime = createRuntime({ ...options, locales: "de" });
const ar: Runtime = createRuntime({ ...options, locales: "ar" });
let session: CaptureSession | undefined;

const $ = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;

function render() {
  $("save-1").textContent = de.t("profile.save");
  $("save-2").textContent = de.t("settings.save");
  $("save-3").textContent = de.t("draft.save");
  $("total").textContent = de.t("cart.total", { amount: 1234.5 });
  $<HTMLInputElement>("search").placeholder = de.t("search.placeholder");
  $("hint").title = de.t("search.hint");
  $<HTMLImageElement>("logo").alt = de.t("logo.alt");
  $<HTMLInputElement>("submit").value = de.t("form.submit");
  $("hidden-display").textContent = de.t("hidden.note");
  $("hidden-visibility").textContent = de.t("hidden.note");
  $("offscreen").textContent = de.t("offscreen.note");
  $("clipped").textContent = de.t("offscreen.note");
  $("greeting").textContent = de.t("greeting", { name: de.t("user.name") });
  $("long").textContent = de.t("long.text");
  $("rtl-save").textContent = ar.t("profile.save");
  $("rtl-long").textContent = ar.t("long.text");
  $("inline").firstChild!.textContent = `Vorher ${de.t("user.name")} nachher`;
  const provider = $("provider") as HTMLElement & { runtime?: Runtime };
  if (provider.runtime !== de) provider.runtime = de;
}

function probes(): Probe[] {
  const out: Probe[] = [];
  for (const el of Array.from(document.querySelectorAll<HTMLElement>("[data-probe]"))) {
    const r = el.getBoundingClientRect();
    out.push({ id: el.id, x: r.x, y: r.y, width: r.width, height: r.height });
    const range = document.createRange();
    range.selectNodeContents(el);
    const t = range.getBoundingClientRect();
    out.push({ id: `${el.id} text`, x: t.x, y: t.y, width: t.width, height: t.height });
  }
  return out;
}

window.fixture = {
  render,
  start: () => {
    session = startCapture([de, ar], { probe });
    render();
  },
  stop: () => session?.stop(),
  collect: () => session!.collect(),
  probes,
  hooked: () => [de.hooked, ar.hooked],
};
