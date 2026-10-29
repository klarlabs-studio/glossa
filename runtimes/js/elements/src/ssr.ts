/**
 * Server-side and build-time rendering of `<glossa-*>` markup
 * (`@klarlabs-studio/glossa-elements/ssr`): pure string processing, no DOM and no Lit.
 *
 * `prerender(html, runtime)` replaces the inline default inside every
 * `<glossa-text|rich|plural|select>` whose message resolves with the
 * translation (escaped text and safe elements only), and adds `lang`/`dir` to
 * `<glossa-provider>` tags that have none. A static page then ships translated
 * HTML that reads right without JavaScript; when the elements load, the same
 * translation renders in their shadow root, so nothing flickers. Elements
 * whose message is missing keep their inline default. `<script>`, `<style>`,
 * `<textarea>`, `<template>` and comments are left alone.
 */
import type { Runtime } from "@klarlabs-studio/glossa-runtime";

import { escapeHtml, parseVars, partsToTree, resolveParts, treeToHtml } from "./parts.js";

export interface PrerenderOptions {
  /** Extra attributes for every element whose content was replaced (e.g. `data-allow-mismatch`). */
  attributes?: Record<string, string>;
}

const ATTR = /([^\s"'>/=]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>`]+)))?/g;
const TAG = /(?:"[^"]*"|'[^']*'|[^'">])*/.source;
const SCAN = new RegExp(
  `<!--[\\s\\S]*?-->|<(script|style|textarea|template)\\b[\\s\\S]*?<\\/\\1\\s*>|<glossa-(text|rich|plural|select|provider)(?=[\\s/>])(${TAG})>`,
  "gi",
);

const ENTITIES: Record<string, string> = { amp: "&", lt: "<", gt: ">", quot: '"', apos: "'" };

/** Decode the character references an HTML serializer puts in attribute values. */
const decode = (s: string) =>
  s.replace(/&(#x[0-9a-f]+|#\d+|[a-z]+);/gi, (m, e: string) => {
    if (e[0] !== "#") return ENTITIES[e.toLowerCase()] ?? m;
    const n = e[1] === "x" || e[1] === "X" ? parseInt(e.slice(2), 16) : parseInt(e.slice(1), 10);
    try {
      return String.fromCodePoint(n);
    } catch {
      return m;
    }
  });

const attrs = (source: string) => {
  const out: Record<string, string> = {};
  for (const m of source.matchAll(ATTR)) {
    out[m[1]!.toLowerCase()] = decode(m[2] ?? m[3] ?? m[4] ?? "");
  }
  return out;
};

const escapeAttr = (s: string) => escapeHtml(s).replace(/"/g, "&quot;");

/** The values an element formats its message with, from its attributes. */
function valuesOf(kind: string, a: Record<string, string>): Record<string, unknown> | undefined {
  const vars = parseVars(a.vars);
  if (kind === "plural") return { ...vars, count: Number(a.count ?? 0) };
  if (kind === "select") return { ...vars, [a.name || "value"]: a.value ?? "" };
  return kind === "rich" ? vars : undefined;
}

/** Index just after the `</glossa-…>` that closes the element opened before `from`, or -1. */
function closing(html: string, tag: string, from: number): [number, number] | [-1, -1] {
  const re = new RegExp(`<(/?)${tag}(?=[\\s/>])${TAG}>`, "gi");
  re.lastIndex = from;
  let depth = 1;
  for (let m = re.exec(html); m; m = re.exec(html)) {
    depth += m[1] ? -1 : 1;
    if (depth === 0) return [m.index, re.lastIndex];
  }
  return [-1, -1];
}

/** Render the `<glossa-*>` elements in `html` with `runtime`'s active release and locale. */
export function prerender(
  html: string,
  runtime: Pick<Runtime, "parts" | "locale" | "dir">,
  opts: PrerenderOptions = {},
): string {
  const extra = (a: Record<string, string>) =>
    Object.entries(opts.attributes ?? {})
      .filter(([k]) => !(k.toLowerCase() in a))
      .map(([k, v]) => ` ${k}="${escapeAttr(v)}"`)
      .join("");
  let out = "";
  let at = 0;
  const re = new RegExp(SCAN.source, SCAN.flags);
  for (let m = re.exec(html); m; m = re.exec(html)) {
    const kind = m[2]?.toLowerCase();
    if (!kind) continue;
    const source = m[3]!;
    const a = attrs(source);
    const openEnd = re.lastIndex;
    if (kind === "provider") {
      if (runtime.locale && !("lang" in a) && !("dir" in a)) {
        out +=
          html.slice(at, m.index) +
          `<glossa-provider lang="${escapeAttr(runtime.locale)}" dir="${runtime.dir}"${source}>`;
        at = openEnd;
      }
      continue;
    }
    const id = a.key || a.message;
    const parts = id ? resolveParts(runtime, id, valuesOf(kind, a)) : undefined;
    const [closeStart, closeEnd] = closing(html, `glossa-${kind}`, openEnd);
    if (!parts || closeStart < 0) continue; // keep the inline default; nested elements still render
    const name = html.slice(m.index + 1, m.index + 1 + `glossa-${kind}`.length);
    out +=
      html.slice(at, m.index) +
      `<${name}${source}${extra(a)}>${treeToHtml(partsToTree(parts))}</${name}>`;
    at = closeEnd;
    re.lastIndex = closeEnd;
  }
  return out + html.slice(at);
}
