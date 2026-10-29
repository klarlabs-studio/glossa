/**
 * The workflows port (RFC 0006 §2): definitions as documents with their
 * immutable versions and lint, a project's bindings, and the instances
 * that run under them with their transition logs.
 *
 * `apiWorkflows` implements it through the generated client, every
 * response checked by zod (./workflows-schemas.ts). Screens inject the
 * port, so component tests hand them an in-memory one
 * (../test/fake-workflows.ts).
 *
 * A save the server refuses as `invalid_workflow` throws a
 * `WorkflowRejected`, which carries the findings: the editor shows them
 * inline. The shared `Problem` schema can't carry them — its `findings`
 * are QA findings, a different shape.
 */
import { inject, type InjectionKey } from "vue";
import { client } from "./client";
import { all } from "./endpoints";
import { ApiError, done, failure, read, send, type RawResult, type Versioned } from "./errors";
import type { ProjectRef } from "./releases";
import { page } from "./schemas";
import { WorkflowResolution } from "./work-schemas";
import {
  WorkflowBinding,
  WorkflowDefinition,
  WorkflowDefinitionSaved,
  WorkflowDefinitionVersion,
  WorkflowInstance,
  WorkflowLintResult,
  WorkflowProblem,
  WorkflowTransition,
  type WorkflowDocument,
  type WorkflowFinding,
  type WorkflowInstanceStatus,
} from "./workflows-schemas";

const PAGE = 100;
const value = async <T>(p: Promise<Versioned<T>>): Promise<T> => (await p).value;
const opt = (signal?: AbortSignal) => (signal ? { signal } : {});

/** A save refused because the document doesn't compile or lint; nothing was stored. */
export class WorkflowRejected extends ApiError {
  readonly workflowFindings: WorkflowFinding[];
  constructor(status: number, message: string, findings: WorkflowFinding[]) {
    super(status, "invalid_workflow", message);
    this.name = "WorkflowRejected";
    this.workflowFindings = findings;
  }
}

/** Like `read`, but an `invalid_workflow` refusal keeps its findings. */
async function readSave(request: Promise<RawResult>): Promise<Versioned<WorkflowDefinitionSaved>> {
  const r = await send(request);
  if (!r.response.ok) {
    const p = WorkflowProblem.safeParse(r.error);
    if (p.success && p.data.code === "invalid_workflow") {
      throw new WorkflowRejected(r.response.status, p.data.detail ?? p.data.title, p.data.findings ?? []);
    }
    throw failure(r);
  }
  return read(Promise.resolve(r), WorkflowDefinitionSaved);
}

export interface InstanceQuery {
  definition?: string;
  status?: WorkflowInstanceStatus;
  locale?: string;
  /** A message key. */
  message?: string;
  /** A message's or a release request's id. */
  subject_id?: string;
}

export interface BindingInput {
  definition_id: string;
  /** Empty: every locale. */
  locales?: string[];
  namespace?: string;
}

export interface ResolveQuery {
  subject: "translation" | "release_request";
  locale?: string;
  namespace?: string;
}

export interface WorkflowsPort {
  /** Live definitions at their latest version; with `project`, the ones it may bind (the tenant's and its own). */
  definitions(tenant: string, project?: string, signal?: AbortSignal): Promise<WorkflowDefinition[]>;
  /** One definition with its ETag (the latest version), for `If-Match` on the next save. */
  definition(tenant: string, id: string, signal?: AbortSignal): Promise<Versioned<WorkflowDefinition>>;
  /** Version 1 of a new definition; `project` makes it that project's alone. */
  create(tenant: string, document: WorkflowDocument, project?: string): Promise<Versioned<WorkflowDefinitionSaved>>;
  /** The next version; `etag` is the version the author edited (412 when another save overtook it). */
  save(tenant: string, id: string, document: WorkflowDocument, etag: string): Promise<Versioned<WorkflowDefinitionSaved>>;
  remove(tenant: string, id: string): Promise<void>;
  /** Newest first. */
  versions(tenant: string, id: string, signal?: AbortSignal): Promise<WorkflowDefinitionVersion[]>;
  version(tenant: string, id: string, version: number, signal?: AbortSignal): Promise<WorkflowDefinitionVersion>;
  /** What a save would say, storing nothing. */
  lint(tenant: string, document: WorkflowDocument, signal?: AbortSignal): Promise<WorkflowLintResult>;
  /** In creation order: what "later" means between two equally specific bindings. */
  bindings(p: ProjectRef, signal?: AbortSignal): Promise<WorkflowBinding[]>;
  bind(p: ProjectRef, input: BindingInput): Promise<WorkflowBinding>;
  unbind(p: ProjectRef, id: string): Promise<void>;
  /** Which binding a new instance for this subject would start under; `bound: false` is none (M4's behaviour). */
  resolve(p: ProjectRef, query: ResolveQuery, signal?: AbortSignal): Promise<WorkflowResolution>;
  /** One page of instances, newest first as the server orders them. */
  instances(p: ProjectRef, query?: InstanceQuery, pageToken?: string, signal?: AbortSignal): Promise<{ items: WorkflowInstance[]; next: string | undefined }>;
  instance(p: ProjectRef, id: string, signal?: AbortSignal): Promise<WorkflowInstance>;
  /** Oldest first, every page. */
  transitions(p: ProjectRef, id: string, signal?: AbortSignal): Promise<WorkflowTransition[]>;
}

