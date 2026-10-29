// @vitest-environment node
/**
 * Capture code never reaches a production bundle (RFC 0004 §5.1): an app
 * built on the runtime and every component package contains none of it, and
 * the capture package itself tree-shakes.
 * The size budgets (RFC 0005 §5.1) are measured on @klarlabs-studio/glossa/capture, in the umbrella package.
 */
import { describe, expect, it } from "vitest";
import { build } from "esbuild";

async function bundle(contents: string): Promise<string> {
  const out = await build({
    stdin: { contents, resolveDir: import.meta.dirname, loader: "ts" },
    bundle: true,
    minify: true,
    format: "esm",
    write: false,
    platform: "browser",
    external: ["vue", "react", "react-dom", "lit", "@lit/context"],
    logLevel: "silent",
  });
  return out.outputFiles[0]!.text;
}

/** Things only capture code has: the start mark, range geometry, the session API. */
const CAPTURE = [/\u2063|\\u2063/, /getClientRects/, /startCapture|collectRegions|stripMarkers/];
/** Things only the visual probe pass has (RFC 0005 \u00a75.2). */
const PROBES = /glossa\.finding\/v1|untranslated-on-screen|text-clipped/;

describe("tree-shaking", () => {
  it("an app on @klarlabs-studio/glossa-runtime and the components has no capture code", async () => {
    const js = await bundle(`
      import { createRuntime } from "@klarlabs-studio/glossa-runtime";
      import { GlossaText } from "@klarlabs-studio/glossa-elements";
      import { createGlossa as vue, GlossaText as VueText } from "@klarlabs-studio/glossa-vue";
      import { createGlossa as react, T } from "@klarlabs-studio/glossa-react";
      const rt = createRuntime({ locales: "de" });
      console.log(rt.t("x"), GlossaText, vue({ runtime: rt }), VueText, react({ runtime: rt }), T);
    `);
    expect(js).toContain("onRender"); // the runtime's extension point is there…
    for (const re of CAPTURE) expect(js).not.toMatch(re); // …the capture module isn't.
  });

  it("the capture package tree-shakes: markers alone don't pull in the capture script", async () => {
    const js = await bundle(`import { strip } from "@klarlabs-studio/glossa-capture"; console.log(strip("x"));`);
    expect(js).not.toMatch(/getClientRects|onRender/);
    const all = await bundle(`import { startCapture } from "@klarlabs-studio/glossa-capture"; console.log(startCapture);`);
    expect(all).toMatch(/getClientRects/);
  });

  /**
   * The probe pass is given to a session, never imported by it, so a bundle
   * that measures nothing — `@klarlabs-studio/glossa-overlay`, the in-product editor served to
   * end users — doesn't carry it (RFC 0005 §5.1).
   */
  it("a session without the probe pass leaves ./probes out; asking for it brings it in", async () => {
    const session = await bundle(`
      import { startCapture } from "@klarlabs-studio/glossa-capture";
      console.log(startCapture([]).collect());
    `);
    expect(session).toMatch(/getClientRects/); // the capture script is there…
    expect(session).not.toMatch(PROBES); // …the probes aren't.

    const probing = await bundle(`
      import { startCapture } from "@klarlabs-studio/glossa-capture";
      import { probe } from "@klarlabs-studio/glossa-capture/probes";
      console.log(startCapture([], { probe }).collect());
    `);
    expect(probing).toMatch(PROBES);
  });
});
