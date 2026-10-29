/**
 * An in-memory IntelligencePort for component tests, with Intelligence's
 * rules in miniature: keys are write-only (only `api_key_set` comes
 * back), settings and routing policies start at version 0 (ETag "0",
 * which writes them only while still unsaved) with consent off, a
 * project's routing policy answers its own ETag while it inherits the
 * tenant's, a fill queues a job per message listed in `pending` and
 * settles them on `settle()` into the suggestions given, the review queue
 * is pending suggestions by score then risk tags, and a decided suggestion
 * can't be decided again. A fill preview plans a provider call per
 * `pending` suggestion (refused while `warnings` say consent is off),
 * adjusted by `plan`.
 * Test-only: nothing in the app imports it.
 */
import { ApiError, type Versioned } from "../api/errors";
import type { IntelligencePort } from "../api/intelligence";
import type {
  AIFill,
  AIFillPreview,
  AIFillPreviewLocale,
  AIJob,
  AIProjectSettings,
  AIProvider,
  AIRoutingPolicyView,
  AISettings,
  AISpendEntry,
  AISuggestion,
  AISuggestionSource,
} from "../api/intelligence-schemas";

const NOW = "2026-09-19T08:00:00Z";

export interface FakeIntelligence extends IntelligencePort {
  readonly calls: Array<[string, ...unknown[]]>;
  readonly state: {
    providers: AIProvider[];
    keys: Map<string, string>;
    settings: AISettings;
    project: AIProjectSettings;
    suggestions: AISuggestion[];
    /** Behave as for an assigned member: suggestions are not decidable. */
    assigned: boolean;
    jobs: AIJob[];
    spend: AISpendEntry[];
  };
  /** Suggestions a fill produces when its jobs settle. */
  pending: AISuggestion[];
  /** Warnings the next fill (and its preview) reports. */
  warnings: string[];
  /** Overrides for each locale of the next fill preview. */
  plan: Partial<AIFillPreviewLocale>;
  /** Finish every queued job. */
  settle(): void;
}

/** A suggestion's message as it is now, as the server embeds it. */
export function suggestionSource(key: string, over: Partial<AISuggestionSource> = {}): AISuggestionSource {
  return {
    message_key: key,
    namespace: "default",
    state: "active",
    source_revision: 1,
    text: "Hello, {name}!",
    syntax: "mf1",
    mf2: "Hello, {$name}!",
    model: { type: "message", declarations: [], pattern: ["Hello, ", { type: "expression", arg: { type: "variable", name: "name" } }, "!"] },
    ...over,
  };
}

export function suggestion(over: Partial<AISuggestion> = {}): AISuggestion {
  return {
    source: suggestionSource(over.message_key ?? "greeting"),
    id: "s1",
    job_id: "j1",
    project_id: "p",
    message_id: "m1",
    message_key: "greeting",
    namespace: "default",
    locale: "de",
    source_revision: 1,
    message: "Hallo, {$name}!",
    model: { type: "message", declarations: [], pattern: ["Hallo"] },
    findings: [],
    term_findings: [],
    provenance: { origin: "ai", provider: "anthropic", model: "claude-sonnet-5", prompt_version: "translate/v1", tm_unit_ids: ["u1"], term_ids: [], style_version: "g1@2", repairs: 0 },
    score: 0.62,
    explanation: [
      { factor: "origin", value: 0, contribution: -0.2, reason: "ai suggestion" },
      { factor: "tm_match", value: 0.8, contribution: 0.1, reason: "a fuzzy translation-memory match" },
      { factor: "self_assessment", value: 0.9, contribution: 0.04, reason: "the model's self-assessment (weighted lightly)" },
    ],
    action: "review_required",
    risk_tags: [],
    calls: [],
    usage: { input_tokens: 900, output_tokens: 40 },
    cost_micro_usd: 2120,
    status: "pending",
    version: 1,
    created_at: NOW,
    ...over,
  };
}

