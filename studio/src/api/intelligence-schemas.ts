/**
 * zod schemas for the Intelligence context (RFC 0003 §3): providers,
 * settings, routing, budgets, fills, jobs, suggestions and insights.
 * Money is integer micro-USD (1 USD = 1 000 000). Kept out of ./schemas
 * so only the screens that use AI load them; the compile-time
 * assertions at the bottom tie each to the contract.
 */
import { z } from "zod";
import type { components } from "./schema";
import { MF2Message, QAFinding, Syntax } from "./schemas";

const timestamp = z.string().min(1);
const id = z.string().min(1);
const microUSD = z.number().int().min(0);

export const AIProviderKind = z.enum(["anthropic", "openai_compatible", "gemini"]);
export const AIProvider = z.object({
  id,
  name: z.string().min(1),
  kind: AIProviderKind,
  base_url: z.string().optional(),
  models: z.array(z.string()),
  enabled: z.boolean(),
  api_key_set: z.boolean(),
  version: z.number().int(),
  created_by: z.string(),
  created_at: timestamp,
  updated_by: z.string(),
  updated_at: timestamp,
});

export const AISettings = z.object({
  provider_consent: z.boolean(),
  consent_changed_by: z.string().optional(),
  consent_changed_at: timestamp.optional(),
  max_concurrent_jobs: z.number().int().min(1),
  monthly_budget_micro_usd: microUSD,
  version: z.number().int(),
  updated_by: z.string().optional(),
  updated_at: timestamp.optional(),
});

export const AIPrice = z.object({
  input_per_mtok: z.number().min(0),
  output_per_mtok: z.number().min(0),
  cache_read_per_mtok: z.number().min(0).optional(),
  cache_write_per_mtok: z.number().min(0).optional(),
});
export const AIPriceTable = z.record(z.string(), AIPrice);
export const AIPrices = z.object({ defaults: AIPriceTable, overrides: AIPriceTable, effective: AIPriceTable, version: z.number().int() });

export const AIProviderSpend = z.object({
  provider: z.string(),
  model: z.string(),
  cost_micro_usd: microUSD,
  calls: z.number().int(),
  input_tokens: z.number().int(),
  output_tokens: z.number().int(),
});
export const AIBudget = z.object({
  monthly_budget_micro_usd: microUSD,
  spent_micro_usd: microUSD,
  remaining_micro_usd: microUSD,
  calls: z.number().int(),
  month_start: timestamp,
  by_provider: z.array(AIProviderSpend),
});

export const AITask = z.enum(["translate", "review", "explain", "assess"]);
export const AIUsage = z.object({
  input_tokens: z.number().int(),
  output_tokens: z.number().int(),
  cache_read_tokens: z.number().int().optional(),
  cache_write_tokens: z.number().int().optional(),
});
export const AISpendEntry = z.object({
  id,
  job_id: id.optional(),
  project_id: id.optional(),
  task: AITask,
  provider: z.string(),
  model: z.string(),
  usage: AIUsage,
  cost_micro_usd: microUSD,
  priced: z.boolean(),
  occurred_at: timestamp,
});

export const AIRoute = z.object({
  provider: z.string().min(1),
  model: z.string().min(1),
  max_tokens: z.number().int().min(1),
  temperature: z.number().min(0).max(2).optional(),
  effort: z.enum(["low", "medium", "high", "max"]).optional(),
});
export const AIRoutingRule = z.object({ task: AITask, locales: z.array(z.string()).optional(), routes: z.array(AIRoute).min(1) });
export const AIRoutingPolicy = z.object({ rules: z.array(AIRoutingRule) });
export const AIRoutingPolicyView = z.object({
  policy: AIRoutingPolicy,
  source: z.enum(["default", "tenant", "project"]),
  version: z.number().int(),
  updated_by: z.string().optional(),
  updated_at: timestamp.optional(),
});

