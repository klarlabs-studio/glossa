/**
 * The Unicode suite's test-only functions (`:test:function`, `:test:select`,
 * `:test:format`, specified in the suite README) on the runtime's function
 * API. Test use only.
 */
import type { MessageFunction, MessageValue } from "../index.js";

interface TestState {
  decimalPlaces: number;
  failsFormat: boolean;
  failsSelect: boolean;
}

const fail = (type: string) => Object.assign(new Error(type), { type });

function asInt(v: unknown): number | undefined {
  const x = v && typeof v === "object" ? v.valueOf() : v;
  const n = typeof x === "string" && /^(0|[1-9]\d*)$/.test(x) ? Number(x) : x;
  return Number.isInteger(n) ? (n as number) : undefined;
}

const make =
  (canFormat: boolean, canSelect: boolean): MessageFunction =>
  (ctx, options, operand) => {
    const st: TestState = { decimalPlaces: 0, failsFormat: false, failsSelect: false };
    let input: unknown = operand;
    if (input && typeof input === "object") {
      const prev = input as MessageValue & { test?: TestState };
      if (prev.test) Object.assign(st, prev.test);
      input = prev.valueOf();
    }
    if (typeof input === "string") {
      try {
        input = JSON.parse(input);
      } catch {
        // rejected below
      }
    }
    if (typeof input !== "number") throw fail("bad-operand");
    const value = input;
    if ("decimalPlaces" in options) {
      const dp = asInt(options.decimalPlaces);
      if (dp !== 0 && dp !== 1) throw fail("bad-option");
      st.decimalPlaces = dp;
    }
    if ("fails" in options) {
      const f =
        options.fails && typeof options.fails === "object"
          ? options.fails.valueOf()
          : options.fails;
      if (f === "select" || f === "always") st.failsSelect = true;
      if (f === "format" || f === "always") st.failsFormat = true;
      if (f !== "never" && !st.failsSelect && !st.failsFormat) ctx.onError("bad-option");
    }
    const mv: MessageValue & { test: TestState } = { type: "test", test: st, valueOf: () => value };
    if (canSelect) {
      mv.select = (keys) => {
        if (st.failsSelect) throw fail("bad-option");
        const pref = value !== 1 ? [] : st.decimalPlaces ? ["1.0", "1"] : ["1"];
        return pref.filter((k) => keys.includes(k));
      };
    }
    if (canFormat) {
      mv.toParts = () => {
        if (st.failsFormat) throw fail("bad-option");
        const abs = Math.abs(value);
        const int = Math.floor(abs);
        const frac = st.decimalPlaces ? `.${Math.floor((abs - int) * 10)}` : "";
        return [
          {
            type: "test",
            locale: "und",
            parts: [{ type: "test", value: `${value < 0 ? "-" : ""}${int}${frac}` }],
          },
        ];
      };
    }
    return mv;
  };

export const testFunctions: Record<string, MessageFunction> = {
  "test:function": make(true, true),
  "test:select": make(false, true),
  "test:format": make(true, false),
};
