/**
 * Unit-testing code that imports `/astro/client` (issue #81): `helpers.ts` calls `getRuntime()`,
 * and this file stubs nothing; `glossaAstroTesting()` in vitest.config.ts serves the virtual modules.
 */
import { createRuntime } from "@klarlabs-studio/glossa-runtime";
import { afterEach, describe, expect, it } from "vitest";

import { getRuntime } from "../src/client.js";
import { currentRuntime, has, provideRuntime, t } from "../src/translate.js";
import { release, text } from "../src/testing/release.js";
import { cancelLabel, saveLabel } from "./unit-fixture/helpers.js";

afterEach(() => provideRuntime(undefined));

describe("a helper that uses getRuntime() under Vitest", () => {
  it("imports and renders the inline default, with no document and no stubs", () => {
    expect(saveLabel()).toBe("Speichern");
  });

  it("publishes the page runtime for the Astro-free helper", () => {
    const rt = getRuntime();
    expect(currentRuntime()).toBe(rt);
    expect(cancelLabel()).toBe("Abbrechen");
  });
});

describe("translate: t(id, fallback) outside Astro", () => {
  it("returns the fallback as written without a runtime", () => {
    expect(currentRuntime()).toBeUndefined();
    expect(t("form.cancel", "Abbrechen {$n}", { n: 1 })).toBe("Abbrechen {$n}");
    expect(has("form.cancel")).toBe(false);
  });

  it("renders with a provided runtime", () => {
    const r = release("rel_1", 1, { de: { "form.cancel": text("Zurück") } });
    provideRuntime(
      createRuntime({
        locales: "de",
        bundled: r,
        storage: null,
        refreshInterval: 0,
      }),
    );
    expect(t("form.cancel", "Abbrechen")).toBe("Zurück");
    expect(t("form.other", "Anderes")).toBe("Anderes");
    expect(has("form.cancel")).toBe(true);
  });
});
