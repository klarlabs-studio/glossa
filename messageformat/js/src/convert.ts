/**
 * Conversion between Glossa's canonical data model (plain JSON, exactly
 * `message.schema.json`) and the `messageformat` reference implementation's
 * in-memory model (`functionRef` instead of `function`, CST back-links under a
 * symbol key, `quoted` flags on literals, optional `comment`s).
 */
import type { Model } from "messageformat";
import type {
  Attributes,
  Declaration,
  Expression,
  Literal,
  Markup,
  Message,
  Options,
  Pattern,
  VariableRef,
} from "./model.js";

type RefExpression = Model.Expression;
type RefValue = Model.Literal | Model.VariableRef;

const value = (v: RefValue): Literal | VariableRef =>
  v.type === "literal"
    ? { type: "literal", value: String(v.value) }
    : { type: "variable", name: v.name };

function record<T, U>(
  rec: Record<string, T> | undefined,
  map: (v: T) => U,
): Record<string, U> | undefined {
  if (!rec) return undefined;
  const out: Record<string, U> = {};
  for (const [k, v] of Object.entries(rec)) out[k] = map(v);
  return out;
}

const attributes = (attrs: Model.Attributes | undefined): Attributes | undefined =>
  record<true | Model.Literal, true | Literal>(attrs, (v) =>
    v === true ? true : (value(v) as Literal),
  );

function expression(expr: RefExpression): Expression {
  const out: Expression = { type: "expression" };
  if (expr.arg) out.arg = value(expr.arg);
  if (expr.functionRef) {
    out.function = { type: "function", name: expr.functionRef.name };
    const opts = record(expr.functionRef.options, value);
    if (opts) out.function.options = opts;
  }
  const attrs = attributes(expr.attributes);
  if (attrs) out.attributes = attrs;
  return out;
}

function markup(m: Model.Markup): Markup {
  const out: Markup = { type: "markup", kind: m.kind, name: m.name };
  const opts = record(m.options, value);
  if (opts) out.options = opts;
  const attrs = attributes(m.attributes);
  if (attrs) out.attributes = attrs;
  return out;
}

const pattern = (p: Model.Pattern): Pattern =>
  p.map((el) => (typeof el === "string" ? el : el.type === "markup" ? markup(el) : expression(el)));

function declaration(d: Model.Declaration): Declaration {
  return d.type === "input"
    ? {
        type: "input",
        name: d.name,
        value: expression(d.value) as Expression & { arg: VariableRef },
      }
    : { type: "local", name: d.name, value: expression(d.value) };
}

/** Reference model → canonical plain-JSON data model. */
export function fromReference(msg: Model.Message): Message {
  const declarations = msg.declarations.map(declaration);
  if (msg.type === "message")
    return { type: "message", declarations, pattern: pattern(msg.pattern) };
  return {
    type: "select",
    declarations,
    selectors: msg.selectors.map((s) => ({ type: "variable", name: s.name })),
    variants: msg.variants.map((v) => ({
      keys: v.keys.map((k) =>
        k.type === "*"
          ? k.value === undefined
            ? { type: "*" }
            : { type: "*", value: k.value }
          : { type: "literal", value: String(k.value) },
      ),
      value: pattern(v.value),
    })),
  };
}

// ── canonical → reference ─────────────────────────────────────────────

const refValue = (v: Literal | VariableRef): RefValue => ({ ...v });

const refOptions = (opts: Options | undefined): Model.Options | undefined => record(opts, refValue);

const refAttributes = (attrs: Attributes | undefined): Model.Attributes | undefined =>
  record<true | Literal, true | Model.Literal>(attrs, (v) => (v === true ? true : { ...v }));

function refExpression(expr: Expression): RefExpression {
  const out: Record<string, unknown> = { type: "expression" };
  if (expr.arg) out.arg = refValue(expr.arg);
  if (expr.function) {
    const fn: Model.FunctionRef = { type: "function", name: expr.function.name };
    const opts = refOptions(expr.function.options);
    if (opts) fn.options = opts;
    out.functionRef = fn;
  }
  const attrs = refAttributes(expr.attributes);
  if (attrs) out.attributes = attrs;
  return out as RefExpression;
}

function refMarkup(m: Markup): Model.Markup {
  const out: Model.Markup = { type: "markup", kind: m.kind, name: m.name };
  const opts = refOptions(m.options);
  if (opts) out.options = opts;
  const attrs = refAttributes(m.attributes);
  if (attrs) out.attributes = attrs;
  return out;
}

const refPattern = (p: Pattern): Model.Pattern =>
  p.map((el) =>
    typeof el === "string" ? el : el.type === "markup" ? refMarkup(el) : refExpression(el),
  );

function refDeclaration(d: Declaration): Model.Declaration {
  return d.type === "input"
    ? {
        type: "input",
        name: d.name,
        value: refExpression(d.value) as Model.Expression<Model.VariableRef>,
      }
    : { type: "local", name: d.name, value: refExpression(d.value) };
}

/** Canonical data model → the reference implementation's model. */
export function toReference(msg: Message): Model.Message {
  const declarations = msg.declarations.map(refDeclaration);
  if (msg.type === "message")
    return { type: "message", declarations, pattern: refPattern(msg.pattern) };
  return {
    type: "select",
    declarations,
    selectors: msg.selectors.map((s) => ({ type: "variable", name: s.name })),
    variants: msg.variants.map((v) => ({
      keys: v.keys.map((k) => ({ ...k })),
      value: refPattern(v.value),
    })),
  };
}
