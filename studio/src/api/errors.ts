/**
 * Every failed call becomes an ApiError carrying the problem's stable
 * `code` (platform/README.md: branch on codes, never on titles), the
 * per-field errors and, for `structural_qa_failed`, the QA findings.
 */
import type { z } from "zod";
import { Problem, type QAFinding } from "./schemas";

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly problem: Problem | undefined;

  constructor(status: number, code: string, message: string, problem?: Problem) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.problem = problem;
  }

  get findings(): QAFinding[] {
    return this.problem?.findings ?? [];
  }

  get fieldErrors(): Array<{ pointer: string; detail: string }> {
    return this.problem?.errors ?? [];
  }
}

export const isApiError = (e: unknown, code?: string): e is ApiError =>
  e instanceof ApiError && (code === undefined || e.code === code);

/** The shape openapi-fetch resolves with. */
export interface RawResult {
  data?: unknown;
  error?: unknown;
  response: Response;
}

export interface Versioned<T> {
  value: T;
  /** The resource's ETag, for `If-Match` on the next change. */
  etag: string | undefined;
}

/** Awaits a request; a transport failure becomes `network_error`. */
export async function send(request: Promise<RawResult>): Promise<RawResult> {
  try {
    return await request;
  } catch (e) {
    if (e instanceof DOMException && e.name === "AbortError") throw e;
    throw new ApiError(0, "network_error", "The server could not be reached.");
  }
}

export function failure(r: RawResult): ApiError {
  const parsed = Problem.safeParse(r.error);
  if (parsed.success) {
    const p = parsed.data;
    return new ApiError(r.response.status, p.code, p.detail ?? p.title, p);
  }
  return new ApiError(r.response.status, "unexpected_response", `Unexpected response (HTTP ${r.response.status}).`);
}

/** Resolve a request to a validated body and its ETag. */
export async function read<S extends z.ZodType>(request: Promise<RawResult>, schema: S): Promise<Versioned<z.infer<S>>> {
  const r = await send(request);
  if (!r.response.ok) throw failure(r);
  const parsed = schema.safeParse(r.data);
  if (!parsed.success) {
    throw new ApiError(r.response.status, "invalid_response", `The server's response didn't match the contract: ${parsed.error.message}`);
  }
  return { value: parsed.data, etag: r.response.headers.get("ETag") ?? undefined };
}

/** Resolve a request that returns no body. */
export async function done(request: Promise<RawResult>): Promise<void> {
  const r = await send(request);
  if (!r.response.ok) throw failure(r);
}
