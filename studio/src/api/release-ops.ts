/**
 * The release-operations port (RFC 0006 §5): an environment's approval
 * requirement, the release requests it creates and the decisions on
 * them, and staged rollouts.
 *
 * `apiReleaseOps` implements it through the generated client; component
 * tests hand screens an in-memory one (../test/fake-release-ops.ts).
 *
 * A release request's approvals — who granted or denied, and when — are
 * not on the request: they are Workflow's Approval for that subject,
 * found by listing the project's release-request approvals and picking
 * the newest for this request (the API has no `subject_id` filter).
 */
import { inject, type InjectionKey } from "vue";
import { client } from "./client";
import { all } from "./endpoints";
import { read, type Versioned } from "./errors";
import type { ProjectRef } from "./releases";
import { Environment, page, type EnvironmentApproval, type EnvironmentPolicy } from "./schemas";
import { Approval } from "./work-schemas";
import { ReleaseRequest, Rollout, type ReleaseRequestState } from "./release-ops-schemas";

const PAGE = 100;
const value = async <T>(p: Promise<Versioned<T>>): Promise<T> => (await p).value;
const opt = (signal?: AbortSignal) => (signal ? { signal } : {});

export interface ReleaseRequestQuery {
  environment?: string;
  state?: ReleaseRequestState;
}

/** An environment's settings, changed together under one `If-Match`. */
export interface EnvironmentSettings {
  policy: EnvironmentPolicy;
  /** Set or change the requirement. */
  approval?: EnvironmentApproval;
  /** Switch the requirement off (the API never sends `null`). */
  clear_approval?: boolean;
}

export interface StartRolloutInput {
  release_id: string;
  percent: number;
  /** Omitted: 14 days. */
  max_duration_seconds?: number;
  force?: boolean;
  force_reason?: string;
}

export interface ReleaseOpsPort {
  /** Newest first. */
  releaseRequests(p: ProjectRef, query?: ReleaseRequestQuery, signal?: AbortSignal): Promise<ReleaseRequest[]>;
  releaseRequest(p: ProjectRef, id: string, signal?: AbortSignal): Promise<ReleaseRequest>;
  /** The newest approval Workflow holds for this request; undefined before the workflow has asked. */
  requestApproval(p: ProjectRef, id: string, signal?: AbortSignal): Promise<Approval | undefined>;
  /** A person's decision; the deploy is the workflow's, as the last approver. */
  decide(p: ProjectRef, id: string, decision: "granted" | "denied", reason?: string): Promise<Approval>;
  withdraw(p: ProjectRef, id: string, reason?: string): Promise<ReleaseRequest>;
  /** Policy and approval requirement together; changing who approves also needs `workflows.manage`. */
  configureEnvironment(p: ProjectRef, environment: string, settings: EnvironmentSettings, etag: string): Promise<Versioned<Environment>>;
  /** Newest first: the active one, if any, then the ended ones. */
  rollouts(p: ProjectRef, environment: string, signal?: AbortSignal): Promise<Rollout[]>;
  /** With its ETag, for `If-Match` on an advance. */
  rollout(p: ProjectRef, environment: string, id: string, signal?: AbortSignal): Promise<Versioned<Rollout>>;
  startRollout(p: ProjectRef, environment: string, input: StartRolloutInput, idempotencyKey: string): Promise<Versioned<Rollout>>;
  /** Any percent may follow any other; `If-Match` is required. */
  advance(p: ProjectRef, environment: string, id: string, percent: number, etag: string): Promise<Versioned<Rollout>>;
  complete(p: ProjectRef, environment: string, id: string, etag?: string): Promise<Versioned<Rollout>>;
  /** Never slowed by a stale tag: send one only when you have it. */
  abort(p: ProjectRef, environment: string, id: string, etag?: string): Promise<Versioned<Rollout>>;
}

const REQ = "/v1/tenants/{tenant}/projects/{project}/release-requests/{release_request}" as const;
const ENV = "/v1/tenants/{tenant}/projects/{project}/environments/{environment}" as const;
const ROLLOUT = "/v1/tenants/{tenant}/projects/{project}/environments/{environment}/rollouts/{rollout}" as const;
const ifMatch = (etag?: string) => (etag ? { "If-Match": etag } : {});

export const apiReleaseOps: ReleaseOpsPort = {
  releaseRequests: (p, q = {}, signal) =>
    all((page_token) =>
      value(
        read(
          client.GET("/v1/tenants/{tenant}/projects/{project}/release-requests", { params: { path: p, query: { page_size: PAGE, page_token, ...q } }, ...opt(signal) }),
          page(ReleaseRequest),
        ),
      ),
    ),
  releaseRequest: (p, release_request, signal) => value(read(client.GET(REQ, { params: { path: { ...p, release_request } }, ...opt(signal) }), ReleaseRequest)),
  async requestApproval(p, id, signal) {
    const list = await all((page_token) =>
      value(
        read(
          client.GET("/v1/tenants/{tenant}/approvals", {
            params: { path: { tenant: p.tenant }, query: { project: p.project, subject: "release_request", page_size: PAGE, page_token } },
            ...opt(signal),
          }),
          page(Approval),
        ),
      ),
    );
    // Oldest first: the last one for this request is its current approval.
    return list.filter((a) => a.subject_id === id).at(-1);
  },
  decide: (p, release_request, decision, reason) =>
    value(read(client.POST(`${REQ}/approvals`, { params: { path: { ...p, release_request } }, body: reason ? { decision, reason } : { decision } }), Approval)),
  withdraw: (p, release_request, reason) =>
    value(read(client.POST(`${REQ}/withdrawal`, { params: { path: { ...p, release_request } }, body: reason ? { reason } : {} }), ReleaseRequest)),
  configureEnvironment: (p, environment, settings, etag) =>
    read(
      client.PATCH(ENV, {
        params: { path: { ...p, environment }, header: { "If-Match": etag } },
        body: {
          policy: settings.policy,
          ...(settings.approval ? { approval: settings.approval } : {}),
          ...(settings.clear_approval ? { clear_approval: true } : {}),
        },
      }),
      Environment,
    ),
  rollouts: (p, environment, signal) =>
    all((page_token) =>
      value(read(client.GET(`${ENV}/rollouts`, { params: { path: { ...p, environment }, query: { page_size: PAGE, page_token } }, ...opt(signal) }), page(Rollout))),
    ),
  rollout: (p, environment, rollout, signal) => read(client.GET(ROLLOUT, { params: { path: { ...p, environment, rollout } }, ...opt(signal) }), Rollout),
  startRollout: (p, environment, input, key) =>
    read(client.POST(`${ENV}/rollouts`, { params: { path: { ...p, environment }, header: { "Idempotency-Key": key } }, body: input }), Rollout),
  advance: (p, environment, rollout, percent, etag) =>
    read(client.PATCH(ROLLOUT, { params: { path: { ...p, environment, rollout }, header: { "If-Match": etag } }, body: { percent } }), Rollout),
  complete: (p, environment, rollout, etag) =>
    read(client.POST(`${ROLLOUT}/completion`, { params: { path: { ...p, environment, rollout }, header: ifMatch(etag) } }), Rollout),
  abort: (p, environment, rollout, etag) =>
    read(client.POST(`${ROLLOUT}/abort`, { params: { path: { ...p, environment, rollout }, header: ifMatch(etag) } }), Rollout),
};

export const RELEASE_OPS: InjectionKey<ReleaseOpsPort> = Symbol("release-ops");

export function useReleaseOps(): ReleaseOpsPort {
  return inject(RELEASE_OPS, apiReleaseOps);
}
