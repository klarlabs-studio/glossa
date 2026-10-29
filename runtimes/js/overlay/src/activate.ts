/**
 * Starting an editor session on a page (RFC 0004 §5): a capture session
 * marks what the page's runtimes render, Alt+click (or Alt+Enter on a
 * focused element) finds the message under it, and `<glossa-overlay>`
 * opens on it. The loader (`@klarlabs-studio/glossa-runtime/dev`) decides whether a page
 * may have a session at all — never production — and calls this; the popup
 * that mints the token is a later slice, so this takes a token provider as
 * given.
 */
import { startCapture } from "@klarlabs-studio/glossa-capture";
import type { CaptureSession } from "@klarlabs-studio/glossa-capture";
import type { Message as Model, Runtime } from "@klarlabs-studio/glossa-runtime";

import { OverlayApi } from "./api.js";
import type { ApiOptions, InContext } from "./api.js";
import { GlossaOverlay, deepActive } from "./overlay.js";
import type { OverlayHost } from "./overlay.js";
import { caretAt, locate } from "./target.js";
import type { Target } from "./target.js";

export interface ActivateOptions extends ApiOptions {
  /** The locale being edited (the page's target locale). */
  locale: string;
  /** The page's runtimes; several for independent islands. */
  runtimes: Runtime | readonly Runtime[];
  /** Studio's origin for "Open in Studio". Default: the API's origin (Studio serves `/v1`). */
  studioBase?: string;
  /** The route pattern edits record in `origin_detail.in_context`. Default: `location.pathname`. */
  route?: string | (() => string);
  /** Default: the global document. */
  document?: Document;
}

export interface Overlay {
  readonly element: GlossaOverlay;
  readonly session: CaptureSession;
  /** Open the panel on a message, as an Alt+click would. */
  open(target: Pick<Target, "id"> & Partial<Target>): Promise<void>;
  /** End the session: close the panel, drop previews, remove the markers and the listeners. */
  deactivate(): void;
}

/** The composed path from `el` up, through shadow hosts, like an event's. */
function pathOf(el: Element | undefined): Element[] {
  const out: Element[] = [];
  for (let n: Node | null = el ?? null; n; n = n.parentNode ?? (n as ShadowRoot).host ?? null) {
    if (n instanceof Element) out.push(n);
  }
  return out;
}

export function activate(o: ActivateOptions): Overlay {
  const doc = o.document ?? document;
  const win = doc.defaultView ?? window;
  const runtimes: readonly Runtime[] = Array.isArray(o.runtimes)
    ? o.runtimes
    : [o.runtimes as Runtime];
  const api = new OverlayApi(o);
  const studio = (o.studioBase ?? new URL(o.apiBase, win.location.href).origin).replace(/\/+$/, "");
  const previewed = new Set<string>();
  const seg = encodeURIComponent;

  const host: OverlayHost = {
    api,
    locale: o.locale,
    studioUrl: (key) =>
      `${studio}/t/${seg(o.tenant)}/p/${seg(o.project)}/translate?locale=${seg(o.locale)}&key=${seg(key)}`,
    inContext: (): InContext => ({
      route: typeof o.route === "function" ? o.route() : (o.route ?? win.location.pathname),
      viewport: { width: win.innerWidth, height: win.innerHeight },
    }),
    preview(id: string, model?: Model) {
      for (const rt of runtimes) rt.override(id, o.locale, model);
      if (model) previewed.add(id);
      else previewed.delete(id);
    },
    wait: (ms) => new Promise((resolve) => win.setTimeout(resolve, ms)),
  };

  const session = startCapture(runtimes);
  const element = doc.createElement("glossa-overlay") as GlossaOverlay;
  element.host = host;
  doc.body.append(element);

  const openAt = (e: Event, target: Target | undefined) => {
    if (!target) return;
    e.preventDefault();
    e.stopPropagation();
    void element.open(target);
  };

  const onClick = (e: MouseEvent) => {
    if (!e.altKey || e.button !== 0) return;
    const path = e.composedPath();
    if (path.includes(element)) return;
    openAt(e, locate(session.renders, path, caretAt(doc, e.clientX, e.clientY)));
  };

  // The keyboard way in: Alt+Enter on a focused control or link that shows a message.
  const onKey = (e: KeyboardEvent) => {
    if (!e.altKey || e.key !== "Enter") return;
    const active = deepActive(doc);
    if (!active || pathOf(active).includes(element)) return;
    openAt(e, locate(session.renders, pathOf(active)));
  };

  doc.addEventListener("click", onClick, true);
  doc.addEventListener("keydown", onKey, true);

  let active = true;
  return {
    element,
    session,
    open: (target) => element.open(target),
    deactivate() {
      if (!active) return;
      active = false;
      doc.removeEventListener("click", onClick, true);
      doc.removeEventListener("keydown", onKey, true);
      element.close();
      for (const id of [...previewed]) host.preview(id);
      element.remove();
      session.stop();
    },
  };
}

export { GlossaOverlay };
