/**
 * An in-memory ReleaseOpsPort for component tests, with the API's rules in
 * miniature (RFC 0006 §5), over a releases fake's environments, releases
 * and release requests so a publish held there is a request here:
 *
 * - deciding: the requester can't (`own_text`, 403); before the workflow
 *   asked (`asked` lacks the request) it is `approval_not_requested`; a
 *   closed request is `release_request_closed` (409). Enough distinct
 *   grants deploy it through the releases fake; a denial closes it.
 * - environments: settings change under their ETag (412), `approval` and
 *   `clear_approval` together are refused, and an approval that isn't
 *   n 1–10 of exactly one party with `distinct_from_requester` is
 *   `invalid_approval` (422); a branch environment is `approval_on_branch`.
 * - rollouts: one active per environment (`rollout_active`), none where
 *   approval is required (`rollout_needs_approval`) or nothing is served
 *   (`rollout_no_stable`); advancing needs the current ETag (412),
 *   completing and aborting take it optionally; an ended rollout is
 *   `rollout_ended`.
 *
 * `fail` makes a whole method fail; `hold` keeps its answer pending.
 * Test-only: nothing in the app imports it.
 */
import { ApiError, type Versioned } from "../api/errors";
import type { ReleaseOpsPort } from "../api/release-ops";
import type { ReleaseRequest, Rollout } from "../api/release-ops-schemas";
import type { Environment } from "../api/schemas";
import type { Approval } from "../api/work-schemas";
import type { FakeReleases } from "./fake-releases";

type Method = keyof ReleaseOpsPort;

export interface FakeReleaseOps extends ReleaseOpsPort {
  readonly calls: Array<[Method, ...unknown[]]>;
  readonly state: {
    /** Rollouts by environment, newest first, each with its version (the ETag). */
    rollouts: Map<string, Array<{ rollout: Rollout; etag: number }>>;
    /** The approval Workflow holds per request id; absent: not asked yet. */
    approvals: Map<string, Approval>;
    /** The person deciding, as the server records them. */
    self: string;
  };
  fail: Partial<Record<Method, ApiError>>;
  hold: Set<Method>;
  /** Make the workflow ask for approval on a request (what its instance does a moment after the 202). */
  ask(requestId: string): Approval;
  /** Add a release request as the releases fake would hold it. */
  addRequest(r: ReleaseRequest): void;
}

const NOW = "2026-09-20T08:00:00Z";
const never = <T>() => new Promise<T>(() => undefined);

export function request(over: Partial<ReleaseRequest> = {}): ReleaseRequest {
  return {
    id: "rr1",
    environment: "production",
    release_id: "rel_1",
    action: "promote",
    requester: "person:dev",
    approval: { n: 2, from: { role: "reviewer" }, distinct_from_requester: true },
    gate: { met: true },
    forced: false,
    state: "pending",
    created_at: NOW,
    ...over,
  };
}

export function rollout(over: Partial<Rollout> = {}): Rollout {
  return {
    id: "ro1",
    environment: "production",
    release_id: "rel_2",
    stable_release_id: "rel_1",
    percent: 10,
    status: "active",
    max_duration_seconds: 14 * 86_400,
    expires_at: "2026-10-04T08:00:00Z",
    forced: false,
    started_by: "person:dev",
    started_at: NOW,
    updated_at: NOW,
    ...over,
  };
}