const DEF = "/v1/tenants/{tenant}/workflow-definitions/{workflow_definition}" as const;
const INSTANCE = "/v1/tenants/{tenant}/projects/{project}/workflow-instances/{workflow_instance}" as const;

export const apiWorkflows: WorkflowsPort = {
  definitions: (tenant, project, signal) =>
    all((page_token) =>
      value(
        read(
          client.GET("/v1/tenants/{tenant}/workflow-definitions", {
            params: { path: { tenant }, query: { page_size: PAGE, page_token, ...(project ? { project } : {}) } },
            ...opt(signal),
          }),
          page(WorkflowDefinition),
        ),
      ),
    ),
  definition: (tenant, workflow_definition, signal) =>
    read(client.GET(DEF, { params: { path: { tenant, workflow_definition } }, ...opt(signal) }), WorkflowDefinition),
  create: (tenant, document, project) =>
    readSave(
      client.POST("/v1/tenants/{tenant}/workflow-definitions", {
        params: { path: { tenant }, query: project ? { project } : {} },
        body: document,
      }),
    ),
  save: (tenant, workflow_definition, document, etag) =>
    readSave(
      client.POST(`${DEF}/versions`, {
        params: { path: { tenant, workflow_definition }, header: { "If-Match": etag } },
        body: document,
      }),
    ),
  remove: (tenant, workflow_definition) => done(client.DELETE(DEF, { params: { path: { tenant, workflow_definition } } })),
  versions: (tenant, workflow_definition, signal) =>
    all((page_token) =>
      value(
        read(
          client.GET(`${DEF}/versions`, { params: { path: { tenant, workflow_definition }, query: { page_size: PAGE, page_token } }, ...opt(signal) }),
          page(WorkflowDefinitionVersion),
        ),
      ),
    ),
  version: (tenant, workflow_definition, version, signal) =>
    value(read(client.GET(`${DEF}/versions/{version}`, { params: { path: { tenant, workflow_definition, version } }, ...opt(signal) }), WorkflowDefinitionVersion)),
  lint: (tenant, document, signal) =>
    value(read(client.POST("/v1/tenants/{tenant}/workflow-definition-lints", { params: { path: { tenant } }, body: document, ...opt(signal) }), WorkflowLintResult)),
  bindings: (p, signal) =>
    all((page_token) =>
      value(
        read(
          client.GET("/v1/tenants/{tenant}/projects/{project}/workflow-bindings", { params: { path: p, query: { page_size: PAGE, page_token } }, ...opt(signal) }),
          page(WorkflowBinding),
        ),
      ),
    ),
  bind: (p, input) =>
    value(
      read(
        client.POST("/v1/tenants/{tenant}/projects/{project}/workflow-bindings", {
          params: { path: p },
          body: {
            definition_id: input.definition_id,
            ...(input.locales?.length ? { locales: input.locales } : {}),
            ...(input.namespace ? { namespace: input.namespace } : {}),
          },
        }),
        WorkflowBinding,
      ),
    ),
  unbind: (p, workflow_binding) =>
    done(client.DELETE("/v1/tenants/{tenant}/projects/{project}/workflow-bindings/{workflow_binding}", { params: { path: { ...p, workflow_binding } } })),
  resolve: (p, q, signal) =>
    value(
      read(
        client.GET("/v1/tenants/{tenant}/projects/{project}/workflow-resolution", {
          params: { path: p, query: { subject: q.subject, ...(q.locale ? { locale: q.locale } : {}), ...(q.namespace ? { namespace: q.namespace } : {}) } },
          ...opt(signal),
        }),
        WorkflowResolution,
      ),
    ),
  async instances(p, q = {}, page_token, signal) {
    const pg = await value(
      read(
        client.GET("/v1/tenants/{tenant}/projects/{project}/workflow-instances", {
          params: { path: p, query: { page_size: 50, page_token, ...q } },
          ...opt(signal),
        }),
        page(WorkflowInstance),
      ),
    );
    return { items: pg.items, next: pg.next_page_token };
  },
  instance: (p, workflow_instance, signal) => value(read(client.GET(INSTANCE, { params: { path: { ...p, workflow_instance } }, ...opt(signal) }), WorkflowInstance)),
  transitions: (p, workflow_instance, signal) =>
    all((page_token) =>
      value(
        read(
          client.GET(`${INSTANCE}/transitions`, { params: { path: { ...p, workflow_instance }, query: { page_size: PAGE, page_token } }, ...opt(signal) }),
          page(WorkflowTransition),
        ),
      ),
    ),
};

export const WORKFLOWS: InjectionKey<WorkflowsPort> = Symbol("workflows");

/** The provided port, else the API adapter. */
export function useWorkflows(): WorkflowsPort {
  return inject(WORKFLOWS, apiWorkflows);
}
