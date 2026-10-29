/**
 * Terminology in the editor: where recognized terms sit in the source as
 * written, and how term statuses read. The server reports spans as code
 * point offsets into the message's *visible* text (placeholders become
 * U+FFFC, variants are joined by newlines); the editor shows the source
 * as written, so each hit is located there by its words, occurrence by
 * occurrence.
 */
import type { TermHit, TermStatus } from "../api/knowledge-schemas";

export interface Segment {
  text: string;
  hit?: TermHit;
}

/** Code point slice (the API's offsets count code points, JS strings UTF-16 units). */
export function cpSlice(s: string, start: number, end?: number): string {
  return Array.from(s).slice(start, end).join("");
}

function nthIndexOf(haystack: string, needle: string, n: number, from = 0): number {
  let at = from - 1;
  for (let i = 0; i <= n; i++) {
    at = haystack.indexOf(needle, at + 1);
    if (at < 0) return -1;
  }
  return at;
}

function countBefore(haystack: string, needle: string, end: number): number {
  let n = 0;
  for (let at = haystack.indexOf(needle); at >= 0 && at < end; at = haystack.indexOf(needle, at + needle.length)) n++;
  return n;
}

/**
 * Split `raw` (the source as written) into plain and highlighted
 * segments. A hit whose words can't be found in `raw` (escaped text, say)
 * isn't highlighted inline; the terms pane still lists it.
 */
export function highlight(raw: string, analyzed: string, hits: readonly TermHit[]): Segment[] {
  const placed: Array<{ from: number; to: number; hit: TermHit }> = [];
  for (const hit of [...hits].sort((a, b) => a.start - b.start)) {
    if (!hit.text) continue;
    const before = cpSlice(analyzed, 0, hit.start);
    const n = countBefore(before, hit.text, before.length);
    const from = nthIndexOf(raw, hit.text, n);
    if (from < 0) continue;
    const to = from + hit.text.length;
    if (placed.some((p) => from < p.to && to > p.from)) continue;
    placed.push({ from, to, hit });
  }
  placed.sort((a, b) => a.from - b.from);
  const out: Segment[] = [];
  let at = 0;
  for (const p of placed) {
    if (p.from > at) out.push({ text: raw.slice(at, p.from) });
    out.push({ text: raw.slice(p.from, p.to), hit: p.hit });
    at = p.to;
  }
  if (at < raw.length || out.length === 0) out.push({ text: raw.slice(at) });
  return out;
}

/** Allowed target terms first (preferred, admitted), then the ones to avoid. */
export const STATUS_ORDER: readonly TermStatus[] = ["preferred", "admitted", "deprecated", "forbidden"];

export const statusTone = (s: TermStatus): "ok" | "neutral" | "warn" | "err" =>
  s === "preferred" ? "ok" : s === "admitted" ? "neutral" : s === "deprecated" ? "warn" : "err";

export const allowed = (s: TermStatus): boolean => s === "preferred" || s === "admitted";

/** Sort terms by status order, then text. */
export function byStatus<T extends { status: TermStatus; text: string }>(terms: readonly T[]): T[] {
  return [...terms].sort((a, b) => STATUS_ORDER.indexOf(a.status) - STATUS_ORDER.indexOf(b.status) || a.text.localeCompare(b.text));
}
