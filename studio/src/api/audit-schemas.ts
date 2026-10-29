/**
 * zod schemas for the tenant's audit trail and its exports (RFC 0006
 * §6): hash-chained, content-free entries, export jobs, and the public
 * key document a verifier pins. An entry holds identifiers, selectors
 * and the shapes of everything else — never text — and the screens show
 * it that way.
 */
import { z } from "zod";
import type { components } from "./schema";

const timestamp = z.string().min(1);
const hash = z.string().regex(/^[0-9a-f]{64}$/);

export const AuditSource = z.enum(["outbox", "direct", "import"]);

export const AuditEntry = z.object({
  sequence: z.number().int().min(1),
  event_id: z.string(),
  source: AuditSource,
  action: z.string(),
  actor: z.string(),
  occurred_at: timestamp,
  aggregate_type: z.string(),
  aggregate_id: z.string(),
  project_id: z.string().optional(),
  locale: z.string().optional(),
  summary: z.record(z.string(), z.unknown()),
  request_id: z.string().optional(),
  trace_id: z.string().optional(),
  prev_hash: hash,
  hash,
});

export const AuditExportJobState = z.enum(["queued", "running", "succeeded", "failed"]);

export const AuditExportFile = z.object({
  path: z.string(),
  sha256: hash,
  bytes: z.number().int().min(0),
  download_url: z.string().optional(),
});

export const AuditExportJob = z.object({
  id: z.string().min(1),
  state: AuditExportJobState,
  from: timestamp.optional(),
  to: timestamp.optional(),
  first_sequence: z.number().int().optional(),
  last_sequence: z.number().int().optional(),
  entry_count: z.number().int().min(0).optional(),
  first_prev_hash: hash.optional(),
  last_hash: hash.optional(),
  key_id: z.string().optional(),
  entries: AuditExportFile.optional(),
  manifest: AuditExportFile.optional(),
  failure_code: z.string().optional(),
  failure_message: z.string().optional(),
  attempts: z.number().int(),
  created_by: z.string(),
  created_at: timestamp,
  started_at: timestamp.optional(),
  finished_at: timestamp.optional(),
  updated_at: timestamp,
  expires_at: timestamp,
  files_deleted_at: timestamp.optional(),
});

/** `glossa.audit.keys/1`, served at `/.well-known/glossa-audit-keys.json` (not in openapi.yaml). */
export const AuditKeyDocument = z.object({
  format: z.literal("glossa.audit.keys/1"),
  keys: z.array(z.object({ key_id: z.string(), algorithm: z.string(), public_key: z.string(), active: z.boolean() })),
});

export type AuditSource = z.infer<typeof AuditSource>;
export type AuditEntry = z.infer<typeof AuditEntry>;
export type AuditExportJob = z.infer<typeof AuditExportJob>;
export type AuditExportFile = z.infer<typeof AuditExportFile>;
export type AuditKeyDocument = z.infer<typeof AuditKeyDocument>;

type C = components["schemas"];
type Fits<A, B> = [A] extends [B] ? true : false;
type Assert<T extends true> = T;
export type AuditContractAlignment = [
  Assert<Fits<C["AuditEntry"], AuditEntry>>,
  Assert<Fits<C["AuditExportJob"], AuditExportJob>>,
  Assert<Fits<C["AuditSource"], AuditSource>>,
];
