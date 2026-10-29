/**
 * The slice of the Studio API (platform/api/openapi.yaml) the overlay calls,
 * with an in-context bearer token (RFC 0004 §5.2) and never with cookies.
 * Types mirror the spec's schemas of the same name, trimmed to the members
 * the overlay reads; `src/testing/contract.ts` checks the fake API's answers
 * against the real schemas.
 */
import type { Message as Model } from "@klarlabs-studio/glossa-runtime";

export type Syntax = "mf1" | "mf2";
export type ReviewState = "draft" | "needs_review" | "approved" | "rejected";

/** RFC 9457 problem details with Glossa's `code` and per-field `errors`. */
export interface Problem {
  type: string;
  title: string;
  status: number;
  code: string;
  detail?: string;
  errors?: Array<{ pointer: string; detail: string }>;
  findings?: QAFinding[];
}

export interface QAFinding {
  code: string;
  severity: "error" | "warning";
  subject?: string;
  message: string;
}

export interface MessageContent {
  text: string;
  syntax: Syntax;
  model: Model;
}

/** `Message`: `key` is the path segment every message endpoint takes. */
export interface Message {
  id: string;
  key: string;
  namespace: string;
  description: string;
  max_length?: number;
  state: "active" | "proposed" | "obsolete";
  source: MessageContent;
  source_revision: number;
}

export interface Translation {
  locale: string;
  text: string;
  syntax: Syntax;
  model: Model;
  state: ReviewState;
  origin: string;
  author: string;
  outdated: boolean;
  warnings: QAFinding[];
  revision: number;
  updated_at: string;
}

export interface TranslationRevision {
  revision: number;
  kind: "content" | "review";
  text: string;
  state: ReviewState;
  origin: string;
  origin_detail: Record<string, unknown>;
  author: string;
  created_at: string;
}

export interface MessagePreview {
  valid: boolean;
  message?: Model;
  errors: Array<{ stage: "parse" | "format"; code: string; message: string }>;
}

export interface TermFinding {
  code: "term_missing" | "term_forbidden";
  severity: "error" | "warning";
  side: "source" | "target";
  text: string;
  suggestions: string[];
  message: string;
}

export interface AISuggestion {
  id: string;
  locale: string;
  /** The translation in canonical MF2 syntax. */
  message: string;
  model: Model;
  score: number;
  action: string;
  status: string;
  findings: QAFinding[];
  term_findings: Array<{ message: string }>;
}

export interface AIFillPreview {
  warnings: string[];
  locales: Array<{
    locale: string;
    keys: string[];
    existing: number;
    tm_exact: number;
    provider: number;
    refused: Record<string, number>;
    skipped: Record<string, number>;
  }>;
}

export interface AIFill {
  id: string;
  jobs_created: number;
  jobs_existing: number;
  /** The jobs this fill queued or reused, so a caller polls those and not the whole list. */
  job_ids?: string[];
  warnings: string[];
}

/** A job's state, for following a fill the editor asked for. */
export interface AIJob {
  id: string;
  state: "queued" | "running" | "succeeded" | "skipped" | "failed" | "dead" | "cancelled";
  failure_code?: string;
  suggestion_id?: string;
}

/** What the in-context origin detail of an edit carries (RFC 0004 §5.3). */
export interface InContext {
  route: string;
  viewport: { width: number; height: number };
}

/**
 * Returns the current in-context bearer token. `@klarlabs-studio/glossa-runtime/dev`
 * backs it with Studio's authorization popup (RFC 0004 §5.2): the token
 * lives in memory, is renewed through the popup, and is asked for again
 * after `onAuthFailure`.
 */
export type TokenProvider = () => string | Promise<string>;

export interface ApiOptions {
  /** The API origin (and base path, if any), e.g. `https://studio.example.com`. */
  apiBase: string;
  token: TokenProvider;
  /**
   * Called once when the API refuses the token (401), so whoever holds
   * the grant drops it and the next call asks for a fresh one. The
   * failing request still fails; the editor offers to try again.
   */
  onAuthFailure?: () => void;
  tenant: string;
  project: string;
  fetch?: typeof fetch;
}

/** A non-2xx answer, with its problem details when the body had them. */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly problem?: Problem,
  ) {
    super(problem?.detail ?? problem?.title ?? `HTTP ${status}`);
    this.name = "ApiError";
  }
  get code(): string | undefined {
    return this.problem?.code;
  }
}

/** A resource and the ETag it came with. */
export interface Tagged<T> {
  value: T;
  etag?: string;
}

const seg = encodeURIComponent;

export class OverlayApi {
  private readonly base: string;
  private readonly fetch: typeof fetch;

  constructor(private readonly o: ApiOptions) {
    this.base = o.apiBase.replace(/\/+$/, "");
    this.fetch = o.fetch ?? ((input, init) => globalThis.fetch(input, init));
  }

  get tenant(): string {
    return this.o.tenant;
  }

  get project(): string {
    return this.o.project;
  }

  private projectPath(rest: string): string {
    return `/v1/tenants/${seg(this.o.tenant)}/projects/${seg(this.o.project)}${rest}`;
  }

  private translationPath(key: string, locale: string, rest = ""): string {
    return this.projectPath(`/messages/${seg(key)}/translations/${seg(locale)}${rest}`);
  }

