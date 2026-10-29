/**
 * The typed HTTP client: openapi-fetch over the generated contract, plus
 * the session conventions of platform/README.md — the cookie travels on
 * its own (same origin), unsafe requests carry `X-CSRF-Token`, and a 401
 * outside the auth endpoints means the session is gone.
 */
import createClient, { type Client, type Middleware } from "openapi-fetch";
import type { paths } from "./schema";

const UNSAFE = new Set(["POST", "PUT", "PATCH", "DELETE"]);

let csrfToken: string | undefined;
const expiredListeners = new Set<() => void>();

export function setCsrfToken(token: string | undefined): void {
  csrfToken = token;
}

/** The session's CSRF token, for the few requests that don't go through the typed client (uploads with progress). */
export function currentCsrfToken(): string | undefined {
  return csrfToken;
}

/** Called when a request that needed a session got a 401. */
export function onSessionExpired(listener: () => void): () => void {
  expiredListeners.add(listener);
  return () => expiredListeners.delete(listener);
}

export const csrf: Middleware = {
  onRequest({ request }) {
    if (csrfToken && UNSAFE.has(request.method)) request.headers.set("X-CSRF-Token", csrfToken);
    return request;
  },
};

/** Tell the app a request that needed a session got a 401 (outside the auth endpoints). */
export function reportUnauthenticated(path: string): void {
  if (path.startsWith("/v1/auth/") || path === "/v1/me") return;
  for (const l of expiredListeners) l();
}

export const sessionExpiry: Middleware = {
  onResponse({ request, response }) {
    if (response.status === 401) reportUnauthenticated(new URL(request.url).pathname);
    return response;
  },
};

export type ApiClient = Client<paths>;

export function createApiClient(options: { baseUrl?: string; fetch?: typeof fetch } = {}): ApiClient {
  const client = createClient<paths>({
    baseUrl: options.baseUrl ?? globalThis.location?.origin ?? "",
    credentials: "same-origin",
    // Late-bound so tests (and polyfills) can replace globalThis.fetch.
    fetch: options.fetch ?? ((input: Request) => globalThis.fetch(input)),
  });
  client.use(csrf, sessionExpiry);
  return client;
}

export const client: ApiClient = createApiClient();
