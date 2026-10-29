/**
 * zod schemas for the Quality context (RFC 0005 §2): one finding shape
 * whatever layer found it, the check run that produced it, and the
 * waivers that accept findings without hiding them. Kept out of
 * ./schemas so only the quality screen loads them; the compile-time
 * assertions at the bottom tie each to the contract.
 */
import { z } from "zod";
import type { components } from "./schema";

const timestamp = z.string().min(1);
const id = z.string().min(1);

/** `sha256(layer, code, message, locale, subject)`, truncated — and of nothing else. */
export const Fingerprint = z.string().regex(/^f_[0-9a-f]{16}$/);

export const FindingLayer = z.enum([
  "structure",
  "parity",
  "completeness",
  "terminology",
  "style",
  "length",
  "locale",
  "source",
  "visual",
  "linguistic",
]);

/**
 * `waived` is a rendering of a finding, not a third rank a layer may
 * emit: a waived finding is still computed, still reported and counted
 * on its own.
 */
export const FindingSeverity = z.enum(["error", "warning", "waived"]);

export const FindingSpan = z.object({
  side: z.enum(["source", "target"]),
  start: z.number().int(),
  end: z.number().int(),
});

export const FindingLocus = z.object({
  message: id.optional(),
  key: z.string().optional(),
  locale: z.string().optional(),
  revision: id.optional(),
  namespace: z.string().optional(),
  file: z.string().optional(),
  line: z.number().int().optional(),
  column: z.number().int().optional(),
  route: z.string().optional(),
  component: z.string().optional(),
  /** The capture a visual finding is about; with `region`, what to crop. */
  capture: id.optional(),
  region: z.string().optional(),
  span: FindingSpan.optional(),
});

export const FindingFix = z.object({
  kind: z.enum(["replace", "shorten", "use-term", "adopt-source-change"]),
  to: z.number().int().optional(),
  term: id.optional(),
  hint: z.string().optional(),
});

export const Finding = z.object({
  schema: z.literal("glossa.finding/v1"),
  fingerprint: Fingerprint,
  layer: FindingLayer,
  code: z.string(),
  severity: FindingSeverity,
  locus: FindingLocus,
  message: z.string(),
  subject: z.string().optional(),
  detail: z.string().optional(),
  evidence: z.record(z.string(), z.unknown()).optional(),
  fix: FindingFix.optional(),
  source_revision: z.number().int().optional(),
  /** The waiver that accepted it. Present exactly when `severity` is `waived`. */
  waiver: id.optional(),
});

export const CheckRunTrigger = z.enum(["cli", "pull_request", "write", "capture", "api"]);
export const CheckRunConclusion = z.enum(["success", "failure", "neutral"]);

/** `waived` is counted on its own and is never part of `errors` or `warnings`. */
export const CheckRunCounts = z.object({
  errors: z.number().int().min(0),
  warnings: z.number().int().min(0),
  waived: z.number().int().min(0),
});

export const CheckRun = z.object({
  id,
  ref: z.string(),
  commit: z.string().optional(),
  trigger: CheckRunTrigger,
  policy_version: z.number().int().min(0),
  /** The layers the run computed: a layer not in it was never looked at. */
  layers: z.array(FindingLayer),
  counts: CheckRunCounts,
  conclusion: CheckRunConclusion.optional(),
  created_by: z.string(),
  started_at: timestamp,
  completed_at: timestamp.optional(),
});

export const CheckRunList = z.object({ items: z.array(CheckRun), next_page_token: z.string().optional() });

export const FindingList = z.object({
  items: z.array(Finding),
  /** The run the page came from; absent when nothing has been checked yet. */
  run: CheckRun.optional(),
  /** The whole run as it stands now, whatever the filters select. */
  counts: CheckRunCounts,
  next_page_token: z.string().optional(),
});

export const WaiverScope = z.enum(["project", "branch"]);

/** What a waiver accepts, from the newest stored finding carrying its fingerprint. */
export const WaiverFinding = z.object({
  layer: FindingLayer.optional(),
  code: z.string().optional(),
  locale: z.string().optional(),
  key: z.string().optional(),
  namespace: z.string().optional(),
  message: z.string().optional(),
});

export const Waiver = z.object({
  id,
  fingerprint: Fingerprint,
  reason: z.string(),
  scope: WaiverScope,
  ref: z.string().optional(),
  source_revision: z.number().int(),
  /** It stands now — neither revoked nor expired. */
  active: z.boolean(),
  accepts: WaiverFinding.optional(),
  created_by: z.string(),
  created_at: timestamp,
  expires_at: timestamp.optional(),
  revoked_at: timestamp.optional(),
});

export const WaiverList = z.object({ items: z.array(Waiver), next_page_token: z.string().optional() });

export type Fingerprint = z.infer<typeof Fingerprint>;
export type FindingLayer = z.infer<typeof FindingLayer>;
export type FindingSeverity = z.infer<typeof FindingSeverity>;
export type FindingSpan = z.infer<typeof FindingSpan>;
export type FindingLocus = z.infer<typeof FindingLocus>;
export type FindingFix = z.infer<typeof FindingFix>;
export type Finding = z.infer<typeof Finding>;
export type FindingList = z.infer<typeof FindingList>;
export type CheckRunTrigger = z.infer<typeof CheckRunTrigger>;
export type CheckRunConclusion = z.infer<typeof CheckRunConclusion>;
export type CheckRunCounts = z.infer<typeof CheckRunCounts>;
export type CheckRun = z.infer<typeof CheckRun>;
export type CheckRunList = z.infer<typeof CheckRunList>;
export type WaiverScope = z.infer<typeof WaiverScope>;
export type WaiverFinding = z.infer<typeof WaiverFinding>;
export type Waiver = z.infer<typeof Waiver>;
export type WaiverList = z.infer<typeof WaiverList>;

/** A finding to accept. The reason is required and non-empty. */
export type CreateWaiver = components["schemas"]["CreateWaiver"];

// ── contract alignment (compile time only) ─────────────────────────────
type C = components["schemas"];
type Fits<A, B> = [A] extends [B] ? true : false;
type Assert<T extends true> = T;
export type QualityContractAlignment = [
  Assert<Fits<FindingLayer, C["FindingLayer"]>>,
  Assert<Fits<FindingSeverity, C["FindingSeverity"]>>,
  Assert<Fits<FindingSpan, C["FindingSpan"]>>,
  Assert<Fits<FindingLocus, C["FindingLocus"]>>,
  Assert<Fits<FindingFix, C["FindingFix"]>>,
  Assert<Fits<Finding, C["Finding"]>>,
  Assert<Fits<FindingList, C["FindingList"]>>,
  Assert<Fits<CheckRunCounts, C["CheckRunCounts"]>>,
  Assert<Fits<CheckRun, C["CheckRun"]>>,
  Assert<Fits<CheckRunList, C["CheckRunList"]>>,
  Assert<Fits<WaiverFinding, C["WaiverFinding"]>>,
  Assert<Fits<Waiver, C["Waiver"]>>,
  Assert<Fits<WaiverList, C["WaiverList"]>>,
];
