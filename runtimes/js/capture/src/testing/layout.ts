/**
 * A stated layout for jsdom. Test-only.
 *
 * jsdom has no layout engine: every box is zero and every scroll size is 0,
 * so the geometry probes (RFC 0005 §5.2) would have nothing to measure. A
 * test states the boxes instead, by element id, which makes the assertions
 * exact and independent of a browser's fonts. Real geometry is covered by
 * Playwright in `e2e/`, the same split `collectRegions` already follows.
 */
export interface Box {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface Stated {
  /** The element's border box, and the default rect of text inside it. */
  box?: Box;
  /** One rect per line box the element's text covers; defaults to `[box]`. */
  lines?: Box[];
  /** `clientWidth`/`clientHeight`, for the clipping probe. */
  client?: [number, number];
  /** `scrollWidth`/`scrollHeight`, for the clipping probe. */
  scroll?: [number, number];
}

const ZERO: Box = { x: 0, y: 0, width: 0, height: 0 };

const rect = (b: Box) => ({
  ...b,
  left: b.x,
  top: b.y,
  right: b.x + b.width,
  bottom: b.y + b.height,
  toJSON: () => b,
});

const list = (boxes: Box[]) => {
  const rects = boxes.map(rect);
  return Object.assign(rects, { item: (i: number) => rects[i] ?? null, length: rects.length });
};

const SIZES = [
  ["clientWidth", "client", 0],
  ["clientHeight", "client", 1],
  ["scrollWidth", "scroll", 0],
  ["scrollHeight", "scroll", 1],
] as const;

/**
 * State the page's layout: `{ "<element id>": { box, lines, client, scroll } }`,
 * plus the document's own size. Returns the undo, which every test must call.
 */
export function layout(byId: Record<string, Stated>, page = { width: 1280, height: 800 }): () => void {
  const of = (n: unknown): Stated | undefined => {
    const id = (n as Element | null)?.id;
    return id ? byId[id] : undefined;
  };
  const root = (n: Element) => n === n.ownerDocument.documentElement || n === n.ownerDocument.body;

  const undo: Array<() => void> = [];
  const patch = (target: object, prop: string, value: PropertyDescriptor) => {
    const was = Object.getOwnPropertyDescriptor(target, prop);
    Object.defineProperty(target, prop, { configurable: true, ...value });
    undo.push(() => {
      if (was) Object.defineProperty(target, prop, was);
      else delete (target as Record<string, unknown>)[prop];
    });
  };

  patch(Element.prototype, "getBoundingClientRect", {
    value(this: Element) {
      return rect(root(this) ? { x: 0, y: 0, ...page } : (of(this)?.box ?? ZERO));
    },
    writable: true,
  });
  patch(Range.prototype, "getClientRects", {
    value(this: Range) {
      const el = this.startContainer.parentElement;
      const stated = of(el);
      return list(stated?.lines ?? (stated?.box ? [stated.box] : []));
    },
    writable: true,
  });
  for (const [prop, from, i] of SIZES) {
    patch(Element.prototype, prop, {
      get(this: Element) {
        if (root(this)) return i === 0 ? page.width : page.height;
        return of(this)?.[from]?.[i] ?? 0;
      },
    });
  }
  return () => undo.splice(0).forEach((f) => f());
}

/** `document.fonts.check()` answering `has` for everything. jsdom ships no font set. */
export function fonts(has: boolean): () => void {
  const was = Object.getOwnPropertyDescriptor(document, "fonts");
  Object.defineProperty(document, "fonts", { configurable: true, value: { check: () => has } });
  return () => {
    if (was) Object.defineProperty(document, "fonts", was);
    else delete (document as unknown as Record<string, unknown>).fonts;
  };
}
