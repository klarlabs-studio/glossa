/**
 * The Intelligence port (RFC 0003 §3): AI configuration, fills and jobs,
 * suggestions and the review queue, insights. `apiIntelligence`
 * implements it over the generated /v1 client with every response
 * checked by zod; component tests provide an in-memory fake
 * (src/test/fake-intelligence.ts). Like the Knowledge port it is loaded
 * with the screens that use it.
 */
import { inject, type InjectionKey } from "vue";
import { client } from "./client";
import { all } from "./endpoints";
import { done, read, type Versioned } from "./errors";
import * as I from "./intelligence-schemas";
import type { components } from "./schema";
import { page } from "./schemas";

type Body<N extends keyof components["schemas"]> = components["schemas"][N];
export type ProviderInput = Body<"CreateAIProvider">;
export type ProviderUpdate = Body<"UpdateAIProvider">;
export type SettingsUpdate = Body<"UpdateAISettings">;
export type ProjectSettingsUpdate = Body<"UpdateAIProjectSettings">;
/** A fill (or its preview): which messages by translation state, narrowed by namespace, key prefix or keys. */
// `force` is the in-product editor's affordance (RFC 0004 §5.3):
// re-translating text that is already current. Studio's fill dialog is
// about what is missing or stale, so it never sends it.
export type FillInput = Omit<Body<"CreateAIFill">, "include_outdated" | "select" | "force"> & { select: I.AIFillSelect };
export type PriceOverrides = Body<"PutAIPrices">["overrides"];

export interface ProjectRef {
  tenant: string;
  project: string;
}
export interface Page<T> {
  items: T[];
  next: string | undefined;
}
export interface SuggestionQuery {
  project?: string;
  status?: I.AISuggestionStatus;
  locale?: string;
  message?: string;
  job?: string;
}
export interface JobQuery {
  project?: string;
  state?: I.AIJobState;
  locale?: string;
  fill?: string;
  message?: string;
}
export interface DisclosureQuery {
  project?: string;
  job?: string;
  message?: string;
  provider?: string;
}

export interface IntelligencePort {
  providers(tenant: string): Promise<I.AIProvider[]>;
  provider(tenant: string, id: string): Promise<Versioned<I.AIProvider>>;
  createProvider(tenant: string, body: ProviderInput, idempotencyKey: string): Promise<I.AIProvider>;
  updateProvider(tenant: string, id: string, body: ProviderUpdate, etag: string): Promise<I.AIProvider>;
  deleteProvider(tenant: string, id: string): Promise<void>;

  /**
   * Settings, prices and routing policies exist with defaults before anyone
   * saves them, with the ETag `"0"`: writing with it creates them only while
   * nobody has (else 412, like any stale ETag).
   */
  settings(tenant: string): Promise<Versioned<I.AISettings>>;
  updateSettings(tenant: string, body: SettingsUpdate, etag: string): Promise<Versioned<I.AISettings>>;
  prices(tenant: string): Promise<Versioned<I.AIPrices>>;
  putPrices(tenant: string, overrides: PriceOverrides, etag: string): Promise<Versioned<I.AIPrices>>;
  budget(tenant: string): Promise<I.AIBudget>;
  /** Every priced call since `since` (default: this month), newest first. */
  spend(tenant: string, since?: string): Promise<I.AISpendEntry[]>;

  /** The tenant's policy in effect (or the default). */
  routing(tenant: string): Promise<Versioned<I.AIRoutingPolicyView>>;
  putRouting(tenant: string, policy: I.AIRoutingPolicy, etag: string): Promise<Versioned<I.AIRoutingPolicyView>>;
  /**
   * The policy in effect for a project: its own, else the tenant's, else
   * the default. The ETag is the project's own policy's (`"0"` while it has none).
   */
  projectRouting(p: ProjectRef): Promise<Versioned<I.AIRoutingPolicyView>>;
  putProjectRouting(p: ProjectRef, policy: I.AIRoutingPolicy, etag: string): Promise<Versioned<I.AIRoutingPolicyView>>;
  deleteProjectRouting(p: ProjectRef): Promise<void>;
  projectSettings(p: ProjectRef): Promise<Versioned<I.AIProjectSettings>>;
  updateProjectSettings(p: ProjectRef, body: ProjectSettingsUpdate, etag: string): Promise<Versioned<I.AIProjectSettings>>;

