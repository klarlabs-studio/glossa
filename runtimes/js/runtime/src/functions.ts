/**
 * The default MessageFormat 2 functions on `Intl.*`: `:string :number
 * :integer :percent :currency :unit :offset :date :time :datetime`.
 *
 * Kept deliberately small, since this ships to browsers. Behaviour follows the
 * spec (LDML 48) and matches the `messageformat` reference implementation, so
 * both produce the same output for the same locale data.
 */

/** A resolved value, as returned by a function. */
export interface MessageValue {
  type: string;
  /** Text direction of the formatted value; drives bidi isolation. */
  dir?: "ltr" | "rtl" | "auto";
  /** Resolved options, inherited when this value is another function's operand. */
  options?: Record<string, unknown>;
  valueOf(): unknown;
  /** The variant keys this value matches, most preferred first. Absent: can't select. */
  select?(keys: string[]): string[];
  /** Absent: can't be formatted. */
  toParts?(): ExpressionPart[];
}

export interface FunctionContext {
  locales: string[];
  /** Set by the `u:dir` option. */
  dir?: "ltr" | "rtl" | "auto";
  /** Names of the options given as literals (not variables). */
  literals: Set<string>;
  /** Report a non-fatal error (e.g. `bad-option`); formatting continues. */
  onError(type: string): void;
}

/**
 * A function handler. Throw an error with a `type` (e.g. `bad-operand`) to
 * make the expression fall back.
 */
export type MessageFunction = (
  ctx: FunctionContext,
  options: Record<string, unknown>,
  operand?: unknown,
) => MessageValue;

/** A formatted placeholder. */
export interface ExpressionPart {
  type: string;
  dir?: "ltr" | "rtl";
  locale?: string;
  id?: string;
  parts?: Array<{ type: string; value: string }>;
  value?: unknown;
}

type Opts = Record<string, unknown>;
type Numeric = number | bigint;

const cache = new Map<string, unknown>();

/** Intl formatters are expensive to build; reuse them per locale + options. */
function intl<T>(C: new (l: string[], o: object) => T, locales: string[], opts: object): T {
  const key = C.name + locales + JSON.stringify(opts);
  let f = cache.get(key) as T | undefined;
  if (!f) cache.set(key, (f = new C(locales, opts)));
  return f;
}

/** The default text direction of a locale, from CLDR via Intl.Locale. */
export function dirOf(locale: string | undefined): "ltr" | "rtl" | "auto" {
  try {
    const l = new Intl.Locale(locale as string) as Intl.Locale & {
      getTextInfo?(): { direction?: "ltr" | "rtl" };
      textInfo?: { direction?: "ltr" | "rtl" };
    };
    return (
      (l.getTextInfo?.() ?? l.textInfo)?.direction ??
      (/^(Adlm|Arab|Hebr|Mand|Nkoo|Rohg|Syrc|Thaa)$/.test(l.maximize().script ?? "")
        ? "rtl"
        : "ltr")
    );
  } catch {
    return "auto";
  }
}

/** Unwrap a resolved value (or a Number/String object) to its raw value and options. */
export function unwrap(v: unknown): [unknown, Opts | undefined] {
  let opts: Opts | undefined;
  if (v && typeof v === "object") {
    opts = (v as MessageValue).options;
    v = v.valueOf();
  }
  return [v, opts];
}

function asString(v: unknown): string {
  const [x] = unwrap(v);
  if (typeof x === "string") return x;
  throw 0;
}

function asInt(v: unknown): number {
  let [x] = unwrap(v);
  if (typeof x === "string" && /^(0|[1-9]\d*)$/.test(x)) x = +x;
  if (Number.isInteger(x) && (x as number) >= 0) return x as number;
  throw 0;
}

function numeric(operand: unknown): [Numeric, Opts | undefined] {
  let [v, opts] = unwrap(operand);
  if (typeof v === "string") {
    try {
      v = JSON.parse(v);
    } catch {
      // not a number-literal; rejected below
    }
  }
  if (typeof v !== "number" && typeof v !== "bigint") throw "bad-operand";
  return [v, opts];
}

