/**
 * The script Studio serves at `/overlay/v1/overlay.js` (RFC 0004 §5.1),
 * bundled with Lit and `@klarlabs-studio/glossa-capture` by `scripts/bundle.mjs` into
 * `dist/bundle/overlay.js`. Running it defines `<glossa-overlay>` and hands
 * `activate` to the overlay loader (`@klarlabs-studio/glossa-runtime/dev`), which added this
 * script with its SRI hash after checking the page's runtimes. It's a
 * module script, so the handover goes through a well-known symbol rather
 * than exports.
 */
import type { OverlayModule } from "@klarlabs-studio/glossa-runtime/dev";

import { activate } from "./activate.js";

/** Replaced by the bundler with the package's version. */
declare const OVERLAY_VERSION: string;

export const overlay = {
  version: typeof OVERLAY_VERSION === "string" ? OVERLAY_VERSION : "0.0.0-dev",
  activate,
} satisfies OverlayModule;

(globalThis as Record<symbol, unknown>)[Symbol.for("glossa.overlay")] = overlay;
