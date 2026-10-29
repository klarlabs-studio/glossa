/**
 * An in-memory IntegrationPort for component tests, with Integration's
 * rules in miniature: an import waits for its file, then each read of
 * the job advances it (queued → running → succeeded) and its results
 * come from `resultsFor`; a non-dry-run import of a file already
 * imported with the same options reuses that job's result; catalogs need
 * a project and `po` can't be exported; a running import stops after its
 * batch when cancelled, an export can be cancelled only while queued.
 * Test-only: nothing in the app imports it.
 */
import { ApiError } from "../api/errors";
import type { ExportFile, ExportRequest, ImportRequest, IntegrationPort, Page } from "../api/integration";
import { sha256Hex } from "../lib/integration";
import type { ExportJob, ImportCounts, ImportJob, ImportResult, IntegrationFormat, KnowledgeKind, NamespaceSummary } from "../api/integration-schemas";

export interface FakeIntegration extends IntegrationPort {
  readonly calls: Array<[string, ...unknown[]]>;
  readonly imports: ImportJob[];
  readonly exports: ExportJob[];
  /** The text uploaded for each import. */
  readonly files: Map<string, string>;
  /** What an import's file yields, by its text and the job. Default: nothing. */
  resultsFor: (text: string, job: ImportJob) => Array<Omit<ImportResult, "seq">>;
  /** The bytes of an export's file. */
  exportContent: (job: ExportJob) => string;
  /** Results per page (default 100). */
  pageSize: number;
  /** Hold jobs where they are (reads don't advance them). */
  hold: boolean;
  /** The project's namespaces (Catalog's listing). */
  namespaceList: NamespaceSummary[];
}

const NOW = "2026-09-19T08:00:00Z";
const formatOf = (k: KnowledgeKind): IntegrationFormat => (k === "tm" ? "tmx" : "tbx");
const kindOf = (f: IntegrationFormat) => (f === "tmx" ? "tm" : f === "tbx" ? "termbase" : "catalog");
const zero = (): ImportCounts => ({ created: 0, updated: 0, unchanged: 0, conflict: 0, invalid: 0 });
const hex = (text: string) => {
  let h = 0;
  for (const c of text) h = (h * 31 + c.codePointAt(0)!) >>> 0;
  return h.toString(16).padStart(8, "0").repeat(8);
};

