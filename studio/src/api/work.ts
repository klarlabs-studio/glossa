/**
 * The work port (RFC 0006 §3.1–§3.3): my assignments and what I do with
 * them, the approvals I may decide, the groups that decide who that is,
 * and whether a project's translations run under a workflow at all.
 *
 * `apiWork` implements it through the generated client, every response
 * checked by zod (./work-schemas.ts carries the compile-time contract
 * assertions). Screens inject the port, so component tests hand them an
 * in-memory one (../test/fake-work.ts).
 *
 * Nothing here turns a failure into an empty list: a refused or broken
 * read throws, and each screen says "could not be read" rather than
 * "nothing here".
 */
import { inject, type InjectionKey } from "vue";
import { client } from "./client";
import { all, projects as projectsApi, translations } from "./endpoints";
import { read, type Versioned } from "./errors";
import type { ProjectRef } from "./releases";
import { page, type Project, type ProjectTranslation } from "./schemas";
import { Approval, Assignment, Group, WorkflowResolution, type ApprovalState, type AssignmentState } from "./work-schemas";

const PAGE = 100;
const value = async <T>(p: Promise<Versioned<T>>): Promise<T> => (await p).value;
const opt = (signal?: AbortSignal) => (signal ? { signal } : {});

export interface AssignmentQuery {
  project?: string;
  state?: AssignmentState;
}

export interface ApprovalQuery {
  project?: string;
  state?: ApprovalState;
  subject?: "translation" | "release_request";
}

export interface WorkPort {
  /** The caller's own work, oldest first: given to them, a role they hold, a group they are in or their vendor. */
  myAssignments(tenant: string, query?: AssignmentQuery, signal?: AbortSignal): Promise<Assignment[]>;
  assignment(tenant: string, id: string, signal?: AbortSignal): Promise<Assignment>;
  accept(tenant: string, id: string): Promise<Assignment>;
  /** A claim, not a decision: the workflow decides what follows (§3.1). */
  complete(tenant: string, id: string): Promise<Assignment>;
  decline(tenant: string, id: string, reason?: string): Promise<Assignment>;
  approvals(tenant: string, query?: ApprovalQuery, signal?: AbortSignal): Promise<Approval[]>;
  /** A person's decision. Tokens, authors (four-eyes) and the ineligible are refused by the server. */
  decide(tenant: string, id: string, decision: "granted" | "denied", reason?: string): Promise<Approval>;
  groups(tenant: string, signal?: AbortSignal): Promise<Group[]>;
  /** Which workflow a translation in this locale would run under; `bound: false` is none. */
  resolveWorkflow(p: ProjectRef, locale?: string, signal?: AbortSignal): Promise<WorkflowResolution>;
  /** The projects the caller may see, to name an assignment's project. */
  projects(tenant: string): Promise<Project[]>;
  /**
   * The current translation of each named message in one locale, by
   * message id: the text under approval and who wrote it. A message
   * with no translation is simply absent from the map.
   */
  unitTexts(p: ProjectRef, locale: string, messageIds: ReadonlySet<string>, signal?: AbortSignal): Promise<Map<string, ProjectTranslation>>;
}

export const apiWork: WorkPort = {
  myAssignments: (tenant, q = {}, signal) =>
    all((page_token) =>
      value(
        read(
          client.GET("/v1/tenants/{tenant}/assignments", {
            params: { path: { tenant }, query: { mine: true, page_size: PAGE, page_token, ...q } },
            ...opt(signal),
          }),
          page(Assignment),
        ),
      ),
    ),
  assignment: (tenant, assignment, signal) =>
    value(read(client.GET("/v1/tenants/{tenant}/assignments/{assignment}", { params: { path: { tenant, assignment } }, ...opt(signal) }), Assignment)),
  accept: (tenant, assignment) =>
    value(read(client.POST("/v1/tenants/{tenant}/assignments/{assignment}/acceptance", { params: { path: { tenant, assignment } } }), Assignment)),
  complete: (tenant, assignment) =>
    value(read(client.POST("/v1/tenants/{tenant}/assignments/{assignment}/completion", { params: { path: { tenant, assignment } } }), Assignment)),
  decline: (tenant, assignment, reason) =>
    value(
      read(
        client.POST("/v1/tenants/{tenant}/assignments/{assignment}/decline", {
          params: { path: { tenant, assignment } },
          body: reason ? { reason } : {},
        }),
        Assignment,
      ),
    ),
  approvals: (tenant, q = {}, signal) =>
    all((page_token) =>
      value(
        read(
          client.GET("/v1/tenants/{tenant}/approvals", { params: { path: { tenant }, query: { page_size: PAGE, page_token, ...q } }, ...opt(signal) }),
          page(Approval),
        ),
      ),
    ),
  decide: (tenant, approval, decision, reason) =>
    value(
      read(
        client.POST("/v1/tenants/{tenant}/approvals/{approval}/decisions", {
          params: { path: { tenant, approval } },
          body: reason ? { decision, reason } : { decision },
        }),
        Approval,
      ),
    ),
  groups: (tenant, signal) =>
    all((page_token) =>
      value(read(client.GET("/v1/tenants/{tenant}/groups", { params: { path: { tenant }, query: { page_size: PAGE, page_token } }, ...opt(signal) }), page(Group))),
    ),
  resolveWorkflow: (p, locale, signal) =>
    value(
      read(
        client.GET("/v1/tenants/{tenant}/projects/{project}/workflow-resolution", {
          params: { path: p, query: { subject: "translation", ...(locale ? { locale } : {}) } },
          ...opt(signal),
        }),
        WorkflowResolution,
      ),
    ),
  projects: (tenant) => projectsApi.list(tenant),
  async unitTexts(p, locale, ids, signal) {
    const out = new Map<string, ProjectTranslation>();
    let token: string | undefined;
    do {
      const pg = await translations.projectPage(p, locale, token, signal);
      for (const t of pg.items) if (ids.has(t.message_id)) out.set(t.message_id, t);
      token = pg.next_page_token;
      // Stop as soon as every unit asked about is found.
    } while (token && out.size < ids.size);
    return out;
  },
};

export const WORK: InjectionKey<WorkPort> = Symbol("work");

/** The provided port, else the API adapter. */
export function useWork(): WorkPort {
  return inject(WORK, apiWork);
}
