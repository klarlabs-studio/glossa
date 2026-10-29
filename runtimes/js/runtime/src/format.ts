/**
 * A minimal MessageFormat 2 interpreter over the precompiled data model.
 * There's no parser: release artifacts carry the canonical data model.
 *
 * Formatting never throws. Errors go to `onError`, and the failing
 * placeholder renders as its fallback (`{$name}`, `{|literal|}`, `{:fn}`).
 */
import { builtins, dirOf, number, string, unknown, unwrap } from "./functions.js";
import type {
  ExpressionPart,
  FunctionContext,
  MessageFunction,
  MessageValue,
} from "./functions.js";
import type { Expression, Literal, Markup, Message, Pattern, VariableRef } from "./model.js";

/** An error reported while formatting. `source` identifies the placeholder. */
export interface MessageError {
  type: string;
  source: string;
}

export interface FormatOptions {
  /** `"default"` (the default) isolates placeholders with Unicode bidi isolates, per the spec. */
  bidiIsolation?: "default" | "none";
  /** The message's base direction; derived from the locale when omitted. */
  dir?: "ltr" | "rtl" | "auto";
  /** Extra functions, keyed by name without the colon; they win over the built-ins. */
  functions?: Record<string, MessageFunction>;
  /** Called once per error. Formatting continues with a fallback. */
  onError?: (error: MessageError) => void;
}

export interface TextPart {
  type: "text";
  value: string;
}

export interface MarkupPart {
  type: "markup";
  kind: "open" | "standalone" | "close";
  name: string;
  id?: string;
  options?: Record<string, unknown>;
}

export interface BidiIsolationPart {
  type: "bidiIsolation";
  value: string;
}

export interface FallbackPart {
  type: "fallback";
  source: string;
}

export type Part = TextPart | MarkupPart | BidiIsolationPart | FallbackPart | ExpressionPart;

interface Resolved extends MessageValue {
  source: string;
  isolate?: boolean;
  id?: string;
}

const LRI = "\u2066";
const RLI = "\u2067";
const FSI = "\u2068";
const PDI = "\u2069";

const fallback = (source: string): Resolved => ({
  type: "fallback",
  source,
  valueOf: () => undefined,
  toParts: () => [{ type: "fallback", source } as ExpressionPart],
});

const errorType = (e: unknown): string =>
  typeof e === "string"
    ? e
    : e instanceof RangeError
      ? "bad-option"
      : typeof (e as { type?: unknown })?.type === "string"
        ? (e as { type: string }).type
        : "bad-function-result";

const sourceOf = (v: Literal | VariableRef): string =>
  v.type === "literal" ? `|${v.value.replace(/[\\|]/g, "\\$&")}|` : `$${v.name}`;

/** Format a message to parts: text, markup, bidi isolates, fallbacks and formatted values. */
export function formatToParts(
  message: Message,
  locale: string | string[],
  values: Record<string, unknown> = {},
  opts: FormatOptions = {},
): Part[] {
  const report = (type: string, source: string) => opts.onError?.({ type, source });
  try {
    return interpret(message, ([] as string[]).concat(locale), values ?? {}, opts, report);
  } catch {
    report("bad-message", "\ufffd");
    return [{ type: "fallback", source: "\ufffd" }];
  }
}

/** Join formatted parts into the string `format()` returns: markup renders as nothing. */
export function partsToString(parts: Part[]): string {
  let out = "";
  for (const p of parts) {
    out +=
      p.type === "markup"
        ? ""
        : p.type === "fallback"
          ? `{${(p as FallbackPart).source}}`
          : (p as ExpressionPart).parts
            ? (p as ExpressionPart).parts!.map((x) => x.value).join("")
            : String((p as ExpressionPart).value);
  }
  return out;
}

/** Format a message to a string. */
export function format(
  message: Message,
  locale: string | string[],
  values?: Record<string, unknown>,
  opts?: FormatOptions,
): string {
  return partsToString(formatToParts(message, locale, values, opts));
}

