/**
 * An in-memory WorkflowsPort for component tests, with the API's rules in
 * miniature (RFC 0006 §2.3): a save is linted first and refused with every
 * finding (`WorkflowRejected`), a save carries the version it edited as
 * its ETag (412 when another save overtook it), a name is unique among
 * live definitions (`workflow_definition_exists`), and a binding's
 * selector is unique (`workflow_binding_exists`).
 *
 * `lintWith` decides what lint says about a document (default: valid,
 * no findings). `fail` makes a whole method fail; `hold` keeps its answer
 * pending, for the loading state.
 *
 * Test-only: nothing in the app imports it.
 */
import { ApiError, type Versioned } from "../api/errors";
import type { ProjectRef } from "../api/releases";
import { precedence } from "../lib/workflow";
import { WorkflowRejected, type WorkflowsPort } from "../api/workflows";
import type {
  WorkflowBinding,
  WorkflowDefinition,
  WorkflowDefinitionVersion,
  WorkflowDocument,
  WorkflowInstance,
  WorkflowLintResult,
  WorkflowTransition,
} from "../api/workflows-schemas";

const NOW = "2026-09-19T08:00:00Z";

/** The seeded default (platform/internal/workflow/defaults/review.json), abridged to what screens render. */
export function reviewDocument(name = "review"): WorkflowDocument {
  return {
    schema: "glossa.workflow/v1",
    name,
    subject: "translation",
    chart: {
      id: name,
      initial: "current",
      states: {
        current: {
          id: "current",
          type: "atomic",
          transitions: [
            { event: "translation.outdated", target: "reviewing", actions: ["back_to_review"] },
            { event: "translation.revised", target: "done" },
          ],
        },
        reviewing: {
          id: "reviewing",
          type: "atomic",
          entry: ["ask_a_reviewer"],
          transitions: [
            { event: "approval.granted", target: "done", guard: "one_approval", actions: ["approve"] },
            { event: "approval.denied", target: "done", actions: ["reject"] },
          ],
        },
        done: { id: "done", type: "final" },
      },
    },
    guards: { one_approval: { use: "approvals_at_least", n: 1, distinct_from_author: true } },
    actions: {
      back_to_review: { use: "set_review_state", state: "needs_review" },
      ask_a_reviewer: { use: "request_approval", n: 1, from: { role: "reviewer" } },
      approve: { use: "set_review_state", state: "approved" },
      reject: { use: "set_review_state", state: "rejected" },
    },
  };
}

export function definition(over: Partial<WorkflowDefinition> = {}): WorkflowDefinition {
  return { id: "wd1", name: "review", subject: "translation", version: 1, created_by: "person:owner", created_at: NOW, ...over };
}

export function binding(over: Partial<WorkflowBinding> = {}): WorkflowBinding {
  return { id: "wb1", project_id: "p", definition_id: "wd1", subject: "translation", locales: [], position: 1, created_by: "person:owner", created_at: NOW, ...over };
}

export function instance(over: Partial<WorkflowInstance> = {}): WorkflowInstance {
  return {
    id: "wi1",
    project_id: "p",
    definition_id: "wd1",
    definition_version: 1,
    subject: "translation",
    subject_id: "m1",
    locale: "de",
    state: "reviewing",
    status: "active",
    created_at: NOW,
    updated_at: NOW,
    ...over,
  };
}

export function transition(over: Partial<WorkflowTransition> = {}): WorkflowTransition {
  return { seq: 1, from: "current", event: "translation.outdated", to: "reviewing", outcome: "applied", guards: [], actions: [], actor: "system:workflow", at: NOW, ...over };
}

type Method = keyof WorkflowsPort;

