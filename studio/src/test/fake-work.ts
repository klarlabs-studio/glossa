/**
 * An in-memory WorkPort for component tests, with the API's rules in
 * miniature (RFC 0006 §3.1–§3.2): accepting needs an `open` assignment,
 * completing and declining a live one (`assignment_state`, 409, else);
 * an unknown id is `not_found`; a decided approval takes no more
 * decisions (`approval_closed`); and a refusal the test names for an
 * approval (`own_text`, `not_eligible`, `person_required`) is thrown as
 * the server would throw it.
 *
 * `fail` makes a whole method fail the way a broken or refusing server
 * does, which is the state every screen must keep apart from "nothing
 * here". `hold` keeps a method's answer pending, for the loading state.
 *
 * Test-only: nothing in the app imports it.
 */
import { ApiError } from "../api/errors";
import type { ProjectRef } from "../api/releases";
import type { Project, ProjectTranslation } from "../api/schemas";
import type { WorkPort } from "../api/work";
import type { Approval, Assignment, Group, WorkflowResolution } from "../api/work-schemas";

const NOW = "2026-09-19T08:00:00Z";

export function assignment(over: Partial<Assignment> = {}): Assignment {
  return {
    id: "as1",
    project_id: "p1",
    units: [
      { message_id: "m1", locale: "de" },
      { message_id: "m2", locale: "de" },
    ],
    assignee: { kind: "member", id: "m" },
    permission: "translations.write",
    state: "open",
    created_by: "person:owner",
    created_at: NOW,
    updated_at: NOW,
    ...over,
  };
}

export function approval(over: Partial<Approval> = {}): Approval {
  return {
    id: "ap1",
    project_id: "p1",
    subject: "translation",
    subject_id: "m1",
    locale: "de",
    required: 1,
    eligible: { kind: "role", role: "reviewer" },
    distinct_from_author: true,
    state: "pending",
    decisions: [],
    created_by: "person:owner",
    created_at: NOW,
    ...over,
  };
}

export function project(id: string, name: string): Project {
  return {
    id,
    slug: id,
    name,
    source_locale: "en",
    settings: { default_syntax: "mf2", review_required: true },
    created_at: NOW,
    updated_at: NOW,
  };
}

export function unitText(message_id: string, key: string, text: string, author = "person:vera", locale = "de"): ProjectTranslation {
  return {
    id: `t-${message_id}`,
    message_id,
    locale,
    text,
    syntax: "mf2",
    model: { type: "message", declarations: [], pattern: [text] },
    state: "needs_review",
    origin: "human",
    author,
    source_revision: 1,
    current_source_revision: 1,
    outdated: false,
    warnings: [],
    revision: 1,
    created_at: NOW,
    updated_at: NOW,
    key,
    namespace: "default",
    message_state: "active",
  };
}

type Method = keyof WorkPort;

export interface FakeWork extends WorkPort {
  readonly calls: Array<[Method, ...unknown[]]>;
  readonly state: {
    assignments: Assignment[];
    approvals: Approval[];
    groups: Group[];
    projects: Project[];
    /** Translations by `${project}/${locale}`. */
    texts: Map<string, ProjectTranslation[]>;
    /** The resolution per locale; `"*"` answers every locale not listed. */
    resolution: Map<string, WorkflowResolution>;
    /** A refusal the server gives when this approval is decided. */
    refusals: Map<string, ApiError>;
    /** The person deciding, as the server records them. */
    self: string;
  };
  /** Methods that fail outright. */
  fail: Partial<Record<Method, ApiError>>;
  /** Methods whose answers never arrive (the loading state). */
  hold: Set<Method>;
}

const never = <T>() => new Promise<T>(() => undefined);