const part = (type: string, locale: string | undefined, dir: string | undefined, rest: object) =>
  ({ type, locale, ...(dir === "ltr" || dir === "rtl" ? { dir } : {}), ...rest }) as ExpressionPart;

export const string: MessageFunction = (ctx, _o, operand) => {
  const value = operand === undefined ? "" : String(unwrap(operand)[0]);
  const key = value.normalize();
  return {
    type: "string",
    dir: ctx.dir ?? "auto",
    valueOf: () => value,
    select: (keys) => (keys.includes(key) ? [key] : []),
    toParts: () => [part("string", ctx.locales[0], ctx.dir, { value })],
  };
};

// Numeric function ids double as bits in `numberOptions` (plain literals, so
// they minify away): 1 :number, 2 :integer, 4 :percent, 8 :currency,
// 16 :unit; 32 marks a digit-size (non-negative integer) option.
const numberOptions: Record<string, number> = {
  minimumIntegerDigits: 59, // digits number integer currency unit
  minimumFractionDigits: 53, // digits number percent unit
  maximumFractionDigits: 53, // digits number percent unit
  minimumSignificantDigits: 61, // digits number percent currency unit
  maximumSignificantDigits: 63, // digits number integer percent currency unit
  roundingIncrement: 57, // digits number currency unit
  roundingMode: 29, // number percent currency unit
  roundingPriority: 29, // number percent currency unit
  trailingZeroDisplay: 29, // number percent currency unit
  signDisplay: 23, // number integer percent unit
  useGrouping: 31, // number integer percent currency unit
  select: 3, // number integer
  currency: 8, // currency
  currencySign: 8, // currency
  unit: 16, // unit
  unitDisplay: 16, // unit
};

function numberValue(
  ctx: FunctionContext,
  value: Numeric,
  opts: Opts,
  canSelect: boolean,
): MessageValue {
  if (opts.useGrouping === "never") opts.useGrouping = false;
  if (canSelect && "select" in opts && !ctx.literals.has("select")) {
    ctx.onError("bad-option");
    canSelect = false;
  }
  const nf = intl(Intl.NumberFormat, ctx.locales, opts);
  const locale = nf.resolvedOptions().locale;
  const dir = dirOf(locale);
  const mv: MessageValue = {
    type: "number",
    dir,
    options: opts,
    valueOf: () => value,
    toParts: () => [part("number", locale, dir, { parts: nf.formatToParts(value) })],
  };
  if (canSelect) {
    mv.select = (keys) => {
      const n =
        opts.style === "percent" ? (typeof value === "bigint" ? value * 100n : value * 100) : value;
      const exact = String(n);
      const res = keys.includes(exact) ? [exact] : [];
      if (opts.select !== "exact") {
        const type = opts.select === "ordinal" ? "ordinal" : "cardinal";
        const cat = intl(Intl.PluralRules, ctx.locales, { ...opts, type }).select(Number(n));
        if (keys.includes(cat)) res.push(cat);
      }
      return res;
    };
  }
  return mv;
}

const numberFunction =
  (fn: number): MessageFunction =>
  (ctx, options, operand) => {
    let [value, inherited] = numeric(operand);
    const style = fn === 4 ? "percent" : fn === 8 ? "currency" : fn === 16 ? "unit" : "decimal";
    const opts: Opts = { ...inherited, style };
    if (fn === 2) {
      if (typeof value === "number" && isFinite(value)) value = Math.round(value);
      opts.maximumFractionDigits = 0;
      opts.minimumFractionDigits = opts.minimumSignificantDigits = undefined;
    }
    for (const name in options) {
      const flags = numberOptions[name] ?? 0;
      const v = options[name];
      try {
        if (flags & fn) opts[name] = flags & 32 ? asInt(v) : asString(v);
        else if (fn === 8 && name === "fractionDigits") {
          const s = asString(v);
          opts.minimumFractionDigits = opts.maximumFractionDigits =
            s === "auto" ? undefined : asInt(s);
        } else if (fn === 8 && name === "currencyDisplay") {
          const s = asString(v);
          if (s === "never") ctx.onError("unsupported-operation");
          else opts[name] = s;
        }
      } catch {
        ctx.onError("bad-option");
      }
    }
    if ((fn === 8 && !opts.currency) || (fn === 16 && !opts.unit)) throw "bad-operand";
    return numberValue(ctx, value, opts, fn < 8);
  };

