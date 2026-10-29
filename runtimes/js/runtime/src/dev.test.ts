// @vitest-environment jsdom
/**
 * The overlay loader's runtime guard (RFC 0004 §5.1): the page's runtimes
 * list themselves outside production, and the loader starts a session only
 * after a gesture and only when every runtime has loaded a release whose
 * manifest environment isn't production. jsdom doesn't fetch scripts, so
 * the tests play the overlay script's part: register the module, fire `load`.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { LOADER_ATTRIBUTE, OVERLAY_PATH, loadOverlay, pageRuntimes, refusal } from "./dev.js";
import type { OverlayLoader, OverlayModule } from "./dev.js";
import { createRuntime } from "./runtime.js";
import type { Runtime } from "./runtime.js";
import { release, text } from "./testing/release.js";

const STUDIO = "https://studio.glossa.test";
const INTEGRITY = `sha384-${"A".repeat(64)}`;
const config = { studio: STUDIO, integrity: INTEGRITY, tenant: "ten_1", project: "prj_1" };

const r = release("rel_1", 1, { de: { "cart.checkout": text("Zur Kasse") } });
const bundle = (environment: string) => ({
  manifest: { ...r.manifest, environment },
  artifacts: r.parsed,
});
const runtime = (environment: string) =>
  createRuntime({ bundled: bundle(environment), environment, locales: "de", storage: null });

const REGISTRY = Symbol.for("glossa.runtimes");
const MODULE = Symbol.for("glossa.overlay");
const globals = globalThis as unknown as Record<symbol, unknown>;

let loader: OverlayLoader | undefined;
let runtimes: Runtime[] = [];

beforeEach(() => {
  history.replaceState(null, "", "/");
  vi.spyOn(console, "warn").mockImplementation(() => {});
});

afterEach(() => {
  loader?.dispose();
  loader = undefined;
  for (const rt of runtimes) rt.dispose();
  runtimes = [];
  delete globals[REGISTRY];
  delete globals[MODULE];
  document.head.replaceChildren();
});

const scripts = () =>
  Array.from(document.querySelectorAll<HTMLScriptElement>(`script[${LOADER_ATTRIBUTE}]`));

/** Play the overlay script: register the module (unless told not to) and fire load or error. */
function serve(outcome: "load" | "error" = "load", register = true) {
  const deactivate = vi.fn();
  const activate = vi.fn<OverlayModule["activate"]>(() => ({ deactivate }));
  if (register) globals[MODULE] = { version: "0.0.0", activate } satisfies OverlayModule;
  const el = scripts().at(-1)!;
  el.dispatchEvent(new Event(outcome));
  return { activate, deactivate };
}

const tick = () => new Promise((resolve) => setTimeout(resolve, 0));

const altShiftE = (init: KeyboardEventInit = {}) =>
  window.dispatchEvent(
    new KeyboardEvent("keydown", { code: "KeyE", altKey: true, shiftKey: true, ...init }),
  );

describe("the runtime registry", () => {
  it("lists every browser runtime, production ones included, until disposed", () => {
    const preview = runtime("preview");
    const production = runtime("production");
    runtimes = [preview, production];
    // `refusal` tells a production page (listed, refused below) from a page
    // without Glossa, and `glossa capture` needs the same distinction.
    expect(pageRuntimes()).toEqual([preview, production]);
    preview.dispose();
    expect(pageRuntimes()).toEqual([production]);
    production.dispose();
    expect(pageRuntimes()).toEqual([]);
  });

  it("reports the active release's environment, from its manifest", () => {
    runtimes = [runtime("preview"), createRuntime({ environment: "preview", storage: null })];
    expect(runtimes[0]!.environment).toBe("preview");
    expect(runtimes[1]!.environment).toBeUndefined();
  });
});

describe("refusal", () => {
  const fake = (environment: string | undefined) => ({ environment }) as Runtime;

  it("refuses without runtimes, without a release, and with any production release", () => {
    expect(refusal([])).toMatch(/no Glossa runtime/);
    expect(refusal([fake(undefined)])).toMatch(/no release/);
    expect(refusal([fake("preview"), fake("production")])).toMatch(/production release/);
    expect(refusal([fake(" Production ")])).toMatch(/production release/);
    expect(refusal([fake("preview"), fake("pr-7")])).toBeUndefined();
  });
});

