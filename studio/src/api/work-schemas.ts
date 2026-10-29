/**
 * zod schemas for M5's work surfaces (RFC 0006 §3.1–§3.3): assignments,
 * translation approvals, groups and the workflow resolution that says
 * whether a project's translations run under a workflow at all.
 *
 * Two shape rules the screens depend on:
 *
 * - **An assignment names units, not messages.** A unit is a message
 *   `id` and a locale; the key a person recognises comes from the
 *   catalog the workspace already loads. Nothing here invents a key.
 * - **An approval does not carry the text under approval.** It names
 *   the unit (`subject_id` + `locale`); the text and its author are read
 *   from the translation, which is where four-eyes looks too.
 */
import { z } from "zod";
import type { components } from "./schema";
import { Role } from "./schemas";

const timestamp = z.string().min(1);
const id = z.string().min(1);

/** `open` and `accepted` are live; the others are final. */
export const AssignmentState = z.enum(["open", "accepted", "done", "declined", "expired"]);

/** A party as stored, resolved to Identity's ids: exactly what work is given to, or who may approve. */
export const Assignee = z.object({
  kind: z.enum(["member", "role", "group", "vendor"]),
  /** The member, group or vendor; absent for a role. */
  id: id.optional(),
  role: Role.optional(),
});

export const AssignmentUnit = z.object({
  message_id: id,
  locale: z.string().min(1),
});

export const Assignment = z.object({
  id,
  project_id: id,
  instance_id: id.optional(),
  units: z.array(AssignmentUnit),
  assignee: Assignee,
  permission: z.string(),
  due_at: timestamp.optional(),
  state: AssignmentState,
  created_by: z.string(),
  created_at: timestamp,
  updated_at: timestamp,
  closed_by: z.string().optional(),
  closed_at: timestamp.optional(),
  reason: z.string().optional(),
});

export const WorkflowSubject = z.enum(["translation", "release_request"]);
export const ApprovalState = z.enum(["pending", "granted", "denied"]);

export const ApprovalDecision = z.object({
  /** Who decided (`person:…`). */
  principal: z.string(),
  decision: z.enum(["granted", "denied"]),
  reason: z.string().optional(),
  at: timestamp,
});

export const Approval = z.object({
  id,
  project_id: id,
  instance_id: id.optional(),
  subject: WorkflowSubject,
  /** The message, for a translation unit. */
  subject_id: id,
  /** The translation unit's locale; absent for a release request. */
  locale: z.string().min(1).optional(),
  required: z.number().int().min(1),
  eligible: Assignee,
  /** Four-eyes: the author of the text under approval neither decides nor counts. */
  distinct_from_author: z.boolean(),
  due_at: timestamp.optional(),
  state: ApprovalState,
  decisions: z.array(ApprovalDecision),
  created_by: z.string(),
  created_at: timestamp,
  closed_at: timestamp.optional(),
});

/** A named set of members (RFC 0006 §4.3); `members` are member ids. */
export const Group = z.object({
  id,
  name: z.string(),
  members: z.array(id),
  created_at: timestamp,
  updated_at: timestamp,
});

export const WorkflowBinding = z.object({
  id,
  project_id: id,
  definition_id: id,
  subject: WorkflowSubject,
  locales: z.array(z.string()),
  namespace: z.string().optional(),
  position: z.number().int(),
  created_by: z.string(),
  created_at: timestamp,
});

/** `bound: false` means no binding applies: no instance, M4's behaviour. */
export const WorkflowResolution = z.object({
  bound: z.boolean(),
  binding: WorkflowBinding.optional(),
  definition_id: id.optional(),
  definition_name: z.string().optional(),
  version: z.number().int().min(1).optional(),
});

export type AssignmentState = z.infer<typeof AssignmentState>;
export type Assignee = z.infer<typeof Assignee>;
export type AssignmentUnit = z.infer<typeof AssignmentUnit>;
export type Assignment = z.infer<typeof Assignment>;
export type ApprovalState = z.infer<typeof ApprovalState>;
export type ApprovalDecision = z.infer<typeof ApprovalDecision>;
export type Approval = z.infer<typeof Approval>;
export type Group = z.infer<typeof Group>;
export type WorkflowBinding = z.infer<typeof WorkflowBinding>;
export type WorkflowResolution = z.infer<typeof WorkflowResolution>;

/** Whether an assignment still asks for work: `open` or `accepted`. */
export const isLive = (a: Pick<Assignment, "state">): boolean => a.state === "open" || a.state === "accepted";

// ── contract alignment (compile time only) ─────────────────────────────
//
// The spec is the source: these fail to compile if what the server is
// documented to send stops fitting what the screens parse. One-way, as
// in ./quality-summary-schemas.ts — the server may add a field without
// breaking Studio, and Studio may not expect one the server does not send.
type C = components["schemas"];
type Fits<A, B> = [A] extends [B] ? true : false;
type Assert<T extends true> = T;
export type WorkContractAlignment = [
  Assert<Fits<C["AssignmentState"], AssignmentState>>,
  Assert<Fits<C["Assignee"], Assignee>>,
  Assert<Fits<C["AssignmentUnit"], AssignmentUnit>>,
  Assert<Fits<C["Assignment"], Assignment>>,
  Assert<Fits<C["ApprovalState"], ApprovalState>>,
  Assert<Fits<C["ApprovalDecision"], ApprovalDecision>>,
  Assert<Fits<C["Approval"], Approval>>,
  Assert<Fits<C["Group"], Group>>,
  Assert<Fits<C["WorkflowBinding"], WorkflowBinding>>,
  Assert<Fits<C["WorkflowResolution"], WorkflowResolution>>,
];
