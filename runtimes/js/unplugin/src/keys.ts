/**
 * Message keys and the typed accessor paths `glossa generate` gives them
 * (a port of `TSNames` in platform/internal/cli/codegen), so a call like
 * `m.checkout.paymentFailed(…)` maps back to `checkout.payment-failed`.
 */

/** Catalog key rules: what a literal must look like to count as a usage. */
export const KEY = /^[a-z0-9_-]+(?:\.[a-z0-9_-]+)*$/;
export const MAX_KEY = 200;

export function isKey(s: string): boolean {
  return s.length <= MAX_KEY && KEY.test(s);
}

/** Orders strings by Unicode code point (UTF-8 byte order), not UTF-16 units. */
export function compareCodePoints(a: string, b: string): number {
  const x = a[Symbol.iterator]();
  const y = b[Symbol.iterator]();
  for (;;) {
    const p = x.next();
    const q = y.next();
    if (p.done || q.done) return p.done && q.done ? 0 : p.done ? -1 : 1;
    const d = p.value.codePointAt(0)! - q.value.codePointAt(0)!;
    if (d !== 0) return d;
  }
}

const words = (s: string) => s.split(/[^\p{L}\p{Nd}]+/u).filter(Boolean);
const title = (w: string) => {
  const [first = "", ...rest] = Array.from(w);
  return first.toUpperCase() + rest.join("");
};

/** `payment_failed` → `paymentFailed`, as codegen's camel(). */
export function camel(segment: string): string {
  const ws = words(segment);
  if (ws.length === 0) return "_";
  const out = ws[0]! + ws.slice(1).map(title).join("");
  return /^\p{Nd}/u.test(out) ? `_${out}` : out;
}

/**
 * Accessor path (dot-joined, camelCased segments) → key. Of two keys with
 * the same path only the first in sort order gets it, and a key whose path
 * is also the group of another key gets none, exactly as codegen decides.
 */
export function accessorPaths(keys: Iterable<string>): Map<string, string> {
  const sorted = [...new Set(keys)].sort(compareCodePoints);
  const paths = new Map<string, string[]>();
  const taken = new Map<string, string>();
  for (const k of sorted) {
    const segs = k.split(".").map(camel);
    const p = segs.join(".");
    if (taken.has(p)) continue;
    taken.set(p, k);
    paths.set(k, segs);
  }
  const groups = new Set<string>();
  for (const segs of paths.values()) {
    for (let i = 1; i < segs.length; i++) {
      const other = taken.get(segs.slice(0, i).join("."));
      if (other !== undefined) groups.add(other);
    }
  }
  const out = new Map<string, string>();
  for (const [k, segs] of paths) if (!groups.has(k)) out.set(segs.join("."), k);
  return out;
}
