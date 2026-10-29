/**
 * Pure logic of the import/export screens (RFC 0003 §6): what a file is,
 * which locale it carries, who may import it, what a job's state means,
 * and saving a downloaded file. The server decides; this mirrors its
 * rules (platform/README.md, Integration) so Studio can say why before
 * anything is sent.
 */
import type { ImportJob, ImportSummary, ExportJob, IntegrationFormat, IntegrationJobState, IntegrationKind } from "../api/integration-schemas";
import { allows, allowsFor, type Grant } from "../session/permissions";

export const IMPORT_FORMATS: readonly IntegrationFormat[] = ["xliff", "json", "po", "tmx", "tbx"];
/** `po` is import only. */
export const EXPORT_FORMATS: readonly IntegrationFormat[] = ["xliff", "json", "tmx", "tbx"];

export const kindOf = (f: IntegrationFormat): IntegrationKind => (f === "tmx" ? "tm" : f === "tbx" ? "termbase" : "catalog");
export const isCatalog = (f: IntegrationFormat): boolean => kindOf(f) === "catalog";

const EXTENSIONS: Record<string, IntegrationFormat> = {
  xlf: "xliff",
  xliff: "xliff",
  json: "json",
  po: "po",
  pot: "po",
  tmx: "tmx",
  tbx: "tbx",
};

/** The format a file name says, by its extension. */
export function formatFromName(name: string): IntegrationFormat | undefined {
  const ext = /\.([a-z0-9]+)$/i.exec(name)?.[1]?.toLowerCase();
  return ext ? EXTENSIONS[ext] : undefined;
}

