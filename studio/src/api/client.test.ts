import { afterEach, describe, expect, it, vi } from "vitest";
import { onSessionExpired, setCsrfToken } from "./client";
import { messages, projects, translations } from "./endpoints";
import { ApiError } from "./errors";

const json = (status: number, body: unknown, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": status >= 400 ? "application/problem+json" : "application/json", ...headers },
  });

const problem = (status: number, code: string, extra: object = {}) => ({
  type: `urn:glossa:problem:${code}`,
  title: code,
  status,
  code,
  detail: `detail for ${code}`,
  ...extra,
});

const project = {
  id: "p1",
  slug: "demo",
  name: "Demo",
  source_locale: "en",
  settings: { default_syntax: "mf1", review_required: true },
  created_at: "2026-09-19T12:00:00Z",
  updated_at: "2026-09-19T12:00:00Z",
};

function mockFetch(...responses: Response[]) {
  const fn = vi.fn<(r: Request) => Promise<Response>>();
  for (const r of responses) fn.mockResolvedValueOnce(r);
  vi.stubGlobal("fetch", fn);
  return fn;
}

afterEach(() => setCsrfToken(undefined));

describe("the session conventions", () => {
  it("sends X-CSRF-Token on unsafe requests only", async () => {
    setCsrfToken("csrf-123");
    const fetch = mockFetch(json(200, project, { ETag: '"v1"' }), json(200, project, { ETag: '"v2"' }));
    await projects.get({ tenant: "t", project: "p1" });
    await projects.update({ tenant: "t", project: "p1" }, { name: "Demo 2" }, '"v1"');
    const [get, patch] = fetch.mock.calls.map(([r]) => r);
    expect(get!.headers.get("X-CSRF-Token")).toBeNull();
    expect(patch!.method).toBe("PATCH");
    expect(patch!.headers.get("X-CSRF-Token")).toBe("csrf-123");
    expect(patch!.headers.get("If-Match")).toBe('"v1"');
  });

  it("reports an expired session on a 401 outside the auth endpoints", async () => {
    const expired = vi.fn();
    const off = onSessionExpired(expired);
    mockFetch(json(401, problem(401, "unauthenticated")));
    await expect(projects.get({ tenant: "t", project: "p1" })).rejects.toMatchObject({ code: "unauthenticated" });
    expect(expired).toHaveBeenCalledOnce();
    off();
  });
});

describe("the boundary", () => {
  it("returns validated bodies with their ETag", async () => {
    mockFetch(json(200, project, { ETag: '"v7"' }));
    const r = await projects.get({ tenant: "t", project: "p1" });
    expect(r.value.settings.review_required).toBe(true);
    expect(r.etag).toBe('"v7"');
  });

  it("refuses a body that breaks the contract", async () => {
    mockFetch(json(200, { ...project, settings: { default_syntax: "mf3" } }));
    await expect(projects.get({ tenant: "t", project: "p1" })).rejects.toMatchObject({ code: "invalid_response" });
  });

  it("turns problem details into ApiErrors with codes and QA findings", async () => {
    const findings = [{ code: "missing-argument", severity: "error", subject: "name", message: "the translation does not use {$name}" }];
    mockFetch(json(422, problem(422, "structural_qa_failed", { findings })));
    const err = await translations.put({ tenant: "t", project: "p", message: "greeting", locale: "de" }, { text: "Hallo" }, undefined).catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err.status).toBe(422);
    expect(err.code).toBe("structural_qa_failed");
    expect(err.findings).toEqual(findings);
  });

  it("maps a transport failure to network_error", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("Failed to fetch")));
    await expect(projects.get({ tenant: "t", project: "p1" })).rejects.toMatchObject({ code: "network_error", status: 0 });
  });

  it("reads a missing translation as null, but not a missing locale", async () => {
    const path = { tenant: "t", project: "p", message: "greeting", locale: "de" };
    mockFetch(json(404, problem(404, "not_found")), json(404, problem(404, "locale_not_found")));
    await expect(translations.get(path)).resolves.toBeNull();
    await expect(translations.get(path)).rejects.toMatchObject({ code: "locale_not_found" });
  });

  it("passes list filters as query parameters and drops empty ones", async () => {
    const fetch = mockFetch(json(200, { items: [] }));
    await messages.page({ tenant: "t", project: "p" }, { missing_in: "de", namespace: "", state: "active" });
    const url = new URL(fetch.mock.calls[0]![0].url);
    expect(url.pathname).toBe("/v1/tenants/t/projects/p/messages");
    expect(Object.fromEntries(url.searchParams)).toEqual({ missing_in: "de", state: "active", page_size: "100" });
  });
});
