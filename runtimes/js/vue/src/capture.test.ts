// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";
import { createApp, createSSRApp, nextTick } from "vue";
import { renderToString } from "vue/server-renderer";

import { createGlossa } from "./index.js";
import { App, r1 } from "./testing/app.js";

afterEach(() => {
  document.body.innerHTML = "";
});

const marks = (root: Element) =>
  Array.from(root.querySelectorAll("[data-glossa-id]")).map((el) => [
    el.tagName,
    el.getAttribute("data-glossa-id"),
    el.getAttribute("data-glossa-locale"),
    (el as HTMLElement).style.display,
    el.textContent,
  ]);

describe("capture mode (RFC 0004 §3.1)", () => {
  it("marks <GlossaText> hosts and lets the hook decorate t() only while a hook is installed", async () => {
    const glossa = createGlossa({ bundled: r1, locales: "de", storage: null });
    const container = document.createElement("div");
    document.body.append(container);
    createApp(App).use(glossa).mount(container);
    const plain = container.innerHTML;
    expect(marks(container)).toEqual([]);

    const off = glossa.runtime.onRender((r) => `[${r.output}]`);
    await nextTick();
    expect(marks(container)).toEqual([
      ["SPAN", "terms.hint", "de", "contents", "Lies die AGB."],
      ["SPAN", "athlete.greeting", "de", "contents", "Hallo, \u2068Lina\u2069!"],
      ["SPAN", "no.such.key", null, "contents", "Standard"],
    ]);
    expect(container.querySelector("h1")!.textContent).toBe("[Zur Kasse]");
    expect(container.querySelector("b")!.textContent).toBe("AGB");

    off();
    await nextTick();
    expect(container.innerHTML).toBe(plain);
  });

  it("server-renders no capture markup outside a session", async () => {
    const glossa = createGlossa({ bundled: r1, locales: "de", storage: null });
    const html = await renderToString(createSSRApp(App).use(glossa));
    expect(html).not.toContain("data-glossa");
  });
});
