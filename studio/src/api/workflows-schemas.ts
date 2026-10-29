/**
 * zod schemas for the Workflow context's API (RFC 0006 §2): definitions
 * and their immutable versions, lint findings, bindings, instances and
 * their transition logs.
 *
 * The document itself (`glossa.workflow/v1`) is data the server owns the
 * meaning of: Studio parses only what it renders (the chart's states and
 * transitions, the guard and action names) and sends back exactly what
 * the author typed. A field Studio doesn't know is kept, never dropped.
 */
import { z } from "zod";
import type { components } from "./schema";
import { WorkflowBinding, WorkflowSubject } from "./work-schemas";

const timestamp = z.string().min(1);
const id = z.string().min(1);

/** A `glossa.workflow/v1` document, passed through untouched. */
export const WorkflowDocument = z.record(z.string(), z.unknown());

export const WorkflowFinding = z.object({
  rule: z.string(),
  /** `error` and `warning` refuse a save; `info` is a note. */
  severity: z.enum(["error", "warning", "info"]),
  /** Where in the document (`guards.two_approvals`, `chart.states.reviewing`); absent for the whole document. */
  path: z.string().optional(),
  state: z.string().optional(),
  event: z.string().optional(),
  message: z.string(),
});

export const WorkflowLintResult = z.object({
  valid: z.boolean(),
  findings: z.array(WorkflowFinding),
});

/** The `invalid_workflow` problem: the save was refused, with every finding. */
export const WorkflowProblem = z.object({
  type: z.string(),
  title: z.string(),
  status: z.number().int(),
  code: z.string(),
  detail: z.string().optional(),
  findings: z.array(WorkflowFinding).optional(),
});

export const WorkflowDefinition = z.object({
  id,
  /** Set when only this project may bind it. */
  project_id: id.optional(),
  name: z.string(),
  subject: WorkflowSubject,
  /** The latest version's number. */
  version: z.number().int().min(1),
  created_by: z.string(),
  created_at: timestamp,
  deleted_at: timestamp.optional(),
});

export const WorkflowDefinitionSaved = z.object({
  id,
  project_id: id.optional(),
  name: z.string(),
  subject: WorkflowSubject,
  version: z.number().int().min(1),
  created_by: z.string(),
  created_at: timestamp,
  /** Lint's informational notes; anything worse refused the save. */
  findings: z.array(WorkflowFinding),
});

export const WorkflowDefinitionVersion = z.object({
  definition_id: id,
  version: z.number().int().min(1),
  schema: z.string(),
  document: WorkflowDocument,
  created_by: z.string(),
  created_at: timestamp,
});

export const WorkflowInstanceStatus = z.enum(["active", "finished"]);

export const WorkflowInstance = z.object({
  id,
  project_id: id,
  definition_id: id,
  /** The version it runs on; a new save does not move it. */
  definition_version: z.number().int().min(1),
  subject: WorkflowSubject,
  /** The message, for a translation unit; the release request otherwise. */
  subject_id: id,
  locale: z.string().min(1).optional(),
  state: z.string(),
  status: WorkflowInstanceStatus,
  created_at: timestamp,
  updated_at: timestamp,
});

export const WorkflowGuardOutcome = z.object({ guard: z.string(), passed: z.boolean() });
export const WorkflowActionOutcome = z.object({
  name: z.string(),
  outcome: z.enum(["done", "refused", "failed"]),
  detail: z.string().optional(),
});

export const WorkflowTransition = z.object({
  seq: z.number().int(),
  from: z.string(),
  event: z.string(),
  /** The same as `from` unless `applied`. */
  to: z.string(),
  outcome: z.enum(["applied", "ignored", "refused"]),
  guards: z.array(WorkflowGuardOutcome),
  actions: z.array(WorkflowActionOutcome),
  /** Who caused the event; the transition's actions ran as them. */
  actor: z.string(),
  outbox_event_id: id.optional(),
  at: timestamp,
});

export type WorkflowDocument = z.infer<typeof WorkflowDocument>;
export type WorkflowFinding = z.infer<typeof WorkflowFinding>;
export type WorkflowLintResult = z.infer<typeof WorkflowLintResult>;
export type WorkflowDefinition = z.infer<typeof WorkflowDefinition>;
export type WorkflowDefinitionSaved = z.infer<typeof WorkflowDefinitionSaved>;
export type WorkflowDefinitionVersion = z.infer<typeof WorkflowDefinitionVersion>;
export type WorkflowInstanceStatus = z.infer<typeof WorkflowInstanceStatus>;
export type WorkflowInstance = z.infer<typeof WorkflowInstance>;
export type WorkflowGuardOutcome = z.infer<typeof WorkflowGuardOutcome>;
export type WorkflowActionOutcome = z.infer<typeof WorkflowActionOutcome>;
export type WorkflowTransition = z.infer<typeof WorkflowTransition>;
export { WorkflowBinding, WorkflowSubject };

// ── contract alignment (compile time only) ─────────────────────────────
// One-way, as in ./work-schemas.ts: what the server is documented to
// send must fit what the screens parse.
type C = components["schemas"];
type Fits<A, B> = [A] extends [B] ? true : false;
type Assert<T extends true> = T;
export type WorkflowContractAlignment = [
  Assert<Fits<C["WorkflowFinding"], WorkflowFinding>>,
  Assert<Fits<C["WorkflowLintResult"], WorkflowLintResult>>,
  Assert<Fits<C["WorkflowDefinition"], WorkflowDefinition>>,
  Assert<Fits<C["WorkflowDefinitionSaved"], WorkflowDefinitionSaved>>,
  Assert<Fits<C["WorkflowDefinitionVersion"], WorkflowDefinitionVersion>>,
  Assert<Fits<C["WorkflowInstanceStatus"], WorkflowInstanceStatus>>,
  Assert<Fits<C["WorkflowInstance"], WorkflowInstance>>,
  Assert<Fits<C["WorkflowTransition"], WorkflowTransition>>,
];
