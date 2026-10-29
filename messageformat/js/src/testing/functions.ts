/**
 * The Unicode suite's test-only functions (`:test:function`, `:test:select`,
 * `:test:format`), as specified in the suite README, for the reference
 * formatter. Never register these outside of tests.
 */
import { MessageFunctionError } from "messageformat";
import { asPositiveInteger, asString } from "messageformat/functions";
import type {
  MessageFunction,
  MessageFunctionContext,
  MessageValue,
} from "messageformat/functions";

interface TestOptions {
  canFormat: boolean;
  canSelect: boolean;
  decimalPlaces: 0 | 1;
  failsFormat: boolean;
  failsSelect: boolean;
}

type TestValue = MessageValue<"test"> & { readonly options: TestOptions };

function testFunction(
  ctx: MessageFunctionContext,
  options: Record<string, unknown>,
  canFormat: boolean,
  canSelect: boolean,
  operand: unknown,
): TestValue {
  const opt: TestOptions = {
    canFormat,
    canSelect,
    decimalPlaces: 0,
    failsFormat: false,
    failsSelect: false,
  };
  let input = operand;
  if (typeof input === "object" && input !== null && typeof input.valueOf === "function") {
    const prev = input as Partial<TestValue>;
    if (prev.type === "test" && prev.options) {
      Object.assign(opt, prev.options, { canFormat, canSelect });
    }
    input = input.valueOf();
  }
  if (typeof input === "string") {
    try {
      input = JSON.parse(input);
    } catch {
      // not numeric; rejected below
    }
  }
  if (typeof input !== "number")
    throw new MessageFunctionError("bad-operand", "Input is not numeric");
  const value = input;
  if ("decimalPlaces" in options) {
    let dp: number | undefined;
    try {
      dp = asPositiveInteger(options.decimalPlaces);
    } catch {
      dp = undefined;
    }
    if (dp !== 0 && dp !== 1) {
      throw new MessageFunctionError(
        "bad-option",
        `Invalid decimalPlaces=${String(options.decimalPlaces)}`,
      );
    }
    opt.decimalPlaces = dp;
  }
  if ("fails" in options) {
    let fails: string | undefined;
    try {
      fails = asString(options.fails);
    } catch {
      fails = undefined;
    }
    if (fails === "select" || fails === "always") opt.failsSelect = true;
    if (fails === "format" || fails === "always") opt.failsFormat = true;
    if (fails !== "never" && !opt.failsSelect && !opt.failsFormat) {
      ctx.onError("bad-option", `Invalid fails=${String(options.fails)}`);
    }
  }
  const toString = (): string => {
    if (opt.failsFormat) throw new MessageFunctionError("bad-option", "Formatting failed");
    const abs = Math.abs(value);
    const int = Math.floor(abs);
    const frac = opt.decimalPlaces ? `.${Math.floor((abs - int) * 10)}` : "";
    return `${value < 0 ? "-" : ""}${int}${frac}`;
  };
  const tv: TestValue = {
    type: "test",
    get options() {
      return { ...opt };
    },
    valueOf: () => value,
  };
  if (canSelect) {
    tv.selectKey = (keys) => {
      if (opt.failsSelect) throw new MessageFunctionError("bad-option", "Selection failed");
      if (value === 1) {
        if (opt.decimalPlaces === 1 && keys.has("1.0")) return "1.0";
        if (keys.has("1")) return "1";
      }
      return null;
    };
  }
  if (canFormat) {
    tv.toString = toString;
    tv.toParts = () => [
      { type: "test", locale: "und", parts: [{ type: "test", value: toString() }] },
    ];
  }
  return tv;
}

const make =
  (canFormat: boolean, canSelect: boolean): MessageFunction<"test"> =>
  (ctx, options, operand) =>
    testFunction(ctx, options, canFormat, canSelect, operand);

export const testFunctions: Record<string, MessageFunction<"test">> = {
  "test:function": make(true, true),
  "test:select": make(false, true),
  "test:format": make(true, false),
};
