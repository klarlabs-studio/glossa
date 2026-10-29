/**
 * The Knowledge port (RFC 0003 §2): translation memory, the termbase,
 * terminology QA and style guides. `apiKnowledge` implements it over the
 * generated /v1 client with every response checked by zod; component
 * tests provide an in-memory fake (src/test/fake-knowledge.ts).
 *
 * `useKnowledge()` falls back to `apiKnowledge` when nothing is provided,
 * so the port (and its schemas) load with the screens that use it, not
 * with the app shell.
 */
import { inject, type InjectionKey } from "vue";
import { client } from "./client";
import { all } from "./endpoints";
import { done, read, type Versioned } from "./errors";
import * as K from "./knowledge-schemas";
import type { components } from "./schema";
import { page } from "./schemas";

type Body<N extends keyof components["schemas"]> = components["schemas"][N];
export type TMLookupInput = Body<"TMLookup">;
export type TermRecognitionInput = Body<"TermRecognitionRequest">;
export type TerminologyCheckInput = Body<"TerminologyCheckRequest">;
export type ConceptInput = Body<"CreateTermConcept">;
export type ConceptReplaceInput = Body<"ReplaceTermConcept">;
export type TermInput = Body<"TermInput">;
export type StyleGuideInput = Body<"CreateStyleGuide">;
export type StyleGuideReplaceInput = Body<"ReplaceStyleGuide">;

export interface ConcordanceQuery {
  q: string;
  side?: "source" | "target";
  source_locale?: string;
  target_locale?: string;
  project?: string;
  limit?: number;
}
export interface ConceptQuery {
  project?: string;
  q?: string;
  locale?: string;
  domain?: string;
}
export interface StyleGuideQuery {
  project?: string;
  locale?: string;
  tenant_only?: boolean;
}
export interface EffectiveStyleQuery {
  project?: string;
  locale?: string;
  namespace?: string;
}

/** Narrows a unit's matches; the targets come in `target_syntax`. */
export interface UnitTMQuery {
  target_syntax?: "mf1" | "mf2";
  limit?: number;
  min_score?: number;
}

export interface KnowledgePort {
  /** Exact and fuzzy translation-memory matches for a source message, best first. */
  lookupTM(tenant: string, q: TMLookupInput, signal?: AbortSignal): Promise<K.TMLookupResult>;
  /**
   * The matches of one unit (a message's key in a locale): the workspace's
   * read, and the only one an assigned member (a vendor) has — text and
   * score, never which unit they came from (RFC 0006 §3.3).
   */
  unitTMMatches(tenant: string, project: string, key: string, locale: string, q?: UnitTMQuery, signal?: AbortSignal): Promise<K.UnitTMMatches>;
  /** Active units containing a phrase: how it was translated before. */
  concordance(tenant: string, q: ConcordanceQuery, signal?: AbortSignal): Promise<K.TMConcordance>;
  recognizeTerms(tenant: string, body: TermRecognitionInput, signal?: AbortSignal): Promise<K.TermRecognition>;
  /** Terminology QA of a translation against its source (stores nothing). */
  checkTerminology(tenant: string, body: TerminologyCheckInput, signal?: AbortSignal): Promise<K.TerminologyCheck>;

  concepts(tenant: string, q: ConceptQuery): Promise<K.TermConcept[]>;
  concept(tenant: string, id: string): Promise<Versioned<K.TermConcept>>;
  createConcept(tenant: string, body: ConceptInput, idempotencyKey: string): Promise<Versioned<K.TermConcept>>;
  replaceConcept(tenant: string, id: string, body: ConceptReplaceInput, etag: string): Promise<Versioned<K.TermConcept>>;
  deleteConcept(tenant: string, id: string, etag?: string): Promise<void>;
  /** Newest first. */
  conceptRevisions(tenant: string, id: string): Promise<K.TermConceptRevision[]>;

  styleGuides(tenant: string, q: StyleGuideQuery): Promise<K.StyleGuide[]>;
  styleGuide(tenant: string, id: string): Promise<Versioned<K.StyleGuide>>;
  createStyleGuide(tenant: string, body: StyleGuideInput, idempotencyKey: string): Promise<Versioned<K.StyleGuide>>;
  replaceStyleGuide(tenant: string, id: string, body: StyleGuideReplaceInput, etag: string): Promise<Versioned<K.StyleGuide>>;
  deleteStyleGuide(tenant: string, id: string, etag?: string): Promise<void>;
  /** Newest first. */
  styleGuideVersions(tenant: string, id: string): Promise<K.StyleGuideVersion[]>;
  effectiveStyle(tenant: string, q: EffectiveStyleQuery, signal?: AbortSignal): Promise<K.EffectiveStyleGuide>;
}

const value = async <T>(p: Promise<Versioned<T>>): Promise<T> => (await p).value;
const PAGE = 100;
const withSignal = (signal?: AbortSignal) => (signal ? { signal } : {});
const t = (tenant: string) => ({ tenant });

