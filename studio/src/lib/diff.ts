/**
 * Word-level diff for the "source changed" view of an outdated
 * translation. Placeholders and punctuation are their own tokens, so
 * `{count}` changing to `{total}` reads as one replacement.
 */

export interface DiffSegment {
  op: "equal" | "insert" | "delete";
  text: string;
}

/** Split text into words, whitespace runs, braces and punctuation. */
export function tokenize(text: string): string[] {
  return text.match(/\s+|[{}#<>/|=.,;:!?()[\]"'…]|[^\s{}#<>/|=.,;:!?()[\]"'…]+/gu) ?? [];
}

/** Longest-common-subsequence diff of two token lists. */
export function diffWords(before: string, after: string): DiffSegment[] {
  const a = tokenize(before);
  const b = tokenize(after);
  const n = a.length;
  const m = b.length;
  // lcs[i][j]: LCS length of a[i:] and b[j:]
  const lcs: Uint32Array[] = Array.from({ length: n + 1 }, () => new Uint32Array(m + 1));
  for (let i = n - 1; i >= 0; i--) {
    const row = lcs[i]!;
    const below = lcs[i + 1]!;
    for (let j = m - 1; j >= 0; j--) {
      row[j] = a[i] === b[j] ? below[j + 1]! + 1 : Math.max(below[j]!, row[j + 1]!);
    }
  }
  const out: DiffSegment[] = [];
  const push = (op: DiffSegment["op"], text: string) => {
    const last = out[out.length - 1];
    if (last && last.op === op) last.text += text;
    else out.push({ op, text });
  };
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      push("equal", a[i]!);
      i++;
      j++;
    } else if (lcs[i + 1]![j]! >= lcs[i]![j + 1]!) {
      push("delete", a[i++]!);
    } else {
      push("insert", b[j++]!);
    }
  }
  while (i < n) push("delete", a[i++]!);
  while (j < m) push("insert", b[j++]!);
  return out;
}
