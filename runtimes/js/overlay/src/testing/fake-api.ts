/**
 * A fake of the Studio API endpoints the overlay calls, in memory: messages,
 * translations with ETags and revision history, the MessageFormat preview
 * (the real MF2 parser, from @klarlabs-studio/glossa-messageformat), terminology findings,
 * AI fill previews, fills, suggestions and acceptance. It serves both the
 * component tests (as a `fetch`) and the browser tests (from Playwright's
 * route handler). Every request and response body is checked against the
 * OpenAPI schemas when a validator is given (see `contract.ts`). Test-only.
 */
import { parseMF2 } from "@klarlabs-studio/glossa-messageformat";
import type { Message as Model } from "@klarlabs-studio/glossa-runtime";

export const TENANT = "ten_1";
export const PROJECT = "prj_1";
export const TOKEN = "glossa_ctx_test";
const NOW = "2026-09-19T12:00:00Z";

export interface FakeRequest {
  method: string;
  url: string;
  headers: Record<string, string>;
  body?: string;
}

export interface FakeResponse {
  status: number;
  headers: Record<string, string>;
  body?: unknown;
}

/** Validates `value` against the named components schema; returns the problems. */
export type Validator = (schema: string, value: unknown) => string[];

// The fake builds plain JSON; the contract check, not the type system, keeps it honest.
type Json = Record<string, any>;

interface Stored {
  translation: Json;
  revisions: Json[];
}

const problem = (status: number, code: string, detail: string, extra: Json = {}): FakeResponse => ({
  status,
  headers: { "Content-Type": "application/problem+json" },
  body: {
    type: `urn:glossa:problem:${code}`,
    title: code.replace(/_/g, " "),
    status,
    code,
    detail,
    ...extra,
  },
});

/** The variables a model refers to. */
function variables(m: unknown, out = new Set<string>()): Set<string> {
  if (Array.isArray(m)) for (const x of m) variables(x, out);
  else if (m && typeof m === "object") {
    const o = m as Json;
    if (o.type === "variable" && typeof o.name === "string") out.add(o.name);
    for (const v of Object.values(o)) variables(v, out);
  }
  return out;
}

/** The fake's authoring parser: MF2 as is; MF1 only for `{name}` placeholders. */
export function parse(text: string, syntax: string): Model {
  return parseMF2(syntax === "mf1" ? text.replace(/\{(\w+)\}/g, "{$$$1}") : text) as Model;
}

export class FakeApi {
  readonly requests: FakeRequest[] = [];
  /** Contract problems in requests or responses; tests expect none. */
  readonly violations: string[] = [];
  private readonly messages = new Map<string, Json>();
  private readonly translations = new Map<string, Stored>();
  private readonly terms = new Map<string, Json[]>();
  private readonly suggestionsList: Json[] = [];
  /** Drafts an AI fill produces, by `key locale`, visible after `aiDelay` polls. */
  private readonly drafts = new Map<string, string>();
  private pendingDrafts: Array<{ polls: number; suggestion: Json }> = [];
  /** The jobs fills created, by id, for the editor to follow. */
  private readonly jobs = new Map<string, Json>();
  private ids = 0;
  /** How many suggestion lists a requested suggestion takes to show up. */
  aiDelay = 1;
  /** How a job with no draft behind it ends, so the panel can say why. */
  jobEnding: "skipped" | "failed" | "dead" | "cancelled" | "succeeded" = "skipped";
  /** Answer every request with this status instead (401, 500, …). */
  failWith?: number;

  constructor(private readonly validate?: Validator) {}

  message(key: string, text: string, extra: Json = {}): Json {
    const m = {
      id: `msg_${key.replace(/\W/g, "_")}`,
      key,
      namespace: "default",
      description: "",
      state: "active",
      source: { text, syntax: "mf2", model: parse(text, "mf2"), arguments: [], markup: [] },
      source_revision: 1,
      created_at: NOW,
      updated_at: NOW,
      ...extra,
    };
    this.messages.set(key, m);
    return m;
  }

