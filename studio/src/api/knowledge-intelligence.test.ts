import { afterEach, describe, expect, it, vi } from "vitest";
import { setCsrfToken } from "./client";
import { apiIntelligence } from "./intelligence";
import { apiKnowledge } from "./knowledge";
import { suggestion } from "../test/fake-intelligence";

const json = (status: number, body: unknown, headers: Record<string, string> = {}) =>
  new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { "content-type": status >= 400 ? "application/problem+json" : "application/json", ...headers },
  });

function mockFetch(...responses: Response[]) {
  const fn = vi.fn<(r: Request) => Promise<Response>>();
  for (const r of responses) fn.mockResolvedValueOnce(r);
  vi.stubGlobal("fetch", fn);
  return fn;
}

const settings = { provider_consent: false, max_concurrent_jobs: 4, monthly_budget_micro_usd: 0, version: 0 };

afterEach(() => setCsrfToken(undefined));

describe("apiKnowledge", () => {
  it("looks up translation memory with the message's syntax and key", async () => {
    setCsrfToken("csrf-1");
    const fetch = mockFetch(json(200, { source_normalized: "Create a workspace", matches: [] }));
    const r = await apiKnowledge.lookupTM("t", { source: "Create a workspace", syntax: "mf1", source_locale: "en", target_locale: "de", project_id: "p", message_key: "a.b", limit: 5, min_score: 50, all_projects: false, count_hits: false });
    expect(r.matches).toEqual([]);
    const req = fetch.mock.calls[0]![0];
    expect(new URL(req.url).pathname).toBe("/v1/tenants/t/tm-lookups");
    expect(req.headers.get("X-CSRF-Token")).toBe("csrf-1");
    expect(await req.json()).toMatchObject({ syntax: "mf1", message_key: "a.b", count_hits: false });
  });

  it("reads a unit's matches from the unit's own route", async () => {
    const fetch = mockFetch(
      json(200, {
        source_normalized: "Pay",
        items: [{ score: 100, kind: "exact", source_normalized: "Pay", target: "Zahlen", target_text: "Zahlen", target_syntax: "mf1", target_syntax_fallback: false, variables_adapted: true, project_scoped: true }],
      }),
    );
    const r = await apiKnowledge.unitTMMatches("t", "p", "pay.now", "de", { target_syntax: "mf1", limit: 5 });
    expect(r.items[0]).not.toHaveProperty("message_key");
    const url = new URL(fetch.mock.calls[0]![0].url);
    expect(url.pathname).toBe("/v1/tenants/t/projects/p/messages/pay.now/translations/de/tm-matches");
    expect(url.searchParams.get("target_syntax")).toBe("mf1");
    expect(fetch.mock.calls[0]![0].method).toBe("GET");
  });

  it("rejects a response that breaks the contract", async () => {
    mockFetch(json(200, { analyzed_text: "x", hits: [{ concept_id: "c" }] }));
    await expect(apiKnowledge.recognizeTerms("t", { text: "x", locale: "en" })).rejects.toMatchObject({ code: "invalid_response" });
  });

  it("replaces a concept with If-Match", async () => {
    const concept = { id: "c", definition: "", domain: "", note: "", product_ref: "", terms: [], version: 2, created_by: "x", created_at: "t", updated_by: "x", updated_at: "t" };
    const fetch = mockFetch(json(200, concept, { ETag: '"2"' }));
    const r = await apiKnowledge.replaceConcept("t", "c", { terms: [{ locale: "en", text: "x", case_sensitive: false }] }, '"1"');
    expect(r.etag).toBe('"2"');
    expect(fetch.mock.calls[0]![0].headers.get("If-Match")).toBe('"1"');
  });
});