export const number = numberFunction(1);

const offset: MessageFunction = (ctx, options, operand) => {
  const [value, opts] = numeric(operand);
  const add = "add" in options;
  if (add === "subtract" in options) throw "bad-option";
  let delta: number;
  try {
    delta = asInt(add ? options.add : options.subtract);
  } catch {
    throw "bad-option";
  }
  if (!add) delta = -delta;
  const res = typeof value === "bigint" ? value + BigInt(delta) : value + delta;
  return number(ctx, {}, { valueOf: () => res, options: opts });
};

// Date/time function ids: 0 :date, 1 :time, 2 :datetime.

const dateTimeFunction =
  (kind: number): MessageFunction =>
  (ctx, options, operand) => {
    let [value, inherited] = unwrap(operand);
    const opts: Intl.DateTimeFormatOptions = {};
    if (inherited) {
      opts.calendar = inherited.calendar as string;
      if (kind !== 0) opts.hour12 = inherited.hour12 as boolean;
      opts.timeZone = inherited.timeZone as string;
    }
    if (typeof value === "number" || typeof value === "string") value = new Date(value);
    if (!(value instanceof Date) || isNaN(+value)) throw "bad-operand";
    const date = value;
    const read = (name: string, allowed?: RegExp): string | undefined => {
      if (!(name in options)) return undefined;
      try {
        const s = asString(options[name]);
        if (allowed && !allowed.test(s)) throw 0;
        return s;
      } catch {
        ctx.onError("bad-option");
      }
    };
    const calendar = read("calendar");
    if (calendar) opts.calendar = calendar;
    if (kind !== 0 && "hour12" in options) {
      const h = String(unwrap(options.hour12)[0]);
      if (h === "true" || h === "false") opts.hour12 = h === "true";
      else ctx.onError("bad-option");
    }
    const timeZone = read("timeZone");
    if (timeZone && timeZone !== "input") opts.timeZone = timeZone;
    if (kind !== 1) {
      const fields = (
        read(
          kind ? "dateFields" : "fields",
          /^(weekday|day-weekday|month-day(-weekday)?|year-month-day(-weekday)?)$/,
        ) ?? "year-month-day"
      ).split("-");
      const length = read(kind ? "dateLength" : "length", /^(long|medium|short)$/);
      if (fields.includes("year")) opts.year = "numeric";
      if (fields.includes("month"))
        opts.month = length === "long" ? "long" : length === "short" ? "numeric" : "short";
      if (fields.includes("day")) opts.day = "numeric";
      if (fields.includes("weekday")) opts.weekday = length === "long" ? "long" : "short";
    }
    if (kind !== 0) {
      const precision = read(kind === 1 ? "precision" : "timePrecision", /^(hour|minute|second)$/);
      opts.hour = "numeric";
      if (precision !== "hour") opts.minute = "numeric";
      if (precision === "second") opts.second = "numeric";
      const tzStyle = read(
        "timeZoneStyle",
        /^(long|short)$/,
      ) as Intl.DateTimeFormatOptions["timeZoneName"];
      if (tzStyle) opts.timeZoneName = tzStyle;
    }
    const dtf = intl(Intl.DateTimeFormat, ctx.locales, opts);
    const locale = dtf.resolvedOptions().locale;
    const dir = dirOf(locale);
    return {
      type: "datetime",
      dir,
      options: opts as Opts,
      valueOf: () => date,
      toParts: () => [part("datetime", locale, dir, { parts: dtf.formatToParts(date) })],
    };
  };

export const unknown = (value: unknown): MessageValue => ({
  type: "unknown",
  dir: "auto",
  valueOf: () => value,
  toParts: () => [{ type: "unknown", value }],
});

export const builtins: Record<string, MessageFunction> = {
  string,
  number,
  integer: numberFunction(2),
  percent: numberFunction(4),
  currency: numberFunction(8),
  unit: numberFunction(16),
  offset,
  date: dateTimeFunction(0),
  time: dateTimeFunction(1),
  datetime: dateTimeFunction(2),
};
