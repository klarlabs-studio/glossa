// Type-level checks run with `pnpm lint` (tsc); the runtime assertions with vitest.
import { describe, expect, expectTypeOf, it } from "vitest";
import { createElement as h } from "react";
import { renderToString } from "react-dom/server";

import { GlossaProvider, createGlossa, useMessages } from "./index.js";
import type { MessageValues } from "./index.js";
import { msg, release, text } from "./testing/release.js";

/** What `glossa generate` emits: message ID → the values it takes. */
interface Messages {
  "cart.checkout": MessageValues;
  "athlete.greeting": { name: string };
  "cart.items": { count: number };
}

const r = release("rel_1", 1, {
  de: {
    "cart.checkout": text("Zur Kasse"),
    "athlete.greeting": msg("Hallo, ", { $: "name" }, "!"),
    "cart.items": msg({ $: "count" }, " Artikel"),
  },
});

describe("useMessages<Messages>()", () => {
  it("checks message IDs and values at compile time, and renders like t()", () => {
    let out = "";
    const App = () => {
      const m = useMessages<Messages>();
      out = [
        m.t("cart.checkout"),
        m.t("athlete.greeting", { name: "Lina" }),
        m.t("cart.items", { count: 3 }, { default: "3 items" }),
      ].join("|");
      // @ts-expect-error unknown message ID
      m.t("cart.chekout");
      // @ts-expect-error missing required values
      m.t("athlete.greeting");
      // @ts-expect-error wrong value type
      m.t("cart.items", { count: "3" });
      expectTypeOf(m.parts("cart.checkout")).toBeArray();
      return h("p", null, out);
    };
    const glossa = createGlossa({
      bundled: r,
      locales: "de",
      storage: null,
      bidiIsolation: "none",
    });
    renderToString(h(GlossaProvider, { glossa }, h(App)));
    expect(out).toBe("Zur Kasse|Hallo, Lina!|3 Artikel");
  });
});