describe("apiIntelligence", () => {
  it("reads a unit's suggestions from the unit's own route", async () => {
    const unit = { id: "s1", locale: "de", source_revision: 1, message: "Zahlen", findings: [], term_findings: [], score: 0.7, explanation: [], action: "review_required", risk_tags: [], status: "pending", outdated: false, decidable: false, created_at: "2026-09-19T08:00:00Z" };
    const fetch = mockFetch(json(200, { items: [unit] }));
    const r = await apiIntelligence.unitSuggestions("t", "p", "pay.now", "de");
    expect(r).toEqual([unit]);
    expect(new URL(fetch.mock.calls[0]![0].url).pathname).toBe("/v1/tenants/t/projects/p/messages/pay.now/translations/de/ai-suggestions");
  });

  it("writes settings with the ETag they were read with, \"0\" while never saved", async () => {
    const fetch = mockFetch(json(200, { ...settings, version: 1 }, { ETag: '"1"' }), json(200, { ...settings, version: 2 }, { ETag: '"2"' }));
    const first = await apiIntelligence.updateSettings("t", { provider_consent: true }, '"0"');
    expect(fetch.mock.calls[0]![0].headers.get("If-Match")).toBe('"0"');
    await apiIntelligence.updateSettings("t", { monthly_budget_micro_usd: 5_000_000 }, first.etag!);
    expect(fetch.mock.calls[1]![0].headers.get("If-Match")).toBe('"1"');
    expect(await fetch.mock.calls[1]![0].json()).toEqual({ monthly_budget_micro_usd: 5_000_000 });
  });

  it("sends If-Match \"0\" on every never-saved singleton and surfaces the lost race as 412", async () => {
    const view = { policy: { rules: [] }, source: "project", version: 1 };
    const project = { namespace_tags: {}, auto_translate_locales: [], review: { auto_approve: false, auto_approve_min: 0.92, recommend_min: 0.75 }, version: 1 };
    const lost = json(412, { type: "about:blank", title: "Precondition failed", status: 412, code: "precondition_failed" });
    const fetch = mockFetch(
      json(200, { defaults: {}, overrides: {}, effective: {}, version: 1 }, { ETag: '"1"' }),
      json(200, { ...view, source: "tenant" }, { ETag: '"1"' }),
      json(200, view, { ETag: '"1"' }),
      json(200, project, { ETag: '"1"' }),
      lost,
    );
    const p = { tenant: "t", project: "p" };
    await apiIntelligence.putPrices("t", {}, '"0"');
    await apiIntelligence.putRouting("t", { rules: [] }, '"0"');
    await apiIntelligence.putProjectRouting(p, { rules: [] }, '"0"');
    await apiIntelligence.updateProjectSettings(p, { auto_translate_locales: [] }, '"0"');
    await expect(apiIntelligence.updateProjectSettings(p, { auto_translate_locales: [] }, '"0"')).rejects.toMatchObject({ status: 412, code: "precondition_failed" });
    expect(fetch.mock.calls.map((c) => c[0].headers.get("If-Match"))).toEqual(['"0"', '"0"', '"0"', '"0"', '"0"']);
  });

  it("accepts an edit as MF2 text, and a plain accept with an empty body", async () => {
    const fetch = mockFetch(json(200, suggestion({ status: "accepted" })), json(200, suggestion({ status: "accepted" })));
    await apiIntelligence.accept("t", "s1", { text: "Hallo {$name}" });
    expect(await fetch.mock.calls[0]![0].json()).toEqual({ text: "Hallo {$name}", syntax: "mf2" });
    await apiIntelligence.accept("t", "s1");
    expect(await fetch.mock.calls[1]![0].json()).toEqual({});
    expect(new URL(fetch.mock.calls[1]![0].url).pathname).toBe("/v1/tenants/t/ai-suggestions/s1/acceptance");
  });

  it("narrows the review queue to the member's locales", async () => {
    const fetch = mockFetch(json(200, { items: [suggestion()] }), json(200, { items: [] }));
    const page = await apiIntelligence.reviewQueue({ tenant: "t", project: "p" }, ["de", "fr"]);
    expect(page.items).toHaveLength(1);
    expect(new URL(fetch.mock.calls[0]![0].url).searchParams.getAll("locale")).toEqual(["de", "fr"]);
    await apiIntelligence.reviewQueue({ tenant: "t", project: "p" }, []);
    expect(new URL(fetch.mock.calls[1]![0].url).searchParams.has("locale")).toBe(false);
  });

  it("creates a provider with an Idempotency-Key; the key goes out once and never comes back", async () => {
    const provider = { id: "pv", name: "anthropic", kind: "anthropic", models: [], enabled: true, api_key_set: true, version: 1, created_by: "x", created_at: "t", updated_by: "x", updated_at: "t" };
    const fetch = mockFetch(json(201, provider));
    const r = await apiIntelligence.createProvider("t", { name: "anthropic", kind: "anthropic", enabled: true, api_key: "sk-1" }, "idem");
    expect(r).not.toHaveProperty("api_key");
    expect(r.api_key_set).toBe(true);
    expect(fetch.mock.calls[0]![0].headers.get("Idempotency-Key")).toBe("idem");
  });

  it("previews a fill, selecting by translation state, and checks the answer", async () => {
    const cost = { estimated_micro_usd: 2_000, max_micro_usd: 24_000, unpriced: false };
    const preview = {
      project_id: "p",
      select: "outdated",
      locales: [{ locale: "de", keys: ["a", "b"], existing: 0, tm_exact: 1, provider: 1, refused: {}, skipped: { not_selected: 3 }, cost }],
      warnings: [],
      cost,
    };
    const fetch = mockFetch(json(200, preview), json(200, { ...preview, locales: [{ locale: "de" }] }));
    const r = await apiIntelligence.previewFill({ tenant: "t", project: "p" }, { locales: ["de"], select: "outdated" });
    expect(r.locales[0]!.tm_exact).toBe(1);
    const req = fetch.mock.calls[0]![0];
    expect(new URL(req.url).pathname).toBe("/v1/tenants/t/projects/p/ai-fill-previews");
    expect(await req.json()).toEqual({ locales: ["de"], select: "outdated", include_outdated: false, force: false });
    await expect(apiIntelligence.previewFill({ tenant: "t", project: "p" }, { locales: ["de"], select: "outdated" })).rejects.toMatchObject({ code: "invalid_response" });
  });

  it("follows a fill's jobs across pages", async () => {
    const job = (id: string, state: string) => ({
      id, project_id: "p", message_id: "m", message_key: "k", namespace: "default", locale: "de", source_revision: 1, knowledge_fingerprint: "f",
      trigger: "fill", state, attempts: 0, max_attempts: 5, available_at: "t", created_by: "x", created_at: "t", updated_at: "t",
    });
    const fetch = mockFetch(json(200, { items: [job("a", "queued")], next_page_token: "n" }), json(200, { items: [job("b", "succeeded")] }));
    const jobs = await apiIntelligence.jobs("t", { fill: "f1" });
    expect(jobs.map((j) => j.state)).toEqual(["queued", "succeeded"]);
    expect(new URL(fetch.mock.calls[1]![0].url).searchParams.get("page_token")).toBe("n");
    expect(new URL(fetch.mock.calls[0]![0].url).searchParams.get("fill")).toBe("f1");
  });
});
