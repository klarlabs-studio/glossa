/**
 * `<glossa-*>` elements in markup: HTML entry files, and the HTML that
 * compilers emit as strings (Vue's static vnodes and SSR `_push` templates).
 * A small tokenizer, not a validator: it skips comments and raw-text
 * elements, so escaped markup, comments and script strings never count.
 */
import { isKey } from "./keys.js";

export const ELEMENTS = new Set(["glossa-text", "glossa-rich", "glossa-plural", "glossa-select"]);

/** The tags whose content is text, not markup. */
const RAW_TEXT = new Set(["script", "style", "textarea", "title", "xmp", "noscript"]);

export interface MarkupHit {
  key: string;
  /** Offset of the key's first character in the scanned text. */
  offset: number;
}

interface Attribute {
  name: string;
  value: string;
  offset: number;
}

const TAG = /<([a-zA-Z][a-zA-Z0-9-]*)/y;
const ATTR_NAME = /[^\s"'>/=]+/y;
const UNQUOTED = /[^\s>]+/y;
const SPACE = /\s*/y;

function sticky(re: RegExp, text: string, at: number): RegExpExecArray | null {
  re.lastIndex = at;
  return re.exec(text);
}

function skipSpace(text: string, at: number): number {
  SPACE.lastIndex = at;
  SPACE.exec(text);
  return SPACE.lastIndex;
}

/** Reads a start tag's attributes from `at` (just after its name); returns them and the end. */
function attributes(text: string, at: number): { attrs: Attribute[]; end: number } {
  const attrs: Attribute[] = [];
  let i = at;
  while (i < text.length) {
    i = skipSpace(text, i);
    const c = text[i];
    if (c === ">") return { attrs, end: i + 1 };
    if (c === "/" || c === undefined) {
      i++;
      continue;
    }
    const name = sticky(ATTR_NAME, text, i);
    if (!name) {
      i++;
      continue;
    }
    i += name[0].length;
    const eq = skipSpace(text, i);
    if (text[eq] !== "=") {
      attrs.push({ name: name[0].toLowerCase(), value: "", offset: i });
      continue;
    }
    const v = skipSpace(text, eq + 1);
    const q = text[v];
    if (q === '"' || q === "'") {
      const close = text.indexOf(q, v + 1);
      const end = close < 0 ? text.length : close;
      attrs.push({ name: name[0].toLowerCase(), value: text.slice(v + 1, end), offset: v + 1 });
      i = end + 1;
    } else {
      const m = sticky(UNQUOTED, text, v);
      const value = m ? m[0] : "";
      attrs.push({ name: name[0].toLowerCase(), value, offset: v });
      i = v + value.length;
    }
  }
  return { attrs, end: text.length };
}

/** The message a `<glossa-*>` element names: `key`, else `message`. */
export function elementKey<T extends { value: string }>(
  attrs: Map<string, T>,
): T | undefined {
  const a = attrs.get("key") ?? attrs.get("message");
  return a && isKey(a.value) ? a : undefined;
}

export function scanMarkup(text: string): MarkupHit[] {
  const hits: MarkupHit[] = [];
  let i = 0;
  while (i < text.length) {
    const lt = text.indexOf("<", i);
    if (lt < 0) break;
    if (text.startsWith("<!--", lt)) {
      const end = text.indexOf("-->", lt + 4);
      i = end < 0 ? text.length : end + 3;
      continue;
    }
    const tag = sticky(TAG, text, lt);
    if (!tag) {
      i = lt + 1;
      continue;
    }
    const name = tag[1]!.toLowerCase();
    const { attrs, end } = attributes(text, lt + tag[0].length);
    i = end;
    if (ELEMENTS.has(name)) {
      const hit = elementKey(new Map(attrs.map((a) => [a.name, a])));
      if (hit) hits.push({ key: hit.value, offset: hit.offset });
    } else if (RAW_TEXT.has(name)) {
      const close = text.toLowerCase().indexOf(`</${name}`, i);
      i = close < 0 ? text.length : close;
    }
  }
  return hits;
}