  /** Write a translation as someone else would (a new revision, a new ETag). */
  translate(key: string, locale: string, text: string, author = "ana@example.com"): Json {
    return this.write(
      key,
      locale,
      { text, syntax: "mf2", origin: "human", origin_detail: {} },
      author,
    );
  }

  termFindings(key: string, locale: string, findings: Json[]): void {
    this.terms.set(`${key} ${locale}`, findings);
  }

  /** What an AI fill for this message produces. */
  aiDraft(key: string, locale: string, text: string): void {
    this.drafts.set(`${key} ${locale}`, text);
  }

  /** A pending suggestion that exists already. */
  suggestion(key: string, locale: string, text: string): Json {
    const s = this.makeSuggestion(key, locale, text);
    this.suggestionsList.unshift(s);
    return s;
  }

  translation(key: string, locale: string): Json | undefined {
    return this.translations.get(`${key} ${locale}`)?.translation;
  }

  revisions(key: string, locale: string): Json[] {
    return this.translations.get(`${key} ${locale}`)?.revisions ?? [];
  }

  /** A `fetch` over the fake, for component tests. */
  readonly fetch = async (input: RequestInfo | URL, init: RequestInit = {}): Promise<Response> => {
    const headers: Record<string, string> = {};
    new Headers(init.headers).forEach((v, k) => (headers[k] = v));
    const res = this.handle({
      method: init.method ?? "GET",
      url: String(input),
      headers,
      body: typeof init.body === "string" ? init.body : undefined,
    });
    return new Response(res.body === undefined ? null : JSON.stringify(res.body), {
      status: res.status,
      headers: res.headers,
    });
  };

  handle(req: FakeRequest): FakeResponse {
    this.requests.push(req);
    const lower: Record<string, string> = {};
    for (const [k, v] of Object.entries(req.headers)) lower[k.toLowerCase()] = v;
    const res = this.route(
      req.method,
      new URL(req.url),
      lower,
      req.body ? (JSON.parse(req.body) as Json) : undefined,
    );
    if (this.validate && res.body !== undefined) {
      const problemSchema = (res.body as Json).findings ? "QAProblem" : "Problem";
      const schema = res.status >= 400 ? problemSchema : this.schema;
      if (schema) {
        for (const e of this.validate(schema, res.body))
          this.violations.push(`${req.method} ${req.url} → ${schema}: ${e}`);
      }
    }
    return res;
  }

  /** The response schema of the route that answered last. */
  private schema?: string;

  private check(schema: string, body: unknown): void {
    if (!this.validate) return;
    for (const e of this.validate(schema, body)) this.violations.push(`request ${schema}: ${e}`);
  }

  private ok(
    status: number,
    schema: string,
    body: unknown,
    headers: Record<string, string> = {},
  ): FakeResponse {
    this.schema = schema;
    return { status, headers: { "Content-Type": "application/json", ...headers }, body };
  }

