/**
 * The Quality port (RFC 0005 §2, §9): a check run's findings with
 * today's waivers applied, the waivers themselves, and accepting or
 * taking back a waiver. `apiQuality` implements it over the generated
 * /v1 client with every response checked by zod; component tests
 * provide an in-memory fake (src/test/fake-quality.ts).
 *
 * `useQuality()` falls back to `apiQuality` when nothing is provided, so
 * the port (and its schemas) load with the quality screen, not with the
 * app shell.
 */
import { inject, type InjectionKey } from "vue";
import { client } from "./client";
import { done, read, type Versioned } from "./errors";
import * as Q from "./quality-schemas";
import type { Page, ProjectRef } from "./releases";

/**
 * Which run to read and what to select in it. A finding belongs to a
 * run, so a list reads exactly one: `run`, else the newest run of
 * `branch` or `commit`, else the project's newest.
 */
export interface FindingFilter {
  run?: string | undefined;
  branch?: string | undefined;
  commit?: string | undefined;
  layer?: Q.FindingLayer | undefined;
  /** `waived` selects the accepted findings. */
  severity?: Q.FindingSeverity | undefined;
  code?: string | undefined;
  locale?: string | undefined;
  namespace?: string | undefined;
  message?: string | undefined;
  /** Only the waived, or only those no waiver accepts. Absent: both. */
  waived?: boolean | undefined;
}

export interface WaiverFilter {
  fingerprint?: string | undefined;
  layer?: Q.FindingLayer | undefined;
  code?: string | undefined;
  message?: string | undefined;
  /** Only the waivers that stand now, or only the revoked and expired. Absent: both. */
  active?: boolean | undefined;
}

/** What a page of findings carried, with everything loaded so far. */
export interface Findings {
  items: Q.Finding[];
  /** The run they came from; absent when nothing has been checked yet. */
  run: Q.CheckRun | undefined;
  /** The whole run as it stands now, whatever the filter selected. */
  counts: Q.CheckRunCounts;
  /** More findings exist than `max`. */
  truncated: boolean;
}

export interface QualityPort {
  /** Every finding matching the filter (all pages, at most `max`), with the run and its counts. */
  findings(p: ProjectRef, f?: FindingFilter, max?: number, signal?: AbortSignal): Promise<Findings>;
  /** The project's check runs, newest first. */
  checkRuns(p: ProjectRef, pageToken?: string, signal?: AbortSignal): Promise<Page<Q.CheckRun>>;
  /** Every waiver matching the filter (all pages, at most `max`), newest first. */
  waivers(p: ProjectRef, f?: WaiverFilter, max?: number, signal?: AbortSignal): Promise<Q.Waiver[]>;
  /** Accept a finding. The reason is required and non-empty; a blank one is refused. */
  createWaiver(p: ProjectRef, input: Q.CreateWaiver): Promise<Q.Waiver>;
  /** Take a waiver back: what it accepted is an ordinary finding again. Nothing is deleted. */
  revokeWaiver(p: ProjectRef, waiver: string): Promise<void>;
}

const value = async <T>(p: Promise<Versioned<T>>): Promise<T> => (await p).value;
const withSignal = (signal?: AbortSignal) => (signal ? { signal } : {});
const PAGE = 100;
/** How many findings one view reads at most (a run holds up to 10,000). */
export const MAX_FINDINGS = 1000;
/** How many waivers one view reads at most. */
export const MAX_WAIVERS = 500;

const compact = <T extends object>(o: T): T =>
  Object.fromEntries(Object.entries(o).filter(([, v]) => v !== undefined && v !== "")) as T;

const FINDINGS = "/v1/tenants/{tenant}/projects/{project}/findings" as const;
const WAIVERS = "/v1/tenants/{tenant}/projects/{project}/waivers" as const;

export const apiQuality: QualityPort = {
  findings: async (p, f = {}, max = MAX_FINDINGS, signal) => {
    const items: Q.Finding[] = [];
    let run: Q.CheckRun | undefined;
    let counts: Q.CheckRunCounts = { errors: 0, warnings: 0, waived: 0 };
    let page_token: string | undefined;
    do {
      const got = await value(
        read(
          client.GET(FINDINGS, { params: { path: p, query: compact({ ...f, page_size: PAGE, page_token }) }, ...withSignal(signal) }),
          Q.FindingList,
        ),
      );
      // The run and its counts are the whole run's, the same on every page.
      run = got.run;
      counts = got.counts;
      items.push(...got.items);
      page_token = got.next_page_token;
    } while (page_token && items.length < max);
    return { items: items.slice(0, max), run, counts, truncated: items.length > max || (!!page_token && items.length >= max) };
  },

  checkRuns: async (p, page_token, signal) => {
    const got = await value(
      read(
        client.GET("/v1/tenants/{tenant}/projects/{project}/check-runs", {
          params: { path: p, query: compact({ page_size: PAGE, page_token }) },
          ...withSignal(signal),
        }),
        Q.CheckRunList,
      ),
    );
    return { items: got.items, next: got.next_page_token };
  },

  waivers: async (p, f = {}, max = MAX_WAIVERS, signal) => {
    const out: Q.Waiver[] = [];
    let page_token: string | undefined;
    do {
      const got = await value(
        read(
          client.GET(WAIVERS, { params: { path: p, query: compact({ ...f, page_size: PAGE, page_token }) }, ...withSignal(signal) }),
          Q.WaiverList,
        ),
      );
      out.push(...got.items);
      page_token = got.next_page_token;
    } while (page_token && out.length < max);
    return out.slice(0, max);
  },

  createWaiver: (p, input) => value(read(client.POST(WAIVERS, { params: { path: p }, body: input }), Q.Waiver)),

  revokeWaiver: (p, waiver) =>
    done(client.DELETE("/v1/tenants/{tenant}/projects/{project}/waivers/{waiver}", { params: { path: { ...p, waiver } } })),
};

export const QUALITY: InjectionKey<QualityPort> = Symbol("quality");

/** The provided port, else the API adapter. */
export function useQuality(): QualityPort {
  return inject(QUALITY, apiQuality);
}
