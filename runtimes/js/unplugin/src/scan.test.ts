import { Parser } from "acorn";
import { describe, expect, it } from "vitest";

import { accessorPaths } from "./keys.js";
import { ModuleLocator } from "./locate.js";
import { cook, scan } from "./scan.js";
import type { Node } from "./scan.js";

const parse = (code: string) => Parser.parse(code, { ecmaVersion: "latest", sourceType: "module" }) as unknown as Node;

function found(code: string, keys: string[] = []) {
  return scan(code, parse(code), { accessors: accessorPaths(keys) }).map((h) => ({
    key: h.key,
    kind: h.kind,
    at: code.slice(h.offset, h.offset + (Array.isArray(h.text) ? h.text[0]!.length : h.key.length)),
  }));
}

describe("scan", () => {
  it("finds t calls in their compiled forms", () => {
    const code = [
      `t("a.one"); $t('a.two'); x.t(\`a.three\`); _ctx.$t("a.four");`,
      `_unref(t)("a.five"); (0, i18n.t)("a.six"); $setup["t"]("a.seven");`,
      `t(key); t("Not a key"); at("fake.at"); t(\`a.\${x}\`); t("a." + x);`,
    ].join("\n");
    expect(found(code).map((h) => [h.key, h.kind, h.at])).toEqual([
      ["a.one", "t", "a.one"],
      ["a.two", "t", "a.two"],
      ["a.three", "t", "a.three"],
      ["a.four", "t", "a.four"],
      ["a.five", "t", "a.five"],
      ["a.six", "t", "a.six"],
      ["a.seven", "t", "a.seven"],
    ]);
  });

  it("finds components and elements in factory calls", () => {
    const code = [
      `const _hoisted_1 = { message: "e.hoisted" };`,
      `jsx(T, { id: "c.jsx" }); React.createElement(T, { id: "c.react" }); jsx(Other, { id: "fake.other" });`,
      `_createVNode(_unref(GlossaText), { id: 'c.vue' }); _createVNode($setup["GlossaText"], { id: "c.setup" });`,
      `_createVNode(_component_GlossaText, _mergeProps({ id: "c.global" }, _attrs));`,
      `_createElementVNode("glossa-text", _hoisted_1); _createVNode(_component_glossa_rich, { key: "e.resolved" });`,
      `$$renderComponent($$result, "glossa-text", "glossa-text", { "key": "e.astro" });`,
      `$$renderComponent($$result, "GlossaText", GlossaText, { "id": "c.island" });`,
      `jsx("glossa-text", { message: "x.y", key: "e.key-wins" }); jsx("glossa-texts", { key: "fake.texts" });`,
      `jsx(T, { id: dynamic }); document.createElement("glossa-text");`,
    ].join("\n");
    expect(found(code).map((h) => [h.key, h.kind])).toEqual([
      ["c.jsx", "component"],
      ["c.react", "component"],
      ["c.vue", "component"],
      ["c.setup", "component"],
      ["c.global", "component"],
      ["e.hoisted", "element"],
      ["e.resolved", "element"],
      ["e.astro", "element"],
      ["c.island", "component"],
      ["e.key-wins", "element"],
    ]);
  });

  it("reads markup only where compilers emit it", () => {
    const code = [
      `_createStaticVNode("<p>x</p><glossa-text message=\\"e.static\\"></glossa-text>", 2);`,
      "_push(`<div><glossa-text key=\"e.ssr\">${_ssrInterpolate(x)}</glossa-text></div>`);",
      `const s = "<glossa-text key=\\"fake.string\\"></glossa-text>";`,
      "el.innerHTML = `<glossa-text key=\"fake.template\"></glossa-text>`;",
    ].join("\n");
    expect(found(code).map((h) => [h.key, h.at])).toEqual([
      ["e.static", "e.static"],
      ["e.ssr", "e.ssr"],
    ]);
  });

  it("finds typed accessors by their path, a one-segment path only on `messages`", () => {
    const keys = ["checkout.pay", "checkout.payment_failed", "title"];
    const code = [
      `m.checkout.pay({ amount }); _unref(m).checkout.paymentFailed(); _ctx.m.checkout.pay();`,
      `messages.title(); heading.title(); m.title(); m.checkout.pay; m.checkout["pay"]();`,
    ].join("\n");
    expect(found(code, keys).map((h) => [h.key, h.at])).toEqual([
      ["checkout.pay", "checkout"],
      ["checkout.payment_failed", "checkout"],
      ["checkout.pay", "checkout"],
      ["title", "title"],
    ]);
  });
});

describe("cook", () => {
  it("maps every cooked unit back to its offset in the code", () => {
    const raw = String.raw`a\"b\u{1F968}\n`;
    const { text, at } = cook(raw, 10);
    expect(text).toBe('a"b\u{1F968}\n');
    expect(at).toEqual([10, 11, 13, 14, 14, 23]);
  });
});

describe("ModuleLocator", () => {
  it("counts columns in code points and claims each position once", () => {
    const code = `const s = "🥨"; t("a.b"); t("a.b");\n\tt('a.c');\n`;
    const locator = new ModuleLocator(code, "/virtual/x.ts", null, () => code);
    const at = scan(code, parse(code), { accessors: new Map() }).map((h) => {
      const l = locator.locate(h)!;
      return [h.key, l.line, l.column];
    });
    expect(at).toEqual([
      ["a.b", 1, 19],
      ["a.b", 1, 29],
      ["a.c", 2, 5],
    ]);
  });

  it("without a map, searches the original file from the hit's own position", () => {
    const original = `// a comment\nexport const x = t("a.b");\n`;
    const code = `export const x = t("a.b");\n`;
    const locator = new ModuleLocator(code, "/virtual/x.ts?query", null, () => original);
    const [hit] = scan(code, parse(code), { accessors: new Map() });
    expect(locator.locate(hit!)).toMatchObject({ line: 2, column: 21, source: { path: "/virtual/x.ts" } });
  });
});
