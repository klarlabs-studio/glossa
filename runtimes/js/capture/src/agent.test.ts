/**
 * The page side of `glossa capture` in jsdom: the runtime registry, status,
 * settling and the collected document. Geometry and the black-out are
 * checked in a real browser by the CLI's integration test.
 */
import { afterEach, describe, expect, it } from "vitest";
import { createRuntime } from "@klarlabs-studio/glossa-runtime";

import { REDACT, install } from "./agent.js";
import { hasMarkers } from "./markers.js";
import { fixture } from "./testing/release.js";
import { schemaErrors } from "./testing/schema.js";

afterEach(() => {
  const g = globalThis as Record<string | symbol, unknown>;
  delete g[Symbol.for("glossa.runtimes")];
  delete g.__glossaCapture;
  document.body.innerHTML = "";
  document.querySelectorAll("style, [data-glossa-redaction]").forEach((e) => e.remove());
});

describe("install", () => {
  it("hooks every runtime created afterwards from its first render, and is idempotent", () => {
    const agent = install();
    expect(install()).toBe(agent);
    const rt = createRuntime({ bundled: fixture, locales: "de", environment: "preview", storage: null });
    expect(rt.hooked).toBe(true);
    expect(hasMarkers(rt.t("profile.save"))).toBe(true);
  });

  it("reports each runtime's environment and locale once they're ready", async () => {
    const agent = install();
    expect(await agent.status()).toEqual({ runtimes: 0, environments: [], locales: [] });
    createRuntime({ bundled: fixture, locales: "ar", environment: "preview", storage: null });
    // A production runtime can't activate a preview manifest: nothing is active.
    createRuntime({ bundled: fixture, locales: "de", storage: null });
    expect(await agent.status()).toEqual({
      runtimes: 2,
      environments: ["preview", null],
      locales: ["ar", null],
    });
  });

  it("settles when the DOM is quiet", async () => {
    const agent = install();
    const start = Date.now();
    setTimeout(() => document.body.append("late"), 20);
    await agent.settle(50, 2000);
    expect(document.body.textContent).toBe("late");
    expect(Date.now() - start).toBeGreaterThanOrEqual(60);
  });

  it("collects regions valid against captures.v1, blacks out redacted elements and keeps the page size", () => {
    const agent = install();
    const rt = createRuntime({ bundled: fixture, locales: "de", environment: "preview", storage: null });
    document.body.innerHTML = `<p id="a"></p><div ${REDACT}><span id="b"></span></div>`;
    document.getElementById("a")!.textContent = rt.t("profile.save");
    document.getElementById("b")!.textContent = rt.t("user.name");
    const c = agent.collect();
    expect(schemaErrors(c)).toEqual([]);
    expect(c.renders.map((r) => r.key)).toEqual(["profile.save", "user.name"]);
    expect(c).toMatchObject({ width: expect.any(Number), height: expect.any(Number), redacted: [] });
    const style = [...document.querySelectorAll("style")].map((s) => s.textContent).join("");
    expect(style).toContain(`[${REDACT}]`);
  });
});