describe("loadOverlay", () => {
  it("does nothing without a gesture", async () => {
    runtimes = [runtime("preview")];
    loader = loadOverlay(config);
    await tick();
    expect(scripts()).toEqual([]);
    expect(loader.state).toBe("idle");
  });

  it("on ?glossa=edit with a preview release, adds the overlay with its SRI hash and starts a session", async () => {
    runtimes = [runtime("preview")];
    history.replaceState(null, "", "/checkout?glossa=edit");
    loader = loadOverlay(config);
    await vi.waitFor(() => expect(scripts()).toHaveLength(1));
    const el = scripts()[0]!;
    expect(el.type).toBe("module");
    expect(el.src).toBe(`${STUDIO}${OVERLAY_PATH}`);
    expect(el.getAttribute("integrity")).toBe(INTEGRITY);
    expect(el.getAttribute("crossorigin")).toBe("anonymous");
    expect(el.textContent).toBe("");
    const { activate } = serve();
    await vi.waitFor(() => expect(loader!.state).toBe("active"));
    expect(activate).toHaveBeenCalledOnce();
    const options = activate.mock.calls[0]![0];
    expect(options).toMatchObject({
      apiBase: STUDIO,
      studioBase: STUDIO,
      tenant: "ten_1",
      project: "prj_1",
      locale: "de",
    });
    expect(options.runtimes).toEqual(runtimes);

    // The token comes from Studio's authorization popup (grant.ts). Here
    // there is no window.open, so asking says what the person must do —
    // which is the point: the loader hands over a real provider, not a
    // stub that always refuses.
    await expect(options.token()).rejects.toThrow(/pop-ups/);
    expect(typeof options.onAuthFailure).toBe("function");
  });

  it("refuses a production release even with ?glossa=edit", async () => {
    runtimes = [runtime("production")];
    history.replaceState(null, "", "/?glossa=edit");
    loader = loadOverlay(config);
    await vi.waitFor(() => expect(loader!.state).toBe("refused"));
    expect(scripts()).toEqual([]);
    expect(console.warn).toHaveBeenCalledWith(
      expect.stringMatching(/stays off: a runtime serves a production release/),
    );
  });

  it("refuses when any listed runtime serves a production manifest", async () => {
    runtimes = [runtime("preview")];
    const impostor = { ready: Promise.resolve(), environment: "production" } as Runtime;
    (globals[REGISTRY] as Runtime[]).push(impostor);
    loader = loadOverlay(config);
    await expect(loader.start()).resolves.toBe(false);
    expect(loader.reason).toMatch(/production release/);
    expect(scripts()).toEqual([]);
  });

  it("waits for the runtimes' first load before deciding", async () => {
    let settle!: () => void;
    const loading = {
      ready: new Promise<void>((resolve) => (settle = resolve)),
      environment: undefined as string | undefined,
      locale: "de",
    };
    globals[REGISTRY] = [loading];
    loader = loadOverlay(config);
    const started = loader.start();
    await tick();
    expect(loader.state).toBe("checking");
    loading.environment = "preview";
    settle();
    await vi.waitFor(() => expect(scripts()).toHaveLength(1));
    serve();
    await expect(started).resolves.toBe(true);
  });

  it("Alt+Shift+E starts a session and ends it; other chords don't", async () => {
    runtimes = [runtime("preview")];
    loader = loadOverlay(config);
    altShiftE({ ctrlKey: true });
    window.dispatchEvent(new KeyboardEvent("keydown", { code: "KeyE", altKey: true }));
    await tick();
    expect(scripts()).toEqual([]);
    altShiftE();
    await vi.waitFor(() => expect(scripts()).toHaveLength(1));
    const { deactivate } = serve();
    await vi.waitFor(() => expect(loader!.state).toBe("active"));
    altShiftE();
    expect(deactivate).toHaveBeenCalledOnce();
    expect(loader.state).toBe("idle");
    altShiftE(); // the script is loaded already: a new session, no second script
    await vi.waitFor(() => expect(loader!.state).toBe("active"));
    expect(scripts()).toHaveLength(1);
  });

  it("reports a script that fails its integrity check, and tries again on the next gesture", async () => {
    runtimes = [runtime("preview")];
    loader = loadOverlay(config);
    const first = loader.start();
    await vi.waitFor(() => expect(scripts()).toHaveLength(1));
    serve("error", false);
    await expect(first).resolves.toBe(false);
    expect(loader.state).toBe("failed");
    expect(loader.reason).toMatch(/integrity/);
    const second = loader.start();
    await vi.waitFor(() => expect(scripts()).toHaveLength(2));
    serve("load", false);
    await expect(second).resolves.toBe(false);
    expect(loader.reason).toMatch(/registered no overlay/);
  });

  it("uses the api origin when it isn't Studio's", async () => {
    runtimes = [runtime("preview")];
    loader = loadOverlay({ ...config, api: "https://api.glossa.test" });
    const started = loader.start();
    await vi.waitFor(() => expect(scripts()).toHaveLength(1));
    const { activate } = serve();
    await started;
    expect(activate.mock.calls[0]![0].apiBase).toBe("https://api.glossa.test");
  });

  it("is inert where there is no document (server rendering)", async () => {
    vi.stubGlobal("document", undefined);
    const inert = loadOverlay(config);
    vi.unstubAllGlobals();
    await expect(inert.start()).resolves.toBe(false);
    expect(inert.state).toBe("idle");
  });
});
