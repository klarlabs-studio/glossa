/**
 * The check-policy port (RFC 0005 §4, §9): read what the project grades
 * by, ask what a candidate would change, and save a new version.
 *
 * The one thing this port exists to make easy: **a save and a preview
 * are the same request.** `save(..., { dryRun: true })` answers the
 * impact and stores nothing, which is why the editor can always show
 * the number before the button rather than after it.
 *
 * `apiCheckPolicy` implements it over the generated /v1 client with
 * every response checked by zod; component tests provide an in-memory
 * fake (src/test/fake-policy.ts).
 */
import { inject, type InjectionKey } from "vue";
import { client } from "./client";
import { read, type Versioned } from "./errors";
import * as P from "./policy-schemas";
import type { Page, ProjectRef } from "./releases";

export const POLICY_PATH = "/v1/tenants/{tenant}/projects/{project}/check-policy" as const;
const VERSIONS = "/v1/tenants/{tenant}/projects/{project}/check-policy/versions" as const;

/** How many versions the history reads at most. */
export const MAX_VERSIONS = 100;
const PAGE = 50;

export interface SaveOptions {
  /**
   * How long the save pins the pull requests that predate it to the
   * version they were opened under. Absent, the server's 14 days; `0`
   * pins nothing, which is what a policy that only loosens wants.
   */
  graceDays?: number | undefined;
  /** Answer the impact preview and store nothing. */
  dryRun?: boolean | undefined;
}

export interface CheckPolicyPort {
  /** The policy that grades now, and the record of who put it there. */
  policy(p: ProjectRef, signal?: AbortSignal): Promise<P.PolicyState>;
  /** Save a new version — or, with `dryRun`, answer what saving would change and store nothing. */
  save(p: ProjectRef, document: P.PolicyDocument, options?: SaveOptions): Promise<P.PolicySaved>;
  /** How the policy got to be what it is, newest first. */
  versions(p: ProjectRef, max?: number, signal?: AbortSignal): Promise<Page<P.PolicyVersion>>;
}

const value = async <T>(p: Promise<Versioned<T>>): Promise<T> => (await p).value;
const withSignal = (signal?: AbortSignal) => (signal ? { signal } : {});

export const apiCheckPolicy: CheckPolicyPort = {
  policy: (p, signal) => value(read(client.GET(POLICY_PATH, { params: { path: p }, ...withSignal(signal) }), P.PolicyState)),

  save: (p, document, options = {}) => {
    const body: P.SavePolicy = { policy: document };
    if (options.graceDays !== undefined) body.grace_days = options.graceDays;
    if (options.dryRun) body.dry_run = true;
    return value(read(client.POST(POLICY_PATH, { params: { path: p }, body }), P.PolicySaved));
  },

  versions: async (p, max = MAX_VERSIONS, signal) => {
    const items: P.PolicyVersion[] = [];
    let page_token: string | undefined;
    do {
      const got = await value(
        read(
          client.GET(VERSIONS, {
            params: { path: p, query: page_token ? { page_size: PAGE, page_token } : { page_size: PAGE } },
            ...withSignal(signal),
          }),
          P.PolicyVersionList,
        ),
      );
      items.push(...got.items);
      page_token = got.next_page_token;
    } while (page_token && items.length < max);
    return { items: items.slice(0, max), next: page_token };
  },
};

export const CHECK_POLICY: InjectionKey<CheckPolicyPort> = Symbol("check-policy");

/** The provided port, else the API adapter. */
export function useCheckPolicy(): CheckPolicyPort {
  return inject(CHECK_POLICY, apiCheckPolicy);
}