export const AINamespaceTag = z.enum(["sensitive", "legal", "marketing"]);
export const AIReviewPolicy = z.object({
  auto_approve: z.boolean(),
  auto_approve_min: z.number().gt(0).max(1),
  recommend_min: z.number().gt(0).max(1),
  force_review: z.array(z.string()).optional(),
  auto_approve_environments: z.array(z.string()).optional(),
});
export const AIProjectSettings = z.object({
  namespace_tags: z.record(z.string(), z.array(AINamespaceTag)),
  auto_translate_locales: z.array(z.string()),
  review: AIReviewPolicy,
  version: z.number().int(),
  updated_by: z.string().optional(),
  updated_at: timestamp.optional(),
});

export const AIJobState = z.enum(["queued", "running", "succeeded", "skipped", "failed", "dead", "cancelled"]);
/** Which messages a fill translates, by their translation's state in each locale. */
export const AIFillSelect = z.enum(["missing", "outdated", "missing_or_outdated"]);
export const AIFill = z.object({
  id,
  project_id: id,
  trigger: z.enum(["fill", "locale_added"]),
  locales: z.array(z.string()),
  namespace: z.string().optional(),
  key_prefix: z.string().optional(),
  keys: z.array(z.string()).optional(),
  include_outdated: z.boolean().optional(),
  select: AIFillSelect,
  jobs_created: z.number().int(),
  jobs_existing: z.number().int(),
  skipped: z.record(z.string(), z.number().int()),
  job_states: z.record(z.string(), z.number().int()),
  warnings: z.array(z.string()),
  requested_by: z.string(),
  created_at: timestamp,
});

export const AICostEstimate = z.object({ estimated_micro_usd: microUSD, max_micro_usd: microUSD, unpriced: z.boolean() });
export const AIFillPreviewLocale = z.object({
  locale: z.string(),
  keys: z.array(z.string()),
  existing: z.number().int().min(0),
  tm_exact: z.number().int().min(0),
  provider: z.number().int().min(0),
  refused: z.record(z.string(), z.number().int()),
  skipped: z.record(z.string(), z.number().int()),
  cost: AICostEstimate,
});
/** What a fill would do, per locale, without doing it. */
export const AIFillPreview = z.object({
  project_id: id,
  select: AIFillSelect,
  locales: z.array(AIFillPreviewLocale),
  warnings: z.array(z.string()),
  cost: AICostEstimate,
});

export const AIAuditEntry = z.object({ tool: z.string(), output: z.unknown(), at: timestamp });
export const AIJob = z.object({
  id,
  project_id: id,
  message_id: id,
  message_key: z.string(),
  namespace: z.string(),
  locale: z.string(),
  source_revision: z.number().int(),
  knowledge_fingerprint: z.string(),
  trigger: z.enum(["message_created", "translation_outdated", "locale_added", "fill"]),
  fill_id: id.optional(),
  state: AIJobState,
  attempts: z.number().int(),
  max_attempts: z.number().int(),
  available_at: timestamp,
  failure_code: z.string().optional(),
  last_error: z.string().optional(),
  suggestion_id: id.optional(),
  audit: z.array(AIAuditEntry).optional(),
  created_by: z.string(),
  created_at: timestamp,
  started_at: timestamp.optional(),
  finished_at: timestamp.optional(),
  updated_at: timestamp,
});

