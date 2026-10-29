/**
 * zod schemas for the Knowledge context (RFC 0003 §2): translation
 * memory, the termbase, terminology QA and style guides. Kept out of
 * ./schemas so only the screens that use knowledge load them; the
 * compile-time assertions at the bottom tie each to the contract.
 */
import { z } from "zod";
import type { components } from "./schema";
import { MF2Message, Syntax } from "./schemas";

const timestamp = z.string().min(1);
const id = z.string().min(1);

// ── translation memory ──────────────────────────────────────────────
export const TMUnit = z.object({
  id,
  project_id: id.optional(),
  origin: z.enum(["translation", "import"]),
  translation_id: id.optional(),
  translation_revision: z.number().int().optional(),
  message_id: id.optional(),
  message_key: z.string().optional(),
  namespace: z.string().optional(),
  source_locale: z.string(),
  target_locale: z.string(),
  source: z.string(),
  target: z.string(),
  target_model: MF2Message,
  source_normalized: z.string(),
  signature: z.string(),
  state: z.enum(["active", "retired"]),
  hit_count: z.number().int().min(0),
  last_hit_at: timestamp.optional(),
  created_by: z.string(),
  created_at: timestamp,
  updated_at: timestamp,
  retired_at: timestamp.optional(),
  retired_reason: z.enum(["superseded", "unapproved", "overwritten", "deleted"]).optional(),
  retired_by: z.string().optional(),
});

export const TMMatch = z.object({
  score: z.number().int().min(50).max(101),
  kind: z.enum(["context", "exact", "fuzzy"]),
  target: z.string(),
  target_text: z.string(),
  target_syntax: Syntax,
  target_syntax_fallback: z.boolean(),
  target_model: MF2Message,
  variables_adapted: z.boolean(),
  unit: TMUnit,
});
export const TMLookupResult = z.object({ source_normalized: z.string(), matches: z.array(TMMatch) });

/** A unit's match: text and score, never the unit; no `message_key` for an assigned member (RFC 0006 §3.3). */
export const UnitTMMatch = z.object({
  score: z.number().int().min(50).max(101),
  kind: z.enum(["context", "exact", "fuzzy"]),
  source_normalized: z.string(),
  target: z.string(),
  target_text: z.string(),
  target_syntax: Syntax,
  target_syntax_fallback: z.boolean(),
  variables_adapted: z.boolean(),
  project_scoped: z.boolean(),
  message_key: z.string().optional(),
});
export const UnitTMMatches = z.object({ source_normalized: z.string(), items: z.array(UnitTMMatch) });

export const TMConcordance = z.object({
  matches: z.array(z.object({ similarity: z.number().min(0).max(1), unit: TMUnit })),
});

// ── terminology ─────────────────────────────────────────────────────
export const TermStatus = z.enum(["preferred", "admitted", "deprecated", "forbidden"]);
export const PartOfSpeech = z.enum(["noun", "verb", "adjective", "adverb", "proper_noun", "phrase", "other"]);

export const Term = z.object({
  id,
  locale: z.string(),
  text: z.string(),
  status: TermStatus,
  part_of_speech: PartOfSpeech.optional(),
  case_sensitive: z.boolean(),
  note: z.string().optional(),
});

export const TermConcept = z.object({
  id,
  project_id: id.optional(),
  definition: z.string(),
  domain: z.string(),
  note: z.string(),
  product_ref: z.string(),
  terms: z.array(Term),
  version: z.number().int().min(1),
  created_by: z.string(),
  created_at: timestamp,
  updated_by: z.string(),
  updated_at: timestamp,
});

export const RevisionAction = z.enum(["created", "updated", "deleted"]);
export const TermConceptRevision = z.object({
  version: z.number().int().min(1),
  action: RevisionAction,
  author: z.string(),
  created_at: timestamp,
  concept: TermConcept,
});

export const TermHit = z.object({
  concept_id: id,
  definition: z.string(),
  term: Term,
  start: z.number().int().min(0),
  end: z.number().int().min(0),
  text: z.string(),
  targets: z.array(Term).optional(),
});
export const TermRecognition = z.object({ analyzed_text: z.string(), hits: z.array(TermHit) });

export const TermFinding = z.object({
  code: z.enum(["term_missing", "term_forbidden"]),
  severity: z.enum(["error", "warning"]),
  concept_id: id,
  term_id: id,
  side: z.enum(["source", "target"]),
  start: z.number().int().min(0),
  end: z.number().int().min(0),
  text: z.string(),
  suggestions: z.array(z.string()),
  message: z.string(),
});
export const TerminologyCheck = z.object({ source_text: z.string(), target_text: z.string(), findings: z.array(TermFinding) });

