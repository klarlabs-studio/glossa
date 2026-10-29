import { describe, expect, it } from "vitest";

import {
  alternates,
  localeParams,
  localizePath,
  pageLocale,
  siteLocales,
  stripLocale,
} from "./routing.js";
import type { Routing } from "./routing.js";

const r: Routing = {
  locales: siteLocales(["de", "en", { path: "espanol", codes: ["es", "es-AR"] }]),
  defaultLocale: "de",
  prefixDefaultLocale: false,
};

describe("routing", () => {
  it("reads Astro's i18n.locales", () => {
    expect(r.locales).toEqual([
      { code: "de", path: "de" },
      { code: "en", path: "en" },
      { code: "es", path: "espanol" },
    ]);
  });

  it("picks the page locale from Astro's current locale, else the default", () => {
    expect(pageLocale("en", r)).toBe("en");
    expect(pageLocale("espanol", r)).toBe("es");
    expect(pageLocale("fr", r)).toBe("de");
    expect(pageLocale(undefined, r)).toBe("de");
  });

  it("localizes paths like Astro: the default locale unprefixed unless configured", () => {
    expect(localizePath("/about", "de", r)).toBe("/about");
    expect(localizePath("/about", "en", r)).toBe("/en/about");
    expect(localizePath("/", "en", r)).toBe("/en/");
    expect(localizePath("about", "es", r)).toBe("/espanol/about");
    expect(localizePath("/", "de", { ...r, prefixDefaultLocale: true })).toBe("/de/");
    expect(localizePath("/about", "en", { ...r, base: "/docs/" })).toBe("/docs/en/about");
  });

  it("strips the base and locale prefix", () => {
    expect(stripLocale("/en/about", r)).toBe("/about");
    expect(stripLocale("/en", r)).toBe("/");
    expect(stripLocale("/espanol/", r)).toBe("/");
    expect(stripLocale("/about", r)).toBe("/about");
    expect(stripLocale("/english/x", r)).toBe("/english/x");
    expect(stripLocale("/docs/en/about", { ...r, base: "/docs" })).toBe("/about");
  });

  it("lists hreflang alternates with x-default", () => {
    expect(alternates("/en/about", r)).toEqual([
      { hreflang: "de", href: "/about" },
      { hreflang: "en", href: "/en/about" },
      { hreflang: "es", href: "/espanol/about" },
      { hreflang: "x-default", href: "/about" },
    ]);
  });

  it("builds getStaticPaths params for a [...locale] route", () => {
    expect(localeParams(r)).toEqual([
      { params: { locale: undefined } },
      { params: { locale: "en" } },
      { params: { locale: "espanol" } },
    ]);
  });
});