export const AIAction = z.enum(["auto_approve", "approve_recommended", "review_required"]);
export const AISuggestionStatus = z.enum(["pending", "accepted", "rejected", "auto_applied", "superseded"]);
export const AIConfidenceFactor = z.object({ factor: z.string(), value: z.number(), contribution: z.number(), reason: z.string() });
export const AITermFinding = z.object({
  code: z.enum(["term_missing", "term_forbidden"]),
  concept_id: z.string(),
  term_id: z.string().optional(),
  term: z.string(),
  message: z.string(),
});
export const AIProvenance = z.object({
  origin: z.enum(["ai", "translation_memory"]),
  provider: z.string().optional(),
  model: z.string().optional(),
  prompt_version: z.string().optional(),
  tm_unit_ids: z.array(z.string()).optional(),
  term_ids: z.array(z.string()).optional(),
  style_version: z.string().optional(),
  repairs: z.number().int(),
});
export const AICall = z.object({
  task: AITask,
  provider: z.string(),
  model: z.string(),
  prompt_version: z.string(),
  usage: AIUsage,
  cost_micro_usd: microUSD,
});
export const AIEditDiff = z.object({
  distance: z.number().int(),
  ratio: z.number(),
  terms_added: z.array(z.string()).optional(),
  terms_removed: z.array(z.string()).optional(),
  style_fields: z.array(z.string()).optional(),
});
/** The suggestion's message as it is now (absent once the message is gone). */
export const AISuggestionSource = z.object({
  message_key: z.string(),
  namespace: z.string(),
  // The server reports a proposed message as active here (it is translatable).
  state: z.enum(["active", "obsolete"]),
  source_revision: z.number().int(),
  text: z.string(),
  syntax: Syntax,
  mf2: z.string(),
  model: MF2Message,
});
export const AISuggestion = z.object({
  id,
  job_id: id,
  project_id: id,
  message_id: id,
  message_key: z.string(),
  namespace: z.string(),
  locale: z.string(),
  source_revision: z.number().int(),
  message: z.string(),
  model: MF2Message,
  findings: z.array(QAFinding),
  term_findings: z.array(AITermFinding),
  provenance: AIProvenance,
  score: z.number().min(0).max(1),
  explanation: z.array(AIConfidenceFactor),
  action: AIAction,
  action_note: z.string().optional(),
  risk_tags: z.array(z.string()),
  calls: z.array(AICall),
  usage: AIUsage,
  cost_micro_usd: microUSD,
  status: AISuggestionStatus,
  translation_revision: z.number().int().optional(),
  decided_by: z.string().optional(),
  decided_at: timestamp.optional(),
  decision: z.object({ edit: AIEditDiff.optional(), reason: z.string().optional() }).optional(),
  version: z.number().int(),
  created_at: timestamp,
  source: AISuggestionSource.optional(),
});

/**
 * A suggestion as a unit's workspace shows it: the text, score and
 * explanation, none of the job, provider, cost or memory units behind it
 * (RFC 0006 §3.3). `decidable` is false for an assigned member.
 */
export const UnitAISuggestion = z.object({
  id,
  locale: z.string(),
  source_revision: z.number().int(),
  message: z.string(),
  findings: z.array(QAFinding),
  term_findings: z.array(AITermFinding),
  score: z.number().min(0).max(1),
  explanation: z.array(AIConfidenceFactor),
  action: AIAction,
  action_note: z.string().optional(),
  risk_tags: z.array(z.string()),
  status: AISuggestionStatus,
  outdated: z.boolean(),
  decidable: z.boolean(),
  created_at: timestamp,
});
export const UnitAISuggestions = z.object({ items: z.array(UnitAISuggestion) });

export const AIDisclosure = z.object({
  id,
  job_id: id,
  project_id: id,
  message_id: id,
  locale: z.string(),
  task: AITask,
  provider: z.string(),
  model: z.string(),
  system_sha256: z.string(),
  sent: z.array(z.object({ role: z.enum(["user", "assistant"]), text: z.string() })),
  occurred_at: timestamp,
});

export const AILocaleMetrics = z.object({
  locale: z.string(),
  accepted: z.number().int(),
  edited: z.number().int(),
  rejected: z.number().int(),
  acceptance_rate: z.number(),
  mean_edit_distance: z.number(),
  mean_edit_ratio: z.number(),
});
export const AIMetrics = z.object({ since: timestamp, locales: z.array(AILocaleMetrics) });

export const AIEvalMetrics = z.object({
  cases: z.number().int(),
  structural_pass_rate: z.number(),
  terminology_compliance: z.number(),
  formality_compliance: z.number(),
  mean_edit_ratio: z.number(),
  origin_accuracy: z.number(),
});
export const AIEvalBaseline = z.object({ pairs: z.record(z.string(), AIEvalMetrics) });

