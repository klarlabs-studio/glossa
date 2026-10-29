/**
 * Maps a hit in transformed code back to the original file through the
 * module's combined source map, then pins it to the key itself.
 *
 * Source maps are only as fine as their producers make them: esbuild and
 * Oxc map every token, Vue maps template expressions but not the string
 * literals inside them, Astro maps attributes. So a mapping is the start of
 * the answer, not the answer: from the mapped original position, the hit
 * is pinned to the key in the original text, which must be there exactly
 * as the contract describes (the first character inside the quotes, or an
 * accessor's first path segment followed by `(`):
 *
 * 1. the mapped position plus the hit's distance from its mapping segment
 *    (exact when the producer copied the text verbatim, as most do);
 * 2. else the nearest such key at or after the mapped position (the
 *    segment starts the enclosing expression: `$t(` for `$t("…")`);
 * 3. else the nearest one before it.
 *
 * Every original position is claimed once per module, so two calls of the
 * same key never collapse into one. Without a source map (some webpack
 * loaders) the same search runs from the hit's own line and column, which
 * is exact for code that the loaders didn't reshape.
 */
import { readFileSync } from "node:fs";
import { dirname, isAbsolute, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { TraceMap, decodedMappings } from "@jridgewell/trace-mapping";
import type { SourceMapInput } from "@jridgewell/trace-mapping";

import type { Hit } from "./scan.js";

export interface Source {
  /** Absolute path, without a query. */
  path: string;
  text: string;
}

export interface Located {
  source: Source;
  /** UTF-16 offset of the key (or the accessor's first segment) in `source.text`. */
  offset: number;
  line: number;
  /** 1-based, in Unicode code points. */
  column: number;
}

export type ReadSource = (path: string) => string | undefined;

export const stripQuery = (id: string) => id.replace(/[?#].*$/, "");

/** Reads original files from disk, once each. */
export function sourceReader(): ReadSource {
  const cache = new Map<string, string | undefined>();
  return (path) => {
    if (!cache.has(path)) {
      let text: string | undefined;
      try {
        text = readFileSync(path, "utf8");
      } catch {
        text = undefined;
      }
      cache.set(path, text);
    }
    return cache.get(path);
  };
}

function lineStarts(text: string): number[] {
  const starts = [0];
  for (let i = text.indexOf("\n"); i >= 0; i = text.indexOf("\n", i + 1)) starts.push(i + 1);
  return starts;
}

/** 0-based line and UTF-16 column of an offset. */
function lineColumn(starts: number[], offset: number): [number, number] {
  let lo = 0;
  let hi = starts.length - 1;
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1;
    if (starts[mid]! <= offset) lo = mid;
    else hi = mid - 1;
  }
  return [lo, offset - starts[lo]!];
}

const QUOTES = new Set(['"', "'", "`"]);
const IDENT = /[\p{ID_Continue}$‌‍]/u;
const NAME = /[\p{ID_Start}$_][\p{ID_Continue}$‌‍]*/uy;

function escape(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/** Whether the original text has the hit's key (or accessor path) at `p`. */
function matcher(hit: Hit): { needle: string; at: (text: string, p: number) => boolean } {
  if (Array.isArray(hit.text)) {
    const re = new RegExp(`${hit.text.map(escape).join("\\s*\\??\\.\\s*")}\\s*(?:\\?\\.\\s*)?\\(`, "y");
    return {
      needle: hit.text[0]!,
      at: (text, p) => {
        if (p > 0 && IDENT.test(text[p - 1]!)) return false;
        let q = p - 1;
        while (q >= 0 && /\s/.test(text[q]!)) q--;
        if (text[q] !== ".") return false;
        re.lastIndex = p;
        return re.test(text);
      },
    };
  }
  const key = hit.text;
  const element = hit.kind === "element";
  return {
    needle: key,
    at: (text, p) => {
      if (!text.startsWith(key, p)) return false;
      const before = text[p - 1];
      const after = text[p + key.length];
      if (before !== undefined && QUOTES.has(before)) return after === before;
      // An unquoted HTML attribute value: key=home.title
      return element && before === "=" && (after === undefined || /[\s/>]/.test(after));
    },
  };
}

/** A module's transformed code and its map, with the original-position search. */
export class ModuleLocator {
  private readonly starts: number[];
  private readonly trace: TraceMap | undefined;
  private readonly claimed = new Set<string>();
  private readonly originals = new Map<string, { source: Source; starts: number[] } | undefined>();

  constructor(
    private readonly code: string,
    private readonly id: string,
    map: SourceMapInput | null | undefined,
    private readonly read: ReadSource,
  ) {
    this.starts = lineStarts(code);
    this.trace = usableMap(map);
  }

  locate(hit: Hit): Located | undefined {
    const [line, column] = lineColumn(this.starts, hit.offset);
    const mapped = this.trace ? this.mapped(line, column) : this.unmapped(line, column);
    if (!mapped) return undefined;
    const { original, approx, exact } = mapped;
    const m = matcher(hit);
    const text = original.source.text;
    const free = (p: number) => !this.claimed.has(`${original.source.path}\0${p}`);
    let found: number | undefined;
    if (exact !== undefined && m.at(text, exact) && free(exact)) found = exact;
    for (let p = text.indexOf(m.needle, approx); found === undefined && p >= 0; p = text.indexOf(m.needle, p + 1)) {
      if (m.at(text, p) && free(p)) found = p;
    }
    for (let p = text.lastIndexOf(m.needle, approx - 1); found === undefined && p >= 0; p = text.lastIndexOf(m.needle, p - 1)) {
      if (m.at(text, p) && free(p)) found = p;
      if (p === 0) break;
    }
    if (found === undefined) return undefined;
    this.claimed.add(`${original.source.path}\0${found}`);
    const [l, c] = lineColumn(original.starts, found);
    const lineText = text.slice(original.starts[l]!, original.starts[l]! + c);
    return { source: original.source, offset: found, line: l + 1, column: Array.from(lineText).length + 1 };
  }

  /**
   * The original name of an identifier in the transformed code: the map's
   * `names` entry (esbuild records renames there), else the identifier at
   * the mapped position. Undefined when the original has none there, as
   * for names a bundler invents (`export default function` → `Header_default`).
   */
  name(offset: number, generated: string): string | undefined {
    if (!this.trace) return generated;
    const [line, column] = lineColumn(this.starts, offset);
    const segs = decodedMappings(this.trace)[line] ?? [];
    let seg: readonly number[] | undefined;
    for (let i = segs.length - 1; i >= 0 && !seg; i--) if (segs[i]!.length >= 4 && segs[i]![0]! <= column) seg = segs[i];
    if (!seg) return undefined;
    if (seg.length === 5 && seg[0] === column) return this.trace.names[seg[4]!];
    const original = this.original(sourcePath(this.trace, seg[1]!, this.id), this.trace.sourcesContent?.[seg[1]!]);
    if (!original) return undefined;
    const start = original.starts[seg[2]!];
    if (start === undefined) return undefined;
    const p = start + seg[3]! + (column - seg[0]!);
    if (p > 0 && IDENT.test(original.source.text[p - 1]!)) return undefined;
    NAME.lastIndex = p;
    return NAME.exec(original.source.text)?.[0];
  }

  private original(path: string, content: string | null | undefined) {
    if (!this.originals.has(path)) {
      // Prefer the file itself: a map whose sourcesContent isn't the file
      // (an intermediate step) still points into the right file, roughly.
      const text = this.read(path) ?? content ?? undefined;
      this.originals.set(path, text === undefined ? undefined : { source: { path, text }, starts: lineStarts(text) });
    }
    return this.originals.get(path);
  }

  private mapped(line: number, column: number) {
    const trace = this.trace!;
    const lines = decodedMappings(trace);
    let seg: readonly number[] | undefined;
    let sameLine = true;
    for (let l = line; l >= 0 && !seg; l--) {
      const segs = lines[l] ?? [];
      for (let i = segs.length - 1; i >= 0; i--) {
        const s = segs[i]!;
        if (s.length >= 4 && (l < line || s[0]! <= column)) {
          seg = s;
          break;
        }
      }
      if (!seg) sameLine = false;
    }
    if (!seg) return this.unmapped(line, column);
    const [genColumn, sourceIndex, srcLine, srcColumn] = seg as [number, number, number, number];
    const path = sourcePath(trace, sourceIndex, this.id);
    const original = this.original(path, trace.sourcesContent?.[sourceIndex]);
    if (!original) return undefined;
    const lineStart = original.starts[Math.min(srcLine, original.starts.length - 1)]!;
    const approx = Math.min(lineStart + srcColumn, original.source.text.length);
    const exact = sameLine ? approx + (column - genColumn) : undefined;
    return { original, approx, exact };
  }

  private unmapped(line: number, column: number) {
    const path = stripQuery(this.id);
    const original = this.original(path, this.code);
    if (!original) return undefined;
    const identical = original.source.text === this.code;
    const lineStart = original.starts[Math.min(line, original.starts.length - 1)]!;
    const approx = Math.min(lineStart + column, original.source.text.length);
    return { original, approx, exact: identical ? approx : undefined };
  }
}

function usableMap(map: SourceMapInput | null | undefined): TraceMap | undefined {
  if (!map) return undefined;
  try {
    const trace = new TraceMap(map);
    return trace.sources.length > 0 && decodedMappings(trace).some((l) => l.length > 0) ? trace : undefined;
  } catch {
    return undefined;
  }
}

function sourcePath(trace: TraceMap, index: number, id: string): string {
  const raw = trace.sources[index] ?? "";
  if (raw.startsWith("file://")) return stripQuery(fileURLToPath(raw));
  const resolved = trace.resolvedSources[index] ?? raw;
  if (resolved.startsWith("file://")) return stripQuery(fileURLToPath(resolved));
  if (isAbsolute(raw)) return stripQuery(raw);
  // Relative sources resolve against the module's directory (Rollup, webpack loaders).
  return stripQuery(resolve(dirname(stripQuery(id)), trace.sourceRoot ?? "", raw));
}
