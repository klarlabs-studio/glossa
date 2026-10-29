// Runs in plain Node (see vitest.config.ts): no window, no document.
import { describe, expect, it, vi } from "vitest";
import { createElement as h } from "react";
import { renderToString } from "react-dom/server";
import { createRuntime } from "@klarlabs-studio/glossa-runtime";

import { GlossaProvider, T, createGlossa, useGlossa } from "./index.js";
import { Root, r1 } from "./testing/app.js";
import { msg, release } from "./testing/release.js";

const server = (locale: string) =>
  createGlossa({ bundled: r1, locales: locale, storage: null, bidiIsolation: "none" });

describe("server rendering", () => {
  it("imports and renders without browser globals", () => {
    expect("window" in globalThis || "document" in globalThis).toBe(false);
    const html = renderToString(h(Root, { glossa: server("de") }));
    expect(html).toBe(
      '<main lang="de" dir="ltr">' +
        "<h1>Zur Kasse</h1>" +
        "<p>Lies die <b>AGB</b>.</p>" +
        "<p>Hallo, Lina!</p>" +
        "<p>Standard</p>" +
        "</main>",
    );
  });

  it("renders each request in its own locale", () => {
    const [de, ar] = ["de", "ar-EG"].map((l) => renderToString(h(Root, { glossa: server(l) })));
    expect(de).toContain("<h1>Zur Kasse</h1>");
    expect(ar).toContain('<main lang="ar" dir="rtl"><h1>الدفع</h1>');
  });

  it("doesn't subscribe to a shared runtime on the server, so requests don't leak listeners", () => {
    const runtime = createRuntime({ bundled: r1, locales: "de", storage: null });
    const subscribe = vi.spyOn(runtime, "subscribe");
    for (let i = 0; i < 3; i++) {
      renderToString(h(Root, { glossa: createGlossa({ runtime }) }));
    }
    expect(subscribe).not.toHaveBeenCalled();
  });

  it("falls back to the children, then the message ID", () => {
    const html = renderToString(
      h(
        GlossaProvider,
        { glossa: server("de") },
        h("p", null, h(T, { id: "gone" })),
        h("p", null, h(T, { id: "gone" }, "Weg")),
        h("p", null, h(T, { id: "gone" }, h("i", null, "Weg"))),
      ),
    );
    expect(html).toBe("<p>gone</p><p>Weg</p><p><i>Weg</i></p>");
  });

  it("never renders translation text as HTML, and keeps only safe, attribute-free markup", () => {
    const evil = release("rel_x", 1, {
      de: {
        "x.html": msg("<img src=x onerror=alert(1)>"),
        "x.link": msg({ open: "a" }, "klick", { close: "a" }, " ", { open: "em" }, "hier", {
          close: "em",
        }),
      },
    });
    const glossa = createGlossa({ bundled: evil, locales: "de", storage: null });
    const html = renderToString(
      h(
        GlossaProvider,
        { glossa },
        h("p", null, h(T, { id: "x.html" })),
        h("p", null, h(T, { id: "x.link" })),
      ),
    );
    expect(html).toBe("<p>&lt;img src=x onerror=alert(1)&gt;</p><p>klick <em>hier</em></p>");
  });

  it("renders inline defaults with a warning outside a provider", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const Bare = () => {
      const { t, locale } = useGlossa();
      return h("p", null, `${t("cart.checkout", {}, { default: "Checkout" })}|${locale}`);
    };
    expect(renderToString(h(Bare))).toBe("<p>Checkout|undefined</p>");
    expect(warn).toHaveBeenCalledWith(expect.stringContaining("GlossaProvider"));
    warn.mockRestore();
  });
});
