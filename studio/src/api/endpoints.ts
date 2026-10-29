/**
 * One function per /v1 operation Studio uses. Each validates what it
 * returns (./schemas) and hands back ETags where the contract has them.
 */
import { client } from "./client";
import { ApiError, done, read, type Versioned } from "./errors";
import type { components } from "./schema";
import * as S from "./schemas";

type Body<K extends keyof components["schemas"]> = components["schemas"][K];
type Path = { tenant: string; project: string };
type MessagePath = Path & { message: string };
type TranslationPath = MessagePath & { locale: string };

const value = async <T>(p: Promise<Versioned<T>>): Promise<T> => (await p).value;

/** Fetch every page of a list operation. */
export async function all<T>(fetchPage: (pageToken?: string) => Promise<{ items: T[]; next_page_token?: string | undefined }>): Promise<T[]> {
  const out: T[] = [];
  let token: string | undefined;
  do {
    const page = await fetchPage(token);
    out.push(...page.items);
    token = page.next_page_token;
  } while (token);
  return out;
}

const PAGE = 100;

// ── meta ──────────────────────────────────────────────────────────────
export const meta = {
  /** What this deployment offers: sign-in methods, email, glossa-edge's URL. Public. */
  get: () => value(read(client.GET("/v1/meta"), S.Meta)),
};

// ── auth ──────────────────────────────────────────────────────────────
export const auth = {
  requestMagicLink: (email: string) => done(client.POST("/v1/auth/magic-links", { body: { email } })),
  redeemMagicLink: (token: string) =>
    value(read(client.POST("/v1/auth/magic-link-redemptions", { body: { token } }), S.Session)),
  register: (body: Body<"Registration">) => done(client.POST("/v1/auth/registrations", { body })),
  signInWithPassword: (body: Body<"PasswordSignIn">) =>
    value(read(client.POST("/v1/auth/password-sessions", { body }), S.Session)),
  requestPasswordReset: (email: string) => done(client.POST("/v1/auth/password-resets", { body: { email } })),
  resetPassword: (token: string, password: string) =>
    done(client.POST("/v1/auth/password-reset-redemptions", { body: { token, password } })),
  beginPasskeySignIn: (email: string) =>
    value(read(client.POST("/v1/auth/passkey-challenges", { body: { email } }), S.PasskeyOptions)),
  finishPasskeySignIn: (credential: Record<string, unknown>) =>
    value(read(client.POST("/v1/auth/passkey-sessions", { body: credential, params: {} }), S.Session)),
  signOut: () => done(client.DELETE("/v1/auth/session")),
  signOutEverywhere: () => done(client.DELETE("/v1/auth/sessions")),
};

// ── me ────────────────────────────────────────────────────────────────
export const me = {
  get: () => value(read(client.GET("/v1/me"), S.Me)),
  beginPasskey: () => value(read(client.POST("/v1/me/passkey-challenges"), S.PasskeyOptions)),
  /** Every passkey of the signed-in person, on any device, oldest first. */
  passkeys: () =>
    all((page_token) => value(read(client.GET("/v1/me/passkeys", { params: { query: { page_size: PAGE, page_token } } }), S.page(S.Passkey)))),
  deletePasskey: (passkey: string) => done(client.DELETE("/v1/me/passkeys/{passkey}", { params: { path: { passkey } } })),
  finishPasskey: (name: string, credential: Record<string, unknown>) =>
    value(read(client.POST("/v1/me/passkeys", { body: { name, credential }, params: {} }), S.Passkey)),
  beginTotp: () => value(read(client.PUT("/v1/me/totp"), S.TotpEnrollment)),
  confirmTotp: (code: string) => done(client.POST("/v1/me/totp/confirmation", { body: { code } })),
  disableTotp: (code: string) => done(client.POST("/v1/me/totp/deactivation", { body: { code } })),
};

// ── tenants ───────────────────────────────────────────────────────────
export const tenants = {
  create: (body: Body<"CreateTenant">) => value(read(client.POST("/v1/tenants", { body }), S.Tenant)),
};

