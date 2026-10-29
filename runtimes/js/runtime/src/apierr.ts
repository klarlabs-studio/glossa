/**
 * `@klarlabs-studio/glossa/apierr`: the client side of the Go `apierr` module's envelope.
 *
 * ```json
 * { "error": { "code": "validation_email_required", "message": "Email address is required",
 *              "key": "validation.email.required", "params": { "field": "email" }, "status": 400 } }
 * ```
 *
 * `parseApiError` reads the envelope (also a bare payload, or a pre-apierr `{ "error": "text" }`),
 * `apiErrorMessage` turns it into a message key plus arguments, and `resolveApiError` formats it
 * with a runtime, falling back to the server's English `message`. Nothing here throws on malformed
 * input: the caller already has a failing request to deal with.
 */

/** The wire shape of one error from a Glossa-aware backend (Go `apierr.Error`). */
export interface ApiErrorPayload {
  code: string;
  message: string;
  key: string;
  params?: Record<string, unknown>;
  status: number;
}

/** What to render: the message key, its arguments, and the server's English text for when the key is unknown. */
export interface ApiErrorMessage {
  /** The Glossa message id; empty for a server that sent no key. */
  id: string;
  args: Record<string, unknown>;
  /** The server's English `message`. */
  fallback: string;
}

/** The part of a runtime `resolveApiError` uses: any `Runtime`, the Vue/React/Astro ones included. */
export interface ApiErrorRuntime {
  t(id: string, values?: Record<string, unknown>): string;
  has(id: string): boolean;
}

export interface ResolveApiErrorOptions {
  /** Rendered when `input` is not an error envelope, or carries no text. Default `"Unknown error"`. */
  unknown?: string;
}

const isRecord = (v: unknown): v is Record<string, unknown> => !!v && typeof v === "object";

const fromRecord = (e: Record<string, unknown>): ApiErrorPayload | null =>
  typeof e.code === "string" && typeof e.message === "string"
    ? {
        code: e.code,
        message: e.message,
        key: typeof e.key === "string" ? e.key : "",
        params: isRecord(e.params) ? e.params : undefined,
        status: typeof e.status === "number" ? e.status : 500,
      }
    : null;

/**
 * The payload of an error response body: the canonical envelope, the bare payload, or the
 * pre-apierr `{ "error": "text" }`; `null` for anything else.
 */
export function parseApiError(input: unknown): ApiErrorPayload | null {
  if (!isRecord(input)) return null;
  if (typeof input.error === "string") {
    return { code: "unknown_error", message: input.error, key: "", status: 500 };
  }
  return fromRecord(isRecord(input.error) ? input.error : input);
}

/** The message key and arguments for an error response body; `null` when it isn't one. */
export function apiErrorMessage(input: unknown): ApiErrorMessage | null {
  const payload = parseApiError(input);
  return payload && { id: payload.key, args: payload.params ?? {}, fallback: payload.message };
}

/**
 * The localized text for an error response body: the payload's `key` formatted with its `params`
 * when the runtime's release has it, else the server's English `message`, else `unknown`.
 */
export function resolveApiError(
  runtime: ApiErrorRuntime,
  input: unknown,
  options: ResolveApiErrorOptions = {},
): string {
  const m = apiErrorMessage(input);
  if (m?.id && runtime.has(m.id)) return runtime.t(m.id, m.args);
  return m?.fallback || options.unknown || "Unknown error";
}