export function createFakeReleaseOps(releases: FakeReleases, init: Partial<FakeReleaseOps["state"]> = {}): FakeReleaseOps {
  const state: FakeReleaseOps["state"] = { rollouts: new Map(), approvals: new Map(), self: "person:me", ...init };
  const calls: FakeReleaseOps["calls"] = [];
  const fail: FakeReleaseOps["fail"] = {};
  const hold = new Set<Method>();
  let seq = 0;

  async function gate(m: Method, ...args: unknown[]): Promise<void> {
    calls.push([m, ...args]);
    if (hold.has(m)) await never();
    const f = fail[m];
    if (f) throw f;
  }
  const requests = () => releases.state.requests;
  const findRequest = (id: string) => {
    const r = requests().find((x) => x.id === id);
    if (!r) throw new ApiError(404, "not_found", "No such resource.");
    return r;
  };
  const envOf = (name: string) => {
    const e = releases.state.envs.get(name);
    if (!e) throw new ApiError(404, "not_found", "No such environment.");
    return e;
  };
  const listOf = (env: string) => state.rollouts.get(env) ?? [];
  const findRollout = (env: string, id: string) => {
    const r = listOf(env).find((x) => x.rollout.id === id);
    if (!r) throw new ApiError(404, "not_found", "No such resource.");
    return r;
  };
  const versioned = (r: { rollout: Rollout; etag: number }): Versioned<Rollout> => ({ value: structuredClone(r.rollout), etag: `"${r.etag}"` });
  const end = (env: string, id: string, etag: string | undefined, how: "completed" | "aborted") => {
    const r = findRollout(env, id);
    if (etag !== undefined && etag !== `"${r.etag}"`) throw new ApiError(412, "precondition_failed", "Changed meanwhile.");
    if (r.rollout.status !== "active") throw new ApiError(409, "rollout_ended", "The rollout has ended.");
    if (how === "completed") releases.point(env, r.rollout.release_id, "promote");
    r.rollout = { ...r.rollout, status: how, end: how, ended_by: state.self, ended_at: NOW, updated_at: NOW };
    r.etag++;
    return versioned(r);
  };

  const fake: FakeReleaseOps = {
    calls,
    state,
    fail,
    hold,
    ask(id) {
      const r = findRequest(id);
      const a: Approval = {
        id: `ap_${id}`,
        project_id: "p",
        subject: "release_request",
        subject_id: id,
        required: r.approval.n,
        eligible: r.approval.from.role ? { kind: "role", role: r.approval.from.role } : r.approval.from.group ? { kind: "group", id: r.approval.from.group } : { kind: "member", id: r.approval.from.member ?? "" },
        distinct_from_author: true,
        state: "pending",
        decisions: [],
        created_by: "system:workflow",
        created_at: NOW,
      };
      state.approvals.set(id, a);
      return a;
    },
    addRequest(r) {
      requests().unshift(r);
    },
    async releaseRequests(p, q = {}) {
      await gate("releaseRequests", p, q);
      return structuredClone(requests().filter((r) => (!q.environment || r.environment === q.environment) && (!q.state || r.state === q.state)));
    },
    async releaseRequest(p, id) {
      await gate("releaseRequest", p, id);
      return structuredClone(findRequest(id));
    },
    async requestApproval(p, id) {
      await gate("requestApproval", p, id);
      const a = state.approvals.get(id);
      return a ? structuredClone(a) : undefined;
    },
    async decide(p, id, decision, reason) {
      await gate("decide", p, id, decision, reason);
      const r = findRequest(id);
      if (r.state !== "pending") throw new ApiError(409, "release_request_closed", "The release request is closed.");
      const a = state.approvals.get(id);
      if (!a) throw new ApiError(409, "approval_not_requested", "The workflow has not asked yet.");
      if (r.requester === state.self) throw new ApiError(403, "own_text", "Four-eyes.");
      const decisions = [...a.decisions, { principal: state.self, decision, at: NOW, ...(reason ? { reason } : {}) }];
      const granted = new Set(decisions.filter((d) => d.decision === "granted").map((d) => d.principal)).size;
      const next: Approval = { ...a, decisions, state: decision === "denied" ? "denied" : granted >= a.required ? "granted" : "pending" };
      state.approvals.set(id, next);
      if (next.state === "denied") Object.assign(r, { state: "denied", decided_by: state.self, decided_at: NOW });
      if (next.state === "granted") {
        releases.point(r.environment, r.release_id, r.action);
        Object.assign(r, { state: "deployed", decided_by: state.self, decided_at: NOW });
      }
      return structuredClone(next);
    },
    async withdraw(p, id, reason) {
      await gate("withdraw", p, id, reason);
      const r = findRequest(id);
      if (r.state !== "pending") throw new ApiError(409, "release_request_closed", "The release request is closed.");
      Object.assign(r, { state: "withdrawn", decided_by: state.self, decided_at: NOW, ...(reason ? { reason } : {}) });
      return structuredClone(r);
    },
    async configureEnvironment(p, name, settings, etag): Promise<Versioned<Environment>> {
      await gate("configureEnvironment", p, name, settings, etag);
      const e = envOf(name);
      if (String(e.etag) !== etag) throw new ApiError(412, "precondition_failed", "Changed meanwhile.");
      if (settings.approval && settings.clear_approval) throw new ApiError(400, "invalid_request", "Not both.");
      if ((settings.approval || settings.clear_approval) && e.env.kind === "branch") throw new ApiError(422, "approval_on_branch", "Fixed.");
      const a = settings.approval;
      if (a) {
        const parties = [a.from.member, a.from.role, a.from.group].filter(Boolean).length;
        if (a.n < 1 || a.n > 10 || parties !== 1 || !a.distinct_from_requester) throw new ApiError(422, "invalid_approval", "Invalid approval.");
      }
      const { approval: _old, ...rest } = e.env;
      e.env = { ...rest, policy: structuredClone(settings.policy), updated_at: NOW, ...(a ? { approval: structuredClone(a) } : settings.clear_approval ? {} : _old ? { approval: _old } : {}) };
      e.etag++;
      return { value: structuredClone(e.env), etag: String(e.etag) };
    },
    async rollouts(p, env) {
      await gate("rollouts", p, env);
      envOf(env);
      return listOf(env).map((r) => structuredClone(r.rollout));
    },
    async rollout(p, env, id) {
      await gate("rollout", p, env, id);
      return versioned(findRollout(env, id));
    },
    async startRollout(p, env, input, key) {
      await gate("startRollout", p, env, input, key);
      const e = envOf(env);
      if (input.percent < 0 || input.percent > 100 || !Number.isInteger(input.percent)) throw new ApiError(400, "invalid_percent", "0–100.");
      if (input.max_duration_seconds !== undefined && (input.max_duration_seconds < 3600 || input.max_duration_seconds > 90 * 86_400)) {
        throw new ApiError(400, "invalid_max_duration", "One hour to 90 days.");
      }
      if (e.env.kind === "branch") throw new ApiError(409, "rollout_branch_environment", "Branch.");
      if (e.env.approval) throw new ApiError(409, "rollout_needs_approval", "Approval required.");
      if (!e.env.current_release_id) throw new ApiError(409, "rollout_no_stable", "Nothing served.");
      if (listOf(env).some((r) => r.rollout.status === "active")) throw new ApiError(409, "rollout_active", "One at a time.");
      if (e.env.current_release_id === input.release_id) throw new ApiError(409, "rollout_candidate_served", "Served already.");
      if (input.force && !input.force_reason) throw new ApiError(400, "force_reason_required", "Why?");
      const r: Rollout = {
        id: `ro_${++seq}`,
        environment: env,
        release_id: input.release_id,
        stable_release_id: e.env.current_release_id,
        percent: input.percent,
        status: "active",
        max_duration_seconds: input.max_duration_seconds ?? 14 * 86_400,
        expires_at: "2026-10-04T08:00:00Z",
        forced: !!input.force,
        started_by: state.self,
        started_at: NOW,
        updated_at: NOW,
        ...(input.force_reason ? { force_reason: input.force_reason } : {}),
      };
      const entry = { rollout: r, etag: 1 };
      state.rollouts.set(env, [entry, ...listOf(env)]);
      return versioned(entry);
    },
    async advance(p, env, id, percent, etag) {
      await gate("advance", p, env, id, percent, etag);
      const r = findRollout(env, id);
      if (etag !== `"${r.etag}"`) throw new ApiError(412, "precondition_failed", "Changed meanwhile.");
      if (r.rollout.status !== "active") throw new ApiError(409, "rollout_ended", "The rollout has ended.");
      if (percent < 0 || percent > 100 || !Number.isInteger(percent)) throw new ApiError(400, "invalid_percent", "0–100.");
      r.rollout = { ...r.rollout, percent, updated_at: NOW };
      r.etag++;
      return versioned(r);
    },
    async complete(p, env, id, etag) {
      await gate("complete", p, env, id, etag);
      return end(env, id, etag, "completed");
    },
    async abort(p, env, id, etag) {
      await gate("abort", p, env, id, etag);
      return end(env, id, etag, "aborted");
    },
  };
  return fake;
}