// ── style guides ────────────────────────────────────────────────────
export const StyleFields = z.object({
  formality: z.object({ register: z.enum(["formal", "informal", "neutral"]).optional(), pronoun: z.string().optional() }).optional(),
  tone: z.array(z.string()).optional(),
  punctuation: z
    .object({
      quotes: z.string().optional(),
      nested_quotes: z.string().optional(),
      dash: z.enum(["hyphen", "en", "em"]).optional(),
      space_before_unit: z.boolean().optional(),
      space_before_punctuation: z.boolean().optional(),
      serial_comma: z.boolean().optional(),
      ellipsis: z.string().optional(),
    })
    .optional(),
  numbers: z
    .object({ decimal_separator: z.string().optional(), grouping_separator: z.string().optional(), notes: z.string().optional() })
    .optional(),
  dates: z.object({ format: z.string().optional(), notes: z.string().optional() }).optional(),
});

export const StyleRule = z.object({
  id: z.string().min(1),
  title: z.string().optional(),
  rationale: z.string().optional(),
  good: z.array(z.string()).optional(),
  bad: z.array(z.string()).optional(),
  disabled: z.boolean().optional(),
});

export const StyleGuide = z.object({
  id,
  project_id: id.optional(),
  locale: z.string().optional(),
  namespace: z.string().optional(),
  name: z.string(),
  fields: StyleFields,
  rules: z.array(StyleRule),
  version: z.number().int().min(1),
  created_by: z.string(),
  created_at: timestamp,
  updated_by: z.string(),
  updated_at: timestamp,
});

export const StyleGuideVersion = z.object({
  version: z.number().int().min(1),
  action: RevisionAction,
  author: z.string(),
  created_at: timestamp,
  style_guide: StyleGuide,
});

export const StyleGuideSource = z.object({
  style_guide_id: id,
  version: z.number().int().min(1),
  project_id: id.optional(),
  locale: z.string().optional(),
  namespace: z.string().optional(),
});
export const EffectiveStyleGuide = z.object({ fields: StyleFields, rules: z.array(StyleRule), sources: z.array(StyleGuideSource) });

export type TMUnit = z.infer<typeof TMUnit>;
export type TMMatch = z.infer<typeof TMMatch>;
export type TMLookupResult = z.infer<typeof TMLookupResult>;
export type UnitTMMatch = z.infer<typeof UnitTMMatch>;
export type UnitTMMatches = z.infer<typeof UnitTMMatches>;
export type TMConcordance = z.infer<typeof TMConcordance>;
export type TermStatus = z.infer<typeof TermStatus>;
export type PartOfSpeech = z.infer<typeof PartOfSpeech>;
export type Term = z.infer<typeof Term>;
export type TermConcept = z.infer<typeof TermConcept>;
export type TermConceptRevision = z.infer<typeof TermConceptRevision>;
export type TermHit = z.infer<typeof TermHit>;
export type TermRecognition = z.infer<typeof TermRecognition>;
export type TermFinding = z.infer<typeof TermFinding>;
export type TerminologyCheck = z.infer<typeof TerminologyCheck>;
export type StyleFields = z.infer<typeof StyleFields>;
export type StyleRule = z.infer<typeof StyleRule>;
export type StyleGuide = z.infer<typeof StyleGuide>;
export type StyleGuideVersion = z.infer<typeof StyleGuideVersion>;
export type StyleGuideSource = z.infer<typeof StyleGuideSource>;
export type EffectiveStyleGuide = z.infer<typeof EffectiveStyleGuide>;

// ── contract alignment (compile time only) ─────────────────────────────
type C = components["schemas"];
type Fits<A, B> = [A] extends [B] ? true : false;
type Assert<T extends true> = T;
export type KnowledgeContractAlignment = [
  Assert<Fits<TMUnit, C["TMUnit"]>>,
  Assert<Fits<TMLookupResult, C["TMLookupResult"]>>,
  Assert<Fits<UnitTMMatches, C["UnitTMMatches"]>>,
  Assert<Fits<TMConcordance, C["TMConcordance"]>>,
  Assert<Fits<TermConcept, C["TermConcept"]>>,
  Assert<Fits<TermConceptRevision, C["TermConceptRevision"]>>,
  Assert<Fits<TermRecognition, C["TermRecognition"]>>,
  Assert<Fits<TerminologyCheck, C["TerminologyCheck"]>>,
  Assert<Fits<StyleGuide, C["StyleGuide"]>>,
  Assert<Fits<StyleGuideVersion, C["StyleGuideVersion"]>>,
  Assert<Fits<EffectiveStyleGuide, C["EffectiveStyleGuide"]>>,
];
