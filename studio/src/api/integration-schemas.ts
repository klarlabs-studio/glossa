/**
 * zod schemas for the Integration context (RFC 0003 §5–§6): import and
 * export jobs and an import's per-item results. Kept out of ./schemas so
 * only the import/export screens load them; the compile-time assertions
 * at the bottom tie each to the contract.
 */
import { z } from "zod";
import type { components } from "./schema";
import { ReviewState, Syntax } from "./schemas";

const timestamp = z.string().min(1);
const id = z.string().min(1);

export const IntegrationFormat = z.enum(["xliff", "json", "po", "tmx", "tbx"]);
export const IntegrationKind = z.enum(["catalog", "tm", "termbase"]);
export const ImportMode = z.enum(["dry_run", "merge", "overwrite"]);
export const IntegrationJobState = z.enum(["awaiting_upload", "queued", "running", "succeeded", "failed", "cancelled"]);
export const ImportResultKind = z.enum(["message", "translation", "tm_unit", "concept"]);
export const ImportResultStatus = z.enum(["created", "updated", "unchanged", "conflict", "invalid"]);

export const ImportOptions = z.object({
  locale: z.string().optional(),
  namespace: z.string().optional(),
  syntax: Syntax.optional(),
  state: ReviewState.optional(),
  plural_variable: z.string().optional(),
});

export const IntegrationFile = z.object({
  size: z.number().int().min(0),
  sha256: z.string(),
  content_type: z.string(),
});

export const ImportCounts = z.object({
  created: z.number().int().min(0),
  updated: z.number().int().min(0),
  unchanged: z.number().int().min(0),
  conflict: z.number().int().min(0),
  invalid: z.number().int().min(0),
});
export const ImportSummary = ImportCounts.extend({ by_kind: z.record(z.string(), ImportCounts) });

const jobCommon = {
  id,
  project_id: id.optional(),
  kind: IntegrationKind,
  format: IntegrationFormat,
  state: IntegrationJobState,
  file: IntegrationFile.optional(),
  failure_code: z.string().optional(),
  failure_message: z.string().optional(),
  cancel_requested: z.boolean(),
  attempts: z.number().int().min(0),
  created_by: z.string(),
  created_at: timestamp,
  started_at: timestamp.optional(),
  finished_at: timestamp.optional(),
  updated_at: timestamp,
  expires_at: timestamp,
  files_deleted_at: timestamp.optional(),
};

export const ImportJob = z.object({
  ...jobCommon,
  mode: ImportMode,
  options: ImportOptions,
  file_name: z.string(),
  upload_url: z.string().optional(),
  reused_job_id: id.optional(),
  summary: ImportSummary,
  total_items: z.number().int().min(0),
  processed_items: z.number().int().min(0),
});

export const ImportResult = z.object({
  seq: z.number().int().min(0),
  kind: ImportResultKind,
  key: z.string(),
  locale: z.string().optional(),
  status: ImportResultStatus,
  code: z.string().optional(),
  detail: z.string().optional(),
  line: z.number().int().optional(),
  column: z.number().int().optional(),
  /** The item in the format's own terms: an XLIFF fragment identifier, a JSON pointer, a PO msgctxt/msgid, tu[n], conceptEntry[n]. */
  ref: z.string().optional(),
});

/** A namespace of a project with its message counts (Catalog's listing, for the export dialog). */
export const NamespaceSummary = z.object({
  name: z.string().min(1),
  active_messages: z.number().int().min(0),
  obsolete_messages: z.number().int().min(0),
});

export const ExportOptions = z.object({
  locales: z.array(z.string()).optional(),
  source_locale: z.string().optional(),
  namespaces: z.array(z.string()).optional(),
  states: z.array(ReviewState).optional(),
  layout: z.enum(["flat", "nested"]).optional(),
  syntax: Syntax.optional(),
});

export const ExportJob = z.object({
  ...jobCommon,
  options: ExportOptions,
  file_name: z.string(),
  download_url: z.string().optional(),
  written: z.number().int().min(0),
});

export type IntegrationFormat = z.infer<typeof IntegrationFormat>;
export type IntegrationKind = z.infer<typeof IntegrationKind>;
export type ImportMode = z.infer<typeof ImportMode>;
export type IntegrationJobState = z.infer<typeof IntegrationJobState>;
export type ImportResultKind = z.infer<typeof ImportResultKind>;
export type ImportResultStatus = z.infer<typeof ImportResultStatus>;
export type ImportOptions = z.infer<typeof ImportOptions>;
export type ImportCounts = z.infer<typeof ImportCounts>;
export type ImportSummary = z.infer<typeof ImportSummary>;
export type ImportJob = z.infer<typeof ImportJob>;
export type ImportResult = z.infer<typeof ImportResult>;
export type ExportOptions = z.infer<typeof ExportOptions>;
export type ExportJob = z.infer<typeof ExportJob>;
export type NamespaceSummary = z.infer<typeof NamespaceSummary>;
/** The workspace's knowledge files: its translation memory (TMX) and termbase (TBX). */
export type KnowledgeKind = "tm" | "termbase";

// ── contract alignment (compile time only) ─────────────────────────────
type C = components["schemas"];
type Fits<A, B> = [A] extends [B] ? true : false;
type Assert<T extends true> = T;
export type IntegrationContractAlignment = [
  Assert<Fits<IntegrationFormat, C["IntegrationFormat"]>>,
  Assert<Fits<ImportMode, C["ImportMode"]>>,
  Assert<Fits<IntegrationJobState, C["IntegrationJobState"]>>,
  Assert<Fits<ImportJob, C["ImportJob"]>>,
  Assert<Fits<ImportResult, C["ImportResult"]>>,
  Assert<Fits<ExportJob, C["ExportJob"]>>,
  Assert<Fits<NamespaceSummary, C["NamespaceSummary"]>>,
  Assert<Fits<C["ImportResultStatus"], ImportResultStatus>>,
  Assert<Fits<C["ImportResultKind"], ImportResultKind>>,
];
