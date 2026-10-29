import { parseMF2 } from "@klarlabs-studio/glossa-messageformat";
import { describe, expect, it } from "vitest";
import { format, formatToParts } from "./index.js";
import type { Message, MessageError, MessageFunction } from "./index.js";

const plain = { bidiIsolation: "none" } as const;

describe("format", () => {
  it("interpolates and selects plurals with exact keys first", () => {
    const msg = parseMF2(
      ".input {$n :number} .match $n 0 {{keine}} 1 {{genau eins}} one {{eins}} * {{{$n} Stück}}",
    );
    expect([0, 1, 2, 1234.5].map((n) => format(msg, "de", { n }, plain))).toEqual([
      "keine",
      "genau eins",
      "2 Stück",
      "1.234,5 Stück",
    ]);
  });

  it("selects ordinals", () => {
    const msg = parseMF2(
      ".input {$n :number select=ordinal} .match $n one {{{$n}st}} two {{{$n}nd}} few {{{$n}rd}} * {{{$n}th}}",
    );
    expect([1, 2, 3, 4, 11, 22].map((n) => format(msg, "en", { n }, plain))).toEqual([
      "1st",
      "2nd",
      "3rd",
      "4th",
      "11th",
      "22nd",
    ]);
  });

  it("formats bigint and Number/String objects", () => {
    const msg = parseMF2("{$a} {$b} {$c}");
    const values = { a: 12345678901234567890n, b: new Number(1.5), c: new String("x") };
    expect(format(msg, "en", values, plain)).toBe("12,345,678,901,234,567,890 1.5 x");
  });

  it("formats dates in the given time zone", () => {
    const msg = parseMF2("{$d :datetime dateLength=long timeZone=UTC}");
    const d = new Date(Date.UTC(2026, 8, 19, 14, 5));
    expect(format(msg, "de", { d }, plain)).toBe("19. September 2026 um 14:05");
  });

  it("uses the base direction of the locale for bidi isolation", () => {
    const msg = parseMF2("{$n :number} {$s}");
    expect(format(msg, "en", { n: 1, s: "x" })).toBe("1 \u2068x\u2069");
    expect(format(msg, "he", { n: 1, s: "x" })).toBe("\u20671\u2069 \u2068x\u2069");
    expect(format(msg, "en", { n: 1, s: "x" }, { dir: "rtl" })).toBe("\u20661\u2069 \u2068x\u2069");
  });

  it("accepts a list of locales", () => {
    expect(format(parseMF2("{1234 :number}"), ["de", "en"], {}, plain)).toBe("1.234");
  });
});

describe("never throws", () => {
  it("falls back to {$name} and reports the error", () => {
    const errors: MessageError[] = [];
    const out = format(parseMF2("Hallo {$name}!"), "de", {}, { onError: (e) => errors.push(e) });
    expect(out).toBe("Hallo \u2068{$name}\u2069!");
    expect(errors).toEqual([{ type: "unresolved-variable", source: "$name" }]);
  });

  it("works without onError", () => {
    expect(format(parseMF2("{$x :number}"), "en", { x: "nope" }, plain)).toBe("{$x}");
  });

  it("renders {\ufffd} for a message it cannot interpret", () => {
    const errors: string[] = [];
    const broken = {
      type: "select",
      declarations: [],
      selectors: [{ type: "variable", name: "x" }],
    };
    const out = format(
      broken as unknown as Message,
      "en",
      {},
      { onError: (e) => errors.push(e.type) },
    );
    expect(out).toBe("{\ufffd}");
    expect(errors.at(-1)).toBe("bad-message");
    expect(format(null as unknown as Message, "en")).toBe("{\ufffd}");
  });

  it("does not look up inherited properties of the values object", () => {
    const errors: string[] = [];
    const out = format(
      parseMF2("{$constructor}"),
      "en",
      {},
      { ...plain, onError: (e) => errors.push(e.type) },
    );
    expect(out).toBe("{$constructor}");
    expect(errors).toEqual(["unresolved-variable"]);
  });

  it("maps invalid Intl options to bad-option", () => {
    const errors: string[] = [];
    const out = format(
      parseMF2("{1 :number signDisplay=sometimes}"),
      "en",
      {},
      {
        ...plain,
        onError: (e) => errors.push(e.type),
      },
    );
    expect(out).toBe("{|1|}");
    expect(errors).toEqual(["bad-option"]);
  });
});

describe("formatToParts", () => {
  it("emits markup as structured parts", () => {
    const msg = parseMF2("Read the {#link href=|/terms| u:id=t @title=x}terms{/link}{#br/}");
    expect(formatToParts(msg, "en", {})).toEqual([
      { type: "text", value: "Read the " },
      { type: "markup", kind: "open", name: "link", id: "t", options: { href: "/terms" } },
      { type: "text", value: "terms" },
      { type: "markup", kind: "close", name: "link" },
      { type: "markup", kind: "standalone", name: "br" },
    ]);
    expect(format(msg, "en", {})).toBe("Read the terms");
  });

  it("emits number parts with locale and direction", () => {
    expect(formatToParts(parseMF2("{$n :integer}"), "de", { n: 1234.6 })).toEqual([
      {
        type: "number",
        dir: "ltr",
        locale: "de",
        parts: [
          { type: "integer", value: "1" },
          { type: "group", value: "." },
          { type: "integer", value: "235" },
        ],
      },
    ]);
  });
});

describe("custom functions", () => {
  it("can be registered and receive resolved options", () => {
    const upper: MessageFunction = (ctx, options, operand) => {
      const value = String(operand).toUpperCase() + String(options.suffix ?? "");
      return {
        type: "upper",
        valueOf: () => value,
        select: (keys) => keys.filter((k) => k === value),
        toParts: () => [{ type: "upper", locale: ctx.locales[0], value }],
      };
    };
    const msg = parseMF2(
      ".local $s = {$x :upper suffix=|!|} .match $s |HI!| {{yes {$s}}} * {{no}}",
    );
    expect(format(msg, "en", { x: "hi" }, { ...plain, functions: { upper } })).toBe("yes HI!");
  });
});