/** The format the start of a file looks like. */
export function sniffFormat(head: string): IntegrationFormat | undefined {
  const text = head.replace(/^﻿/, "").trimStart();
  if (/<xliff[\s>]/.test(text)) return "xliff";
  if (/<tmx[\s>]/.test(text)) return "tmx";
  if (/<(martif|tbx)[\s>]/.test(text)) return "tbx";
  if (text.startsWith("{")) return "json";
  if (/^(#.*\n|\s*\n)*msg(ctxt|id)\s/m.test(text)) return "po";
  return undefined;
}

/** The name's word, else the content's. */
export const detectFormat = (name: string, head: string): IntegrationFormat | undefined => formatFromName(name) ?? sniffFormat(head);

const attr = (text: string, name: string) => new RegExp(`\\b${name}\\s*=\\s*["']([^"']+)["']`).exec(text)?.[1];

/** An XLIFF file's languages: `srcLang`/`trgLang` (2.x) or `source-language`/`target-language` (1.2). */
export function xliffLanguages(head: string): { source?: string; target?: string } {
  const root = /<xliff[^>]*>/.exec(head)?.[0] ?? "";
  const file = /<file[^>]*>/.exec(head)?.[0] ?? "";
  const source = attr(root, "srcLang") ?? attr(file, "source-language");
  const target = attr(root, "trgLang") ?? attr(file, "target-language");
  return { ...(source ? { source } : {}), ...(target ? { target } : {}) };
}

/** Whether an XLIFF file carries translations (a `<target>` element) in the part that was read. */
export const xliffHasTargets = (head: string): boolean => /<target[\s>/]/.test(head);

/** A PO file's `Language:` header, as a BCP 47 tag (`pt_BR` → `pt-BR`). */
export function poLanguage(head: string): string | undefined {
  const m = /"Language:\s*([A-Za-z0-9_@-]+)\s*(\\n)?"/.exec(head);
  return m?.[1] ? m[1].replace(/@.*$/, "").replace(/_/g, "-") : undefined;
}

/**
 * The project locale a file name names (`de.json`, `app.de-AT.json`,
 * `messages_pt_BR.json`), matched case-insensitively; the longest wins.
 */
export function localeFromName(name: string, locales: readonly string[]): string | undefined {
  const base = name.replace(/\.[a-z0-9]+$/i, "").toLowerCase().replace(/_/g, "-");
  const parts = base.split(/[.\s/]+/);
  const candidates = [...locales].sort((a, b) => b.length - a.length);
  for (const l of candidates) {
    const low = l.toLowerCase();
    if (parts.includes(low) || base.endsWith(`-${low}`) || base === low) return l;
  }
  return undefined;
}

/** The locale a project calls `code`, if it has it (tags compare case-insensitively). */
export const projectLocale = (code: string | undefined, locales: readonly string[]): string | undefined =>
  code ? locales.find((l) => l.toLowerCase() === code.toLowerCase()) : undefined;

// ── permissions ───────────────────────────────────────────────────────

/** May the member import anything at all here? */
export const canImportAny = (grant: Grant): boolean => grant.has("integration.import") || allows(grant, "integration.manage");

/** Formats the member may import: catalogs with `integration.import` (or manage), TMX and TBX with manage and `knowledge.write`. */
export function importableFormats(grant: Grant): IntegrationFormat[] {
  const manage = allows(grant, "integration.manage");
  return IMPORT_FORMATS.filter((f) => (isCatalog(f) ? canImportAny(grant) : manage && allows(grant, "knowledge.write")));
}

/**
 * Why the member can't import a catalog in `locale`, or undefined when
 * they may: the source locale is a source catalog (manage), a target
 * needs `integration.import` for it (locale-scoped, as translators hold
 * it) or manage.
 */
export function localeBlock(grant: Grant, locale: string, sourceLocale: string | undefined): "source" | "scope" | undefined {
  if (allows(grant, "integration.manage")) return undefined;
  if (sourceLocale && locale.toLowerCase() === sourceLocale.toLowerCase()) return "source";
  return allowsFor(grant, "integration.import", locale) ? undefined : "scope";
}

export const canOverwrite = (grant: Grant): boolean => allows(grant, "integration.manage");

// ── jobs ──────────────────────────────────────────────────────────────

const TERMINAL = new Set<IntegrationJobState>(["succeeded", "failed", "cancelled"]);
export const isTerminal = (s: IntegrationJobState): boolean => TERMINAL.has(s);

/** An import may be cancelled until it ends (a running one stops after its batch); an export only while queued. */
export const canCancelImport = (j: ImportJob): boolean => !isTerminal(j.state) && !j.cancel_requested;
export const canCancelExport = (j: ExportJob): boolean => j.state === "queued" && !j.cancel_requested;

/** How far an import is, 0–1, when the file's size in items is known. */
export function importProgress(j: Pick<ImportJob, "processed_items" | "total_items" | "state">): number | undefined {
  if (j.state === "succeeded") return 1;
  return j.total_items > 0 ? Math.min(1, j.processed_items / j.total_items) : undefined;
}

export const summaryTotal = (s: Pick<ImportSummary, "created" | "updated" | "unchanged" | "conflict" | "invalid">): number =>
  s.created + s.updated + s.unchanged + s.conflict + s.invalid;

/** How often the screens read a running job (tests set 0). */
export const polling = { ms: 1000 };

/** Poll `load` every `ms` until `done` says the value is final; resolves with it. Stops (rejects AbortError) on `signal`. */
export async function pollUntil<T>(load: () => Promise<T>, done: (v: T) => boolean, onValue: (v: T) => void, ms: number, signal?: AbortSignal): Promise<T> {
  for (;;) {
    if (signal?.aborted) throw new DOMException("Stopped.", "AbortError");
    const v = await load();
    onValue(v);
    if (done(v)) return v;
    await new Promise<void>((resolve, reject) => {
      const t = setTimeout(resolve, ms);
      signal?.addEventListener(
        "abort",
        () => {
          clearTimeout(t);
          reject(new DOMException("Stopped.", "AbortError"));
        },
        { once: true },
      );
    });
  }
}

// ── files ─────────────────────────────────────────────────────────────

/** The first bytes of a file as text, enough to recognize it and read its header. */
export const readHead = async (file: Blob, bytes = 64 * 1024): Promise<string> => file.slice(0, bytes).text();

/** SHA-256 of a file, as the server reports it (lowercase hex). */
export async function sha256Hex(file: Blob): Promise<string> {
  const digest = await globalThis.crypto.subtle.digest("SHA-256", await file.arrayBuffer());
  return [...new Uint8Array(digest)].map((b) => b.toString(16).padStart(2, "0")).join("");
}

export function bytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} kB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

/**
 * Hand a downloaded file to the browser to save. The download already
 * happened with the session (an authenticated fetch), so this only names
 * the bytes; the object URL is released right after.
 */
export function saveBlob(blob: Blob, name: string): void {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.rel = "noopener";
  a.style.display = "none";
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 0);
}

// The files of this tab's dry runs, so "Apply" can send the very same file
// again without asking for it. Memory only: a reload asks for it again (and
// checks it's the same by its SHA-256).
const dryRunFiles = new Map<string, File>();
export const rememberFile = (jobId: string, file: File): void => {
  dryRunFiles.set(jobId, file);
};
export const rememberedFile = (jobId: string): File | undefined => dryRunFiles.get(jobId);
/** Forget every kept file (tests; nothing in the app needs it). */
export const forgetFiles = (): void => dryRunFiles.clear();
