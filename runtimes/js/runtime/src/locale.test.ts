import { describe, expect, it } from "vitest";
import {
  acceptLanguage,
  canonicalLocales,
  fallbackChain,
  lookupLocale,
  resolveLocales,
} from "./locale.js";

const manifest = (fallback: Record<string, string[]>, codes: string[], sourceLocale = "de") => ({
  fallback,
  sourceLocale,
  locales: codes.map((code) => ({ code, direction: "ltr" as const })),
});

describe("canonicalLocales", () => {
  it("canonicalizes, maps underscores and deprecated tags, and drops invalid ones", () => {
    expect(canonicalLocales(["EN_us", "iw", "de-at", "not a tag", "", "en-US"])).toEqual([
      "en-US",
      "he",
      "de-AT",
    ]);
  });
});

describe("lookupLocale (RFC 4647 §3.4)", () => {
  const available = ["de", "de-AT", "en", "zh-Hant", "fr-CA", "fr"];

  it("takes the first requested tag that matches, truncating progressively", () => {
    expect(lookupLocale(["en"], available)).toBe("en");
    expect(lookupLocale(["en-GB"], available)).toBe("en");
    expect(lookupLocale(["zh-Hant-TW"], available)).toBe("zh-Hant");
    expect(lookupLocale(["ja", "fr-CA"], available)).toBe("fr-CA");
  });

  it("drops a trailing single-character subtag together with the one before it", () => {
    expect(lookupLocale(["de-AT-u-co-phonebk"], available)).toBe("de-AT");
    expect(lookupLocale(["en-a-bbb-x-priv"], available)).toBe("en");
  });

  it("returns undefined when nothing matches", () => {
    expect(lookupLocale(["ja"], available)).toBeUndefined();
    expect(lookupLocale([], available)).toBeUndefined();
  });
});

describe("fallbackChain (SPEC §4.2)", () => {
  const codes = ["de", "de-AT", "de-CH", "en", "en-CA", "fr", "fr-CA", "zh-Hant", "pt-BR", "pt-PT"];

  it("follows explicit edges, then '*', then the source locale", () => {
    const m = manifest({ "de-AT": ["de"], "*": ["en"] }, codes);
    expect(fallbackChain("de-AT", m)).toEqual(["de-AT", "de", "en"]);
    expect(fallbackChain("de", m)).toEqual(["de", "en"]);
    expect(fallbackChain("en", m)).toEqual(["en", "de"]);
  });

  it("expands explicit edges depth-first", () => {
    const m = manifest(
      { "fr-CA": ["fr", "en-CA"], fr: ["de"], "en-CA": ["en"], "*": ["zh-Hant"] },
      codes,
    );
    expect(fallbackChain("fr-CA", m)).toEqual(["fr-CA", "fr", "de", "en-CA", "en", "zh-Hant"]);
  });

  it("adds available truncations only when the locale has no entry of its own", () => {
    const m = manifest({ "*": ["en"] }, codes);
    expect(fallbackChain("fr-CA", m)).toEqual(["fr-CA", "fr", "en", "de"]);
    expect(fallbackChain("zh-Hant", m)).toEqual(["zh-Hant", "en", "de"]);
    const explicit = manifest({ "fr-CA": ["en"] }, codes);
    expect(fallbackChain("fr-CA", explicit)).toEqual(["fr-CA", "en", "de"]);
    const empty = manifest({ "fr-CA": [] }, codes);
    expect(fallbackChain("fr-CA", empty)).toEqual(["fr-CA", "de"]);
  });

  it("does not recurse through truncations or '*' of fallback targets", () => {
    const m = manifest({ "de-AT": ["de-CH"], "*": ["en"] }, codes);
    expect(fallbackChain("de-AT", m)).toEqual(["de-AT", "de-CH", "en", "de"]);
  });

  it("expands '*' entries through their own explicit edges", () => {
    const m = manifest({ "*": ["en-CA"], "en-CA": ["en"] }, codes);
    expect(fallbackChain("fr", m)).toEqual(["fr", "en-CA", "en", "de"]);
  });

  it("stops at the first repeat in a cycle", () => {
    const m = manifest({ "pt-BR": ["pt-PT"], "pt-PT": ["pt-BR"] }, codes, "en");
    expect(fallbackChain("pt-BR", m)).toEqual(["pt-BR", "pt-PT", "en"]);
    const self = manifest({ en: ["en", "de"], de: ["en"] }, codes, "en");
    expect(fallbackChain("en", self)).toEqual(["en", "de"]);
  });
});

describe("resolveLocales", () => {
  it("concatenates resolvers in order, canonicalized and deduplicated", () => {
    const explicit = undefined;
    const user = () => "de_AT";
    const org = () => null;
    const request = ["en-GB", "de-AT"];
    expect(resolveLocales(explicit, user, org, request, () => ["fr"])).toEqual([
      "de-AT",
      "en-GB",
      "fr",
    ]);
  });

  it("skips resolvers that throw", () => {
    const broken = () => {
      throw new Error("no cookie jar");
    };
    expect(resolveLocales(broken, "en")).toEqual(["en"]);
  });
});

describe("acceptLanguage", () => {
  it("orders by quality, keeping header order for ties, and drops q=0 and '*'", () => {
    expect(acceptLanguage("en;q=0.8, de-AT, de;q=0.9, fr;q=0, *;q=0.1")).toEqual([
      "de-AT",
      "de",
      "en",
    ]);
    expect(acceptLanguage(null)).toEqual([]);
    expect(acceptLanguage("")).toEqual([]);
  });
});
