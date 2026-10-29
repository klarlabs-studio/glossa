/**
 * zod schemas for M5's release policies (RFC 0006 §5): an environment's
 * approval requirement, release requests, the publish or promote that a
 * requirement holds, and staged rollouts.
 */
import { z } from "zod";
import type { components } from "./schema";
import { EnvironmentApproval, EnvironmentApprovalParty } from "./schemas";

const timestamp = z.string().min(1);
const id = z.string().min(1);

export const ReleaseRequestState = z.enum(["pending", "deployed", "denied", "withdrawn", "refused"]);

export const GateVerdict = z.object({
  met: z.boolean(),
  unmet: z.array(z.string()).optional(),
});

export const ReleaseRequest = z.object({
  id,
  environment: z.string().min(1),
  release_id: id,
  action: z.enum(["publish", "promote"]),
  /** Who asked; they never count toward the approval. */
  requester: z.string(),
  /** The requirement the approvers were asked for. */
  approval: EnvironmentApproval,
  /** What the completeness requirement said when the request was made. */
  gate: GateVerdict,
  /** The requester overrode the gate — never the approval. */
  forced: z.boolean(),
  force_reason: z.string().optional(),
  state: ReleaseRequestState,
  decided_by: z.string().optional(),
  decided_at: timestamp.optional(),
  /** Why it was refused or withdrawn. */
  reason: z.string().optional(),
  created_at: timestamp,
});

/** A publish or promote held for approval: no pointer moved. */
export const ReleaseHeld = z.object({
  /** The release the request would deploy. */
  id,
  release_request_id: id,
  release_request: ReleaseRequest,
});

export const RolloutStatus = z.enum(["active", "completed", "aborted"]);
export const RolloutEnd = z.enum(["completed", "aborted", "expired", "rolled_back"]);

export const Rollout = z.object({
  id,
  environment: z.string().min(1),
  /** The candidate. */
  release_id: id,
  /** What the environment served when the rollout started. */
  stable_release_id: id,
  percent: z.number().int().min(0).max(100),
  status: RolloutStatus,
  end: RolloutEnd.optional(),
  max_duration_seconds: z.number().int().min(1),
  expires_at: timestamp,
  forced: z.boolean(),
  force_reason: z.string().optional(),
  started_by: z.string(),
  started_at: timestamp,
  updated_at: timestamp,
  ended_by: z.string().optional(),
  ended_at: timestamp.optional(),
});

export { EnvironmentApproval, EnvironmentApprovalParty };
export type ReleaseRequestState = z.infer<typeof ReleaseRequestState>;
export type GateVerdict = z.infer<typeof GateVerdict>;
export type ReleaseRequest = z.infer<typeof ReleaseRequest>;
export type ReleaseHeld = z.infer<typeof ReleaseHeld>;
export type RolloutStatus = z.infer<typeof RolloutStatus>;
export type RolloutEnd = z.infer<typeof RolloutEnd>;
export type Rollout = z.infer<typeof Rollout>;

type C = components["schemas"];
type Fits<A, B> = [A] extends [B] ? true : false;
type Assert<T extends true> = T;
export type ReleaseOpsContractAlignment = [
  Assert<Fits<C["EnvironmentApprovalParty"], EnvironmentApprovalParty>>,
  Assert<Fits<C["EnvironmentApproval"], EnvironmentApproval>>,
  Assert<Fits<C["ReleaseRequestState"], ReleaseRequestState>>,
  Assert<Fits<C["GateVerdict"], GateVerdict>>,
  Assert<Fits<C["ReleaseRequest"], ReleaseRequest>>,
  Assert<Fits<C["ReleaseHeld"], ReleaseHeld>>,
  Assert<Fits<C["Rollout"], Rollout>>,
];
