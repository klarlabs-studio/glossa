// Runs in plain Node (see vitest.config.ts): no window, no document.
import { describe, expect, it, vi } from "vitest";
import { createSSRApp, defineComponent, h } from "vue";
import { renderToString } from "vue/server-renderer";
import { createRuntime } from "@klarlabs-studio/glossa-runtime";

import { GlossaText, createGlossa, useGlossa } from "./index.js";
import { App, r1 } from "./testing/app.js";

const server = (locale: string) =>
  createGlossa({ bundled: r1, locales: locale, storage: null, bidiIsolation: "none" });

describe("server rendering", () => {
  it("imports and renders without browser globals", async () => {
    expect("window" in globalThis || "document" in globalThis).toBe(false);
    const html = await renderToString(createSSRApp(App).use(server("de")));
    expect(html).toBe(
      '<main lang="de" dir="ltr">' +
        "<h1>Zur Kasse</h1>" +
        "<p><!--[-->Lies die <b>AGB</b>.<!--]--></p>" +
        "<p>Hallo, Lina!</p>" +
        "<p><!--[-->Standard<!--]--></p>" +
        "<p>Zur Kasse</p>" +
        "</main>",
    );
  });

  it("renders each request in its own locale", async () => {
    const [de, ar] = await Promise.all([
      renderToString(createSSRApp(App).use(server("de"))),
      renderToString(createSSRApp(App).use(server("ar-EG"))),
    ]);
    expect(de).toContain("<h1>Zur Kasse</h1>");
    expect(ar).toContain('<main lang="ar" dir="rtl"><h1>الدفع</h1>');
  });

  it("doesn't subscribe to a shared runtime on the server, so requests don't leak listeners", async () => {
    const runtime = createRuntime({ bundled: r1, locales: "de", storage: null });
    const subscribe = vi.spyOn(runtime, "subscribe");
    for (let i = 0; i < 3; i++) {
      await renderToString(createSSRApp(App).use(createGlossa({ runtime })));
    }
    expect(subscribe).not.toHaveBeenCalled();
  });

  it("falls back to the slot, then the message ID", async () => {
    const Missing = defineComponent({
      setup: () => () => [
        h(GlossaText, { id: "gone" }),
        h(GlossaText, { id: "gone" }, () => "Weg"),
      ],
    });
    const html = await renderToString(createSSRApp(Missing).use(server("de")));
    expect(html).toBe("<!--[-->gone<!--[-->Weg<!--]--><!--]-->");
  });

  it("renders inline defaults with a warning when the plugin isn't installed", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const Bare = defineComponent({
      setup() {
        const { t, locale } = useGlossa();
        return () => h("p", `${t("cart.checkout", {}, { default: "Checkout" })}|${locale.value}`);
      },
    });
    expect(await renderToString(createSSRApp(Bare))).toBe("<p>Checkout|undefined</p>");
    expect(warn).toHaveBeenCalledWith(expect.stringContaining("createGlossa"));
    warn.mockRestore();
  });
});