export function createFakeWork(init: Partial<FakeWork["state"]> = {}): FakeWork {
  const state: FakeWork["state"] = {
    assignments: [],
    approvals: [],
    groups: [],
    projects: [project("p1", "Portal")],
    texts: new Map(),
    resolution: new Map([["*", { bound: false }]]),
    refusals: new Map(),
    self: "person:me",
    ...init,
  };
  const calls: FakeWork["calls"] = [];
  const fail: FakeWork["fail"] = {};
  const hold = new Set<Method>();

  async function gate(m: Method, ...args: unknown[]): Promise<void> {
    calls.push([m, ...args]);
    if (hold.has(m)) await never();
    const f = fail[m];
    if (f) throw f;
  }
  const find = (id: string): Assignment => {
    const a = state.assignments.find((x) => x.id === id);
    if (!a) throw new ApiError(404, "not_found", "No such resource.");
    return a;
  };
  const move = (id: string, from: Assignment["state"][], to: Assignment["state"], reason?: string): Assignment => {
    const a = find(id);
    if (!from.includes(a.state)) throw new ApiError(409, "assignment_state", `The assignment is ${a.state}.`);
    const next: Assignment = { ...a, state: to, updated_at: NOW, ...(to === "open" || to === "accepted" ? {} : { closed_at: NOW, closed_by: state.self }), ...(reason ? { reason } : {}) };
    state.assignments = state.assignments.map((x) => (x.id === id ? next : x));
    return next;
  };

  return {
    calls,
    state,
    fail,
    hold,
    async myAssignments(tenant, q = {}) {
      await gate("myAssignments", tenant, q);
      return state.assignments.filter((a) => (!q.project || a.project_id === q.project) && (!q.state || a.state === q.state));
    },
    async assignment(tenant, id) {
      await gate("assignment", tenant, id);
      return find(id);
    },
    async accept(tenant, id) {
      await gate("accept", tenant, id);
      return move(id, ["open"], "accepted");
    },
    async complete(tenant, id) {
      await gate("complete", tenant, id);
      return move(id, ["open", "accepted"], "done");
    },
    async decline(tenant, id, reason) {
      await gate("decline", tenant, id, reason);
      return move(id, ["open", "accepted"], "declined", reason);
    },
    async approvals(tenant, q = {}) {
      await gate("approvals", tenant, q);
      return state.approvals.filter(
        (a) => (!q.project || a.project_id === q.project) && (!q.state || a.state === q.state) && (!q.subject || a.subject === q.subject),
      );
    },
    async decide(tenant, id, decision, reason) {
      await gate("decide", tenant, id, decision, reason);
      const refused = state.refusals.get(id);
      if (refused) throw refused;
      const a = state.approvals.find((x) => x.id === id);
      if (!a) throw new ApiError(404, "not_found", "No such resource.");
      if (a.state !== "pending") throw new ApiError(409, "approval_closed", "The approval is closed.");
      const decisions = [...a.decisions, { principal: state.self, decision, at: NOW, ...(reason ? { reason } : {}) }];
      const grants = new Set(decisions.filter((d) => d.decision === "granted").map((d) => d.principal)).size;
      const next: Approval = {
        ...a,
        decisions,
        state: decision === "denied" ? "denied" : grants >= a.required ? "granted" : "pending",
      };
      state.approvals = state.approvals.map((x) => (x.id === id ? next : x));
      return next;
    },
    async groups(tenant) {
      await gate("groups", tenant);
      return state.groups;
    },
    async resolveWorkflow(p: ProjectRef, locale?: string) {
      await gate("resolveWorkflow", p, locale);
      return state.resolution.get(locale ?? "*") ?? state.resolution.get("*") ?? { bound: false };
    },
    async projects(tenant) {
      await gate("projects", tenant);
      return state.projects;
    },
    async unitTexts(p, locale, ids) {
      await gate("unitTexts", p, locale, [...ids]);
      const out = new Map<string, ProjectTranslation>();
      for (const t of state.texts.get(`${p.project}/${locale}`) ?? []) if (ids.has(t.message_id)) out.set(t.message_id, t);
      return out;
    },
  };
}
