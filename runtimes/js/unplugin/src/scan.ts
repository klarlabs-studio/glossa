/**
 * Finds usages in a module's code **after** the framework transforms: an
 * ESTree AST from the bundler's parser, where Vue templates are render
 * functions, JSX is `jsx()`/`createElement()` calls, `.astro` markup is
 * `$$renderComponent()` calls and TypeScript is gone. Offsets are into the
 * transformed code; `locate.ts` maps them back to the original file.
 *
 * Recognized (RFC 0004 §2.1, runtimes/testdata/usages/README.md):
 * - `t`: `t("…")`, `$t("…")`, `x.t("…")`, `x.$t("…")`, and the compiled
 *   forms `_ctx.$t(…)`, `_unref(t)(…)`, `(0, x.t)(…)`.
 * - `component`: `<T id>` and `<GlossaText id>` as factory calls (`jsx`,
 *   `createElement`, `createVNode`, `ssrRenderComponent`, `renderComponent`, …).
 * - `element`: `<glossa-text|rich|plural|select key|message>` as factory
 *   calls, and in markup strings the compilers emit.
 * - `accessor`: `<receiver>.<path>(…)` whose path is a key's accessor path.
 *
 * Only literal keys count: a string literal or a template literal without
 * substitutions, right at the call site.
 */
import { ELEMENTS, elementKey, scanMarkup } from "./markup.js";
import { isKey } from "./keys.js";

export type Kind = "t" | "component" | "element" | "accessor";

/** One usage in transformed code. */
export interface Hit {
  key: string;
  kind: Kind;
  /** Offset of the key's first character (an accessor's first path segment) in the code. */
  offset: number;
  /** What to find at the original position: the key, or the accessor's path segments. */
  text: string | string[];
  /** The named functions and classes around the hit, innermost first (for the JSX component rule). */
  scope?: Scope;
}

/**
 * A named function or class as the transformed code names it. Bundlers
 * rename (esbuild: `Card` → `Card2`) and invent names (`Header_default`),
 * so the collector reads each name back from the original source and picks
 * the nearest capitalized one.
 */
export interface Scope {
  name: string;
  /** Offset of the name in the transformed code. */
  offset: number;
  parent?: Scope;
}

// A loose ESTree node: the parsers (Rollup, Rolldown/Oxc, acorn) agree on
// the shape and on UTF-16 `start`/`end` offsets.
export interface Node {
  type: string;
  start: number;
  end: number;
  [field: string]: unknown;
}

const isNode = (v: unknown): v is Node =>
  typeof v === "object" && v !== null && typeof (v as Node).type === "string";

/** Vue's, React's, Preact's and Astro's element factories. */
const FACTORIES = new Set([
  "jsx",
  "jsxs",
  "jsxDEV",
  "createElement",
  "h",
  "createVNode",
  "createBlock",
  "createElementVNode",
  "createElementBlock",
  "ssrRenderComponent",
  "renderComponent",
]);
const COMPONENTS = new Set(["T", "GlossaText"]);
const PROPS_WRAPPERS = new Set(["mergeProps", "normalizeProps", "guardReactiveProps"]);
const UNREF = new Set(["unref", "_unref"]);

/** `_createVNode`, `$$renderComponent` → `createVNode`, `renderComponent`. */
const bare = (name: string) => name.replace(/^[_$]+/, "");

function unwrap(n: Node): Node {
  let cur = n;
  for (;;) {
    if (cur.type === "ChainExpression" || cur.type === "ParenthesizedExpression") {
      cur = cur.expression as Node;
    } else if (cur.type === "SequenceExpression") {
      const list = cur.expressions as Node[];
      cur = list[list.length - 1]!;
    } else if (cur.type === "TSAsExpression" || cur.type === "TSNonNullExpression") {
      cur = cur.expression as Node;
    } else {
      return cur;
    }
  }
}

function stringValue(n: Node | undefined): string | undefined {
  if (!n) return undefined;
  if (n.type === "Literal" && typeof n.value === "string") return n.value;
  if (n.type === "StringLiteral") return n.value as string;
  return undefined;
}

/** The property a member expression reads, when it's static: `a.b`, `a["b"]`. */
function memberName(n: Node): string | undefined {
  if (n.type !== "MemberExpression") return undefined;
  const p = n.property as Node;
  if (!n.computed) return p.type === "Identifier" ? (p.name as string) : undefined;
  return stringValue(p);
}

/** The name a callee or reference goes by: `x`, `a.x`, `a["x"]`, `unref(x)`. */
function refName(n: Node): string | undefined {
  const u = unwrap(n);
  if (u.type === "Identifier") return u.name as string;
  if (u.type === "MemberExpression") return memberName(u);
  if (u.type === "CallExpression") {
    const args = u.arguments as Node[];
    const callee = refName(u.callee as Node);
    if (callee && UNREF.has(callee) && args.length === 1) return refName(args[0]!);
  }
  return undefined;
}

