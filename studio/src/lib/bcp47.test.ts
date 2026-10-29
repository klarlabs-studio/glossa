import { describe, expect, it } from "vitest";
import { ancestry, checkLocale, directionOf, localeName } from "./bcp47";

describe("checkLocale", () => {
  it.each([
    ["de", "de", "ltr"],
    ["de_de", "de-DE", "ltr"],
    ["  pt-br ", "pt-BR", "ltr"],
    ["zh-hant-tw", "zh-Hant-TW", "ltr"],
    ["ar", "ar", "rtl"],
    ["iw", "he", "rtl"],
    ["fa-IR", "fa-IR", "rtl"],
    ["ur", "ur", "rtl"],
    ["sr-Latn", "sr-Latn", "ltr"],
  ])("accepts %s as %s (%s)", (input, tag, direction) => {
    expect(checkLocale(input)).toEqual({ ok: true, tag, direction });
  });

  it.each([
    ["", "empty"],
    ["   ", "empty"],
    ["not a locale", "invalid"],
    ["en--US", "invalid"],
    ["und", "invalid"],
    ["en-u-ca-gregory", "extension"],
    ["de-x-private", "extension"],
    ["en-Latn-US-abcdefgh-bcdefghi-cdefghij-defghijk", "too_long"],
  ])("refuses %j (%s)", (input, reason) => {
    expect(checkLocale(input)).toEqual({ ok: false, reason });
  });
});

describe("directionOf", () => {
  it("derives direction from the likely script", () => {
    expect(directionOf("he-IL")).toBe("rtl");
    expect(directionOf("en")).toBe("ltr");
    expect(directionOf("ks-Arab")).toBe("rtl");
  });
});

describe("localeName", () => {
  it("names locales and falls back to the tag", () => {
    expect(localeName("de")).toBe("German");
    expect(localeName("not valid!")).toBe("not valid!");
  });
});

describe("ancestry", () => {
  it("walks truncation parents", () => {
    expect(ancestry("zh-Hant-TW")).toEqual(["zh-Hant-TW", "zh-Hant", "zh"]);
    expect(ancestry("de-AT")).toEqual(["de-AT", "de"]);
  });
  it("follows CLDR parent exceptions", () => {
    expect(ancestry("es-AR")).toEqual(["es-AR", "es-419", "es"]);
    expect(ancestry("pt-AO")).toEqual(["pt-AO", "pt-PT", "pt"]);
  });
});
