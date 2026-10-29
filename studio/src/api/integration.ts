/**
 * The Integration port (RFC 0003 §5–§6): import and export jobs. An
 * import is created, then its file is `PUT` to the job's `upload_url`
 * (with progress, so over XMLHttpRequest), then polled until it ends and
 * its per-item results are paged; an export is created, polled, and its
 * file downloaded with the session (never a bare link: the download
 * needs the cookie, and `ETag` is the file's SHA-256).
 *
 * `apiIntegration` implements it over the generated /v1 client with
 * every response checked by zod; component tests provide an in-memory
 * fake (src/test/fake-integration.ts). Like the other M2 ports it loads
 * with the screens that use it, not with the app shell.
 */
import { inject, type InjectionKey } from "vue";
import { client, currentCsrfToken, reportUnauthenticated } from "./client";
import { ApiError, failure, read, send, type Versioned } from "./errors";
import * as X from "./integration-schemas";
import type { components } from "./schema";
import { page } from "./schemas";

type Body<N extends keyof components["schemas"]> = components["schemas"][N];
export type ImportRequest = Body<"ImportJobRequest">;
export type ExportRequest = Body<"ExportJobRequest">;
export type KnowledgeImportRequest = Body<"KnowledgeImportJobRequest">;
export type KnowledgeExportRequest = Body<"KnowledgeExportJobRequest">;

export interface Page<T> {
  items: T[];
  next: string | undefined;
}
export interface JobQuery {
  /** A project's jobs; without it, every job of the tenant (tenant-wide TMX and TBX included). */
  project?: string;
  state?: X.IntegrationJobState;
}
export interface ResultQuery {
  status?: X.ImportResultStatus;
  kind?: X.ImportResultKind;
}
export interface ExportFile {
  blob: Blob;
  /** From `Content-Disposition`, else the job's `file_name`. */
  name: string;
  /** The file's SHA-256, from the `ETag`. */
  sha256: string | undefined;
}
/** Bytes sent of the total, as an upload goes. */
export type UploadProgress = (loaded: number, total: number) => void;

export interface IntegrationPort {
  /** Newest first. */
  importJobs(tenant: string, q: JobQuery, pageToken?: string): Promise<Page<X.ImportJob>>;
  importJob(tenant: string, id: string): Promise<X.ImportJob>;
  /** A job in `awaiting_upload`; upload its file next. */
  createImport(tenant: string, body: ImportRequest, idempotencyKey: string): Promise<X.ImportJob>;
  /** `PUT` the file to the job's `upload_url`: the job comes back queued, or already succeeded when it reuses an earlier import. */
  upload(tenant: string, job: X.ImportJob, file: Blob, onProgress?: UploadProgress, signal?: AbortSignal): Promise<X.ImportJob>;
  cancelImport(tenant: string, id: string): Promise<X.ImportJob>;
  /** In file order. */
  importResults(tenant: string, id: string, q: ResultQuery, pageToken?: string): Promise<Page<X.ImportResult>>;

  /** Newest first. */
  exportJobs(tenant: string, q: JobQuery, pageToken?: string): Promise<Page<X.ExportJob>>;
  exportJob(tenant: string, id: string): Promise<X.ExportJob>;
  createExport(tenant: string, body: ExportRequest, idempotencyKey: string): Promise<X.ExportJob>;
  cancelExport(tenant: string, id: string): Promise<X.ExportJob>;
  download(tenant: string, job: X.ExportJob): Promise<ExportFile>;

  /** A project's namespaces by name, with their message counts (what an export offers to choose from). */
  namespaces(tenant: string, project: string, pageToken?: string): Promise<Page<X.NamespaceSummary>>;

  // The workspace's own translation memory (TMX) and termbase (TBX): tenant-wide jobs on routes of
  // their own. They are ordinary jobs otherwise: uploaded to, followed and downloaded like any.
  /** The workspace's imports of one kind, newest first. */
  knowledgeImports(tenant: string, kind: X.KnowledgeKind, pageToken?: string): Promise<Page<X.ImportJob>>;
  /** The workspace's exports of one kind, newest first. */
  knowledgeExports(tenant: string, kind: X.KnowledgeKind, pageToken?: string): Promise<Page<X.ExportJob>>;
  /** A tenant-wide TMX or TBX import in `awaiting_upload`. */
  createKnowledgeImport(tenant: string, kind: X.KnowledgeKind, body: KnowledgeImportRequest, idempotencyKey: string): Promise<X.ImportJob>;
  /** A tenant-wide TMX or TBX export, queued. */
  createKnowledgeExport(tenant: string, kind: X.KnowledgeKind, body: KnowledgeExportRequest, idempotencyKey: string): Promise<X.ExportJob>;
}

