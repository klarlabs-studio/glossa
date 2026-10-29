// @vitest-environment node
import { describe, expect, it } from "vitest";
import { createRuntime } from "@klarlabs-studio/glossa-runtime";

import { prerender } from "./ssr.js";
import { match, msg, release, text } from "./testing/release.js";

const r = release(
  "rel_1",
  1,
  {
    de: {
      "cart.checkout": text("Zur Kasse"),
      "athlete.greeting": msg("Hallo, ", { $: "name" }, "!"),
      sessions: match("count", "number", {
        one: ["eine Einheit"],
        "*": [{ $: "count" }, " Einheiten"],
      }),
      role: match("role", "string", { coach: ["Trainerin"], "*": ["Mitglied"] }),
      gender: match("value", "string", { male: ["Er"], "*": ["Sie"] }),
      terms: msg("Lies die ", { open: "b" }, "AGB & <Regeln>", { close: "b" }, "."),
      outer: text("Außen"),
    },
    ar: { "cart.checkout": text("الدفع") },
  },
  { directions: { ar: "rtl" } },
);

const rt = (locale = "de") =>
  createRuntime({ bundled: r, locales: locale, storage: null, bidiIsolation: "none" });

describe("prerender", () => {
  it("replaces the inline default with the translation", () => {
    expect(prerender(`<p><glossa-text key="cart.checkout">Checkout</glossa-text></p>`, rt())).toBe(
      `<p><glossa-text key="cart.checkout">Zur Kasse</glossa-text></p>`,
    );
  });

  it("reads the message ID from message= too", () => {
    expect(prerender(`<glossa-text message="cart.checkout">Checkout</glossa-text>`, rt())).toBe(
      `<glossa-text message="cart.checkout">Zur Kasse</glossa-text>`,
    );
  });

  it("keeps the inline default when the message is missing", () => {
    const html = `<glossa-text key="nope">Default <em>label</em></glossa-text>`;
    expect(prerender(html, rt())).toBe(html);
  });

  it("formats rich, plural and select elements from their attributes", () => {
    const html = [
      `<glossa-rich key="athlete.greeting" vars='{"name":"Sophia"}'>Hi</glossa-rich>`,
      `<glossa-rich key="athlete.greeting" vars="{&quot;name&quot;:&quot;Lina&quot;}">Hi</glossa-rich>`,
      `<glossa-plural key="sessions" count="1">n</glossa-plural>`,
      `<glossa-plural key="sessions" count=4>n</glossa-plural>`,
      `<glossa-select key="role" name="role" value="coach">r</glossa-select>`,
      `<glossa-select key="gender" value="male">g</glossa-select>`,
    ].join("|");
    const out = prerender(html, rt());
    expect(out.replace(/<[^>]+>/g, "")).toBe(
      "Hallo, Sophia!|Hallo, Lina!|eine Einheit|4 Einheiten|Trainerin|Er",
    );
    expect(out).toContain(`vars="{&quot;name&quot;:&quot;Lina&quot;}"`);
  });

  it("escapes translations and renders only safe markup", () => {
    expect(prerender(`<glossa-text key="terms">…</glossa-text>`, rt())).toBe(
      `<glossa-text key="terms">Lies die <b>AGB &amp; &lt;Regeln&gt;</b>.</glossa-text>`,
    );
  });

  it("adds lang and dir to providers that have neither", () => {
    expect(prerender(`<glossa-provider edge="x">…</glossa-provider>`, rt("ar"))).toBe(
      `<glossa-provider lang="ar" dir="rtl" edge="x">…</glossa-provider>`,
    );
    const own = `<glossa-provider lang="de">…</glossa-provider>`;
    expect(prerender(own, rt("ar"))).toBe(own);
  });

  it("renders nested elements when the outer one keeps its default", () => {
    const html = `<glossa-text key="nope">x <glossa-text key="cart.checkout">y</glossa-text></glossa-text>`;
    expect(prerender(html, rt())).toBe(
      `<glossa-text key="nope">x <glossa-text key="cart.checkout">Zur Kasse</glossa-text></glossa-text>`,
    );
    const resolved = `<glossa-text key="outer">x <glossa-text key="cart.checkout">y</glossa-text></glossa-text> <glossa-text key="cart.checkout">z</glossa-text>`;
    expect(prerender(resolved, rt())).toBe(
      `<glossa-text key="outer">Außen</glossa-text> <glossa-text key="cart.checkout">Zur Kasse</glossa-text>`,
    );
  });

  it("leaves scripts, styles, templates and comments alone", () => {
    const html = [
      `<script>const s = '<glossa-text key="cart.checkout">x</glossa-text>';</script>`,
      `<!-- <glossa-text key="cart.checkout">x</glossa-text> -->`,
      `<template><glossa-text key="cart.checkout">x</glossa-text></template>`,
      `<style>glossa-text{color:red}</style>`,
    ].join("");
    expect(prerender(html, rt())).toBe(html);
  });

  it("adds extra attributes to replaced elements, unless they're already there", () => {
    const html = `<glossa-text key="cart.checkout">a</glossa-text><glossa-text key="cart.checkout" data-allow-mismatch="text">b</glossa-text><glossa-text key="nope">c</glossa-text>`;
    expect(prerender(html, rt(), { attributes: { "data-allow-mismatch": "" } })).toBe(
      `<glossa-text key="cart.checkout" data-allow-mismatch="">Zur Kasse</glossa-text><glossa-text key="cart.checkout" data-allow-mismatch="text">Zur Kasse</glossa-text><glossa-text key="nope">c</glossa-text>`,
    );
  });

  it("leaves unclosed elements and other glossa-prefixed tags alone", () => {
    const html = `<glossa-text-x key="cart.checkout">a</glossa-text-x><glossa-text key="cart.checkout">b`;
    expect(prerender(html, rt())).toBe(html);
  });

  it("renders inline defaults everywhere when no release is active", () => {
    const html = `<glossa-provider><glossa-text key="cart.checkout">Checkout</glossa-text></glossa-provider>`;
    expect(prerender(html, createRuntime({ storage: null }))).toBe(html);
  });
});
