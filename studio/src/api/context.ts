/**
 * The Context port (RFC 0004 §2–§3): where a message appears — its
 * usages in the code, the screenshots that show it — and which active
 * messages no current build uses. `apiContext` implements it over the
 * generated /v1 client with every response checked by zod; component
 * tests provide an in-memory fake (src/test/fake-context.ts).
 *
 * `useContextPort()` falls back to `apiContext` when nothing is
 * provided, so the port (and its schemas) load with the panes that use
 * it, not with the app shell.
 */
import { inject, type InjectionKey } from "vue";
import { client } from "./client";
import * as C from "./context-schemas";
import { read, type Versioned } from "./errors";

/** A message's usages or captures: the default branch's, or a branch view's. */
export interface ContextQuery {
  /** A branch view: that branch's latest builds, the default branch's where it didn't rebuild. */
  branch?: string | undefined;
  limit?: number | undefined;
}

/** The current usages on a route, in a component or in a file; filters combine. */
export interface UsageFilter {
  branch?: string | undefined;
  route?: string | undefined;
  component?: string | undefined;
  file?: string | undefined;
}

export interface ContextPort {
  /** Where the message the key names now appears in the code. */
  messageUsages(tenant: string, project: string, key: string, q?: ContextQuery, signal?: AbortSignal): Promise<C.MessageUsages>;
  /** The current captures that show the message, each with its regions and its image's API path. */
  messageCaptures(tenant: string, project: string, key: string, q?: ContextQuery, signal?: AbortSignal): Promise<C.MessageCaptures>;
  /** Every current usage matching the filter (all pages, at most `max`). */
  usages(tenant: string, project: string, f?: UsageFilter, max?: number, signal?: AbortSignal): Promise<C.ContextUsage[]>;
  /** Active messages no current build uses, with the project's context coverage. */
  unusedMessages(tenant: string, project: string, branch?: string, signal?: AbortSignal): Promise<C.UnusedMessageList>;
  /**
   * The project's context coverage without the list: how many builds are
   * current (0 means nothing was uploaded yet), how many messages are
   * active and how many of them are unused.
   */
  coverage(tenant: string, project: string, branch?: string, signal?: AbortSignal): Promise<ContextCoverage>;
}

export interface ContextCoverage {
  current_builds: number;
  active_messages: number;
  unused_messages: number;
}

const value = async <T>(p: Promise<Versioned<T>>): Promise<T> => (await p).value;
const withSignal = (signal?: AbortSignal) => (signal ? { signal } : {});
const PAGE = 100;
/** A message's usages and captures: enough for the pane, which groups them. */
export const DEFAULT_LIMIT = 100;
/** How many current usages one filter reads at most (50 pages). */
export const MAX_USAGES = 5000;

const compact = <T extends object>(o: T): T => Object.fromEntries(Object.entries(o).filter(([, v]) => v !== undefined && v !== "")) as T;

export const apiContext: ContextPort = {
  messageUsages: (tenant, project, message, q = {}, signal) =>
    value(
      read(
        client.GET("/v1/tenants/{tenant}/projects/{project}/messages/{message}/usages", {
          params: { path: { tenant, project, message }, query: compact({ branch: q.branch, limit: q.limit ?? DEFAULT_LIMIT }) },
          ...withSignal(signal),
        }),
        C.MessageUsages,
      ),
    ),

  messageCaptures: (tenant, project, message, q = {}, signal) =>
    value(
      read(
        client.GET("/v1/tenants/{tenant}/projects/{project}/messages/{message}/captures", {
          params: { path: { tenant, project, message }, query: compact({ branch: q.branch, limit: q.limit ?? DEFAULT_LIMIT }) },
          ...withSignal(signal),
        }),
        C.MessageCaptures,
      ),
    ),

  usages: async (tenant, project, f = {}, max = MAX_USAGES, signal) => {
    const out: C.ContextUsage[] = [];
    let page_token: string | undefined;
    do {
      const got = await value(
        read(
          client.GET("/v1/tenants/{tenant}/projects/{project}/usages", {
            params: { path: { tenant, project }, query: compact({ ...f, page_size: PAGE, page_token }) },
            ...withSignal(signal),
          }),
          C.ContextUsageList,
        ),
      );
      out.push(...got.items);
      page_token = got.next_page_token;
    } while (page_token && out.length < max);
    return out;
  },

  unusedMessages: async (tenant, project, branch, signal) => {
    let out: C.UnusedMessageList | undefined;
    let page_token: string | undefined;
    do {
      const got = await value(
        read(
          client.GET("/v1/tenants/{tenant}/projects/{project}/unused-messages", {
            params: { path: { tenant, project }, query: compact({ branch, page_size: PAGE, page_token }) },
            ...withSignal(signal),
          }),
          C.UnusedMessageList,
        ),
      );
      // The counts are the project's, the same on every page.
      out = out ? { ...got, items: [...out.items, ...got.items] } : got;
      page_token = got.next_page_token;
    } while (page_token);
    return out ?? { items: [], current_builds: 0, active_messages: 0, unused_messages: 0 };
  },

  coverage: async (tenant, project, branch, signal) => {
    // One row is enough: the counts are the project's, not the page's.
    const got = await value(
      read(
        client.GET("/v1/tenants/{tenant}/projects/{project}/unused-messages", {
          params: { path: { tenant, project }, query: compact({ branch, page_size: 1 }) },
          ...withSignal(signal),
        }),
        C.UnusedMessageList,
      ),
    );
    return { current_builds: got.current_builds, active_messages: got.active_messages, unused_messages: got.unused_messages };
  },
};

export const CONTEXT: InjectionKey<ContextPort> = Symbol("context");

/** The provided port, else the API adapter. */
export function useContextPort(): ContextPort {
  return inject(CONTEXT, apiContext);
}