const value = async <T>(p: Promise<Versioned<T>>): Promise<T> => (await p).value;
const toPage = <T>(p: { items: T[]; next_page_token?: string | undefined }): Page<T> => ({ items: p.items, next: p.next_page_token });
const PAGE = 50;
const RESULTS_PAGE = 100;

/** The filename of an `attachment` Content-Disposition (`filename*` preferred), if any. */
export function dispositionName(header: string | null): string | undefined {
  if (!header) return undefined;
  const star = /filename\*\s*=\s*(?:UTF-8|utf-8)''([^;]+)/.exec(header);
  if (star?.[1]) {
    try {
      return decodeURIComponent(star[1].trim());
    } catch {
      // fall through to the plain name
    }
  }
  const plain = /filename\s*=\s*("([^"]*)"|[^;]+)/.exec(header);
  const name = (plain?.[2] ?? plain?.[1])?.trim();
  // Never a path: a name is where the browser saves, nothing more.
  return name ? name.split(/[\\/]/).pop() || undefined : undefined;
}

/** The upload URL as a same-origin path: the cookie and CSRF token never go anywhere else. */
function sameOrigin(url: string): string {
  const origin = globalThis.location?.origin ?? "http://localhost";
  const u = new URL(url, origin);
  if (u.origin !== origin) throw new ApiError(0, "invalid_response", "The upload URL isn't on this server.");
  return u.pathname + u.search;
}

function problemFrom(status: number, text: string): ApiError {
  let body: unknown;
  try {
    body = JSON.parse(text);
  } catch {
    body = undefined;
  }
  return failure({ error: body, response: new Response(null, { status }) });
}

/** PUT a file with upload progress (fetch has none), the session's CSRF token and the API's error conventions. */
export function putWithProgress(url: string, file: Blob, onProgress?: UploadProgress, signal?: AbortSignal): Promise<unknown> {
  const path = sameOrigin(url);
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("PUT", path);
    xhr.withCredentials = true;
    xhr.setRequestHeader("Content-Type", "application/octet-stream");
    const token = currentCsrfToken();
    if (token) xhr.setRequestHeader("X-CSRF-Token", token);
    if (onProgress) xhr.upload.addEventListener("progress", (e) => onProgress(e.loaded, e.lengthComputable ? e.total : file.size));
    const abort = () => xhr.abort();
    signal?.addEventListener("abort", abort, { once: true });
    xhr.addEventListener("load", () => {
      signal?.removeEventListener("abort", abort);
      if (xhr.status === 401) reportUnauthenticated(path);
      if (xhr.status < 200 || xhr.status >= 300) return reject(problemFrom(xhr.status, xhr.responseText));
      try {
        resolve(JSON.parse(xhr.responseText));
      } catch {
        reject(new ApiError(xhr.status, "unexpected_response", `Unexpected response (HTTP ${xhr.status}).`));
      }
    });
    xhr.addEventListener("error", () => reject(new ApiError(0, "network_error", "The server could not be reached.")));
    xhr.addEventListener("abort", () => reject(new DOMException("The upload was cancelled.", "AbortError")));
    xhr.send(file);
  });
}

function parseJob(body: unknown): X.ImportJob {
  const r = X.ImportJob.safeParse(body);
  if (!r.success) throw new ApiError(200, "invalid_response", `The server's response didn't match the contract: ${r.error.message}`);
  return r.data;
}