// ── projects & applications ───────────────────────────────────────────
export const projects = {
  list: (tenant: string) =>
    all((page_token) =>
      value(
        read(
          client.GET("/v1/tenants/{tenant}/projects", { params: { path: { tenant }, query: { page_size: PAGE, page_token } } }),
          S.page(S.Project),
        ),
      ),
    ),
  create: (tenant: string, body: Body<"CreateProject">) =>
    read(client.POST("/v1/tenants/{tenant}/projects", { params: { path: { tenant } }, body }), S.Project),
  get: (p: Path) => read(client.GET("/v1/tenants/{tenant}/projects/{project}", { params: { path: p } }), S.Project),
  update: (p: Path, body: Body<"UpdateProject">, etag: string) =>
    read(
      client.PATCH("/v1/tenants/{tenant}/projects/{project}", { params: { path: p, header: { "If-Match": etag } }, body }),
      S.Project,
    ),
  remove: (p: Path, etag?: string) =>
    done(
      client.DELETE("/v1/tenants/{tenant}/projects/{project}", {
        params: { path: p, header: etag ? { "If-Match": etag } : {} },
      }),
    ),
};

export const applications = {
  list: (p: Path) =>
    all((page_token) =>
      value(
        read(
          client.GET("/v1/tenants/{tenant}/projects/{project}/applications", {
            params: { path: p, query: { page_size: PAGE, page_token } },
          }),
          S.page(S.Application),
        ),
      ),
    ),
  create: (p: Path, body: Body<"CreateApplication">) =>
    value(read(client.POST("/v1/tenants/{tenant}/projects/{project}/applications", { params: { path: p }, body }), S.Application)),
  remove: (p: Path & { application: string }) =>
    done(
      client.DELETE("/v1/tenants/{tenant}/projects/{project}/applications/{application}", { params: { path: p, header: {} } }),
    ),
};

// ── messages ──────────────────────────────────────────────────────────
export interface MessageFilters {
  namespace?: string | undefined;
  state?: S.Message["state"] | undefined;
  key_prefix?: string | undefined;
  missing_in?: string | undefined;
  outdated_in?: string | undefined;
}

const compact = <T extends object>(o: T): T =>
  Object.fromEntries(Object.entries(o).filter(([, v]) => v !== undefined && v !== "")) as T;

export const messages = {
  page: (p: Path, filters: MessageFilters, page_token?: string, signal?: AbortSignal) =>
    value(
      read(
        client.GET("/v1/tenants/{tenant}/projects/{project}/messages", {
          params: { path: p, query: { ...compact(filters), page_size: PAGE, page_token } },
          ...(signal ? { signal } : {}),
        }),
        S.page(S.Message),
      ),
    ),
  get: (p: MessagePath) =>
    read(client.GET("/v1/tenants/{tenant}/projects/{project}/messages/{message}", { params: { path: p } }), S.Message),
  upsert: (p: Path, items: Body<"MessageUpsertItem">[]) =>
    value(
      read(client.POST("/v1/tenants/{tenant}/projects/{project}/message-upserts", { params: { path: p }, body: { items } }), S.MessageUpsertResult),
    ),
  sourceRevisions: (p: MessagePath) =>
    all((page_token) =>
      value(
        read(
          client.GET("/v1/tenants/{tenant}/projects/{project}/messages/{message}/source-revisions", {
            params: { path: p, query: { page_size: PAGE, page_token } },
          }),
          S.page(S.SourceRevision),
        ),
      ),
    ),
};

