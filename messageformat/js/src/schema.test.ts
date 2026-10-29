import { readFileSync } from "node:fs";
import { Ajv } from "ajv";
import { describe, expect, it } from "vitest";
import { isMessage, messageSchema } from "./schema.js";
import { parseMF2 } from "./mf2.js";
import { caseKind, messageSchemaPath, suiteCases } from "./testing/index.js";

const ajv = new Ajv({ strict: false });
const jsonSchema = JSON.parse(readFileSync(messageSchemaPath, "utf8")) as object;
const validateJson = ajv.compile(jsonSchema);

const parsable = suiteCases().filter(
  (tc) => caseKind(tc) !== "syntax-error" && caseKind(tc) !== "data-model-error",
);

describe("messageSchema agrees with message.schema.json", () => {
  it.each(parsable.map((tc) => [tc.src, tc] as const))("accepts %j", (_src, tc) => {
    const msg = parseMF2(tc.src);
    expect(validateJson(msg), JSON.stringify(validateJson.errors)).toBe(true);
    expect(messageSchema.safeParse(msg).success).toBe(true);
    expect(isMessage(msg)).toBe(true);
  });

  const lit = { type: "literal", value: "x" };
  const variable = { type: "variable", name: "x" };
  const expr = { type: "expression", arg: variable };
  const invalid: Array<[string, unknown]> = [
    ["null", null],
    ["unknown message type", { type: "nope", declarations: [], pattern: [] }],
    ["missing declarations", { type: "message", pattern: [] }],
    ["number in pattern", { type: "message", declarations: [], pattern: [1] }],
    [
      "expression without arg or function",
      {
        type: "message",
        declarations: [],
        pattern: [{ type: "expression" }],
      },
    ],
    [
      "options as a Map",
      {
        type: "message",
        declarations: [],
        pattern: [
          {
            type: "expression",
            arg: lit,
            function: { type: "function", name: "number", options: new Map() },
          },
        ],
      },
    ],
    [
      "non-string literal",
      {
        type: "message",
        declarations: [],
        pattern: [{ type: "expression", arg: { type: "literal", value: 1 } }],
      },
    ],
    [
      "input declaration with literal arg",
      {
        type: "message",
        pattern: [],
        declarations: [{ type: "input", name: "x", value: { type: "expression", arg: lit } }],
      },
    ],
    [
      "attribute false",
      {
        type: "message",
        declarations: [],
        pattern: [{ ...expr, attributes: { a: false } }],
      },
    ],
    [
      "markup kind",
      {
        type: "message",
        declarations: [],
        pattern: [{ type: "markup", kind: "self", name: "b" }],
      },
    ],
    [
      "literal selector",
      {
        type: "select",
        declarations: [],
        selectors: [lit],
        variants: [],
      },
    ],
    [
      "variant key of wrong type",
      {
        type: "select",
        declarations: [],
        selectors: [variable],
        variants: [{ keys: [variable], value: [] }],
      },
    ],
    [
      "variant without value",
      {
        type: "select",
        declarations: [],
        selectors: [variable],
        variants: [{ keys: [] }],
      },
    ],
  ];
  it.each(invalid)("rejects %s", (_name, value) => {
    const json = value instanceof Object ? (JSON.parse(JSON.stringify(value)) as unknown) : value;
    // A Map serializes to {} which the JSON Schema accepts; zod must reject the Map itself.
    if (!(_name as string).includes("Map")) expect(validateJson(json)).toBe(false);
    expect(messageSchema.safeParse(value).success).toBe(false);
    expect(isMessage(value)).toBe(false);
  });
});