  private route(
    method: string,
    url: URL,
    headers: Record<string, string>,
    body?: Json,
  ): FakeResponse {
    this.schema = undefined;
    if (this.failWith)
      return problem(
        this.failWith,
        this.failWith === 401 ? "unauthenticated" : "internal",
        "fake failure",
      );
    if (headers.authorization !== `Bearer ${TOKEN}`)
      return problem(401, "unauthenticated", "no valid token");
    if (headers.cookie) return problem(400, "invalid_request", "the overlay never sends cookies");
    const p = url.pathname;
    if (method === "POST" && p === "/v1/message-previews") {
      this.check("MessagePreviewRequest", body);
      return this.ok(200, "MessagePreview", this.preview(body!.source, body!.syntax ?? "mf1"));
    }
    const t = p.match(/^\/v1\/tenants\/([^/]+)\/(.*)$/);
    if (!t || decodeURIComponent(t[1]!) !== TENANT)
      return problem(404, "not_found", "no such tenant");
    const rest = t[2]!;
    const jm = rest.match(/^ai-jobs\/([^/]+)$/);
    if (jm && method === "GET") {
      const job = this.jobs.get(decodeURIComponent(jm[1]!));
      return job ? this.ok(200, "AIJob", job) : problem(404, "not_found", "no such job");
    }
    const sug = rest.match(/^ai-suggestions(?:\/([^/]+)\/acceptance)?$/);
    if (sug)
      return sug[1]
        ? this.accept(method, decodeURIComponent(sug[1]), body)
        : this.listSuggestions(url);
    const pr = rest.match(/^projects\/([^/]+)\/(.*)$/);
    if (!pr || decodeURIComponent(pr[1]!) !== PROJECT)
      return problem(404, "not_found", "no such project");
    const sub = pr[2]!;
    let m: RegExpMatchArray | null;
    if ((m = sub.match(/^messages\/([^/]+)$/)) && method === "GET") {
      const msg = this.messages.get(decodeURIComponent(m[1]!));
      return msg
        ? this.ok(200, "Message", msg, { ETag: '"m1"' })
        : problem(404, "not_found", "no such message");
    }
    if ((m = sub.match(/^messages\/([^/]+)\/translations\/([^/]+)(\/revisions)?$/))) {
      const key = decodeURIComponent(m[1]!);
      const locale = decodeURIComponent(m[2]!);
      if (!this.messages.has(key)) return problem(404, "not_found", "no such message");
      const stored = this.translations.get(`${key} ${locale}`);
      if (m[3]) {
        if (!stored) return problem(404, "not_found", "no translation");
        return this.ok(200, "TranslationRevisionList", { items: [...stored.revisions].reverse() });
      }
      if (method === "GET") {
        return stored
          ? this.ok(200, "Translation", stored.translation, { ETag: etag(stored.translation) })
          : problem(404, "not_found", "no translation");
      }
      if (method === "PUT") return this.put(key, locale, headers["if-match"], body!);
    }
    if (sub === "terminology-findings" && method === "GET") {
      const locale = url.searchParams.get("locale")!;
      const prefix = url.searchParams.get("key_prefix") ?? "";
      // `key` names messages exactly; `key_prefix` still matches
      // everything below it, as the server does.
      const keys = url.searchParams.getAll("key");
      const items = [...this.terms]
        .filter(([k]) => k.endsWith(` ${locale}`))
        .filter(([k]) => {
          const key = k.slice(0, k.lastIndexOf(" "));
          return key.startsWith(prefix) && (keys.length === 0 || keys.includes(key));
        })
        .map(([k, findings]) => {
          const key = k.slice(0, k.lastIndexOf(" "));
          const msg = this.messages.get(key)!;
          return {
            message_id: msg.id,
            message_key: key,
            namespace: "default",
            locale,
            state: "needs_review",
            // The message's current source revision, which the server has
            // sent since the terminology check started recording what it
            // was checked against — so a waiver can go stale when it moves.
            source_revision: msg.source_revision,
            source_text: msg.source.text,
            target_text: this.translation(key, locale)?.text ?? "",
            findings,
          };
        });
      return this.ok(200, "ProjectTerminologyFindings", {
        items,
        checked: { [locale]: items.length },
      });
    }
    if (sub === "ai-fill-previews" && method === "POST") {
      this.check("CreateAIFill", body);
      if (body!.force !== true) {
        this.violations.push("ai-fill-previews from the overlay without force");
      }
      return this.ok(200, "AIFillPreview", this.fillPreview(body!));
    }
    if (sub === "ai-fills" && method === "POST") {
      this.check("CreateAIFill", body);
      if (!headers["idempotency-key"]) this.violations.push("ai-fills without an Idempotency-Key");
      return this.fill(body!);
    }
    return problem(404, "not_found", `no route ${method} ${p}`);
  }

  private preview(source: string, syntax: string): Json {
    try {
      const message = parse(source, syntax);
      return { valid: true, message, mf2: source, arguments: [], markup: [], errors: [] };
    } catch (e) {
      const text = e instanceof Error ? e.message : String(e);
      return {
        valid: false,
        arguments: [],
        markup: [],
        errors: [
          {
            stage: "parse",
            code: syntax === "mf1" ? "mf1-syntax-error" : "syntax-error",
            message: text,
          },
        ],
      };
    }
  }