  private async request<T>(
    method: string,
    path: string,
    { body, headers = {} }: { body?: unknown; headers?: Record<string, string> } = {},
  ): Promise<Tagged<T>> {
    const h: Record<string, string> = {
      Accept: "application/json",
      Authorization: `Bearer ${await this.o.token()}`,
      ...headers,
    };
    if (body !== undefined) h["Content-Type"] = "application/json";
    const res = await this.fetch(this.base + path, {
      method,
      headers: h,
      body: body === undefined ? undefined : JSON.stringify(body),
      credentials: "omit",
      mode: "cors",
    });
    const text = await res.text();
    let parsed: unknown;
    try {
      parsed = text ? JSON.parse(text) : undefined;
    } catch {
      parsed = undefined;
    }
    if (res.status === 401) this.o.onAuthFailure?.();
    if (!res.ok) throw new ApiError(res.status, parsed as Problem | undefined);
    return { value: parsed as T, etag: res.headers.get("ETag") ?? undefined };
  }

  getMessage(key: string): Promise<Tagged<Message>> {
    return this.request("GET", this.projectPath(`/messages/${seg(key)}`));
  }

  /** The translation, or `undefined` when the locale has none yet. */
  async getTranslation(key: string, locale: string): Promise<Tagged<Translation> | undefined> {
    try {
      return await this.request<Translation>("GET", this.translationPath(key, locale));
    } catch (e) {
      if (e instanceof ApiError && e.status === 404 && e.code !== "locale_not_found")
        return undefined;
      throw e;
    }
  }

  /** A human edit made in the product: provenance `human`, `origin_detail.in_context`. */
  putTranslation(
    key: string,
    locale: string,
    edit: { text: string; syntax: Syntax; inContext: InContext },
    ifMatch?: string,
  ): Promise<Tagged<Translation>> {
    return this.request("PUT", this.translationPath(key, locale), {
      body: {
        text: edit.text,
        syntax: edit.syntax,
        origin: "human",
        origin_detail: { in_context: edit.inContext },
      },
      headers: ifMatch ? { "If-Match": ifMatch } : {},
    });
  }

  async revisions(key: string, locale: string): Promise<TranslationRevision[]> {
    const r = await this.request<{ items: TranslationRevision[] }>(
      "GET",
      this.translationPath(key, locale, "/revisions?page_size=20"),
    );
    return r.value.items;
  }

  async previewMessage(source: string, syntax: Syntax, locale: string): Promise<MessagePreview> {
    return (
      await this.request<MessagePreview>("POST", "/v1/message-previews", {
        body: { source, syntax, locale },
      })
    ).value;
  }

  /** Terminology findings on this message's translation (empty when none). */
  /**
   * Terminology findings for one message. `key` asks about exactly this
   * key; `key_prefix` would also match every key below it, which is why
   * this used to fetch a page and filter it here.
   */
  async termFindings(key: string, locale: string): Promise<TermFinding[]> {
    const q = `?locale=${seg(locale)}&key=${seg(key)}&page_size=100`;
    const r = await this.request<{
      items: Array<{ message_key: string; findings: TermFinding[] }>;
    }>("GET", this.projectPath(`/terminology-findings${q}`));
    return r.value.items.find((i) => i.message_key === key)?.findings ?? [];
  }

  /** Pending suggestions for a message (by `id`) in a locale, newest first. */
  async suggestions(messageId: string, locale: string): Promise<AISuggestion[]> {
    const q = `?project=${seg(this.o.project)}&message=${seg(messageId)}&locale=${seg(locale)}&status=pending&page_size=5`;
    const r = await this.request<{ items: AISuggestion[] }>(
      "GET",
      `/v1/tenants/${seg(this.o.tenant)}/ai-suggestions${q}`,
    );
    return r.value.items;
  }

  /**
   * What asking for a suggestion would do, without doing it. It forces
   * for the same reason the fill does: the message on screen is current,
   * and a preview that didn't force would only ever answer "nothing to
   * translate" (RFC 0004 5.3).
   */
  async previewFill(key: string, locale: string): Promise<AIFillPreview> {
    return (
      await this.request<AIFillPreview>("POST", this.projectPath("/ai-fill-previews"), {
        body: { locales: [locale], keys: [key], force: true },
      })
    ).value;
  }

  /**
   * Ask for a suggestion for one message. `force` is what makes this
   * work at all from the editor: someone is reading the translation, so
   * it is current by definition, and a plain fill would skip it as
   * `up_to_date` (RFC 0004 §5.3).
   */
  async fill(key: string, locale: string): Promise<AIFill> {
    return (
      await this.request<AIFill>("POST", this.projectPath("/ai-fills"), {
        body: { locales: [locale], keys: [key], force: true },
        headers: { "Idempotency-Key": crypto.randomUUID() },
      })
    ).value;
  }

  /** One job of a fill, for following the jobs a fill named. */
  async job(id: string): Promise<AIJob> {
    return (await this.request<AIJob>("GET", `/v1/tenants/${seg(this.o.tenant)}/ai-jobs/${seg(id)}`)).value;
  }

  /**
   * Accept a suggestion as is, or an edit of it (`text`), which records
   * the edit for the metrics. `inContext` puts the route and viewport on
   * the revision's `origin_detail`, so history says the text was decided
   * in the running product and on which screen (RFC 0004 §5.3).
   */
  async accept(
    id: string,
    edit: { text: string; syntax: Syntax } | undefined,
    inContext: InContext,
  ): Promise<AISuggestion> {
    return (
      await this.request<AISuggestion>(
        "POST",
        `/v1/tenants/${seg(this.o.tenant)}/ai-suggestions/${seg(id)}/acceptance`,
        { body: { ...(edit ?? {}), in_context: inContext } },
      )
    ).value;
  }
}
