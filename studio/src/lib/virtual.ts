/**
 * Fixed-row-height windowing for the message list: only the rows in view
 * (plus a little overscan) are in the DOM, so ten thousand messages cost
 * the same as fifty.
 */

export interface Range {
  start: number;
  /** Exclusive. */
  end: number;
}

export function visibleRange(scrollTop: number, viewport: number, rowHeight: number, count: number, overscan = 8): Range {
  if (count === 0 || rowHeight <= 0) return { start: 0, end: 0 };
  const first = Math.floor(Math.max(0, scrollTop) / rowHeight);
  const inView = Math.ceil(Math.max(0, viewport) / rowHeight) + 1;
  const start = Math.max(0, first - overscan);
  const end = Math.min(count, first + inView + overscan);
  return { start, end };
}

/** The scrollTop that brings row `index` fully into view, or the current one if it already is. */
export function scrollTopFor(index: number, scrollTop: number, viewport: number, rowHeight: number): number {
  const top = index * rowHeight;
  const bottom = top + rowHeight;
  if (top < scrollTop) return top;
  if (bottom > scrollTop + viewport) return bottom - viewport;
  return scrollTop;
}