export type AIProviderKind = z.infer<typeof AIProviderKind>;
export type AIProvider = z.infer<typeof AIProvider>;
export type AISettings = z.infer<typeof AISettings>;
export type AIPrice = z.infer<typeof AIPrice>;
export type AIPrices = z.infer<typeof AIPrices>;
export type AIBudget = z.infer<typeof AIBudget>;
export type AISpendEntry = z.infer<typeof AISpendEntry>;
export type AITask = z.infer<typeof AITask>;
export type AIRoute = z.infer<typeof AIRoute>;
export type AIRoutingRule = z.infer<typeof AIRoutingRule>;
export type AIRoutingPolicy = z.infer<typeof AIRoutingPolicy>;
export type AIRoutingPolicyView = z.infer<typeof AIRoutingPolicyView>;
export type AINamespaceTag = z.infer<typeof AINamespaceTag>;
export type AIReviewPolicy = z.infer<typeof AIReviewPolicy>;
export type AIProjectSettings = z.infer<typeof AIProjectSettings>;
export type AIJobState = z.infer<typeof AIJobState>;
export type AIFillSelect = z.infer<typeof AIFillSelect>;
export type AIFill = z.infer<typeof AIFill>;
export type AICostEstimate = z.infer<typeof AICostEstimate>;
export type AIFillPreviewLocale = z.infer<typeof AIFillPreviewLocale>;
export type AIFillPreview = z.infer<typeof AIFillPreview>;
export type AIJob = z.infer<typeof AIJob>;
export type AIAction = z.infer<typeof AIAction>;
export type AISuggestionStatus = z.infer<typeof AISuggestionStatus>;
export type AIConfidenceFactor = z.infer<typeof AIConfidenceFactor>;
export type AISuggestionSource = z.infer<typeof AISuggestionSource>;
export type AISuggestion = z.infer<typeof AISuggestion>;
export type UnitAISuggestion = z.infer<typeof UnitAISuggestion>;
export type UnitAISuggestions = z.infer<typeof UnitAISuggestions>;
export type AIDisclosure = z.infer<typeof AIDisclosure>;
export type AILocaleMetrics = z.infer<typeof AILocaleMetrics>;
export type AIMetrics = z.infer<typeof AIMetrics>;
export type AIEvalBaseline = z.infer<typeof AIEvalBaseline>;

// ── contract alignment (compile time only) ─────────────────────────────
type C = components["schemas"];
type Fits<A, B> = [A] extends [B] ? true : false;
type Assert<T extends true> = T;
export type IntelligenceContractAlignment = [
  Assert<Fits<UnitAISuggestions, C["UnitAISuggestions"]>>,
  Assert<Fits<AIProvider, C["AIProvider"]>>,
  Assert<Fits<AISettings, C["AISettings"]>>,
  Assert<Fits<AIPrices, C["AIPrices"]>>,
  Assert<Fits<AIBudget, C["AIBudget"]>>,
  Assert<Fits<AISpendEntry, C["AISpendEntry"]>>,
  Assert<Fits<AIRoutingPolicyView, C["AIRoutingPolicyView"]>>,
  Assert<Fits<AIProjectSettings, C["AIProjectSettings"]>>,
  Assert<Fits<AIFill, C["AIFill"]>>,
  Assert<Fits<AIFillPreview, C["AIFillPreview"]>>,
  Assert<Fits<AIJob, C["AIJob"]>>,
  Assert<Fits<AISuggestion, C["AISuggestion"]>>,
  Assert<Fits<AISuggestionSource, C["AISuggestionSource"]>>,
  Assert<Fits<AIDisclosure, C["AIDisclosure"]>>,
  Assert<Fits<AIMetrics, C["AIMetrics"]>>,
  Assert<Fits<AIEvalBaseline, C["AIEvalBaseline"]>>,
];
