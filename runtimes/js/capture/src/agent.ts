/**
 * The page side of `glossa capture` (RFC 0004 §3.2, §10). The CLI bundles
 * this module into one script (`pnpm build:cli`, embedded in the glossa
 * binary) and injects it before the page's own scripts run:
 *
 * - `install()` puts the page's runtime registry
 *   (`globalThis[Symbol.for("glossa.runtimes")]`, which every `createRuntime()`
 *   adds itself to) in place before the page's scripts run, with a `push` that
 *   hooks each runtime into one capture session from its first render.
 * - The CLI then drives `globalThis.__glossaCapture`: `settle()` until the
 *   page is quiet, `status()` to refuse a page whose runtime reports a
 *   `production` manifest, and `collect()` for the regions, with every
 *   `data-glossa-redact` element blacked out before the screenshot.
 *
 * Nothing here is imported by an application: the module isn't reachable
 * from the package's entry point.
 */
import type { Runtime } from "@klarlabs-studio/glossa-runtime";

import { probe } from "./probes.js";
import type { ProbeOptions } from "./probes.js";
import type { Box } from "./regions.js";
import { startCapture } from "./session.js";
import type { SessionCapture } from "./session.js";

/** What the page's runtimes report once their first load settled. */
export interface PageStatus {
  runtimes: number;
  /** Each runtime's active manifest environment; null before a release is active. */
  environments: Array<string | null>;
  /** Each runtime's active locale; null before a release is active. */
  locales: Array<string | null>;
}

/** A capture's regions and probes plus the page's size and what was blacked out. */
export interface PageCapture extends SessionCapture {
  /** The document's size in CSS pixels: what a full-page screenshot covers. */
  width: number;
  height: number;
  /** The blacked-out boxes of `data-glossa-redact` elements, page coordinates. */
  redacted: Box[];
}

export interface Agent {
  status(): Promise<PageStatus>;
  /** Resolves once fonts are loaded and the DOM saw no mutation for `quiet` ms (at most `max` ms). */
  settle(quiet?: number, max?: number): Promise<void>;
  /** `options` carries the source capture's `metrics` as the baseline, where the CLI has one. */
  collect(options?: ProbeOptions): PageCapture;
}

type Global = typeof globalThis & {
  [REGISTRY]?: Runtime[];
  __glossaCapture?: Agent;
};

/** The page's runtime registry, as `@klarlabs-studio/glossa-runtime` names it. */
const REGISTRY: unique symbol = Symbol.for("glossa.runtimes") as never;

/** The attribute that marks an element to black out (RFC 0004 §10). */
export const REDACT = "data-glossa-redact";

// Inside a redacted element everything paints black, even when the page
// reflows for the full-page screenshot; the overlays cover what overflows.
const REDACT_CSS =
  `[${REDACT}],[${REDACT}] *{color:#000!important;background:#000!important;` +
  `border-color:#000!important;text-shadow:none!important;box-shadow:none!important;` +
  `outline-color:#000!important;caret-color:#000!important}` +
  `[${REDACT}] :is(img,video,canvas,svg,picture,iframe,object,embed),` +
  `:is(img,video,canvas,svg,iframe,object,embed)[${REDACT}]{filter:brightness(0)!important}`;

const frame = () =>
  new Promise<void>((resolve) => {
    // Headless pages may throttle animation frames; a timer bounds the wait.
    const t = setTimeout(resolve, 100);
    requestAnimationFrame(() => (clearTimeout(t), resolve()));
  });

/** Every element matching `selector` under `root`, open shadow roots included. */
function deepQuery(root: Document | ShadowRoot, selector: string): Element[] {
  const out = Array.from(root.querySelectorAll(selector));
  for (const el of Array.from(root.querySelectorAll("*"))) {
    if (el.shadowRoot) out.push(...deepQuery(el.shadowRoot, selector));
  }
  return out;
}

const inside = (b: Box, r: Box) => {
  const x = b.x + b.width / 2;
  const y = b.y + b.height / 2;
  return x >= r.x && x <= r.x + r.width && y >= r.y && y <= r.y + r.height;
};

/** Black out every `data-glossa-redact` element and return the boxes covered. */
function redact(doc: Document): Box[] {
  const style = doc.createElement("style");
  style.textContent = REDACT_CSS;
  doc.documentElement.append(style);
  const boxes: Box[] = [];
  for (const el of deepQuery(doc, `[${REDACT}]`)) {
    const r = el.getBoundingClientRect();
    if (r.width <= 0 || r.height <= 0) continue;
    const box = { x: r.left + scrollX, y: r.top + scrollY, width: r.width, height: r.height };
    const cover = doc.createElement("div");
    cover.setAttribute("data-glossa-redaction", "");
    cover.style.cssText =
      `position:absolute;left:${box.x}px;top:${box.y}px;width:${box.width}px;height:${box.height}px;` +
      "background:#000;z-index:2147483647;pointer-events:none;margin:0;padding:0;border:0";
    doc.documentElement.append(cover);
    boxes.push(box);
  }
  return boxes;
}

/**
 * Put the runtime registry and the agent on `g` (the page's global). Runtimes
 * created before this call are picked up too, so injection order can't lose
 * one. Idempotent: a second call returns the first agent.
 */
export function install(g: Global = globalThis as Global): Agent {
  if (g.__glossaCapture) return g.__glossaCapture;
  const runtimes: Runtime[] = (g[REGISTRY] ??= []);
  // The agent is the surface that measures quality, so it is the surface that
  // carries the probe pass (RFC 0005 §5.1).
  const session = startCapture([], { probe });
  runtimes.forEach((rt) => session.add(rt));
  Object.defineProperty(runtimes, "push", {
    configurable: true,
    value: (...added: Runtime[]) => {
      added.forEach((rt) => session.add(rt));
      return Array.prototype.push.apply(runtimes, added);
    },
  });
  const agent: Agent = {
    async status() {
      await Promise.all(runtimes.map((rt) => rt.ready));
      return {
        runtimes: runtimes.length,
        environments: runtimes.map((rt) => rt.environment ?? null),
        locales: runtimes.map((rt) => rt.locale ?? null),
      };
    },
    async settle(quiet = 300, max = 5000) {
      await document.fonts?.ready;
      await new Promise<void>((resolve) => {
        let timer: ReturnType<typeof setTimeout>;
        const done = () => {
          observer.disconnect();
          clearTimeout(timer);
          clearTimeout(hard);
          resolve();
        };
        const observer = new MutationObserver(() => {
          clearTimeout(timer);
          timer = setTimeout(done, quiet);
        });
        observer.observe(document, { subtree: true, childList: true, attributes: true, characterData: true });
        timer = setTimeout(done, quiet);
        const hard = setTimeout(done, max);
      });
      await frame();
      await frame();
    },
    collect(options) {
      // The probes run before the redaction overlays go in: they measure the
      // page as it laid out, not as it is blacked out for the screenshot.
      const { renders, regions, probes, metrics } = session.collect(document, options);
      const redacted = redact(document);
      for (const r of regions) {
        if (r.visible && redacted.some((box) => inside(r.box, box))) r.visible = false;
      }
      const el = document.documentElement;
      const width = Math.ceil(Math.max(el.scrollWidth, document.body?.scrollWidth ?? 0));
      const height = Math.ceil(Math.max(el.scrollHeight, document.body?.scrollHeight ?? 0));
      return { renders, regions, probes, metrics, width, height, redacted };
    },
  };
  g.__glossaCapture = agent;
  return agent;
}