function interpret(
  msg: Message,
  locales: string[],
  values: Record<string, unknown>,
  opts: FormatOptions,
  report: (type: string, source: string) => void,
): Part[] {
  const functions = { ...builtins, ...opts.functions };
  const declarations = new Map(msg.declarations.map((d) => [d.name, d]));
  const locals = new Map<string, Resolved>();
  const plainCtx: FunctionContext = { locales, literals: new Set(), onError: () => {} };

  /**
   * The value of a variable: a resolved declaration, or an external value.
   * `external` is set inside `.input` declarations, whose operand is the
   * external value the declaration shadows.
   */
  const lookup = (name: string, external?: boolean): unknown => {
    if (!external) {
      let local = locals.get(name);
      const decl = declarations.get(name);
      if (!local && decl) locals.set(name, (local = resolve(decl.value, decl.type === "input")));
      if (local) return local;
    }
    // Names are compared in NFC; the data model's names already are.
    const key = Object.hasOwn(values, name)
      ? name
      : Object.keys(values).find((k) => k.normalize() === name);
    const v = key === undefined ? undefined : values[key];
    if (v === undefined) report("unresolved-variable", `$${name}`);
    return v;
  };

  const valueOf = (v: Literal | VariableRef, external?: boolean): unknown =>
    v.type === "literal" ? v.value : lookup(v.name, external);

  /** Resolve an expression to a value. Never throws: failures become fallbacks. */
  function resolve(expr: Expression, external?: boolean): Resolved {
    const { arg, function: fn } = expr;
    const source = arg ? sourceOf(arg) : `:${fn!.name}`;
    if (!fn) {
      if (arg!.type === "literal") return { ...string(plainCtx, {}, arg!.value), source };
      const v = lookup(arg!.name, external);
      if (v === undefined) return fallback(source);
      if (!external && locals.has(arg!.name)) {
        const local = v as Resolved;
        return local.type === "fallback" ? fallback(source) : { ...local, source };
      }
      const t = typeof v;
      const f =
        t === "number" || t === "bigint" || v instanceof Number
          ? number
          : t === "string" || v instanceof String
            ? string
            : null;
      return { ...(f ? f(plainCtx, {}, v) : unknown(v)), source };
    }
    try {
      const operand = arg ? [valueOf(arg, external)] : [];
      if ((operand[0] as Resolved | undefined)?.type === "fallback") throw "bad-operand";
      const handler = functions[fn.name];
      if (!handler) throw "unknown-function";
      const options: Record<string, unknown> = {};
      const literals = new Set<string>();
      let dir: FunctionContext["dir"];
      let id: string | undefined;
      for (const [name, ov] of Object.entries(fn.options ?? {})) {
        const v = valueOf(ov, external);
        if (name === "u:dir") {
          const d = String(unwrap(v)[0]);
          if (d === "ltr" || d === "rtl" || d === "auto") dir = d;
          else if (d !== "inherit") report("bad-option", source);
        } else if (name === "u:id") {
          id = String(unwrap(v)[0]);
        } else if (!name.startsWith("u:")) {
          options[name] = v;
          if (ov.type === "literal") literals.add(name);
        }
      }
      const ctx: FunctionContext = {
        locales,
        dir,
        literals,
        onError: (type) => report(type, source),
      };
      const mv = handler(ctx, options, ...operand);
      return { ...mv, source, dir: dir ?? mv.dir, isolate: !!dir, id };
    } catch (e) {
      report(errorType(e), source);
      return fallback(source);
    }
  }

  const markup = (m: Markup): MarkupPart => {
    const p: MarkupPart = { type: "markup", kind: m.kind, name: m.name };
    for (const [name, ov] of Object.entries(m.options ?? {})) {
      if (name === "u:dir") {
        report("bad-option", sourceOf(ov));
        continue;
      }
      const v = unwrap(valueOf(ov))[0];
      if (name === "u:id") p.id = String(v);
      else (p.options ??= {})[name] = v;
    }
    return p;
  };

  let pattern: Pattern;
  if (msg.type === "select") {
    // Pattern selection per the spec: resolve each selector's preferred keys,
    // keep the variants whose keys all match (or are `*`), then sort them by
    // preference, last selector first, and take the best one.
    const prefs = msg.selectors.map((sel, i) => {
      const mv = resolve({ type: "expression", arg: sel });
      const keys: string[] = [];
      for (const v of msg.variants) if (v.keys[i]!.type !== "*") keys.push(v.keys[i]!.value!);
      try {
        if (!mv.select) throw 0;
        return keys.length ? mv.select(keys) : [];
      } catch {
        report("bad-selector", mv.source);
        return [];
      }
    });
    const rank = (keys: Array<{ type: string; value?: string }>, i: number) =>
      keys[i]!.type === "*" ? prefs[i]!.length : prefs[i]!.indexOf(keys[i]!.value!);
    const candidates = msg.variants.filter((v) => v.keys.every((_k, i) => rank(v.keys, i) >= 0));
    for (let i = prefs.length; i--; ) candidates.sort((a, b) => rank(a.keys, i) - rank(b.keys, i));
    pattern = candidates[0]!.value;
  } else {
    pattern = msg.pattern;
  }

  const bidi = opts.bidiIsolation !== "none";
  const msgDir = opts.dir ?? dirOf(locales[0]);
  const parts: Part[] = [];
  for (const el of pattern) {
    if (typeof el === "string") {
      parts.push({ type: "text", value: el });
    } else if (el.type === "markup") {
      parts.push(markup(el));
    } else {
      const mv = resolve(el);
      let dir = mv.dir;
      let formatted: Part[];
      try {
        if (!mv.toParts) throw "not-formattable";
        formatted = mv.toParts();
        if (mv.id) for (const p of formatted) (p as ExpressionPart).id = mv.id;
      } catch (e) {
        report(errorType(e), mv.source);
        formatted = [{ type: "fallback", source: mv.source }];
        dir = undefined;
      }
      if (bidi && (msgDir !== "ltr" || dir !== "ltr" || mv.isolate)) {
        const open = dir === "ltr" ? LRI : dir === "rtl" ? RLI : FSI;
        parts.push({ type: "bidiIsolation", value: open }, ...formatted, {
          type: "bidiIsolation",
          value: PDI,
        });
      } else {
        parts.push(...formatted);
      }
    }
  }
  return parts;
}