export function createFakeIntegration(): FakeIntegration {
  let seq = 0;
  const id = (p: string) => `${p}_${++seq}`;
  const calls: Array<[string, ...unknown[]]> = [];
  const imports: ImportJob[] = [];
  const exports: ExportJob[] = [];
  const files = new Map<string, string>();
  const results = new Map<string, ImportResult[]>();
  const fingerprints = new Map<string, string>();

  const importOf = (jid: string) => {
    const j = imports.find((x) => x.id === jid);
    if (!j) throw new ApiError(404, "not_found", "No such import.");
    return j;
  };
  const exportOf = (jid: string) => {
    const j = exports.find((x) => x.id === jid);
    if (!j) throw new ApiError(404, "not_found", "No such export.");
    return j;
  };
  const replace = <T extends { id: string }>(list: T[], next: T): T => {
    list.splice(list.findIndex((x) => x.id === next.id), 1, next);
    return next;
  };
  const newestFirst = <T>(list: T[], q: { project?: string }) => [...list].reverse().filter((j) => !q.project || (j as { project_id?: string }).project_id === q.project);
  const pageOf = <T>(items: T[], size: number, token?: string): Page<T> => {
    const from = token ? Number(token) : 0;
    const next = from + size < items.length ? String(from + size) : undefined;
    return { items: items.slice(from, from + size), next };
  };
  const fingerprint = (j: ImportJob, text: string) => JSON.stringify([text, j.project_id, j.format, j.mode, j.options]);

  function finishImport(j: ImportJob): ImportJob {
    const rs = (fake.resultsFor(files.get(j.id) ?? "", j) ?? []).map((r, i) => ({ ...r, seq: i + 1 }));
    results.set(j.id, rs);
    const summary = { ...zero(), by_kind: {} as Record<string, ImportCounts> };
    for (const r of rs) {
      summary[r.status]++;
      const k = (summary.by_kind[r.kind] ??= zero());
      k[r.status]++;
    }
    if (j.mode !== "dry_run") fingerprints.set(fingerprint(j, files.get(j.id) ?? ""), j.id);
    return replace(imports, { ...j, state: "succeeded", summary, total_items: rs.length, processed_items: rs.length, finished_at: NOW });
  }

  function advanceImport(j: ImportJob): ImportJob {
    if (fake.hold) return j;
    if (j.state === "queued") return replace(imports, { ...j, state: "running", started_at: NOW, total_items: 2, processed_items: 1 });
    if (j.state === "running" && j.cancel_requested) return replace(imports, { ...j, state: "cancelled", finished_at: NOW });
    if (j.state === "running") return finishImport(j);
    return j;
  }

  function advanceExport(j: ExportJob): ExportJob {
    if (fake.hold) return j;
    if (j.state === "queued") return replace(exports, { ...j, state: "running", started_at: NOW });
    if (j.state === "running") {
      const content = fake.exportContent(j);
      const locales = j.options.locales ?? [];
      const ext = j.format === "xliff" ? "xlf" : j.format;
      const file_name = locales.length > 1 ? `demo.${ext}.zip` : `demo${locales[0] ? `.${locales[0]}` : ""}.${ext}`;
      return replace(exports, {
        ...j,
        state: "succeeded",
        file_name,
        file: { size: content.length, sha256: hex(content), content_type: locales.length > 1 ? "application/zip" : "application/json" },
        download_url: `/v1/tenants/t/export-jobs/${j.id}/file`,
        written: 3,
        finished_at: NOW,
      });
    }
    return j;
  }

  const fake: FakeIntegration = {
    calls,
    imports,
    exports,
    files,
    resultsFor: () => [],
    exportContent: () => "{}\n",
    pageSize: 100,
    hold: false,
    namespaceList: [],

    async importJobs(_tenant, q, token) {
      calls.push(["importJobs", q]);
      return pageOf(newestFirst(imports, q), 50, token);
    },
    async importJob(_tenant, jid) {
      calls.push(["importJob", jid]);
      return advanceImport(importOf(jid));
    },
    async createImport(_tenant, body: ImportRequest, key) {
      calls.push(["createImport", body, key]);
      if (kindOf(body.format) === "catalog" && !body.project_id) throw new ApiError(400, "project_required", "Catalogs belong to a project.");
      const jid = id("import");
      const job: ImportJob = {
        id: jid,
        ...(body.project_id ? { project_id: body.project_id } : {}),
        kind: kindOf(body.format),
        format: body.format,
        mode: body.mode ?? "merge",
        options: body.options ?? {},
        state: "awaiting_upload",
        file_name: body.file_name ?? "",
        upload_url: `/v1/tenants/t/import-jobs/${jid}/file`,
        summary: { ...zero(), by_kind: {} },
        total_items: 0,
        processed_items: 0,
        cancel_requested: false,
        attempts: 0,
        created_by: "person:me",
        created_at: NOW,
        updated_at: NOW,
        expires_at: NOW,
      };
      imports.push(job);
      return job;
    },
    async upload(_tenant, job, file, onProgress) {
      calls.push(["upload", job.id]);
      const j = importOf(job.id);
      if (j.state !== "awaiting_upload") throw new ApiError(409, "upload_not_expected", "Uploaded already.");
      const text = await file.text();
      if (!text) throw new ApiError(400, "empty_file", "Empty.");
      onProgress?.(file.size / 2, file.size);
      onProgress?.(file.size, file.size);
      files.set(j.id, text);
      const { upload_url: _gone, ...rest } = j;
      const queued: ImportJob = { ...rest, state: "queued", file: { size: file.size, sha256: await sha256Hex(file), content_type: "application/octet-stream" } };
      const earlier = j.mode !== "dry_run" ? fingerprints.get(fingerprint(j, text)) : undefined;
      if (earlier) {
        const prev = importOf(earlier);
        results.set(j.id, results.get(earlier) ?? []);
        return replace(imports, { ...queued, state: "succeeded", reused_job_id: earlier, summary: prev.summary, total_items: prev.total_items, processed_items: prev.processed_items, finished_at: NOW });
      }
      return replace(imports, queued);
    },
    async cancelImport(_tenant, jid) {
      calls.push(["cancelImport", jid]);
      const j = importOf(jid);
      if (j.state === "succeeded" || j.state === "failed") throw new ApiError(409, "job_not_cancellable", "Finished.");
      if (j.state === "running") return replace(imports, { ...j, cancel_requested: true });
      return replace(imports, { ...j, state: "cancelled", finished_at: NOW });
    },
    async importResults(_tenant, jid, q, token) {
      calls.push(["importResults", jid, q, token]);
      importOf(jid);
      const rs = (results.get(jid) ?? []).filter((r) => (!q.status || r.status === q.status) && (!q.kind || r.kind === q.kind));
      return pageOf(rs, fake.pageSize, token);
    },

    async exportJobs(_tenant, q, token) {
      calls.push(["exportJobs", q]);
      return pageOf(newestFirst(exports, q), 50, token);
    },
    async exportJob(_tenant, jid) {
      calls.push(["exportJob", jid]);
      return advanceExport(exportOf(jid));
    },
    async createExport(_tenant, body: ExportRequest, key) {
      calls.push(["createExport", body, key]);
      if (body.format === "po") throw new ApiError(400, "invalid_format", "PO is import only.");
      if (kindOf(body.format) === "catalog" && !body.project_id) throw new ApiError(400, "project_required", "Catalogs belong to a project.");
      const job: ExportJob = {
        id: id("export"),
        ...(body.project_id ? { project_id: body.project_id } : {}),
        kind: kindOf(body.format),
        format: body.format,
        options: body.options ?? {},
        state: "queued",
        file_name: "",
        written: 0,
        cancel_requested: false,
        attempts: 0,
        created_by: "person:me",
        created_at: NOW,
        updated_at: NOW,
        expires_at: NOW,
      };
      exports.push(job);
      return job;
    },
    async cancelExport(_tenant, jid) {
      calls.push(["cancelExport", jid]);
      const j = exportOf(jid);
      if (j.state !== "queued" && j.state !== "cancelled") throw new ApiError(409, "job_not_cancellable", "Running or finished.");
      return replace(exports, { ...j, state: "cancelled", finished_at: NOW });
    },
    async download(_tenant, job): Promise<ExportFile> {
      calls.push(["download", job.id]);
      const j = exportOf(job.id);
      if (j.state !== "succeeded") throw new ApiError(409, "export_not_ready", "Not ready.");
      const content = fake.exportContent(j);
      return { blob: new Blob([content]), name: j.file_name, sha256: j.file?.sha256 };
    },

    async namespaces(_tenant, project, token) {
      calls.push(["namespaces", project, token]);
      return pageOf(fake.namespaceList, 2, token);
    },
    async knowledgeImports(_tenant, kind, token) {
      calls.push(["knowledgeImports", kind]);
      return pageOf([...imports].reverse().filter((j) => !j.project_id && j.kind === kind), 50, token);
    },
    async knowledgeExports(_tenant, kind, token) {
      calls.push(["knowledgeExports", kind]);
      return pageOf([...exports].reverse().filter((j) => !j.project_id && j.kind === kind), 50, token);
    },
    async createKnowledgeImport(tenant, kind, body, key) {
      calls.push(["createKnowledgeImport", kind, body, key]);
      return fake.createImport(tenant, { format: formatOf(kind), ...body }, key);
    },
    async createKnowledgeExport(tenant, kind, body, key) {
      calls.push(["createKnowledgeExport", kind, body, key]);
      if (kind === "termbase" && body.options && Object.keys(body.options).length) throw new ApiError(400, "invalid_options", "TBX takes no options.");
      return fake.createExport(tenant, { format: formatOf(kind), ...body }, key);
    },
  };
  return fake;
}