export const apiIntegration: IntegrationPort = {
  importJobs: async (tenant, q, page_token) =>
    toPage(
      await value(
        read(client.GET("/v1/tenants/{tenant}/import-jobs", { params: { path: { tenant }, query: { ...q, page_size: PAGE, page_token } } }), page(X.ImportJob)),
      ),
    ),
  importJob: (tenant, import_job) =>
    value(read(client.GET("/v1/tenants/{tenant}/import-jobs/{import_job}", { params: { path: { tenant, import_job } } }), X.ImportJob)),
  createImport: (tenant, body, key) =>
    value(read(client.POST("/v1/tenants/{tenant}/import-jobs", { params: { path: { tenant }, header: { "Idempotency-Key": key } }, body }), X.ImportJob)),
  upload: async (tenant, job, file, onProgress, signal) =>
    parseJob(await putWithProgress(job.upload_url ?? `/v1/tenants/${tenant}/import-jobs/${job.id}/file`, file, onProgress, signal)),
  cancelImport: (tenant, import_job) =>
    value(read(client.POST("/v1/tenants/{tenant}/import-jobs/{import_job}/cancellation", { params: { path: { tenant, import_job } } }), X.ImportJob)),
  importResults: async (tenant, import_job, q, page_token) =>
    toPage(
      await value(
        read(
          client.GET("/v1/tenants/{tenant}/import-jobs/{import_job}/results", {
            params: { path: { tenant, import_job }, query: { ...q, page_size: RESULTS_PAGE, page_token } },
          }),
          page(X.ImportResult),
        ),
      ),
    ),

  exportJobs: async (tenant, q, page_token) =>
    toPage(
      await value(
        read(client.GET("/v1/tenants/{tenant}/export-jobs", { params: { path: { tenant }, query: { ...q, page_size: PAGE, page_token } } }), page(X.ExportJob)),
      ),
    ),
  exportJob: (tenant, export_job) =>
    value(read(client.GET("/v1/tenants/{tenant}/export-jobs/{export_job}", { params: { path: { tenant, export_job } } }), X.ExportJob)),
  createExport: (tenant, body, key) =>
    value(read(client.POST("/v1/tenants/{tenant}/export-jobs", { params: { path: { tenant }, header: { "Idempotency-Key": key } }, body }), X.ExportJob)),
  cancelExport: (tenant, export_job) =>
    value(read(client.POST("/v1/tenants/{tenant}/export-jobs/{export_job}/cancellation", { params: { path: { tenant, export_job } } }), X.ExportJob)),
  download: async (tenant, job) => {
    const r = await send(client.GET("/v1/tenants/{tenant}/export-jobs/{export_job}/file", { params: { path: { tenant, export_job: job.id } }, parseAs: "blob" }));
    if (!r.response.ok) throw failure(r);
    if (!(r.data instanceof Blob)) throw new ApiError(r.response.status, "unexpected_response", "The download had no file.");
    const etag = r.response.headers.get("ETag");
    return {
      blob: r.data,
      name: dispositionName(r.response.headers.get("Content-Disposition")) ?? job.file_name,
      sha256: etag ? etag.replace(/^(W\/)?"|"$/g, "") : undefined,
    };
  },

  namespaces: async (tenant, project, page_token) =>
    toPage(
      await value(
        read(
          client.GET("/v1/tenants/{tenant}/projects/{project}/namespaces", { params: { path: { tenant, project }, query: { page_size: 100, page_token } } }),
          page(X.NamespaceSummary),
        ),
      ),
    ),
  knowledgeImports: async (tenant, kind, page_token) => {
    const params = { path: { tenant }, query: { page_size: PAGE, page_token } };
    const call = kind === "tm" ? client.GET("/v1/tenants/{tenant}/tm-import-jobs", { params }) : client.GET("/v1/tenants/{tenant}/termbase-import-jobs", { params });
    return toPage(await value(read(call, page(X.ImportJob))));
  },
  knowledgeExports: async (tenant, kind, page_token) => {
    const params = { path: { tenant }, query: { page_size: PAGE, page_token } };
    const call = kind === "tm" ? client.GET("/v1/tenants/{tenant}/tm-export-jobs", { params }) : client.GET("/v1/tenants/{tenant}/termbase-export-jobs", { params });
    return toPage(await value(read(call, page(X.ExportJob))));
  },
  createKnowledgeImport: (tenant, kind, body, key) => {
    const params = { path: { tenant }, header: { "Idempotency-Key": key } };
    const call =
      kind === "tm" ? client.POST("/v1/tenants/{tenant}/tm-import-jobs", { params, body }) : client.POST("/v1/tenants/{tenant}/termbase-import-jobs", { params, body });
    return value(read(call, X.ImportJob));
  },
  createKnowledgeExport: (tenant, kind, body, key) => {
    const params = { path: { tenant }, header: { "Idempotency-Key": key } };
    const call =
      kind === "tm" ? client.POST("/v1/tenants/{tenant}/tm-export-jobs", { params, body }) : client.POST("/v1/tenants/{tenant}/termbase-export-jobs", { params, body });
    return value(read(call, X.ExportJob));
  },
};

export const INTEGRATION: InjectionKey<IntegrationPort> = Symbol("integration");

/** The provided port, else the API adapter. */
export function useIntegration(): IntegrationPort {
  return inject(INTEGRATION, apiIntegration);
}
