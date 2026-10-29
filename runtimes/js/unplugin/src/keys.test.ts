import { describe, expect, it } from "vitest";

import { accessorPaths, camel, compareCodePoints, isKey } from "./keys.js";

describe("isKey", () => {
  it("follows the catalog key rules", () => {
    expect(isKey("checkout.payment-failed")).toBe(true);
    expect(isKey("a_1.b-2")).toBe(true);
    expect(isKey("Nav.Home")).toBe(false);
    expect(isKey("Hello world")).toBe(false);
    expect(isKey("nav..home")).toBe(false);
    expect(isKey("")).toBe(false);
    expect(isKey("a".repeat(200))).toBe(true);
    expect(isKey("a".repeat(201))).toBe(false);
  });
});

describe("compareCodePoints", () => {
  it("orders by code point (UTF-8 byte order), not UTF-16 unit", () => {
    // U+FF61 is one UTF-16 unit above the surrogates that encode U+1F968.
    expect(compareCodePoints("\u{1F968}", "｡")).toBeGreaterThan(0);
    expect("\u{1F968}" < "｡").toBe(true);
    expect(compareCodePoints("src/Negatives.vue", "src/negatives.html")).toBeLessThan(0);
    expect(compareCodePoints("a", "ab")).toBeLessThan(0);
    expect(compareCodePoints("ab", "ab")).toBe(0);
  });
});

describe("accessorPaths", () => {
  it("camelCases segments like codegen", () => {
    expect(camel("payment_failed")).toBe("paymentFailed");
    expect(camel("payment-failed")).toBe("paymentFailed");
    expect(camel("2fa")).toBe("_2fa");
    expect(camel("-")).toBe("_");
  });

  it("gives colliding paths to the first key in sort order and none to groups", () => {
    const paths = accessorPaths([
      "title",
      "nav.home.title",
      "nav.home",
      "checkout.payment_failed",
      "checkout.payment-failed",
      "checkout.pay",
    ]);
    expect(Object.fromEntries(paths)).toEqual({
      title: "title",
      "nav.home.title": "nav.home.title",
      "checkout.paymentFailed": "checkout.payment-failed",
      "checkout.pay": "checkout.pay",
    });
  });
});