export const apiKnowledge: KnowledgePort = {
  lookupTM: (tenant, body, signal) =>
    value(read(client.POST("/v1/tenants/{tenant}/tm-lookups", { params: { path: t(tenant) }, body, ...withSignal(signal) }), K.TMLookupResult)),
  unitTMMatches: (tenant, project, message, locale, query = {}, signal) =>
    value(
      read(
        client.GET("/v1/tenants/{tenant}/projects/{project}/messages/{message}/translations/{locale}/tm-matches", {
          params: { path: { tenant, project, message, locale }, query },
          ...withSignal(signal),
        }),
        K.UnitTMMatches,
      ),
    ),
  concordance: (tenant, query, signal) =>
    value(read(client.GET("/v1/tenants/{tenant}/tm-concordance", { params: { path: t(tenant), query }, ...withSignal(signal) }), K.TMConcordance)),
  recognizeTerms: (tenant, body, signal) =>
    value(read(client.POST("/v1/tenants/{tenant}/term-recognitions", { params: { path: t(tenant) }, body, ...withSignal(signal) }), K.TermRecognition)),
  checkTerminology: (tenant, body, signal) =>
    value(read(client.POST("/v1/tenants/{tenant}/terminology-checks", { params: { path: t(tenant) }, body, ...withSignal(signal) }), K.TerminologyCheck)),

  concepts: (tenant, q) =>
    all((page_token) =>
      value(
        read(
          client.GET("/v1/tenants/{tenant}/term-concepts", { params: { path: t(tenant), query: { ...q, page_size: PAGE, page_token } } }),
          page(K.TermConcept),
        ),
      ),
    ),
  concept: (tenant, concept) => read(client.GET("/v1/tenants/{tenant}/term-concepts/{concept}", { params: { path: { tenant, concept } } }), K.TermConcept),
  createConcept: (tenant, body, key) =>
    read(
      client.POST("/v1/tenants/{tenant}/term-concepts", { params: { path: t(tenant), header: { "Idempotency-Key": key } }, body }),
      K.TermConcept,
    ),
  replaceConcept: (tenant, concept, body, etag) =>
    read(
      client.PUT("/v1/tenants/{tenant}/term-concepts/{concept}", { params: { path: { tenant, concept }, header: { "If-Match": etag } }, body }),
      K.TermConcept,
    ),
  deleteConcept: (tenant, concept, etag) =>
    done(client.DELETE("/v1/tenants/{tenant}/term-concepts/{concept}", { params: { path: { tenant, concept }, header: etag ? { "If-Match": etag } : {} } })),
  conceptRevisions: (tenant, concept) =>
    all((page_token) =>
      value(
        read(
          client.GET("/v1/tenants/{tenant}/term-concepts/{concept}/revisions", {
            params: { path: { tenant, concept }, query: { page_size: PAGE, page_token } },
          }),
          page(K.TermConceptRevision),
        ),
      ),
    ),

  styleGuides: (tenant, q) =>
    all((page_token) =>
      value(
        read(
          client.GET("/v1/tenants/{tenant}/style-guides", { params: { path: t(tenant), query: { ...q, page_size: PAGE, page_token } } }),
          page(K.StyleGuide),
        ),
      ),
    ),
  styleGuide: (tenant, style_guide) =>
    read(client.GET("/v1/tenants/{tenant}/style-guides/{style_guide}", { params: { path: { tenant, style_guide } } }), K.StyleGuide),
  createStyleGuide: (tenant, body, key) =>
    read(client.POST("/v1/tenants/{tenant}/style-guides", { params: { path: t(tenant), header: { "Idempotency-Key": key } }, body }), K.StyleGuide),
  replaceStyleGuide: (tenant, style_guide, body, etag) =>
    read(
      client.PUT("/v1/tenants/{tenant}/style-guides/{style_guide}", { params: { path: { tenant, style_guide }, header: { "If-Match": etag } }, body }),
      K.StyleGuide,
    ),
  deleteStyleGuide: (tenant, style_guide, etag) =>
    done(
      client.DELETE("/v1/tenants/{tenant}/style-guides/{style_guide}", {
        params: { path: { tenant, style_guide }, header: etag ? { "If-Match": etag } : {} },
      }),
    ),
  styleGuideVersions: (tenant, style_guide) =>
    all((page_token) =>
      value(
        read(
          client.GET("/v1/tenants/{tenant}/style-guides/{style_guide}/versions", {
            params: { path: { tenant, style_guide }, query: { page_size: PAGE, page_token } },
          }),
          page(K.StyleGuideVersion),
        ),
      ),
    ),
  effectiveStyle: (tenant, query, signal) =>
    value(read(client.GET("/v1/tenants/{tenant}/effective-style-guide", { params: { path: t(tenant), query }, ...withSignal(signal) }), K.EffectiveStyleGuide)),
};

export const KNOWLEDGE: InjectionKey<KnowledgePort> = Symbol("knowledge");

/** The provided port, else the API adapter. */
export function useKnowledge(): KnowledgePort {
  return inject(KNOWLEDGE, apiKnowledge);
}