export function createFakeIntelligence(): FakeIntelligence {
  let seq = 0;
  const id = (p: string) => `${p}_${++seq}`;
  const calls: Array<[string, ...unknown[]]> = [];
  const tag = (v: number) => `"${v}"`;
  /** Every write names the version it's based on; singletons answer "0" while unsaved. */
  const need = (etag: string, v: number) => {
    if (etag !== tag(v)) throw new ApiError(412, "precondition_failed", "stale");
  };
  const state: FakeIntelligence["state"] = {
    providers: [],
    keys: new Map(),
    settings: { provider_consent: false, max_concurrent_jobs: 4, monthly_budget_micro_usd: 0, version: 0 },
    project: { namespace_tags: {}, auto_translate_locales: [], review: { auto_approve: false, auto_approve_min: 0.92, recommend_min: 0.75 }, version: 0 },
    suggestions: [],
    assigned: false,
    jobs: [],
    spend: [],
  };
  let routing: AIRoutingPolicyView = {
    policy: { rules: [{ task: "translate", routes: [{ provider: "anthropic", model: "claude-sonnet-5", max_tokens: 8192 }] }] },
    source: "default",
    version: 0,
  };
  /** The project's own policy, when it has one. */
  let projectRouting: AIRoutingPolicyView | undefined;
  let fills: AIFill[] = [];
  const suggestionOf = (sid: string) => {
    const s = state.suggestions.find((x) => x.id === sid);
    if (!s) throw new ApiError(404, "not_found", "No such suggestion.");
    return s;
  };
  const decide = (sid: string, patch: Partial<AISuggestion>) => {
    const s = suggestionOf(sid);
    if (s.status !== "pending") throw new ApiError(409, "suggestion_decided", "decided");
    Object.assign(s, { ...patch, version: s.version + 1, decided_by: "person:me", decided_at: NOW });
    return structuredClone(s);
  };

  const port: FakeIntelligence = {
    calls,
    state,
    pending: [],
    warnings: [],
    plan: {},
    settle() {
      for (const j of state.jobs.filter((x) => x.state === "queued")) {
        const s = port.pending.find((x) => x.message_key === j.message_key);
        j.state = s ? "succeeded" : "failed";
        if (s) {
          state.suggestions.push({ ...s, job_id: j.id });
          j.suggestion_id = s.id;
        } else j.failure_code = "provider_error";
      }
    },

    async providers() {
      return structuredClone(state.providers);
    },
    async provider(_t, pid) {
      const p = state.providers.find((x) => x.id === pid);
      if (!p) throw new ApiError(404, "not_found", "none");
      return { value: structuredClone(p), etag: tag(p.version) };
    },
    async createProvider(_t, body, key) {
      calls.push(["createProvider", body, key]);
      if (state.providers.some((p) => p.name === body.name)) throw new ApiError(409, "provider_name_taken", "taken");
      const p: AIProvider = {
        id: id("provider"),
        name: body.name,
        kind: body.kind,
        ...(body.base_url ? { base_url: body.base_url } : {}),
        models: body.models ?? [],
        enabled: body.enabled ?? true,
        api_key_set: !!body.api_key,
        version: 1,
        created_by: "person:me",
        created_at: NOW,
        updated_by: "person:me",
        updated_at: NOW,
      };
      if (body.api_key) state.keys.set(p.id, body.api_key);
      state.providers.push(p);
      return structuredClone(p);
    },
    async updateProvider(_t, pid, body, etag) {
      calls.push(["updateProvider", pid, body, etag]);
      const p = state.providers.find((x) => x.id === pid)!;
      need(etag, p.version);
      if (body.api_key) state.keys.set(pid, body.api_key);
      if (body.clear_api_key) state.keys.delete(pid);
      Object.assign(p, {
        ...(body.name ? { name: body.name } : {}),
        ...(body.models ? { models: body.models } : {}),
        ...(body.enabled !== undefined ? { enabled: body.enabled } : {}),
        api_key_set: state.keys.has(pid),
        version: p.version + 1,
      });
      return structuredClone(p);
    },
    async deleteProvider(_t, pid) {
      calls.push(["deleteProvider", pid]);
      state.providers = state.providers.filter((p) => p.id !== pid);
    },

    async settings() {
      return { value: structuredClone(state.settings), etag: tag(state.settings.version) };
    },
    async updateSettings(_t, body, etag) {
      calls.push(["updateSettings", body, etag]);
      need(etag, state.settings.version);
      const consentChanged = body.provider_consent !== undefined && body.provider_consent !== state.settings.provider_consent;
      state.settings = {
        ...state.settings,
        ...body,
        ...(consentChanged ? { consent_changed_by: "person:me", consent_changed_at: NOW } : {}),
        version: state.settings.version + 1,
      };
      return { value: structuredClone(state.settings), etag: tag(state.settings.version) };
    },
    async prices() {
      return {
        value: { defaults: { "anthropic/claude-sonnet-5": { input_per_mtok: 2, output_per_mtok: 10 } }, overrides: {}, effective: { "anthropic/claude-sonnet-5": { input_per_mtok: 2, output_per_mtok: 10 } }, version: state.settings.version },
        etag: tag(state.settings.version),
      };
    },
    async putPrices(_t, overrides, etag) {
      calls.push(["putPrices", overrides, etag]);
      // Prices and settings share a version.
      need(etag, state.settings.version);
      state.settings = { ...state.settings, version: state.settings.version + 1 };
      return { value: { defaults: {}, overrides, effective: overrides, version: state.settings.version }, etag: tag(state.settings.version) };
    },
    async budget() {
      const spent = state.spend.reduce((n, e) => n + e.cost_micro_usd, 0);
      return {
        monthly_budget_micro_usd: state.settings.monthly_budget_micro_usd,
        spent_micro_usd: spent,
        remaining_micro_usd: Math.max(0, state.settings.monthly_budget_micro_usd - spent),
        calls: state.spend.length,
        month_start: "2026-09-01T00:00:00Z",
        by_provider: spent ? [{ provider: "anthropic", model: "claude-sonnet-5", cost_micro_usd: spent, calls: state.spend.length, input_tokens: 1000, output_tokens: 100 }] : [],
      };
    },
    async spend() {
      return structuredClone(state.spend);
    },

    async routing() {
      return { value: structuredClone(routing), etag: tag(routing.version) };
    },
    async putRouting(_t, policy, etag) {
      calls.push(["putRouting", policy, etag]);
      need(etag, routing.version);
      routing = { policy, source: "tenant", version: routing.version + 1 };
      return { value: structuredClone(routing), etag: tag(routing.version) };
    },
    async projectRouting() {
      return { value: structuredClone(projectRouting ?? routing), etag: tag(projectRouting?.version ?? 0) };
    },
    async putProjectRouting(_p, policy, etag) {
      calls.push(["putProjectRouting", policy, etag]);
      need(etag, projectRouting?.version ?? 0);
      projectRouting = { policy, source: "project", version: (projectRouting?.version ?? 0) + 1 };
      return { value: structuredClone(projectRouting), etag: tag(projectRouting.version) };
    },
    async deleteProjectRouting() {
      calls.push(["deleteProjectRouting"]);
      projectRouting = undefined;
    },
    async projectSettings() {
      return { value: structuredClone(state.project), etag: tag(state.project.version) };
    },
    async updateProjectSettings(_p, body, etag) {
      calls.push(["updateProjectSettings", body, etag]);
      need(etag, state.project.version);
      if (body.review?.auto_approve && !body.review.auto_approve_environments?.length) throw new ApiError(422, "auto_approve_ineligible", "no environments");
      state.project = { ...state.project, ...body, version: state.project.version + 1 } as AIProjectSettings;
      return { value: structuredClone(state.project), etag: tag(state.project.version) };
    },

    async previewFill(p, body) {
      calls.push(["previewFill", body]);
      const keys = port.pending.map((x) => x.message_key);
      const consentOff = port.warnings.includes("provider_consent_off");
      const cost = (n: number) => ({ estimated_micro_usd: n * 1_000, max_micro_usd: n * 12_000, unpriced: false });
      const locales = body.locales.map(
        (locale): AIFillPreviewLocale => ({
          locale,
          keys,
          existing: 0,
          tm_exact: 0,
          provider: consentOff ? 0 : keys.length,
          refused: consentOff && keys.length ? { provider_consent: keys.length } : {},
          skipped: {},
          cost: cost(consentOff ? 0 : keys.length),
          ...port.plan,
        }),
      );
      const sum = (f: (c: AIFillPreviewLocale["cost"]) => number) => locales.reduce((n, l) => n + f(l.cost), 0);
      const preview: AIFillPreview = {
        project_id: p.project,
        select: body.select,
        locales,
        warnings: [...port.warnings],
        cost: { estimated_micro_usd: sum((c) => c.estimated_micro_usd), max_micro_usd: sum((c) => c.max_micro_usd), unpriced: locales.some((l) => l.cost.unpriced) },
      };
      return structuredClone(preview);
    },
    async createFill(p, body, key) {
      calls.push(["createFill", body, key]);
      const fillId = id("fill");
      const jobs = port.pending.map(
        (s): AIJob => ({
          id: id("job"),
          project_id: p.project,
          message_id: s.message_id,
          message_key: s.message_key,
          namespace: s.namespace,
          locale: body.locales[0]!,
          source_revision: 1,
          knowledge_fingerprint: "fp",
          trigger: "fill",
          fill_id: fillId,
          state: "queued",
          attempts: 0,
          max_attempts: 5,
          available_at: NOW,
          created_by: "person:me",
          created_at: NOW,
          updated_at: NOW,
        }),
      );
      state.jobs.push(...jobs);
      const fill: AIFill = {
        id: fillId,
        project_id: p.project,
        trigger: "fill",
        locales: body.locales,
        select: body.select,
        jobs_created: jobs.length,
        jobs_existing: 0,
        skipped: {},
        job_states: { queued: jobs.length },
        warnings: [...port.warnings],
        requested_by: "person:me",
        created_at: NOW,
      };
      fills.push(fill);
      return structuredClone(fill);
    },
    async fill(_t, fid) {
      return structuredClone(fills.find((f) => f.id === fid)!);
    },
    async cancelFill(_t, fid) {
      calls.push(["cancelFill", fid]);
      for (const j of state.jobs) if (j.fill_id === fid && j.state === "queued") j.state = "cancelled";
      return structuredClone(fills.find((f) => f.id === fid)!);
    },
    async jobs(_t, q) {
      calls.push(["jobs", q]);
      return structuredClone(state.jobs.filter((j) => !q.fill || j.fill_id === q.fill));
    },
    async job(_t, jid) {
      return structuredClone(state.jobs.find((j) => j.id === jid)!);
    },

    async suggestions(_t, q, pageSize = 50) {
      calls.push(["suggestions", q]);
      const items = [...state.suggestions]
        .reverse()
        .filter((s) => (!q.message || s.message_id === q.message) && (!q.locale || s.locale === q.locale) && (!q.status || s.status === q.status));
      return { items: structuredClone(items.slice(0, pageSize)), next: undefined };
    },
    async unitSuggestions(_t, _project, key, locale) {
      calls.push(["unitSuggestions", key, locale]);
      return [...state.suggestions]
        .reverse()
        .filter((s) => s.message_key === key && s.locale === locale)
        .slice(0, 5)
        .map((s) => ({
          id: s.id,
          locale: s.locale,
          source_revision: s.source_revision,
          message: s.message,
          findings: s.findings,
          term_findings: s.term_findings,
          score: s.score,
          explanation: s.explanation,
          action: s.action,
          ...(s.action_note ? { action_note: s.action_note } : {}),
          risk_tags: s.risk_tags,
          status: s.status,
          outdated: false,
          decidable: !state.assigned,
          created_at: s.created_at,
        }));
    },
    async suggestion(_t, sid) {
      const s = suggestionOf(sid);
      return { value: structuredClone(s), etag: tag(s.version) } satisfies Versioned<AISuggestion>;
    },
    async accept(_t, sid, edit) {
      calls.push(["accept", sid, edit]);
      const s = suggestionOf(sid);
      return decide(sid, {
        status: "accepted",
        translation_revision: 1,
        ...(edit ? { decision: { edit: { distance: Math.abs(edit.text.length - s.message.length) + 1, ratio: 0.2 } } } : {}),
      });
    },
    async reject(_t, sid, reason) {
      calls.push(["reject", sid, reason]);
      return decide(sid, { status: "rejected", ...(reason ? { decision: { reason } } : {}) });
    },
    async reviewQueue(_p, locales) {
      calls.push(["reviewQueue", locales]);
      const items = state.suggestions
        .filter((s) => s.status === "pending" && s.action !== "auto_approve" && (!locales.length || locales.includes(s.locale)))
        .sort((a, b) => a.score - b.score || b.risk_tags.length - a.risk_tags.length);
      return { items: structuredClone(items), next: undefined };
    },

    async disclosures() {
      return { items: [], next: undefined };
    },
    async metrics() {
      return { since: NOW, locales: [{ locale: "de", accepted: 3, edited: 1, rejected: 1, acceptance_rate: 0.75, mean_edit_distance: 4, mean_edit_ratio: 0.1 }] };
    },
    async evalBaseline() {
      return { pairs: { all: { cases: 10, structural_pass_rate: 1, terminology_compliance: 0.9, formality_compliance: 0.8, mean_edit_ratio: 0.05, origin_accuracy: 1 } } };
    },
  };
  return port;
}
