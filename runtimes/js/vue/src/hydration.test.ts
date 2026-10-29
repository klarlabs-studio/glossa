// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { createApp, createSSRApp, defineComponent, h, nextTick } from "vue";
import { renderToString } from "vue/server-renderer";
import { createRuntime, memoryStorage } from "@klarlabs-studio/glossa-runtime";

import { createGlossa, useGlossa } from "./index.js";
import { App, r1, r2 } from "./testing/app.js";
import { DELIVERY_KEY, EDGE, fakeEdge } from "./testing/release.js";

async function settle(): Promise<void> {
  for (let i = 0; i < 5; i++) await new Promise((r) => setTimeout(r, 0));
  await nextTick();
}

/** Server-render `App` with the bundled r1, the way an SSR or static build does. */
async function serverHtml(locale = "de"): Promise<HTMLElement> {
  const glossa = createGlossa({ bundled: r1, locales: locale, storage: null });
  const container = document.createElement("div");
  container.innerHTML = await renderToString(createSSRApp(App).use(glossa));
  document.body.append(container);
  return container;
}

/** Console output mentioning hydration, e.g. Vue's "Hydration text mismatch" warnings. */
function hydrationNoise() {
  const seen: string[] = [];
  const record = (...args: unknown[]) => seen.push(args.map(String).join(" "));
  vi.spyOn(console, "warn").mockImplementation(record);
  vi.spyOn(console, "error").mockImplementation(record);
  return () => seen.filter((s) => /hydrat/i.test(s));
}

afterEach(() => {
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

describe("hydration", () => {
  it("hydrates server HTML without mismatches, then shows a newer persisted release", async () => {
    // A returning visitor: an earlier visit persisted r2 from the edge.
    const edge = fakeEdge(() => r2);
    const storage = memoryStorage();
    const config = { edge: EDGE, deliveryKey: DELIVERY_KEY, transport: edge.transport, storage };
    await createRuntime({ ...config, locales: "de", refreshInterval: 0 }).ready;

    const container = await serverHtml();
    const before = container.innerHTML;
    const noise = hydrationNoise();
    const glossa = createGlossa({ ...config, bundled: r1, locales: "de", refreshInterval: 0 });
    createSSRApp(App).use(glossa).mount(container);

    expect(noise()).toEqual([]);
    expect(container.innerHTML).toBe(before);
    await glossa.runtime.ready;
    await settle();
    expect(container.querySelector("h1")!.textContent).toBe("Zur Kasse v2");
    expect(container.textContent).toContain("Neue AGB");
    expect(container.textContent).toContain("Servus, ⁨Lina⁩!");
  });

  it("re-renders on a locale switch, with lang and dir", async () => {
    const container = await serverHtml();
    const glossa = createGlossa({ bundled: r1, locales: "de", storage: null });
    const noise = hydrationNoise();
    const app = createSSRApp(App).use(glossa);
    app.mount(container);
    expect(noise()).toEqual([]);
    const Switch = defineComponent({
      setup() {
        const g = useGlossa();
        return () => h("button", { onClick: () => g.setLocale("ar") }, g.locale.value);
      },
    });
    const host = document.createElement("div");
    document.body.append(host);
    const other = createApp(Switch).use(createGlossa({ runtime: glossa.runtime }));
    other.mount(host);
    host.querySelector("button")!.click();
    await settle();
    const main = container.querySelector("main")!;
    expect([main.lang, main.dir, main.querySelector("h1")!.textContent]).toEqual([
      "ar",
      "rtl",
      "الدفع",
    ]);
    expect(host.textContent).toBe("ar");
    app.unmount();
    other.unmount();
  });

  it("stops listening and disposes the runtime it created on unmount; leaves a given one running", async () => {
    const own = createGlossa({ bundled: r1, locales: "de", storage: null });
    const shared = createRuntime({ bundled: r1, locales: "de", storage: null });
    const disposeOwn = vi.spyOn(own.runtime, "dispose");
    const disposeShared = vi.spyOn(shared, "dispose");
    for (const glossa of [own, createGlossa({ runtime: shared })]) {
      const host = document.createElement("div");
      document.body.append(host);
      const app = createApp(App).use(glossa);
      app.mount(host);
      app.unmount();
    }
    expect(disposeOwn).toHaveBeenCalledOnce();
    expect(disposeShared).not.toHaveBeenCalled();
  });
});
