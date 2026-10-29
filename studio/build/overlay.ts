/**
 * Studio serves the in-product editor (RFC 0004 §5.1, the delivery layer).
 * `@klarlabs-studio/glossa/overlay`'s bundle is published at `/overlay/v1/overlay.js`, with
 * `/overlay/v1/overlay.json` (`{ version, integrity }`) beside it. Preview
 * deployments pin that `integrity` in their build (`@klarlabs-studio/glossa/unplugin`), the
 * browser checks it, and `crossorigin="anonymous"` means the response needs
 * CORS — which the image's nginx adds for this path only
 * (`studio/docker/templates/conf.d/studio.conf.template`).
 */
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import type { Plugin } from "vite";

/** Where the overlay lives below Studio's origin; `v1` is the loader's contract. */
export const OVERLAY_DIR = "overlay/v1";
export const OVERLAY_JS = `${OVERLAY_DIR}/overlay.js`;
export const OVERLAY_JSON = `${OVERLAY_DIR}/overlay.json`;

export interface OverlayRelease {
  /** `@klarlabs-studio/glossa`'s version (the overlay ships inside it). */
  version: string;
  /** SRI hash of the exact bytes served at `/overlay/v1/overlay.js`. */
  integrity: string;
}

export interface OverlayAssets {
  script: Buffer;
  release: OverlayRelease;
  /** `overlay.json`, as it is served. */
  json: string;
}

/** The overlay bundle from the installed package, and the hash that pins it. */
export function overlayAssets(): OverlayAssets {
  const require = createRequire(import.meta.url);
  const script = readFileSync(require.resolve("@klarlabs-studio/glossa/overlay/bundle"));
  const { version } = JSON.parse(
    readFileSync(require.resolve("@klarlabs-studio/glossa/package.json"), "utf8"),
  ) as { version: string };
  const release: OverlayRelease = {
    version,
    integrity: `sha384-${createHash("sha384").update(script).digest("base64")}`,
  };
  return { script, release, json: `${JSON.stringify(release, null, 2)}\n` };
}

/** Publishes the overlay with the SPA, in `vite build` and in `vite dev`. */
export function overlayDelivery(): Plugin {
  return {
    name: "glossa:overlay-delivery",
    generateBundle() {
      const { script, json } = overlayAssets();
      this.emitFile({ type: "asset", fileName: OVERLAY_JS, source: script });
      this.emitFile({ type: "asset", fileName: OVERLAY_JSON, source: json });
    },
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        const path = (req.url ?? "").split("?")[0]?.replace(/^\//, "");
        if (path !== OVERLAY_JS && path !== OVERLAY_JSON) return next();
        const { script, json } = overlayAssets();
        res.setHeader("Content-Type", path === OVERLAY_JS ? "text/javascript" : "application/json");
        res.setHeader("Access-Control-Allow-Origin", "*");
        res.setHeader("Cross-Origin-Resource-Policy", "cross-origin");
        res.end(path === OVERLAY_JS ? script : json);
      });
    },
  };
}