/** A literal key at the call site: its value and the offset of its first character. */
export function literalKey(n: Node | undefined): { key: string; offset: number } | undefined {
  if (!n) return undefined;
  const u = unwrap(n);
  let key: string | undefined;
  if (u.type === "TemplateLiteral") {
    const quasis = u.quasis as Node[];
    if ((u.expressions as Node[]).length === 0 && quasis.length === 1) {
      key = (quasis[0]!.value as { cooked?: string | null }).cooked ?? undefined;
    }
  } else {
    key = stringValue(u);
  }
  return key !== undefined && isKey(key) ? { key, offset: u.start + 1 } : undefined;
}

// ── Cooking JS string literals (for markup inside strings) ─────────────

const SIMPLE_ESCAPES: Record<string, string> = {
  n: "\n",
  t: "\t",
  r: "\r",
  b: "\b",
  f: "\f",
  v: "\v",
  "0": "\0",
};

/** A string literal's text with, for every UTF-16 unit, its offset in the code. */
export function cook(raw: string, base: number): { text: string; at: number[] } {
  let text = "";
  const at: number[] = [];
  const add = (s: string, pos: number) => {
    text += s;
    for (let k = 0; k < s.length; k++) at.push(pos);
  };
  for (let i = 0; i < raw.length; ) {
    const c = raw[i]!;
    if (c !== "\\") {
      add(c, base + i);
      i++;
      continue;
    }
    const d = raw[i + 1] ?? "";
    if (d === "\r" || d === "\n" || d === "\u2028" || d === "\u2029") {
      i += d === "\r" && raw[i + 2] === "\n" ? 3 : 2;
    } else if (d === "x") {
      add(String.fromCharCode(parseInt(raw.slice(i + 2, i + 4), 16)), base + i);
      i += 4;
    } else if (d === "u" && raw[i + 2] === "{") {
      const close = raw.indexOf("}", i + 3);
      add(String.fromCodePoint(parseInt(raw.slice(i + 3, close), 16)), base + i);
      i = close + 1;
    } else if (d === "u") {
      add(String.fromCharCode(parseInt(raw.slice(i + 2, i + 6), 16)), base + i);
      i += 6;
    } else {
      add(SIMPLE_ESCAPES[d] ?? d, base + i);
      i += 2;
    }
  }
  return { text, at };
}

// ── The walk ─────────────────────────────────────────────────────────────

export interface ScanOptions {
  /** Accessor path → key (`accessorPaths`). */
  accessors: Map<string, string>;
}

/** The name a function or class gives its body: its own, or (arrows) the const it's assigned to. */
function scopeName(n: Node, parent: Node | undefined, grand: Node | undefined): Node | undefined {
  switch (n.type) {
    case "FunctionDeclaration":
    case "FunctionExpression":
    case "ClassDeclaration":
    case "ClassExpression":
      return (n.id as Node | null) ?? undefined;
    case "ArrowFunctionExpression":
      if (
        parent?.type === "VariableDeclarator" &&
        parent.init === n &&
        grand?.type === "VariableDeclaration" &&
        grand.kind === "const" &&
        (parent.id as Node).type === "Identifier"
      ) {
        return parent.id as Node;
      }
      return undefined;
    default:
      return undefined;
  }
}

