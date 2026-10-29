/**
 * The capture script (RFC 0004 §3.1–§3.2): where did the page render its
 * messages? `glossa capture` evaluates it after navigation and pairs the
 * result with a full-page screenshot.
 *
 * - Component hosts (`[data-glossa-id]`) are regions of kind `element`: the
 *   host's box, or for a `display: contents` host the box of what it renders.
 * - Marked `t()` text becomes one region of kind `text` per line it covers,
 *   from `Range.getClientRects()`.
 * - Marked attribute values (`placeholder`, `title`, `aria-label`, `alt`, a
 *   button's `value`, …) become regions of kind `attribute`: the element's box.
 * - A region that is zero-size, clipped away, off the page, `visibility:
 *   hidden` or transparent has `visible: false`, so the gap shows.
 *
 * Boxes are CSS pixels from the top-left of the full page. Open shadow roots
 * are searched too. The result's `renders` and `regions` are the fields of a
 * `glossa.captures/v1` capture (runtimes/testdata/schemas/captures.v1.schema.json).
 */
import { MARK, decodeIndex, hasMarkers, ranges, strip } from "./markers.js";

export interface Box {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface Region {
  /** For kind `element`: the host's message ID. */
  key?: string;
  /** For kinds `text` and `attribute`: the index into `renders`. */
  index?: number;
  kind: "element" | "text" | "attribute";
  /** For kind `attribute`: the attribute's name. */
  attribute?: string;
  box: Box;
  visible: boolean;
}

/** A render log entry, as captures.v1 names it. */
export interface CapturedRender {
  index: number;
  key: string;
  /** The locale the message resolved from; `und` for an inline default. */
  locale: string;
}

/** What the capture script returns: a capture's `renders` and `regions`. */
export interface Capture {
  renders: CapturedRender[];
  regions: Region[];
}

/**
 * A region's element and the text it rendered: what the visual probe pass
 * (RFC 0005 §5.2) needs and a box can't say. `collectRegions` hands one to
 * `onHost` per region, in the same order as `regions`. Neither the element
 * nor the text ever enters a capture.
 */
export interface Host {
  /** The component host, the attribute's element, or the text run's parent. */
  el: Element | null;
  /** What the region rendered, markers stripped. */
  text: string;
}

/** What the script needs to know about each render in the session's log. */
export interface LogEntry {
  id: string;
  locale: string | undefined;
}

// The captures.v1 rules for keys, locales and attribute names.
const KEY = /^[a-z0-9_-]+(?:\.[a-z0-9_-]+)*$/;
const LOCALE = /^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$/;
const ATTRIBUTE = /^[a-z][a-z0-9-]*$/;
/** Whether captures.v1 (and a finding's locus) can name this message. */
export const validKey = (k: string | null | undefined): k is string =>
  !!k && k.length <= 200 && KEY.test(k);
/** Whether captures.v1 (and a finding's locus) can name this locale. */
export const validLocale = (l: string | null | undefined): l is string =>
  !!l && l.length <= 35 && LOCALE.test(l);

/** Text in these is never rendered as page text (a textarea's is its value). */
const NOT_TEXT = /^(SCRIPT|STYLE|NOSCRIPT|TEXTAREA)$/;
/** Inputs whose value shows as text in the page. */
const VALUE_INPUT = /^(button|submit|reset|text|search|email|url|tel)$/;

type Rect = { left: number; top: number; right: number; bottom: number };

const round = (n: number) => Math.round(n * 100) / 100;
const area = (r: Rect) => r.right > r.left && r.bottom > r.top;
const union = (a: Rect, b: Rect): Rect => ({
  left: Math.min(a.left, b.left),
  top: Math.min(a.top, b.top),
  right: Math.max(a.right, b.right),
  bottom: Math.max(a.bottom, b.bottom),
});
const intersect = (a: Rect, b: Rect): Rect => ({
  left: Math.max(a.left, b.left),
  top: Math.max(a.top, b.top),
  right: Math.min(a.right, b.right),
  bottom: Math.min(a.bottom, b.bottom),
});
const plain = (r: DOMRectReadOnly): Rect => ({
  left: r.left,
  top: r.top,
  right: r.right,
  bottom: r.bottom,
});

/** Client rects on the same line merged into one, top to bottom. */
function lines(rects: Rect[]): Rect[] {
  const out: Rect[] = [];
  for (const r of rects.filter(area).sort((a, b) => a.top - b.top || a.left - b.left)) {
    const last = out[out.length - 1];
    const overlap = last ? Math.min(last.bottom, r.bottom) - Math.max(last.top, r.top) : 0;
    if (last && overlap >= Math.min(last.bottom - last.top, r.bottom - r.top) / 2) {
      out[out.length - 1] = union(last, r);
    } else out.push(r);
  }
  return out;
}

/** The parent in the flat tree: the slot a node is assigned to, else its parent or shadow host. */
export function flatParent(n: Node): Element | null {
  const slot = (n as Element | Text).assignedSlot;
  if (slot) return slot;
  if (n.parentElement) return n.parentElement;
  const p = n.parentNode;
  return p && "host" in p ? (p as ShadowRoot).host : null;
}

class Page {
  private readonly styles = new Map<Element, CSSStyleDeclaration>();
  private readonly sx: number;
  private readonly sy: number;
  private readonly width: number;
  private readonly height: number;

