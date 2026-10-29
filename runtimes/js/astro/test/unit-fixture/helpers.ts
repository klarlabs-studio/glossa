// A shared helper as a product writes it: it reaches for the page's runtime through `/astro/client`.
// The tests import it as is, with nothing stubbed in the test files (the vitest config has the plugin).
import { getRuntime } from "../../src/client.js";
import { t } from "../../src/translate.js";

export const saveLabel = () => getRuntime().t("form.save", {}, { default: "Speichern" });

/** The same through the Astro-free helper, which a library can depend on. */
export const cancelLabel = () => t("form.cancel", "Abbrechen");
