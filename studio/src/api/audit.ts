/**
 * The audit port (RFC 0006 §6.2): the tenant's entries, and the export
 * jobs that write them as a signed `glossa.audit/v1` directory.
 *
 * `apiAudit` implements it through the generated client; component tests
 * hand screens an in-memory one (../test/fake-audit.ts).
 */
import { inject, type InjectionKey } from "vue";
import { client } from "./client";
import { all } from "./endpoints";
import { read, type Versioned } from "./errors";
import { AuditEntry, AuditExportJob, AuditKeyDocument, type AuditSource } from "./audit-schemas";
import { page } from "./schemas";

const PAGE = 50;
const value = async <T>(p: Promise<Versioned<T>>): Promise<T> => (await p).value;
const opt = (signal?: AbortSignal) => (signal ? { signal } : {});

export interface AuditFilter {
  /** Inclusive, RFC 3339. */
  from?: string;
  /** Exclusive, RFC 3339. */
  to?: string;
  actor?: string;
  action?: string;
  project?: string;
  source?: AuditSource;
  aggregate_type?: string;
  aggregate_id?: string;
}

export interface AuditPage {
  items: AuditEntry[];
  /** Pass it back to read the next page. */
  next?: string | undefined;
}

/** An export is a time range or a sequence range, never both. */
export type AuditExportRange = { from: string; to: string } | { first_sequence: number; last_sequence?: number };

export interface AuditPort {
  /** One page, newest first. */
  entries(tenant: string, filter: AuditFilter, pageToken?: string, signal?: AbortSignal): Promise<AuditPage>;
  entry(tenant: string, sequence: number, signal?: AbortSignal): Promise<AuditEntry>;
  exportJobs(tenant: string, signal?: AbortSignal): Promise<AuditExportJob[]>;
  exportJob(tenant: string, id: string, signal?: AbortSignal): Promise<AuditExportJob>;
  createExport(tenant: string, range: AuditExportRange, idempotencyKey: string): Promise<AuditExportJob>;
  /** The deployment's published audit keys; `null` when it publishes none (exports are off). */
  keys(signal?: AbortSignal): Promise<AuditKeyDocument | null>;
}

const JOB = "/v1/tenants/{tenant}/audit-export-jobs/{audit_export_job}" as const;

export const KEYS_PATH = "/.well-known/glossa-audit-keys.json";

export const apiAudit: AuditPort = {
  async entries(tenant, f, page_token, signal) {
    const r = await value(
      read(
        client.GET("/v1/tenants/{tenant}/audit-entries", { params: { path: { tenant }, query: { ...f, page_size: PAGE, page_token } }, ...opt(signal) }),
        page(AuditEntry),
      ),
    );
    return { items: r.items, next: r.next_page_token };
  },
  entry: (tenant, sequence, signal) =>
    value(read(client.GET("/v1/tenants/{tenant}/audit-entries/{sequence}", { params: { path: { tenant, sequence } }, ...opt(signal) }), AuditEntry)),
  exportJobs: (tenant, signal) =>
    all((page_token) =>
      value(read(client.GET("/v1/tenants/{tenant}/audit-export-jobs", { params: { path: { tenant }, query: { page_size: PAGE, page_token } }, ...opt(signal) }), page(AuditExportJob))),
    ),
  exportJob: (tenant, id, signal) => value(read(client.GET(JOB, { params: { path: { tenant, audit_export_job: id } }, ...opt(signal) }), AuditExportJob)),
  createExport: (tenant, range, key) =>
    value(
      read(client.POST("/v1/tenants/{tenant}/audit-export-jobs", { params: { path: { tenant }, header: { "Idempotency-Key": key } }, body: range }), AuditExportJob),
    ),
  async keys(signal) {
    // Deployment-wide and public: outside /v1, no token (platform/README.md, Audit export format).
    const res = await fetch(KEYS_PATH, { headers: { Accept: "application/json" }, ...opt(signal) });
    if (res.status === 404) return null;
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    return AuditKeyDocument.parse(await res.json());
  },
};

export const AUDIT: InjectionKey<AuditPort> = Symbol("audit");

export function useAudit(): AuditPort {
  return inject(AUDIT, apiAudit);
}