  constructor(private readonly doc: Document) {
    const win = doc.defaultView;
    const el = doc.documentElement;
    this.sx = win?.scrollX ?? 0;
    this.sy = win?.scrollY ?? 0;
    this.width = Math.max(el.scrollWidth, doc.body?.scrollWidth ?? 0);
    this.height = Math.max(el.scrollHeight, doc.body?.scrollHeight ?? 0);
  }

  style(el: Element): CSSStyleDeclaration {
    let s = this.styles.get(el);
    if (!s) this.styles.set(el, (s = this.doc.defaultView!.getComputedStyle(el)));
    return s;
  }

  box(r: Rect | undefined): Box {
    if (!r) return { x: 0, y: 0, width: 0, height: 0 };
    return {
      x: round(r.left + this.sx),
      y: round(r.top + this.sy),
      width: round(r.right - r.left),
      height: round(r.bottom - r.top),
    };
  }

  /** `r` cut to the boxes of the scrolling or clipping containers above `from`. */
  clip(r: Rect, from: Element | null): Rect {
    for (let el = from; el; el = flatParent(el)) {
      const s = this.style(el);
      if (s.overflowX !== "visible" || s.overflowY !== "visible") {
        r = intersect(r, plain(el.getBoundingClientRect()));
      }
    }
    return r;
  }

  /** Whether `el`'s content is hidden: `visibility`, opacity 0, `display: none` or `content-visibility` above. */
  hidden(el: Element | null): boolean {
    if (!el) return false;
    if (this.style(el).visibility !== "visible" && this.style(el).visibility !== "") return true;
    let boxed: Element | null = el;
    while (boxed && this.style(boxed).display === "contents") boxed = flatParent(boxed);
    return boxed?.checkVisibility?.({ opacityProperty: true, visibilityProperty: true }) === false;
  }

  /** Whether a clipped box shows on the page: bigger than a pixel and inside the document. */
  shows(r: Rect): boolean {
    const b = this.box(r);
    return (
      b.width > 1 &&
      b.height > 1 &&
      b.x + b.width > 0 &&
      b.y + b.height > 0 &&
      b.x < this.width &&
      b.y < this.height
    );
  }

  /**
   * Regions for rendered content with these client rects (one per line, or
   * one for the whole element), clipped by the containers above `anchor`.
   */
  regions(rects: Rect[], anchor: Element | null, perLine: boolean): Array<Omit<Region, "kind">> {
    const parts = perLine ? lines(rects) : rects.filter(area).reduce<Rect[]>(
      (acc, r) => (acc.length ? [union(acc[0]!, r)] : [r]),
      [],
    );
    const hidden = this.hidden(anchor);
    const shown = hidden ? [] : parts.map((r) => this.clip(r, anchor)).filter((r) => this.shows(r));
    if (shown.length) return shown.map((r) => ({ box: this.box(r), visible: true }));
    const whole = parts.reduce<Rect | undefined>((acc, r) => (acc ? union(acc, r) : r), undefined);
    return [{ box: this.box(whole), visible: false }];
  }

  /** The client rects of what `el` renders; through `display: contents`, shadow roots and slots. */
  rendered(el: Element): Rect[] {
    if (this.style(el).display !== "contents") return [plain(el.getBoundingClientRect())];
    const kids =
      el.shadowRoot?.childNodes ??
      (el instanceof HTMLSlotElement ? el.assignedNodes({ flatten: true }) : el.childNodes);
    const out: Rect[] = [];
    for (const n of Array.from(kids)) {
      if (n.nodeType === Node.TEXT_NODE) {
        const range = this.doc.createRange();
        range.selectNodeContents(n);
        out.push(...Array.from(range.getClientRects?.() ?? [], plain));
      } else if (n.nodeType === Node.ELEMENT_NODE) out.push(...this.rendered(n as Element));
    }
    return out;
  }

