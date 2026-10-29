/**
 * zod schemas for the check policy (RFC 0005 §4): the document Studio
 * edits, the version behind it, and the impact preview a `dry_run` save
 * answers before anything is stored.
 *
 * Kept out of ./schemas, like the rest of Quality, so only the policy
 * screen loads them. The compile-time assertions at the bottom tie each
 * shape to the generated contract in the consumer direction.
 */
import { z } from "zod";
import type { components } from "./schema";
import { FindingLayer } from "./quality-schemas";

const timestamp = z.string().min(1);

/**
 * Which locales must be complete. The wire spells the three modes out;
 * the Go document has nil for "every locale" and an empty slice for
 * "none", and the API layer translates. Studio only ever sees these.
 */
export const LocaleRequirement = z.enum(["all", "listed", "none"]);

/** What a rule makes of the findings it selects. `off` means "don't compute". */
export const RuleSeverity = z.enum(["error", "warning", "off"]);

/**
 * `enforce` may change a run's conclusion; `warn` computes and reports
 * at the rule's severity and can never fail a run. `warn` is the
 * on-ramp of §4.3 — ship the rule, watch the number, flip it — so it is
 * a first-class control in the editor and not a footnote.
 */
export const RuleMode = z.enum(["enforce", "warn"]);

export const PolicyRule = z.object({
  layer: FindingLayer.optional(),
  code: z.string().optional(),
  locale: z.string().optional(),
  namespace: z.string().optional(),
  environment: z.string().optional(),
  severity: RuleSeverity,
  mode: RuleMode.optional(),
});

export const PolicyEnvironment = z.object({
  /** Absent: this environment inherits the document's requirement. */
  require_complete: LocaleRequirement.optional(),
  locales: z.array(z.string()).optional(),
  require_review: z.literal("approved").optional(),
});

export const PolicyDocument = z.object({
  schema: z.literal("glossa.check-policy/v1").optional(),
  require_complete: LocaleRequirement,
  locales: z.array(z.string()).optional(),
  fail_on: z.enum(["error", "warning", "never"]),
  missing_translations: z.enum(["error", "warning"]),
  environments: z.record(z.string(), PolicyEnvironment).optional(),
  /** In document order. Order is part of the meaning: ties in specificity go to the later rule. */
  rules: z.array(PolicyRule).optional(),
});

export const PolicyState = z.object({
  /** Monotonic. `0` is a project that has never saved one. */
  version: z.number().int().min(0),
  document: PolicyDocument,
  effective_from: timestamp.optional(),
  /** Until when pull requests opened before `effective_from` keep grading against `pinned_version`. */
  grace_until: timestamp.optional(),
  pinned_version: z.number().int().min(0).optional(),
  created_by: z.string().optional(),
  created_at: timestamp.optional(),
});

/** What one rule of the candidate did. A rule that changed nothing is listed too. */
export const PolicyRuleImpact = z.object({
  /** The rule's index in the candidate document's `rules`. */
  rule: z.number().int().min(0),
  selector: PolicyRule.optional(),
  matched: z.number().int().min(0),
  changed: z.number().int().min(0),
  newly_failing: z.number().int().min(0),
});

/**
 * An open pull request the candidate would newly fail: its branch, its
 * number, and where it is when the server knows the project's
 * repository.
 */
export const PolicyPullRequest = z.object({
  ref: z.string(),
  number: z.number().int().min(1),
  url: z.string().optional(),
});

export const PolicyImpact = z.object({
  findings: z.number().int().min(0),
  runs: z.number().int().min(0),
  raised: z.number().int().min(0),
  lowered: z.number().int().min(0),
  silenced: z.number().int().min(0),
  newly_failing: z.number().int().min(0),
  no_longer_failing: z.number().int().min(0),
  /** The number that decides whether this policy ships with a grace. */
  open_pull_requests: z.number().int().min(0),
  newly_failing_refs: z.array(z.string()).optional(),
  /** The ones `open_pull_requests` counts, named. */
  newly_failing_pull_requests: z.array(PolicyPullRequest).optional(),
  no_longer_failing_refs: z.array(z.string()).optional(),
  rules: z.array(PolicyRuleImpact),
});

export const PolicySaved = z.object({
  /** Nothing was stored. */
  dry_run: z.boolean(),
  policy: PolicyState,
  impact: PolicyImpact,
});

export const PolicyVersion = z.object({
  version: z.number().int().min(0),
  document: PolicyDocument,
  effective_from: timestamp.optional(),
  grace_until: timestamp.optional(),
  created_by: z.string(),
  created_at: timestamp,
});

export const PolicyVersionList = z.object({ items: z.array(PolicyVersion), next_page_token: z.string().optional() });

export type LocaleRequirement = z.infer<typeof LocaleRequirement>;
export type RuleSeverity = z.infer<typeof RuleSeverity>;
export type RuleMode = z.infer<typeof RuleMode>;
export type PolicyRule = z.infer<typeof PolicyRule>;
export type PolicyEnvironment = z.infer<typeof PolicyEnvironment>;
export type PolicyDocument = z.infer<typeof PolicyDocument>;
export type PolicyState = z.infer<typeof PolicyState>;
export type PolicyRuleImpact = z.infer<typeof PolicyRuleImpact>;
export type PolicyPullRequest = z.infer<typeof PolicyPullRequest>;
export type PolicyImpact = z.infer<typeof PolicyImpact>;
export type PolicySaved = z.infer<typeof PolicySaved>;
export type PolicyVersion = z.infer<typeof PolicyVersion>;
export type PolicyVersionList = z.infer<typeof PolicyVersionList>;

/** What a save sends: the whole document, how long it pins open pull requests, and whether it stores anything. */
export type SavePolicy = components["schemas"]["SaveCheckPolicy"];

// ── contract alignment (compile time only) ─────────────────────────────
//
// One-way, in the consumer direction: the *contract's* shape must fit
// the shape this file describes, so the screen can never require a
// field the server may leave out, and the server may add one without
// breaking the editor. The reverse direction would let a zod object
// that has grown stricter than the spec compile happily and then fail
// at runtime on a perfectly legal response.
type C = components["schemas"];
type Fits<A, B> = [A] extends [B] ? true : false;
type Assert<T extends true> = T;
export type CheckPolicyContractAlignment = [
  Assert<Fits<C["CheckPolicyLocaleRequirement"], LocaleRequirement>>,
  Assert<Fits<C["CheckPolicyRule"], PolicyRule>>,
  Assert<Fits<C["CheckPolicyRule"]["severity"], RuleSeverity>>,
  Assert<Fits<C["CheckPolicyEnvironment"], PolicyEnvironment>>,
  Assert<Fits<C["CheckPolicyDocument"], PolicyDocument>>,
  Assert<Fits<C["CheckPolicyState"], PolicyState>>,
  Assert<Fits<C["CheckPolicyRuleImpact"], PolicyRuleImpact>>,
  Assert<Fits<C["CheckPolicyPullRequest"], PolicyPullRequest>>,
  Assert<Fits<C["CheckPolicyImpact"], PolicyImpact>>,
  Assert<Fits<C["CheckPolicySaved"], PolicySaved>>,
  Assert<Fits<C["CheckPolicyVersion"], PolicyVersion>>,
  Assert<Fits<C["CheckPolicyVersionList"], PolicyVersionList>>,
  // And the other way for what the editor *sends*: a document Studio
  // builds must be one the spec accepts.
  Assert<Fits<PolicyDocument, C["CheckPolicyDocument"]>>,
];
