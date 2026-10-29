// @vitest-environment jsdom
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { StrictMode, act, createElement as h } from "react";
import { createRoot, hydrateRoot } from "react-dom/client";
import { renderToString } from "react-dom/server";
import { createRuntime, memoryStorage } from "@klarlabs-studio/glossa-runtime";

import { GlossaProvider, createGlossa, useGlossa } from "./index.js";
import { Root, r1, r2 } from "./testing/app.js";
import { DELIVERY_KEY, EDGE, fakeEdge } from "./testing/edge.js";
import { serving } from "./testing/release.js";

beforeAll(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
});

/** Let loads, activations and React's updates settle. */
async function settle(): Promise<void> {
  await act(async () => {
    for (let i = 0; i < 5; i++) await new Promise((r) => setTimeout(r, 0));
  });
}

/** Server-render `Root` with the bundled r1, the way an SSR or static build does. */
function serverHtml(locale = "de"): HTMLElement {
  const glossa = createGlossa({ bundled: r1, locales: locale, storage: null });
  const container = document.createElement("div");
  container.innerHTML = renderToString(h(Root, { glossa }));
  document.body.append(container);
  return container;
}

/** Console output and recoverable errors mentioning hydration, e.g. React's mismatch errors. */
function hydrationNoise() {
  const seen: string[] = [];
  const record = (...args: unknown[]) => seen.push(args.map(String).join(" "));
  vi.spyOn(console, "warn").mockImplementation(record);
  vi.spyOn(console, "error").mockImplementation(record);
  return {
    onRecoverableError: (e: unknown) => record("recoverable:", e),
    noise: () => seen.filter((s) => /hydrat|recoverable|match/i.test(s)),
  };
}

afterEach(() => {
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

describe("hydration", () => {
  it("hydrates server HTML without mismatches while a newer persisted release is waiting, then shows it", async () => {
    // A returning visitor: an earlier visit persisted r2 from the edge.
    const edge = fakeEdge(serving(r2));
    const storage = memoryStorage();
    const config = { edge: EDGE, deliveryKey: DELIVERY_KEY, transport: edge.transport, storage };
    await createRuntime({ ...config, locales: "de", refreshInterval: 0 }).ready;

    const container = serverHtml();
    const h1 = container.querySelector("h1");
    const { onRecoverableError, noise } = hydrationNoise();
    const glossa = createGlossa({ ...config, bundled: r1, locales: "de", refreshInterval: 0 });
    // r2 activates before React hydrates: the hydrating render must still match r1.
    await glossa.runtime.ready;
    expect(glossa.runtime.release?.id).toBe("rel_2");
    const root = await act(async () =>
      hydrateRoot(container, h(StrictMode, null, h(Root, { glossa })), { onRecoverableError }),
    );
    await settle();

    expect(noise()).toEqual([]);
    // Hydration adopted the server's nodes instead of re-rendering the tree.
    expect(container.querySelector("h1")).toBe(h1);
    expect(h1!.textContent).toBe("Zur Kasse v2");
    expect(container.textContent).toContain("Neue AGB");
    expect(container.textContent).toContain("Servus, ⁨Lina⁩!");
    act(() => root.unmount());
  });

  it("hydrates, then re-renders on a locale switch from another root sharing the runtime, with lang and dir", async () => {
    const container = serverHtml();
    const glossa = createGlossa({ bundled: r1, locales: "de", storage: null });
    const { onRecoverableError, noise } = hydrationNoise();
    const app = await act(async () =>
      hydrateRoot(container, h(Root, { glossa }), { onRecoverableError }),
    );
    expect(noise()).toEqual([]);

    const Switch = () => {
      const g = useGlossa();
      return h("button", { onClick: () => void g.setLocales("ar") }, g.locale);
    };
    const host = document.createElement("div");
    document.body.append(host);
    const other = createRoot(host);
    act(() =>
      other.render(
        h(GlossaProvider, { glossa: createGlossa({ runtime: glossa.runtime }) }, h(Switch)),
      ),
    );
    act(() => host.querySelector("button")!.click());
    await settle();

    const main = container.querySelector("main")!;
    expect([main.lang, main.dir, main.querySelector("h1")!.textContent]).toEqual([
      "ar",
      "rtl",
      "الدفع",
    ]);
    expect(host.textContent).toBe("ar");
    act(() => {
      app.unmount();
      other.unmount();
    });
  });

  it("subscribes only while mounted, and leaves the runtime running", () => {
    const glossa = createGlossa({ bundled: r1, locales: "de", storage: null });
    const offs: Array<ReturnType<typeof vi.fn>> = [];
    const subscribe = glossa.runtime.subscribe.bind(glossa.runtime);
    vi.spyOn(glossa.runtime, "subscribe").mockImplementation((f) => {
      const off = vi.fn(subscribe(f));
      offs.push(off);
      return off;
    });
    const dispose = vi.spyOn(glossa.runtime, "dispose");
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    act(() => root.render(h(StrictMode, null, h(Root, { glossa }))));
    expect(host.querySelector("h1")!.textContent).toBe("Zur Kasse");
    expect(offs.length).toBeGreaterThan(0);
    act(() => root.unmount());
    expect(offs.every((off) => off.mock.calls.length === 1)).toBe(true);
    expect(dispose).not.toHaveBeenCalled();
  });
});
