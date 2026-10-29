/** The component tree the SSR and hydration tests render. Test-only. */
import { defineComponent, h } from "vue";

import { GlossaText, useGlossa } from "../index.js";
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

export const App = defineComponent({
  name: "App",
  setup() {
    const { t, locale, dir } = useGlossa();
    return () =>
      h("main", { lang: locale.value, dir: dir.value }, [
        h("h1", t("cart.checkout")),
        h("p", [h(GlossaText, { id: "terms.hint" })]),
        h("p", [h(GlossaText, { id: "athlete.greeting", values: { name: "Lina" } })]),
        h("p", [h(GlossaText, { id: "no.such.key" }, () => "Standard")]),
        h("p", [h(Global)]),
      ]);
  },
});

/** Uses the `$t` global property instead of the composable. */
const Global = defineComponent({
  render() {
    return this.$t("cart.checkout");
  },
});
