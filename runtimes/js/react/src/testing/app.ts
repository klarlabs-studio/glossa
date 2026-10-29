/** The component tree the SSR and hydration tests render. Test-only. */
import { createElement as h } from "react";
import type { ReactNode } from "react";

import { GlossaProvider, T, useGlossa } from "../index.js";
import type { Glossa } from "../index.js";
import { msg, release, text } from "./release.js";

export const r1 = release(
  "rel_1",
  1,
  {
    de: {
      "cart.checkout": text("Zur Kasse"),
      "terms.hint": msg("Lies die ", { open: "b" }, "AGB", { close: "b" }, "."),
      "athlete.greeting": msg("Hallo, ", { $: "name" }, "!"),
    },
    en: { "cart.checkout": text("Checkout") },
    ar: { "cart.checkout": text("الدفع") },
  },
  { directions: { ar: "rtl" }, fallback: { "*": ["de"] } },
);

export const r2 = release("rel_2", 2, {
  de: {
    "cart.checkout": text("Zur Kasse v2"),
    "terms.hint": text("Neue AGB"),
    "athlete.greeting": msg("Servus, ", { $: "name" }, "!"),
  },
  en: { "cart.checkout": text("Checkout v2") },
});

export function App(): ReactNode {
  const { t, locale, dir } = useGlossa();
  return h(
    "main",
    { lang: locale, dir },
    h("h1", null, t("cart.checkout")),
    h("p", null, h(T, { id: "terms.hint" })),
    h("p", null, h(T, { id: "athlete.greeting", values: { name: "Lina" } })),
    h("p", null, h(T, { id: "no.such.key" }, "Standard")),
  );
}

/** `App` under a provider, the way an app's root renders it. */
export const Root = ({ glossa }: { glossa: Glossa }) => h(GlossaProvider, { glossa }, h(App));