  private put(key: string, locale: string, ifMatch: string | undefined, body: Json): FakeResponse {
    this.check("PutTranslation", body);
    const stored = this.translations.get(`${key} ${locale}`);
    if (stored && !ifMatch) return problem(428, "precondition_required", "If-Match is required");
    if (stored && ifMatch !== etag(stored.translation)) {
      return problem(412, "precondition_failed", "the translation changed");
    }
    if (!stored && ifMatch) return problem(412, "precondition_failed", "no translation to match");
    let model: Model;
    try {
      model = parse(body.text, body.syntax ?? "mf1");
    } catch (e) {
      return problem(400, "invalid_message", e instanceof Error ? e.message : String(e), {
        errors: [{ pointer: "/text", detail: "doesn't parse" }],
      });
    }
    const source = this.messages.get(key)!;
    const missing = [...variables(source.source.model)].filter((v) => !variables(model).has(v));
    if (missing.length) {
      return problem(422, "structural_qa_failed", "structurally incompatible", {
        findings: missing.map((v) => ({
          code: "missing-argument",
          severity: "error",
          subject: v,
          message: `The translation doesn't use {$${v}}.`,
        })),
      });
    }
    const t = this.write(key, locale, body, "overlay@example.com");
    return this.ok(stored ? 200 : 201, "Translation", t, { ETag: etag(t) });
  }

  private write(key: string, locale: string, body: Json, author: string): Json {
    const k = `${key} ${locale}`;
    const stored = this.translations.get(k);
    const revision = (stored?.translation.revision ?? 0) + 1;
    const translation = {
      id: `tr_${key}_${locale}`,
      message_id: this.messages.get(key)!.id,
      locale,
      text: body.text,
      syntax: body.syntax ?? "mf1",
      model: parse(body.text, body.syntax ?? "mf1"),
      state: "needs_review",
      origin: body.origin ?? "human",
      author,
      source_revision: 1,
      current_source_revision: 1,
      outdated: false,
      warnings: [],
      revision,
      created_at: stored?.translation.created_at ?? NOW,
      updated_at: NOW,
    };
    const rev = {
      revision,
      kind: "content",
      text: body.text,
      syntax: translation.syntax,
      state: "needs_review",
      origin: translation.origin,
      origin_detail: body.origin_detail ?? {},
      author,
      self_review: false,
      source_revision: 1,
      findings: [],
      created_at: NOW,
    };
    this.translations.set(k, { translation, revisions: [...(stored?.revisions ?? []), rev] });
    return translation;
  }

  private makeSuggestion(key: string, locale: string, text: string): Json {
    const msg = this.messages.get(key)!;
    return {
      id: `sug_${++this.ids}`,
      job_id: `job_${this.ids}`,
      project_id: PROJECT,
      message_id: msg.id,
      message_key: key,
      namespace: "default",
      locale,
      source_revision: 1,
      message: text,
      model: parse(text, "mf2"),
      findings: [],
      term_findings: [],
      provenance: { origin: "ai", provider: "fake", model: "fake-1", repairs: 0 },
      score: 0.82,
      explanation: [],
      action: "approve_recommended",
      risk_tags: [],
      calls: [],
      usage: { input_tokens: 10, output_tokens: 5 },
      cost_micro_usd: 12,
      status: "pending",
      version: 1,
      created_at: NOW,
    };
  }

  private listSuggestions(url: URL): FakeResponse {
    for (const p of this.pendingDrafts) {
      if (--p.polls <= 0) this.suggestionsList.unshift(p.suggestion);
    }
    this.pendingDrafts = this.pendingDrafts.filter((p) => p.polls > 0);
    const q = url.searchParams;
    const items = this.suggestionsList.filter(
      (s) =>
        (!q.get("message") || s.message_id === q.get("message")) &&
        (!q.get("locale") || s.locale === q.get("locale")) &&
        (!q.get("status") || s.status === q.get("status")),
    );
    return this.ok(200, "AISuggestionList", { items });
  }

