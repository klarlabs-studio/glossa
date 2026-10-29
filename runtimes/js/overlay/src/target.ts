/**
 * Which message is under the pointer (RFC 0004 §3.1, §5.3)? A capture
 * session marks what the page renders: `t()` strings carry invisible
 * markers whose index points into the session's render log, and component
 * hosts carry `data-glossa-id`/`data-glossa-locale`. This module turns a
 * click (or a focused element) into one of those.
 */
import { ranges } from "@klarlabs-studio/glossa-capture";
import type { LoggedRender } from "@klarlabs-studio/glossa-capture";

/** A rendered message the overlay can open. */
export interface Target {
  /** The message ID, which is the catalog key. */
  id: string;
  /** The locale it rendered from; undefined when the inline default rendered. */
  locale?: string;
  /** Where it was found. */
  kind: "text" | "attribute" | "element";
  /** The element it was found on, for focus return and highlighting. */
  element: Element;
}

/** The innermost marked range around offset `at` in `s`, as a render index. */
function indexAt(s: string, at: number): number | undefined {
  // `ranges` lists innermost ranges first, so the first hit is the most specific.
  return ranges(s).find((m) => m.from <= at && at <= m.to)?.index;
}

/** The outermost marked range in `s`: the whole message an attribute or element shows. */
function outermost(s: string): number | undefined {
  let best: { index: number; from: number; to: number } | undefined;
  for (const m of ranges(s)) if (!best || m.to - m.from > best.to - best.from) best = m;
  return best?.index;
}

/**
 * The render under a caret position: the text node's element's text runs are
 * joined (a framework may split one string over adjacent text nodes) and the
 * innermost marked range around the caret wins.
 */
function atCaret(node: Node, offset: number): { index: number; element: Element } | undefined {
  const parent = node.parentElement;
  if (node.nodeType !== Node.TEXT_NODE || !parent) return undefined;
  let joined = "";
  let at = -1;
  for (const child of Array.from(parent.childNodes)) {
    if (child.nodeType !== Node.TEXT_NODE) continue;
    if (child === node) at = joined.length + offset;
    joined += (child as Text).data;
  }
  const index = at < 0 ? undefined : indexAt(joined, at);
  return index === undefined ? undefined : { index, element: parent };
}

type CaretDocument = Document & {
  caretPositionFromPoint?(x: number, y: number): { offsetNode: Node; offset: number } | null;
  caretRangeFromPoint?(x: number, y: number): Range | null;
};

/** The text node and offset under a viewport point, where the browser can tell. */
export function caretAt(
  doc: Document,
  x: number,
  y: number,
): { node: Node; offset: number } | undefined {
  const d = doc as CaretDocument;
  const p = d.caretPositionFromPoint?.(x, y);
  if (p) return { node: p.offsetNode, offset: p.offset };
  const r = d.caretRangeFromPoint?.(x, y);
  return r ? { node: r.startContainer, offset: r.startOffset } : undefined;
}

/** Whether `inner` is `outer` or inside it, across shadow roots. */
function within(inner: Node, outer: Element): boolean {
  for (let n: Node | null = inner; n; n = n.parentNode ?? (n as ShadowRoot).host ?? null) {
    if (n === outer) return true;
  }
  return false;
}

/** Marked attribute values of `el` (placeholder, title, aria-label, alt, an input's value…). */
function attributeIndex(el: Element): number | undefined {
  const values = Array.from(el.attributes, (a) => a.value);
  if (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement) values.push(el.value);
  for (const v of values) {
    const i = outermost(v);
    if (i !== undefined) return i;
  }
  return undefined;
}

/**
 * The message at a point of an event: the innermost marked `t()` text at the
 * caret, a marked attribute of the clicked element, the nearest component
 * host, and last the one message the clicked element's text shows.
 */
export function locate(
  log: readonly LoggedRender[],
  path: readonly EventTarget[],
  caret?: { node: Node; offset: number },
): Target | undefined {
  const entry = (index: number | undefined, element: Element, kind: Target["kind"]) => {
    const r = index === undefined ? undefined : log[index];
    return r && { id: r.id, locale: r.locale, kind, element };
  };
  const elements = path.filter((t): t is Element => t instanceof Element);
  const clicked = elements[0];
  const host = elements.find((el) => el.hasAttribute("data-glossa-id"));
  const text = caret && atCaret(caret.node, caret.offset);
  if (text && (!host || within(text.element, host))) {
    const found = entry(text.index, text.element, "text");
    if (found) return found;
  }
  if (clicked) {
    const found = entry(attributeIndex(clicked), clicked, "attribute");
    if (found) return found;
  }
  if (host) {
    const id = host.getAttribute("data-glossa-id")!;
    const locale = host.getAttribute("data-glossa-locale") ?? undefined;
    return { id, locale, kind: "element", element: host };
  }
  return clicked ? entry(sole(clicked.textContent ?? ""), clicked, "text") : undefined;
}

/** The render index when `s` shows exactly one top-level message, else undefined. */
function sole(s: string): number | undefined {
  const all = ranges(s);
  const top = all.filter((m) => !all.some((o) => o !== m && o.from <= m.from && m.to <= o.to));
  const indexes = new Set(top.map((m) => m.index));
  return indexes.size === 1 ? top[0]!.index : undefined;
}