  /** The box of a closed `<select>` for text in its selected `<option>`, which has no layout of its own. */
  option(text: Node): { rects: Rect[]; anchor: Element } | undefined {
    const option = text.parentElement?.closest("option");
    const select = option?.closest("select");
    if (!option || !select) return undefined;
    return { rects: option.selected ? [plain(select.getBoundingClientRect())] : [], anchor: select };
  }
}

interface Open {
  node: Text;
  offset: number;
  index: number;
}

/**
 * Find every rendered message under `root` (default: the whole document,
 * with open shadow roots). `log` is the session's render log: a marker's
 * index is a position in it.
 */
export function collectRegions(
  log: readonly LogEntry[],
  root: Document | Element | ShadowRoot = document,
  onHost?: (host: Host) => void,
): Capture {
  const doc = root.nodeType === Node.DOCUMENT_NODE ? (root as Document) : root.ownerDocument!;
  const page = new Page(doc);
  const regions: Region[] = [];
  const used = new Set<number>();
  const known = (index: number) => index >= 0 && index < log.length && validKey(log[index]!.id);

  const add = (
    kind: Region["kind"],
    found: Array<Omit<Region, "kind">>,
    extra: Partial<Region>,
    host: Host,
  ) => {
    for (const r of found) {
      regions.push({ ...extra, kind, ...r });
      onHost?.(host);
    }
    if (extra.index !== undefined) used.add(extra.index);
  };

  const element = (el: Element) => {
    const key = el.getAttribute("data-glossa-id");
    if (validKey(key)) {
      const host = { el, text: strip(el.textContent ?? "") };
      add("element", page.regions(page.rendered(el), el, false), { key }, host);
    }
    const values: Array<[string, string]> = [];
    for (const a of Array.from(el.attributes)) {
      if (!(el instanceof HTMLInputElement && a.name === "value")) values.push([a.name, a.value]);
    }
    if (el instanceof HTMLInputElement && VALUE_INPUT.test(el.type)) values.push(["value", el.value]);
    if (el instanceof HTMLTextAreaElement) values.push(["value", el.value]);
    for (const [name, value] of values) {
      if (!hasMarkers(value) || !ATTRIBUTE.test(name)) continue;
      for (const m of ranges(value)) {
        if (!known(m.index)) continue;
        const rects = [plain(el.getBoundingClientRect())];
        const host = { el, text: strip(value.slice(m.from, m.to)) };
        add("attribute", page.regions(rects, el, false), { index: m.index, attribute: name }, host);
      }
    }
  };

  const text = (start: Open, end: Text, offset: number) => {
    if (!known(start.index)) return;
    const range = doc.createRange();
    range.setStart(start.node, start.offset);
    range.setEnd(end, offset);
    const option = page.option(start.node);
    const rects = option?.rects ?? Array.from(range.getClientRects?.() ?? [], plain);
    const anchor = option?.anchor ?? flatParent(start.node);
    const host = { el: anchor, text: strip(range.toString()) };
    add("text", page.regions(rects, anchor, true), { index: start.index }, host);
  };

  const scope = (tree: Node) => {
    const open: Open[] = [];
    const shadows: ShadowRoot[] = [];
    const walker = doc.createTreeWalker(tree, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT, {
      acceptNode: (n) =>
        n.nodeType === Node.TEXT_NODE && NOT_TEXT.test(n.parentElement?.tagName ?? "")
          ? NodeFilter.FILTER_REJECT
          : NodeFilter.FILTER_ACCEPT,
    });
    for (let n: Node | null = walker.currentNode; n; n = walker.nextNode()) {
      if (n.nodeType === Node.ELEMENT_NODE) {
        element(n as Element);
        const shadow = (n as Element).shadowRoot;
        if (shadow) shadows.push(shadow);
      } else if (n.nodeType === Node.TEXT_NODE && hasMarkers((n as Text).data)) {
        for (const m of (n as Text).data.matchAll(MARK)) {
          if (m[1] === undefined) {
            const o = open.pop();
            if (o) text(o, n as Text, m.index);
          } else {
            const index = decodeIndex(m[1]);
            open.push({ node: n as Text, offset: m.index + m[0].length, index });
          }
        }
      }
    }
    for (const s of shadows) scope(s);
  };

  scope(root);
  const renders = [...used]
    .sort((a, b) => a - b)
    .map((index) => {
      const { id, locale } = log[index]!;
      return { index, key: id, locale: validLocale(locale) ? locale : "und" };
    });
  return { renders, regions };
}
