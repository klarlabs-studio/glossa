import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import type { Part } from "@klarlabs-studio/glossa-runtime";
import { SAFE_TAGS, parseVars, partsToTree, treeToHtml } from "./parts.js";

/** runtimes/testdata/markup.json: the safe-markup rules shared with the Go runtime. */
const markup = JSON.parse(
  readFileSync(
    join(dirname(fileURLToPath(import.meta.url)), "../../../testdata/markup.json"),
    "utf8",
  ),
) as {
  safeTags: string[];
  voidTags: string[];
  cases: Array<{ description: string; parts: Part[]; html: string }>;
};

describe("runtimes/testdata/markup.json", () => {
  it("SAFE_TAGS is the shared list", () => {
    expect([...SAFE_TAGS].sort()).toEqual([...markup.safeTags].sort());
  });

  it.each(markup.cases.map((c) => [c.description, c] as const))("%s", (_name, c) => {
    expect(treeToHtml(partsToTree(c.parts))).toBe(c.html);
  });
});

const open = (name: string): Part => ({ type: "markup", kind: "open", name });
const close = (name: string): Part => ({ type: "markup", kind: "close", name });
const t = (value: string): Part => ({ type: "text", value });

describe("partsToTree", () => {
  it("joins text, formatted values, bidi isolates and fallbacks into one string", () => {
    const parts: Part[] = [
      t("Hallo "),
      { type: "bidiIsolation", value: "⁨" },
      { type: "string", value: "Lina" },
      { type: "bidiIsolation", value: "⁩" },
      t(", du hast "),
      {
        type: "number",
        parts: [
          { type: "integer", value: "1" },
          { type: "group", value: "." },
          { type: "integer", value: "000" },
        ],
      },
      t(" Punkte "),
      { type: "fallback", source: "$missing" },
    ];
    expect(partsToTree(parts)).toEqual(["Hallo ⁨Lina⁩, du hast 1.000 Punkte {$missing}"]);
  });

  it("turns safe markup into elements and nests them", () => {
    const parts = [
      t("Tippe "),
      open("b"),
      t("hier "),
      open("em"),
      t("jetzt"),
      close("em"),
      close("b"),
      t("."),
    ];
    expect(partsToTree(parts)).toEqual([
      "Tippe ",
      { tag: "b", children: ["hier ", { tag: "em", children: ["jetzt"] }] },
      ".",
    ]);
  });

  it("renders standalone markup like br as an empty element", () => {
    expect(
      partsToTree([t("a"), { type: "markup", kind: "standalone", name: "br" }, t("b")]),
    ).toEqual(["a", { tag: "br", children: [] }, "b"]);
  });

  it("keeps only the content of markup that isn't on the safe list", () => {
    const parts = [
      t("Klick "),
      open("link"),
      t("hier"),
      close("link"),
      open("script"),
      t("x"),
      close("script"),
    ];
    expect(partsToTree(parts)).toEqual(["Klick hierx"]);
    expect(partsToTree([{ type: "markup", kind: "standalone", name: "img" }])).toEqual([]);
  });

  it("drops markup options, so translations can't carry attributes", () => {
    const parts: Part[] = [
      { type: "markup", kind: "open", name: "b", options: { onclick: "alert(1)", class: "x" } },
      t("x"),
      close("b"),
    ];
    expect(partsToTree(parts)).toEqual([{ tag: "b", children: ["x"] }]);
  });

  it("closes unclosed markup at the end and ignores stray or crossed closes", () => {
    expect(partsToTree([open("b"), t("x")])).toEqual([{ tag: "b", children: ["x"] }]);
    expect(partsToTree([t("x"), close("b")])).toEqual(["x"]);
    expect(
      partsToTree([open("b"), t("1"), open("i"), t("2"), close("b"), t("3"), close("i")]),
    ).toEqual([{ tag: "b", children: ["1", { tag: "i", children: ["2"] }] }, "3"]);
  });
});

describe("treeToHtml", () => {
  it("escapes text and serializes safe elements", () => {
    const tree = partsToTree([
      t("<img src=x onerror=alert(1)> & "),
      open("strong"),
      t('"fett"'),
      close("strong"),
      { type: "markup", kind: "standalone", name: "br" },
    ]);
    expect(treeToHtml(tree)).toBe(
      '&lt;img src=x onerror=alert(1)&gt; &amp; <strong>"fett"</strong><br>',
    );
  });
});

describe("parseVars", () => {
  it("parses a JSON object and ignores anything else", () => {
    expect(parseVars('{"name":"Sophia","n":3}')).toEqual({ name: "Sophia", n: 3 });
    expect(parseVars(null)).toEqual({});
    expect(parseVars("not json")).toEqual({});
    expect(parseVars("[1,2]")).toEqual({});
    expect(parseVars("42")).toEqual({});
  });
});