  /** What `createFill` would do with the same input, writing nothing. */
  previewFill(p: ProjectRef, body: FillInput): Promise<I.AIFillPreview>;
  createFill(p: ProjectRef, body: FillInput, idempotencyKey: string): Promise<I.AIFill>;
  fill(tenant: string, id: string): Promise<I.AIFill>;
  cancelFill(tenant: string, id: string): Promise<I.AIFill>;
  /** Every job matching, newest first. */
  jobs(tenant: string, q: JobQuery): Promise<I.AIJob[]>;
  job(tenant: string, id: string): Promise<I.AIJob>;

  /** Newest first. */
  suggestions(tenant: string, q: SuggestionQuery, pageSize?: number, pageToken?: string): Promise<Page<I.AISuggestion>>;
  suggestion(tenant: string, id: string): Promise<Versioned<I.AISuggestion>>;
  /**
   * The newest suggestions for one unit (a message's key in a locale),
   * as its workspace shows them — the read an assigned member (a vendor)
   * has, without the job, cost or memory units behind them.
   */
  unitSuggestions(tenant: string, project: string, key: string, locale: string, signal?: AbortSignal): Promise<I.UnitAISuggestion[]>;
  /** Accept as is, or with `text` the edit instead (MF2 unless `syntax` says otherwise). */
  accept(tenant: string, id: string, edit?: { text: string; syntax?: "mf1" | "mf2" }, etag?: string): Promise<I.AISuggestion>;
  reject(tenant: string, id: string, reason?: string, etag?: string): Promise<I.AISuggestion>;
  /** Pending suggestions riskiest first, narrowed to `locales` when given. */
  reviewQueue(p: ProjectRef, locales: string[], pageToken?: string): Promise<Page<I.AISuggestion>>;

  disclosures(tenant: string, q: DisclosureQuery, pageToken?: string): Promise<Page<I.AIDisclosure>>;
  metrics(p: ProjectRef, since?: string): Promise<I.AIMetrics>;
  evalBaseline(tenant: string): Promise<I.AIEvalBaseline>;
}

const value = async <T>(p: Promise<Versioned<T>>): Promise<T> => (await p).value;
const PAGE = 100;
/** `If-Match` on a decision when the caller has the suggestion's ETag. */
const ifMatch = (etag: string | undefined) => (etag ? { "If-Match": etag } : {});
/**
 * The contract types `include_outdated` (the older spelling of
 * `select: missing_or_outdated`) as required because it has a default;
 * false leaves the selection to `select`.
 */
const fillBody = (body: FillInput): Body<"CreateAIFill"> => ({ ...body, include_outdated: false, force: false });
const toPage = <T>(p: { items: T[]; next_page_token?: string | undefined }): Page<T> => ({ items: p.items, next: p.next_page_token });

