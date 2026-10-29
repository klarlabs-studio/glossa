/**
 * An in-memory AuditPort for component tests, with the API's rules in
 * miniature (RFC 0006 §6): entries newest first and cursor-paged, filters
 * combined, an unknown sequence is `not_found`, an export is a time range
 * or a sequence range (`range_too_long`, `sequence_out_of_range`), a job
 * is queued, then running, then done as `advance` says, and a deployment
 * without a key answers `audit_export_unavailable`.
 *
 * `fail` makes a whole method fail; `hold` keeps its answer pending;
 * `refuse` fails the next call of a method once.
 *
 * Test-only: nothing in the app imports it.
 */
import type { AuditFilter, AuditPort } from "../api/audit";
import type { AuditEntry, AuditExportJob, AuditKeyDocument } from "../api/audit-schemas";
import { ApiError } from "../api/errors";

const NOW = "2026-10-04T10:00:00Z";
const H = (n: number) => n.toString(16).padStart(64, "0");

export function entry(sequence: number, over: Partial<AuditEntry> = {}): AuditEntry {
  return {
    sequence,
    event_id: `ev-${sequence}`,
    source: "outbox",
    action: "localization.translation.revised",
    actor: "person:0190a1b2-0000-7000-8000-000000000001",
    occurred_at: new Date(Date.UTC(2026, 9, 1, 9, sequence)).toISOString().replace(".000", ""),
    aggregate_type: "translation",
    aggregate_id: `tr_${sequence}`,
    locale: "de",
    summary: { revision: sequence, state: "approved", text: "string(len=12)" },
    prev_hash: H(sequence - 1),
    hash: H(sequence),
    ...over,
  };
}

export function job(over: Partial<AuditExportJob> = {}): AuditExportJob {
  return { id: "aej-1", state: "queued", attempts: 0, created_by: "person:x", created_at: NOW, updated_at: NOW, expires_at: "2099-01-01T00:00:00Z", ...over };
}

/** A succeeded job with both files. */
export function doneJob(over: Partial<AuditExportJob> = {}): AuditExportJob {
  return job({
    state: "succeeded",
    first_sequence: 1,
    last_sequence: 12,
    entry_count: 12,
    key_id: "audit-2026",
    entries: { path: "entries.jsonl", sha256: H(1), bytes: 4096, download_url: "/v1/tenants/t/audit-export-jobs/aej-1/file" },
    manifest: { path: "manifest.json", sha256: H(2), bytes: 812, download_url: "/v1/tenants/t/audit-export-jobs/aej-1/manifest" },
    ...over,
  });
}

export const keyDocument: AuditKeyDocument = {
  format: "glossa.audit.keys/1",
  keys: [
    { key_id: "audit-2026", algorithm: "Ed25519", public_key: "AAAA", active: true },
    { key_id: "audit-2025", algorithm: "Ed25519", public_key: "BBBB", active: false },
  ],
};

type Method = keyof AuditPort;

export interface FakeAudit extends AuditPort {
  readonly calls: Array<[Method, ...unknown[]]>;
  readonly state: { entries: AuditEntry[]; jobs: AuditExportJob[]; keys: AuditKeyDocument | null; pageSize: number; unavailable: boolean };
  fail: Partial<Record<Method, ApiError>>;
  refuse: Partial<Record<Method, ApiError>>;
  hold: Set<Method>;
  /** Moves every open job one step: queued → running → succeeded. */
  advance(): void;
}

const never = <T>() => new Promise<T>(() => undefined);

export function createFakeAudit(init: Partial<FakeAudit["state"]> = {}): FakeAudit {
  const state: FakeAudit["state"] = { entries: [], jobs: [], keys: keyDocument, pageSize: 50, unavailable: false, ...init };
  const calls: FakeAudit["calls"] = [];
  const fail: FakeAudit["fail"] = {};
  const refuse: FakeAudit["refuse"] = {};
  const hold = new Set<Method>();
  let seq = 1;

  async function gate(m: Method, ...args: unknown[]): Promise<void> {
    calls.push([m, ...args]);
    if (hold.has(m)) await never();
    const f = fail[m];
    if (f) throw f;
    const once = refuse[m];
    if (once) {
      delete refuse[m];
      throw once;
    }
  }

  const matches = (e: AuditEntry, f: AuditFilter) =>
    (!f.actor || e.actor === f.actor) &&
    (!f.action || e.action === f.action) &&
    (!f.source || e.source === f.source) &&
    (!f.project || e.project_id === f.project) &&
    (!f.from || Date.parse(e.occurred_at) >= Date.parse(f.from)) &&
    (!f.to || Date.parse(e.occurred_at) < Date.parse(f.to));

  return {
    calls,
    state,
    fail,
    refuse,
    hold,
    advance() {
      state.jobs = state.jobs.map((j) => (j.state === "queued" ? { ...j, state: "running" } : j.state === "running" ? doneJob({ ...j, state: "succeeded", id: j.id }) : j));
    },
    async entries(tenant, filter, token) {
      await gate("entries", tenant, filter, token);
      const all = state.entries.filter((e) => matches(e, filter)).sort((a, b) => b.sequence - a.sequence);
      const start = token ? Number(token) : 0;
      const items = all.slice(start, start + state.pageSize);
      const end = start + items.length;
      return { items: structuredClone(items), next: end < all.length ? String(end) : undefined };
    },
    async entry(tenant, sequence) {
      await gate("entry", tenant, sequence);
      const e = state.entries.find((x) => x.sequence === sequence);
      if (!e) throw new ApiError(404, "not_found", "No such entry.");
      return structuredClone(e);
    },
    async exportJobs(tenant) {
      await gate("exportJobs", tenant);
      return structuredClone(state.jobs);
    },
    async exportJob(tenant, id) {
      await gate("exportJob", tenant, id);
      const j = state.jobs.find((x) => x.id === id);
      if (!j) throw new ApiError(404, "not_found", "No such job.");
      return structuredClone(j);
    },
    async createExport(tenant, range, key) {
      await gate("createExport", tenant, range, key);
      if (state.unavailable) throw new ApiError(503, "audit_export_unavailable", "No audit key.");
      if ("from" in range && Date.parse(range.to) - Date.parse(range.from) > 31 * 86_400_000) throw new ApiError(422, "range_too_long", "At most 31 days.");
      if ("first_sequence" in range && range.first_sequence > state.entries.length) throw new ApiError(422, "sequence_out_of_range", "Past the head.");
      const j = job({ id: `aej-new-${++seq}`, ...("from" in range ? { from: range.from, to: range.to } : {}) });
      state.jobs = [j, ...state.jobs];
      return structuredClone(j);
    },
    async keys() {
      await gate("keys");
      return structuredClone(state.keys);
    },
  };
}