export interface FakeWorkflows extends WorkflowsPort {
  readonly calls: Array<[Method, ...unknown[]]>;
  readonly state: {
    definitions: WorkflowDefinition[];
    /** Every version of every definition, by definition id, oldest first. */
    versions: Map<string, WorkflowDefinitionVersion[]>;
    bindings: WorkflowBinding[];
    instances: WorkflowInstance[];
    /** Transition logs by instance id, oldest first. */
    transitions: Map<string, WorkflowTransition[]>;
    self: string;
  };
  /** What lint (and every save) says about a document. */
  lintWith: (doc: WorkflowDocument) => WorkflowLintResult;
  fail: Partial<Record<Method, ApiError>>;
  hold: Set<Method>;
}

const never = <T>() => new Promise<T>(() => undefined);

export function createFakeWorkflows(init: Partial<FakeWorkflows["state"]> = {}): FakeWorkflows {
  const state: FakeWorkflows["state"] = {
    definitions: [],
    versions: new Map(),
    bindings: [],
    instances: [],
    transitions: new Map(),
    self: "person:me",
    ...init,
  };
  // A definition given without versions gets its latest version, so screens can open it.
  for (const d of state.definitions) {
    if (!state.versions.has(d.id)) {
      state.versions.set(d.id, [{ definition_id: d.id, version: d.version, schema: "glossa.workflow/v1", document: reviewDocument(d.name), created_by: d.created_by, created_at: d.created_at }]);
    }
  }
  const calls: FakeWorkflows["calls"] = [];
  const fail: FakeWorkflows["fail"] = {};
  const hold = new Set<Method>();
  let seq = 100;

  async function gate(m: Method, ...args: unknown[]): Promise<void> {
    calls.push([m, ...args]);
    if (hold.has(m)) await never();
    const f = fail[m];
    if (f) throw f;
  }
  const findDef = (id: string) => {
    const d = state.definitions.find((x) => x.id === id);
    if (!d) throw new ApiError(404, "not_found", "No such resource.");
    return d;
  };
  const refuseInvalid = (fake: FakeWorkflows, doc: WorkflowDocument) => {
    const r = fake.lintWith(doc);
    if (!r.valid) throw new WorkflowRejected(422, "The workflow document is not valid.", r.findings);
    return r.findings.filter((f) => f.severity === "info");
  };
  const store = (d: WorkflowDefinition, doc: WorkflowDocument) => {
    const list = state.versions.get(d.id) ?? [];
    list.push({ definition_id: d.id, version: d.version, schema: String(doc.schema ?? "glossa.workflow/v1"), document: structuredClone(doc), created_by: state.self, created_at: NOW });
    state.versions.set(d.id, list);
  };

  const fake: FakeWorkflows = {
    calls,
    state,
    fail,
    hold,
    lintWith: () => ({ valid: true, findings: [] }),
    async definitions(tenant, project) {
      await gate("definitions", tenant, project);
      return state.definitions.filter((d) => !d.deleted_at && (!d.project_id || d.project_id === project || project === undefined));
    },
    async definition(tenant, id): Promise<Versioned<WorkflowDefinition>> {
      await gate("definition", tenant, id);
      const d = findDef(id);
      return { value: structuredClone(d), etag: `"${d.version}"` };
    },
    async create(tenant, doc, project) {
      await gate("create", tenant, doc, project);
      const findings = refuseInvalid(fake, doc);
      const name = String(doc.name ?? "");
      if (state.definitions.some((d) => !d.deleted_at && d.name === name && d.project_id === project)) {
        throw new ApiError(409, "workflow_definition_exists", "A definition with this name exists.");
      }
      const d: WorkflowDefinition = { id: `wd${++seq}`, name, subject: doc.subject === "release_request" ? "release_request" : "translation", version: 1, created_by: state.self, created_at: NOW, ...(project ? { project_id: project } : {}) };
      state.definitions.push(d);
      store(d, doc);
      return { value: { ...structuredClone(d), findings }, etag: `"1"` };
    },
    async save(tenant, id, doc, etag) {
      await gate("save", tenant, id, doc, etag);
      const d = findDef(id);
      if (etag !== `"${d.version}"`) throw new ApiError(412, "precondition_failed", "Changed meanwhile.");
      const findings = refuseInvalid(fake, doc);
      d.version += 1;
      store(d, doc);
      return { value: { ...structuredClone(d), created_by: state.self, created_at: NOW, findings }, etag: `"${d.version}"` };
    },
    async remove(tenant, id) {
      await gate("remove", tenant, id);
      const d = findDef(id);
      d.deleted_at = NOW;
      state.bindings = state.bindings.filter((b) => b.definition_id !== id);
    },
    async versions(tenant, id) {
      await gate("versions", tenant, id);
      findDef(id);
      return structuredClone([...(state.versions.get(id) ?? [])].reverse());
    },
    async version(tenant, id, version) {
      await gate("version", tenant, id, version);
      const v = (state.versions.get(id) ?? []).find((x) => x.version === version);
      if (!v) throw new ApiError(404, "not_found", "No such resource.");
      return structuredClone(v);
    },
    async lint(tenant, doc) {
      await gate("lint", tenant, doc);
      return fake.lintWith(doc);
    },
    async bindings(p: ProjectRef) {
      await gate("bindings", p);
      return structuredClone(state.bindings.filter((b) => b.project_id === p.project));
    },
    async bind(p, input) {
      await gate("bind", p, input);
      const d = findDef(input.definition_id);
      const locales = [...(input.locales ?? [])].sort();
      const same = state.bindings.find(
        (b) => b.project_id === p.project && b.subject === d.subject && b.locales.join() === locales.join() && (b.namespace ?? "") === (input.namespace ?? ""),
      );
      if (same) throw new ApiError(409, "workflow_binding_exists", "A binding with this selector exists.");
      const b: WorkflowBinding = {
        id: `wb${++seq}`,
        project_id: p.project,
        definition_id: d.id,
        subject: d.subject,
        locales,
        position: Math.max(0, ...state.bindings.map((x) => x.position)) + 1,
        created_by: state.self,
        created_at: NOW,
        ...(input.namespace ? { namespace: input.namespace } : {}),
      };
      state.bindings.push(b);
      return structuredClone(b);
    },
    async unbind(p, id) {
      await gate("unbind", p, id);
      if (!state.bindings.some((b) => b.id === id)) throw new ApiError(404, "not_found", "No such resource.");
      state.bindings = state.bindings.filter((b) => b.id !== id);
    },
    async resolve(p, q) {
      await gate("resolve", p, q);
      const match = precedence(
        state.bindings.filter(
          (b) =>
            b.project_id === p.project &&
            b.subject === q.subject &&
            (!b.locales.length || (!!q.locale && b.locales.includes(q.locale))) &&
            (!b.namespace || b.namespace === q.namespace),
        ),
      )[0];
      if (!match) return { bound: false };
      const d = findDef(match.definition_id);
      return { bound: true, binding: structuredClone(match), definition_id: d.id, definition_name: d.name, version: d.version };
    },
    async instances(p, q = {}, pageToken) {
      await gate("instances", p, q, pageToken);
      const items = state.instances.filter(
        (i) =>
          i.project_id === p.project &&
          (!q.definition || i.definition_id === q.definition) &&
          (!q.status || i.status === q.status) &&
          (!q.locale || i.locale === q.locale) &&
          (!q.subject_id || i.subject_id === q.subject_id),
      );
      return { items: structuredClone(items), next: undefined };
    },
    async instance(p, id) {
      await gate("instance", p, id);
      const i = state.instances.find((x) => x.id === id && x.project_id === p.project);
      if (!i) throw new ApiError(404, "not_found", "No such resource.");
      return structuredClone(i);
    },
    async transitions(p, id) {
      await gate("transitions", p, id);
      return structuredClone(state.transitions.get(id) ?? []);
    },
  };
  return fake;
}
