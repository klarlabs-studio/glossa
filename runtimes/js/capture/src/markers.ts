/**
 * Invisible markers around `t()` strings (RFC 0004 §3.1): a start mark, the
 * render's index in the session log in binary, the text, an end mark.
 *
 * The characters are the Unicode "invisible operators" U+2061–U+2064. They are
 * default-ignorable (browsers draw nothing and advance nothing for them),
 * bidi class BN (ignored by the bidi algorithm), joining type transparent
 * (Arabic letters next to them keep their shapes) and line-break class AL (no
 * new break opportunity inside a word). ZWJ/ZWNJ would change shaping and
 * ZWSP would add break opportunities, so they're avoided.
 *
 * Markers nest: a marked string passed as a value into another message stays
 * marked inside the outer one.
 */

/** Opens a marked range; the index digits follow. */
export const START = "\u2063";
/** Closes the innermost open range. */
export const END = "\u2064";
const ZERO = "\u2061";
const ONE = "\u2062";

/** A start mark with its index, or an end mark. Group 1: the index digits. */
export const MARK = /\u2063([\u2061\u2062]*)|\u2064/g;

/** `text` wrapped in markers for render `index`. */
export function mark(index: number, text: string): string {
  let digits = "";
  for (const d of index.toString(2)) digits += d === "0" ? ZERO : ONE;
  return START + digits + text + END;
}

/** The index a start mark's digits encode; -1 for none. */
export function decodeIndex(digits: string): number {
  if (!digits) return -1;
  let n = 0;
  for (const c of digits) n = n * 2 + (c === ONE ? 1 : 0);
  return n;
}

/** Whether `s` has a marker in it. */
export const hasMarkers = (s: string): boolean => s.includes(START) || s.includes(END);

/** `s` without markers: what the page shows outside a session. */
export const strip = (s: string): string => s.replace(MARK, "");

/** A marked range in one string: `[from, to)` is the text between the marks. */
export interface Marked {
  index: number;
  from: number;
  to: number;
}

/**
 * The marked ranges in `s`, innermost first where they nest. Unbalanced marks
 * (a string the application cut or spliced) are ignored.
 */
export function ranges(s: string): Marked[] {
  const out: Marked[] = [];
  const open: Array<{ index: number; from: number }> = [];
  for (const m of s.matchAll(MARK)) {
    if (m[0] === END) {
      const o = open.pop();
      if (o && o.index >= 0) out.push({ index: o.index, from: o.from, to: m.index });
    } else {
      open.push({ index: decodeIndex(m[1] ?? ""), from: m.index + m[0].length });
    }
  }
  return out;
}