// ── locales & fallback graph ──────────────────────────────────────────
export const locales = {
  list: (p: Path) =>
    all((page_token) =>
      value(
        read(
          client.GET("/v1/tenants/{tenant}/projects/{project}/locales", { params: { path: p, query: { page_size: PAGE, page_token } } }),
          S.page(S.ProjectLocale),
        ),
      ),
    ),
  add: (p: Path, code: string) =>
    value(read(client.POST("/v1/tenants/{tenant}/projects/{project}/locales", { params: { path: p }, body: { code } }), S.ProjectLocale)),
  remove: (p: Path & { locale: string }) =>
    done(client.DELETE("/v1/tenants/{tenant}/projects/{project}/locales/{locale}", { params: { path: p } })),
  fallback: (p: Path) =>
    read(client.GET("/v1/tenants/{tenant}/projects/{project}/fallback-graph", { params: { path: p } }), S.FallbackGraph),
  putFallback: (p: Path, fallback: Record<string, string[]>, etag: string | undefined) =>
    read(
      client.PUT("/v1/tenants/{tenant}/projects/{project}/fallback-graph", {
        params: { path: p, header: etag ? { "If-Match": etag } : {} },
        body: { fallback },
      }),
      S.FallbackGraph,
    ),
};

// ── message preview ───────────────────────────────────────────────────
export const preview = {
  /** Parse (and with values, format) a message with the server's kernel — the one MF1 converter. */
  message: (body: Body<"MessagePreviewRequest">, signal?: AbortSignal) =>
    value(read(client.POST("/v1/message-previews", { body, ...(signal ? { signal } : {}) }), S.MessagePreview)),
};

// ── translations ──────────────────────────────────────────────────────
export const translations = {
  /** The translation, or null when the message has none in this locale yet. */
  get: async (p: TranslationPath): Promise<Versioned<S.Translation> | null> => {
    try {
      return await read(
        client.GET("/v1/tenants/{tenant}/projects/{project}/messages/{message}/translations/{locale}", { params: { path: p } }),
        S.Translation,
      );
    } catch (e) {
      if (e instanceof ApiError && e.status === 404 && e.code === "not_found") return null;
      throw e;
    }
  },
  put: (p: TranslationPath, body: Body<"PutTranslation">, etag: string | undefined) =>
    read(
      client.PUT("/v1/tenants/{tenant}/projects/{project}/messages/{message}/translations/{locale}", {
        params: { path: p, header: etag ? { "If-Match": etag } : {} },
        body,
      }),
      S.Translation,
    ),
  review: (p: TranslationPath, state: S.ReviewState, etag: string) =>
    read(
      client.POST("/v1/tenants/{tenant}/projects/{project}/messages/{message}/translations/{locale}/reviews", {
        params: { path: p, header: { "If-Match": etag } },
        body: { state },
      }),
      S.Translation,
    ),
  /** One page of the project-wide listing: translations of active messages in one locale. */
  projectPage: (p: Path, locale: string, page_token?: string, signal?: AbortSignal) =>
    value(
      read(
        client.GET("/v1/tenants/{tenant}/projects/{project}/translations", {
          params: { path: p, query: { locale: [locale], message_state: "active", page_size: PAGE, page_token } },
          ...(signal ? { signal } : {}),
        }),
        S.page(S.ProjectTranslation),
      ),
    ),
  /** Per-locale counts: translated, missing, outdated, review states. */
  stats: (p: Path, signal?: AbortSignal) =>
    value(read(client.GET("/v1/tenants/{tenant}/projects/{project}/translation-stats", { params: { path: p }, ...(signal ? { signal } : {}) }), S.TranslationStats)),
  revisions: (p: TranslationPath) =>
    all((page_token) =>
      value(
        read(
          client.GET("/v1/tenants/{tenant}/projects/{project}/messages/{message}/translations/{locale}/revisions", {
            params: { path: p, query: { page_size: PAGE, page_token } },
          }),
          S.page(S.TranslationRevision),
        ),
      ),
    ),
};

// ── members (to name the people behind `person:<id>`) ─────────────────
export const members = {
  list: (tenant: string) =>
    all((page_token) =>
      value(read(client.GET("/v1/tenants/{tenant}/members", { params: { path: { tenant }, query: { page_size: PAGE, page_token } } }), S.page(S.Member))),
    ),
};