  private fillPreview(body: Json): Json {
    const locale = body.locales[0] as string;
    const key = (body.keys as string[])[0]!;
    const current = this.translation(key, locale);
    // A forced request treats a current translation as due, which is the
    // whole point of asking from the editor (RFC 0004 5.3).
    const due = body.force === true || !current || current.outdated;
    const cost = {
      estimated_micro_usd: due ? 1200 : 0,
      max_micro_usd: due ? 5000 : 0,
      unpriced: false,
    };
    return {
      project_id: PROJECT,
      select: "missing_or_outdated",
      warnings: [],
      cost,
      locales: [
        {
          locale,
          keys: due ? [key] : [],
          existing: 0,
          tm_exact: 0,
          provider: due ? 1 : 0,
          refused: {},
          skipped: due ? {} : { up_to_date: 1 },
          cost,
        },
      ],
    };
  }

  private fill(body: Json): FakeResponse {
    const locale = body.locales[0] as string;
    const key = (body.keys as string[])[0]!;
    // The editor asks about text someone is reading, which is current by
    // definition. Without `force` the server would skip it as
    // `up_to_date` (RFC 0004 §5.3), so a fill from the overlay that
    // doesn't force is a bug in the overlay.
    if (body.force !== true) {
      this.violations.push("ai-fills from the overlay without force");
    }
    const draft = this.drafts.get(`${key} ${locale}`);
    const jobId = `job_${++this.ids}`;
    const msg = this.messages.get(key);
    // A job a caller can actually read back: the contract's AIJob, not
    // just the two fields the overlay happens to look at.
    this.jobs.set(jobId, {
      id: jobId,
      project_id: PROJECT,
      message_id: msg?.id ?? `msg_${key}`,
      message_key: key,
      namespace: "default",
      locale,
      source_revision: 1,
      knowledge_fingerprint: "f".repeat(64),
      trigger: "fill",
      state: draft === undefined ? this.jobEnding : "running",
      attempts: 1,
      max_attempts: 5,
      available_at: NOW,
      created_by: "overlay@example.com",
      created_at: NOW,
      updated_at: NOW,
    });
    if (draft !== undefined) {
      this.pendingDrafts.push({
        polls: this.aiDelay,
        suggestion: this.makeSuggestion(key, locale, draft),
      });
    }
    return this.ok(201, "AIFill", {
      id: `fill_${++this.ids}`,
      project_id: PROJECT,
      trigger: "fill",
      locales: [locale],
      keys: [key],
      select: "missing_or_outdated",
      jobs_created: draft === undefined ? 0 : 1,
      jobs_existing: 0,
      job_ids: [jobId],
      skipped: {},
      job_states: draft === undefined ? {} : { queued: 1 },
      warnings: [],
      requested_by: "overlay@example.com",
      created_at: NOW,
    });
  }

  private accept(method: string, id: string, body?: Json): FakeResponse {
    if (method !== "POST") return problem(404, "not_found", "no route");
    this.check("AcceptAISuggestion", body ?? {});
    const s = this.suggestionsList.find((x) => x.id === id);
    if (!s) return problem(404, "not_found", "no such suggestion");
    if (s.status !== "pending") return problem(409, "suggestion_decided", "decided already");
    const edited = typeof body?.text === "string" && body.text !== s.message;
    // The editor always says where it was; Studio's review queue doesn't.
    if (body?.in_context === undefined) {
      this.violations.push("accepting a suggestion from the overlay without in_context");
    }
    const t = this.write(
      s.message_key,
      s.locale,
      {
        text: edited ? body!.text : s.message,
        syntax: edited ? (body!.syntax ?? "mf2") : "mf2",
        origin: "ai",
        origin_detail: { suggestion: s.id, edited, in_context: body?.in_context },
      },
      "overlay@example.com",
    );
    Object.assign(s, {
      status: "accepted",
      translation_revision: t.revision,
      decided_by: "overlay@example.com",
      decided_at: NOW,
    });
    return this.ok(200, "AISuggestion", s, { ETag: `"s${s.version}"` });
  }
}

const etag = (t: Json) => `"r${t.revision}"`;