export const apiIntelligence: IntelligencePort = {
  providers: (tenant) =>
    all((page_token) =>
      value(read(client.GET("/v1/tenants/{tenant}/ai-providers", { params: { path: { tenant }, query: { page_size: PAGE, page_token } } }), page(I.AIProvider))),
    ),
  provider: (tenant, ai_provider) =>
    read(client.GET("/v1/tenants/{tenant}/ai-providers/{ai_provider}", { params: { path: { tenant, ai_provider } } }), I.AIProvider),
  createProvider: (tenant, body, key) =>
    value(read(client.POST("/v1/tenants/{tenant}/ai-providers", { params: { path: { tenant }, header: { "Idempotency-Key": key } }, body }), I.AIProvider)),
  updateProvider: (tenant, ai_provider, body, etag) =>
    value(
      read(
        client.PATCH("/v1/tenants/{tenant}/ai-providers/{ai_provider}", { params: { path: { tenant, ai_provider }, header: { "If-Match": etag } }, body }),
        I.AIProvider,
      ),
    ),
  deleteProvider: (tenant, ai_provider) =>
    done(client.DELETE("/v1/tenants/{tenant}/ai-providers/{ai_provider}", { params: { path: { tenant, ai_provider } } })),

  settings: (tenant) => read(client.GET("/v1/tenants/{tenant}/ai-settings", { params: { path: { tenant } } }), I.AISettings),
  updateSettings: (tenant, body, etag) =>
    read(client.PUT("/v1/tenants/{tenant}/ai-settings", { params: { path: { tenant }, header: { "If-Match": etag } }, body }), I.AISettings),
  prices: (tenant) => read(client.GET("/v1/tenants/{tenant}/ai-prices", { params: { path: { tenant } } }), I.AIPrices),
  putPrices: (tenant, overrides, etag) =>
    read(client.PUT("/v1/tenants/{tenant}/ai-prices", { params: { path: { tenant }, header: { "If-Match": etag } }, body: { overrides } }), I.AIPrices),
  budget: (tenant) => value(read(client.GET("/v1/tenants/{tenant}/ai-budget", { params: { path: { tenant } } }), I.AIBudget)),
  spend: (tenant, since) =>
    all((page_token) =>
      value(
        read(
          client.GET("/v1/tenants/{tenant}/ai-spend", { params: { path: { tenant }, query: { page_size: PAGE, page_token, ...(since ? { since } : {}) } } }),
          page(I.AISpendEntry),
        ),
      ),
    ),

  routing: (tenant) => read(client.GET("/v1/tenants/{tenant}/ai-routing-policy", { params: { path: { tenant } } }), I.AIRoutingPolicyView),
  putRouting: (tenant, policy, etag) =>
    read(client.PUT("/v1/tenants/{tenant}/ai-routing-policy", { params: { path: { tenant }, header: { "If-Match": etag } }, body: policy }), I.AIRoutingPolicyView),
  projectRouting: (p) =>
    read(client.GET("/v1/tenants/{tenant}/projects/{project}/ai-routing-policy", { params: { path: p } }), I.AIRoutingPolicyView),
  putProjectRouting: (p, policy, etag) =>
    read(
      client.PUT("/v1/tenants/{tenant}/projects/{project}/ai-routing-policy", { params: { path: p, header: { "If-Match": etag } }, body: policy }),
      I.AIRoutingPolicyView,
    ),
  deleteProjectRouting: (p) => done(client.DELETE("/v1/tenants/{tenant}/projects/{project}/ai-routing-policy", { params: { path: p } })),
  projectSettings: (p) => read(client.GET("/v1/tenants/{tenant}/projects/{project}/ai-settings", { params: { path: p } }), I.AIProjectSettings),
  updateProjectSettings: (p, body, etag) =>
    read(client.PUT("/v1/tenants/{tenant}/projects/{project}/ai-settings", { params: { path: p, header: { "If-Match": etag } }, body }), I.AIProjectSettings),

  previewFill: (p, body) =>
    value(read(client.POST("/v1/tenants/{tenant}/projects/{project}/ai-fill-previews", { params: { path: p }, body: fillBody(body) }), I.AIFillPreview)),
  createFill: (p, body, key) =>
    value(
      read(
        client.POST("/v1/tenants/{tenant}/projects/{project}/ai-fills", { params: { path: p, header: { "Idempotency-Key": key } }, body: fillBody(body) }),
        I.AIFill,
      ),
    ),
  fill: (tenant, ai_fill) => value(read(client.GET("/v1/tenants/{tenant}/ai-fills/{ai_fill}", { params: { path: { tenant, ai_fill } } }), I.AIFill)),
  cancelFill: (tenant, ai_fill) =>
    value(read(client.POST("/v1/tenants/{tenant}/ai-fills/{ai_fill}/cancellation", { params: { path: { tenant, ai_fill } } }), I.AIFill)),
  jobs: (tenant, q) =>
    all((page_token) =>
      value(read(client.GET("/v1/tenants/{tenant}/ai-jobs", { params: { path: { tenant }, query: { ...q, page_size: PAGE, page_token } } }), page(I.AIJob))),
    ),
  job: (tenant, ai_job) => value(read(client.GET("/v1/tenants/{tenant}/ai-jobs/{ai_job}", { params: { path: { tenant, ai_job } } }), I.AIJob)),

  suggestions: async (tenant, q, pageSize = 50, page_token) =>
    toPage(
      await value(
        read(
          client.GET("/v1/tenants/{tenant}/ai-suggestions", { params: { path: { tenant }, query: { ...q, page_size: pageSize, page_token } } }),
          page(I.AISuggestion),
        ),
      ),
    ),
  unitSuggestions: async (tenant, project, message, locale, signal) =>
    (
      await value(
        read(
          client.GET("/v1/tenants/{tenant}/projects/{project}/messages/{message}/translations/{locale}/ai-suggestions", {
            params: { path: { tenant, project, message, locale } },
            ...(signal ? { signal } : {}),
          }),
          I.UnitAISuggestions,
        ),
      )
    ).items,
  suggestion: (tenant, ai_suggestion) =>
    read(client.GET("/v1/tenants/{tenant}/ai-suggestions/{ai_suggestion}", { params: { path: { tenant, ai_suggestion } } }), I.AISuggestion),
  accept: (tenant, ai_suggestion, edit, etag) =>
    value(
      read(
        client.POST("/v1/tenants/{tenant}/ai-suggestions/{ai_suggestion}/acceptance", {
          params: { path: { tenant, ai_suggestion }, header: ifMatch(etag) },
          body: edit ? { text: edit.text, syntax: edit.syntax ?? "mf2" } : {},
        }),
        I.AISuggestion,
      ),
    ),
  reject: (tenant, ai_suggestion, reason, etag) =>
    value(
      read(
        client.POST("/v1/tenants/{tenant}/ai-suggestions/{ai_suggestion}/rejection", {
          params: { path: { tenant, ai_suggestion }, header: ifMatch(etag) },
          body: reason ? { reason } : {},
        }),
        I.AISuggestion,
      ),
    ),
  reviewQueue: async (p, locales, page_token) =>
    toPage(
      await value(
        read(
          client.GET("/v1/tenants/{tenant}/projects/{project}/ai-review-queue", {
            params: { path: p, query: { page_size: 50, page_token, ...(locales.length ? { locale: locales } : {}) } },
          }),
          page(I.AISuggestion),
        ),
      ),
    ),

  disclosures: async (tenant, q, page_token) =>
    toPage(
      await value(
        read(client.GET("/v1/tenants/{tenant}/ai-disclosures", { params: { path: { tenant }, query: { ...q, page_size: 50, page_token } } }), page(I.AIDisclosure)),
      ),
    ),
  metrics: (p, since) =>
    value(read(client.GET("/v1/tenants/{tenant}/projects/{project}/ai-metrics", { params: { path: p, query: since ? { since } : {} } }), I.AIMetrics)),
  evalBaseline: (tenant) => value(read(client.GET("/v1/tenants/{tenant}/ai-eval-baseline", { params: { path: { tenant } } }), I.AIEvalBaseline)),
};

export const INTELLIGENCE: InjectionKey<IntelligencePort> = Symbol("intelligence");

/** The provided port, else the API adapter. */
export function useIntelligence(): IntelligencePort {
  return inject(INTELLIGENCE, apiIntelligence);
}
