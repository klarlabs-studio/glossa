/**
 * The fallback graph editor works on an ordered list of rows; the API
 * speaks the manifest's `fallback` object. These convert between the two
 * and check a draft with the server's rules before it is sent, so most
 * mistakes are explained next to the row that has them.
 */

export interface FallbackRow {
  /** A project locale, or `*` for every locale without its own row. */
  from: string;
  /** Locales to try, in order. */
  chain: string[];
}

export type FallbackIssue =
  | { row: number; code: "fallback_unknown_locale"; locale: string }
  | { row: number; code: "fallback_self_reference"; locale: string }
  | { row: number; code: "fallback_duplicate"; locale: string }
  | { row: number; code: "fallback_duplicate_row"; locale: string }
  | { row: number; code: "fallback_empty_chain"; locale: string }
  | { row: number; code: "fallback_cycle"; locale: string; path: string[] };

export const WILDCARD = "*";

export function toRows(fallback: Record<string, string[]>): FallbackRow[] {
  // `*` last: it applies to everything the other rows don't name.
  return Object.entries(fallback)
    .sort(([a], [b]) => (a === WILDCARD ? 1 : b === WILDCARD ? -1 : a.localeCompare(b)))
    .map(([from, chain]) => ({ from, chain: [...chain] }));
}

export function toGraph(rows: readonly FallbackRow[]): Record<string, string[]> {
  const out: Record<string, string[]> = {};
  for (const r of rows) out[r.from] = [...r.chain];
  return out;
}

/** Check a draft against the project's locales; returns every issue found. */
export function validateRows(rows: readonly FallbackRow[], locales: readonly string[]): FallbackIssue[] {
  const known = new Set(locales);
  const issues: FallbackIssue[] = [];
  const seenFrom = new Set<string>();
  rows.forEach((r, row) => {
    if (seenFrom.has(r.from)) issues.push({ row, code: "fallback_duplicate_row", locale: r.from });
    seenFrom.add(r.from);
    if (r.from !== WILDCARD && !known.has(r.from)) issues.push({ row, code: "fallback_unknown_locale", locale: r.from });
    if (r.chain.length === 0) issues.push({ row, code: "fallback_empty_chain", locale: r.from });
    const seen = new Set<string>();
    for (const l of r.chain) {
      if (!known.has(l)) issues.push({ row, code: "fallback_unknown_locale", locale: l });
      if (l === r.from) issues.push({ row, code: "fallback_self_reference", locale: l });
      if (seen.has(l)) issues.push({ row, code: "fallback_duplicate", locale: l });
      seen.add(l);
    }
  });
  issues.push(...findCycles(rows));
  return issues;
}

/**
 * A cycle is a locale reachable from itself through explicit rows. The
 * wildcard row never makes one (`*` → en, en → de is fine), as on the
 * server.
 */
function findCycles(rows: readonly FallbackRow[]): FallbackIssue[] {
  const graph = new Map<string, string[]>();
  for (const r of rows) if (r.from !== WILDCARD) graph.set(r.from, r.chain);
  const next = (l: string) => graph.get(l) ?? [];
  const issues: FallbackIssue[] = [];
  const reported = new Set<string>();
  rows.forEach((r, row) => {
    const start = r.from;
    // A direct self reference is reported as such, not as a cycle.
    const stack: Array<{ node: string; path: string[] }> = r.chain
      .filter((c) => c !== start)
      .map((c) => ({ node: c, path: [start, c] }));
    const visited = new Set<string>();
    while (stack.length > 0) {
      const { node, path } = stack.pop()!;
      if (node === start && start !== WILDCARD) {
        const key = [...new Set(path)].sort().join(">");
        if (!reported.has(key)) {
          reported.add(key);
          issues.push({ row, code: "fallback_cycle", locale: start, path });
        }
        continue;
      }
      if (visited.has(node)) continue;
      visited.add(node);
      for (const n of next(node)) stack.push({ node: n, path: [...path, n] });
    }
  });
  return issues;
}
