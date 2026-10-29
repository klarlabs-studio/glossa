import { describe, expect, it } from "vitest";
import { MessageSyntaxError, format, formatToParts, parseMF2, stringify } from "./mf2.js";
import { fromReference, toReference } from "./convert.js";
import type { Message } from "./model.js";
import { testFunctions } from "./testing/functions.js";
import { caseKind, caseName, paramValues, suiteCases } from "./testing/index.js";

const cases = suiteCases();
const byKind = (kind: ReturnType<typeof caseKind>) =>
  cases.filter((tc) => caseKind(tc) === kind).map((tc) => [caseName(tc), tc] as const);

describe("parseMF2 against the Unicode suite", () => {
  it.each(byKind("syntax-error"))("rejects syntax error %s", (_n, tc) => {
    expect(() => parseMF2(tc.src)).toThrow(MessageSyntaxError);
  });

  it.each(byKind("data-model-error"))("rejects data model error %s", (_n, tc) => {
    expect(() => parseMF2(tc.src)).toThrow(MessageSyntaxError);
  });

  const parsable = [...byKind("valid"), ...byKind("error")];
  it.each(parsable)("round-trips %s through stringify", (_n, tc) => {
    const msg = parseMF2(tc.src);
    expect(parseMF2(stringify(msg))).toEqual(msg);
  });

  it.each(parsable)("formats %s like the suite expects", (_n, tc) => {
    const errors: string[] = [];
    const out = format(parseMF2(tc.src), tc.locale, paramValues(tc.params), {
      bidiIsolation: tc.bidiIsolation ?? "default",
      onError: (e) => errors.push(e.type),
      functions: testFunctions,
    });
    if (tc.exp !== undefined) expect(out).toBe(tc.exp);
    expect(errors).toEqual((tc.expErrors ?? []).map((e) => e.type));
  });
});

describe("the data model is plain JSON", () => {
  it("uses objects for options and attributes and `function` for the function ref", () => {
    const msg = parseMF2("{$n :number minimumFractionDigits=2 @translate=no}");
    expect(msg).toEqual({
      type: "message",
      declarations: [],
      pattern: [
        {
          type: "expression",
          arg: { type: "variable", name: "n" },
          function: {
            type: "function",
            name: "number",
            options: { minimumFractionDigits: { type: "literal", value: "2" } },
          },
          attributes: { translate: { type: "literal", value: "no" } },
        },
      ],
    });
    expect(JSON.parse(JSON.stringify(msg))).toEqual(msg);
  });

  it("drops parser-only details (CST links, `quoted`, comments)", () => {
    const msg = parseMF2(".input {$x :string} .match $x |a| {{A}} * {{B}}");
    expect(msg).toEqual({
      type: "select",
      declarations: [
        {
          type: "input",
          name: "x",
          value: {
            type: "expression",
            arg: { type: "variable", name: "x" },
            function: { type: "function", name: "string" },
          },
        },
      ],
      selectors: [{ type: "variable", name: "x" }],
      variants: [
        { keys: [{ type: "literal", value: "a" }], value: ["A"] },
        { keys: [{ type: "*" }], value: ["B"] },
      ],
    });
    expect(Object.getOwnPropertySymbols(msg)).toEqual([]);
  });

  it("converts to the reference model and back without loss", () => {
    const src =
      ".local $a = {|x| :string u:id=a} .input {$n :number select=ordinal} .match $n " +
      "one {{{#b}{$a}{/b}}} * {{{#img src=$a @alt=|y| /}}}";
    const msg: Message = parseMF2(src);
    const ref = toReference(msg);
    expect(JSON.stringify(ref)).not.toContain('"function":');
    expect(fromReference(ref)).toEqual(msg);
  });
});

describe("format via the reference formatter", () => {
  it("enables the draft functions", () => {
    const msg = parseMF2("{$amount :currency currency=EUR} ({$share :percent})");
    expect(format(msg, "de-DE", { amount: 1234.5, share: 0.25 })).toBe(
      "1.234,50\u00a0€ (25\u00a0%)",
    );
  });

  it("reports errors through onError and falls back instead of throwing", () => {
    const errors: string[] = [];
    const out = format(parseMF2("Hi {$name}"), "en", {}, { onError: (e) => errors.push(e.type) });
    expect(out).toBe("Hi \u2068{$name}\u2069");
    expect(errors).toEqual(["unresolved-variable"]);
  });

  it("formats markup to parts", () => {
    const parts = formatToParts(parseMF2("{#b}bold{/b}"), "en", {});
    expect(parts).toEqual([
      { type: "markup", kind: "open", name: "b" },
      { type: "text", value: "bold" },
      { type: "markup", kind: "close", name: "b" },
    ]);
  });
});