export function scan(code: string, program: Node, options: ScanOptions): Hit[] {
  const hits: Hit[] = [];
  // Module-level `const _hoisted_1 = { … }`: Vue hoists static props objects.
  const hoisted = new Map<string, Node>();
  for (const stmt of (program.body as Node[] | undefined) ?? []) {
    const decl = stmt.type === "ExportNamedDeclaration" ? (stmt.declaration as Node | null) : stmt;
    if (decl?.type !== "VariableDeclaration" || decl.kind !== "const") continue;
    for (const d of decl.declarations as Node[]) {
      const id = d.id as Node;
      if (id.type === "Identifier" && d.init) hoisted.set(id.name as string, d.init as Node);
    }
  }

  const propsObjects = (n: Node | undefined, depth = 0): Node[] => {
    if (!n || depth > 4) return [];
    const u = unwrap(n);
    if (u.type === "ObjectExpression") return [u];
    if (u.type === "Identifier") {
      const init = hoisted.get(u.name as string);
      return init ? propsObjects(init, depth + 1) : [];
    }
    if (u.type === "CallExpression") {
      const callee = refName(u.callee as Node);
      if (callee && PROPS_WRAPPERS.has(bare(callee))) {
        return (u.arguments as Node[]).flatMap((a) => propsObjects(a, depth + 1));
      }
    }
    return [];
  };

  const props = (objects: Node[]): Map<string, { value: string; offset: number; node: Node }> => {
    const out = new Map<string, { value: string; offset: number; node: Node }>();
    for (const o of objects) {
      for (const p of o.properties as Node[]) {
        if (p.type !== "Property") continue;
        const k = p.key as Node;
        const name = p.computed ? stringValue(k) : k.type === "Identifier" ? (k.name as string) : stringValue(k);
        if (name === undefined || out.has(name)) continue;
        const lit = literalKey(p.value as Node);
        out.set(name, { value: lit?.key ?? "", offset: lit?.offset ?? -1, node: p.value as Node });
      }
    }
    return out;
  };

  /** What a factory's argument renders: a Glossa component, a Glossa element, or neither. */
  const designator = (n: Node): "component" | "element" | undefined => {
    const s = stringValue(unwrap(n));
    if (s !== undefined) return ELEMENTS.has(s) ? "element" : undefined;
    const name = refName(n);
    if (!name) return undefined;
    if (COMPONENTS.has(name)) return "component";
    const resolved = /^_component_(.+)$/.exec(name)?.[1];
    if (resolved && COMPONENTS.has(resolved)) return "component";
    if (resolved && ELEMENTS.has(resolved.replace(/_/g, "-"))) return "element";
    return undefined;
  };

  const markup = (text: string, at: (i: number) => number, component: Scope | undefined) => {
    for (const m of scanMarkup(text)) {
      hits.push({ key: m.key, kind: "element", offset: at(m.offset), text: m.key, scope: component });
    }
  };

  const markupArg = (n: Node, component: Scope | undefined) => {
    const u = unwrap(n);
    if (u.type === "TemplateLiteral") {
      for (const q of u.quasis as Node[]) {
        const { text, at } = cook(code.slice(q.start, q.end), q.start);
        markup(text, (i) => at[i]!, component);
      }
    } else if (stringValue(u) !== undefined) {
      const { text, at } = cook(code.slice(u.start + 1, u.end - 1), u.start + 1);
      markup(text, (i) => at[i]!, component);
    }
  };

  const factory = (args: Node[], component: Scope | undefined) => {
    for (let i = 0; i < Math.min(args.length, 3); i++) {
      const kind = designator(args[i]!);
      if (!kind) continue;
      for (const a of args.slice(i + 1, i + 3)) {
        const objects = propsObjects(a);
        if (objects.length === 0) continue;
        const ps = props(objects);
        const hit = kind === "component" ? ps.get("id") : elementKey(ps);
        if (hit && hit.offset >= 0 && isKey(hit.value)) {
          hits.push({ key: hit.value, kind, offset: hit.offset, text: hit.value, scope: component });
        }
        break;
      }
      return;
    }
  };

  const accessor = (callee: Node, component: Scope | undefined) => {
    if (options.accessors.size === 0) return;
    const segs: Node[] = [];
    let cur = unwrap(callee);
    while (cur.type === "MemberExpression" && !cur.computed && (cur.property as Node).type === "Identifier") {
      segs.unshift(cur.property as Node);
      cur = unwrap(cur.object as Node);
    }
    const names = segs.map((s) => s.name as string);
    for (let from = 0; from < names.length; from++) {
      const path = names.slice(from);
      const key = options.accessors.get(path.join("."));
      if (key === undefined) continue;
      if (path.length === 1) {
        const receiver = from > 0 ? names[from - 1] : refName(cur);
        if (receiver !== "messages") continue;
      }
      hits.push({ key, kind: "accessor", offset: segs[from]!.start, text: path, scope: component });
      return;
    }
  };

  const call = (n: Node, component: Scope | undefined) => {
    const callee = unwrap(n.callee as Node);
    const args = n.arguments as Node[];
    const name = refName(callee);
    if (name === "t" || name === "$t") {
      const lit = literalKey(args[0]);
      if (lit) hits.push({ key: lit.key, kind: "t", offset: lit.offset, text: lit.key, scope: component });
      return;
    }
    if (name && FACTORIES.has(bare(name))) return factory(args, component);
    if (name && bare(name) === "createStaticVNode" && args[0]) return markupArg(args[0], component);
    if (callee.type === "Identifier" && name === "_push") {
      for (const a of args) markupArg(a, component);
      return;
    }
    if (callee.type === "MemberExpression") accessor(callee, component);
  };

  const walk = (n: Node, parent: Node | undefined, grand: Node | undefined, component: Scope | undefined) => {
    const id = scopeName(n, parent, grand);
    const own: Scope | undefined = id ? { name: id.name as string, offset: id.start, parent: component } : component;
    if (n.type === "CallExpression") call(n, own);
    else if (n.type === "TaggedTemplateExpression" && refName(n.tag as Node) === "$$render") {
      markupArg(n.quasi as Node, own);
    }
    for (const key in n) {
      if (key === "type" || key === "start" || key === "end" || key === "loc" || key === "range") continue;
      const v = n[key];
      if (Array.isArray(v)) {
        for (const c of v) if (isNode(c)) walk(c, n, parent, own);
      } else if (isNode(v)) {
        walk(v, n, parent, own);
      }
    }
  };
  walk(program, undefined, undefined, undefined);
  return hits;
}
