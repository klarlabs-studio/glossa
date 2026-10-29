/**
 * The quality summary port (RFC 0005 §8): the seven numbers for one
 * project, per project and per locale, from one cached endpoint.
 *
 * `apiQualitySummary` implements it through the generated client with
 * the response checked by zod, as every other port here does. It went in
 * with a hand-rolled fetch while `getQualitySummary` was still a sibling
 * slice's unmerged work; the operation has since landed, so the request
 * is the generated one and ./quality-summary-schemas.ts carries the
 * compile-time contract assertion that keeps the zod shapes and the
 * spec's from drifting.
 *
 * A project whose server does not answer this yet is *not* a healthy
 * project: `unavailable()` says so, and every screen renders it as "not
 * measured" rather than as zero.
 */
import { inject, type InjectionKey } from "vue";
import { client } from "./client";
import { isApiError, read, type Versioned } from "./errors";
import { QualitySummary } from "./quality-summary-schemas";
import type { ProjectRef } from "./releases";

export const SUMMARY_PATH = "/v1/tenants/{tenant}/projects/{project}/quality-summary" as const;

export interface QualitySummaryPort {
  /** The seven numbers for this project, per project and per locale. */
  summary(p: ProjectRef, signal?: AbortSignal): Promise<QualitySummary>;
}

const value = async <T>(p: Promise<Versioned<T>>): Promise<T> => (await p).value;

export const apiQualitySummary: QualitySummaryPort = {
  summary: (p, signal) =>
    value(read(client.GET(SUMMARY_PATH, { params: { path: p }, ...(signal ? { signal } : {}) }), QualitySummary)),
};

export const QUALITY_SUMMARY: InjectionKey<QualitySummaryPort> = Symbol("quality-summary");

/** The provided port, else the API adapter. */
export function useQualitySummary(): QualitySummaryPort {
  return inject(QUALITY_SUMMARY, apiQualitySummary);
}

/**
 * Whether a failed summary read means "this server does not report the
 * numbers" rather than "something went wrong". A 404 or a 501 is the
 * former — the endpoint is not deployed here — and the screens answer it
 * with "not measured", quietly. Anything else is a real failure and is
 * shown.
 */
export function notReported(e: unknown): boolean {
  if (!isApiError(e)) return false;
  return e.status === 404 || e.status === 501 || e.code === "not_found" || e.code === "unexpected_response";
}
