/**
 * The message the authorize popup posts back to the page that opened it
 * (RFC 0004 §5.2).
 *
 * This is a security boundary, so it is small and it is checked on both
 * sides. Studio posts to the exact origin it was asked to authorize and
 * to no other; the editor accepts a message only from Studio's own
 * origin, only through its own `window.opener` handshake, and only when
 * it carries this envelope's `type` and `channel`. The `channel` is a
 * nonce the opener generates: it ties an answer to the request that
 * asked for it, so a second popup, a stale message or an unrelated
 * `postMessage` from the same origin is ignored rather than acted on.
 *
 * The runtime keeps its own copy of these constants
 * (`runtimes/js/runtime/src/dev.ts`) because the two ship separately and
 * a shared import would tie the product's bundle to Studio's. The names
 * and the shape are the contract; changing either is a breaking change
 * for every deployed overlay.
 */

/** The envelope's discriminator. Never reused for anything else. */
export const IN_CONTEXT_MESSAGE = "glossa.in-context-grant";

/** A grant, on its way to the page that asked for it. */
export interface InContextGrantMessage {
  type: typeof IN_CONTEXT_MESSAGE;
  /** The opener's nonce, echoed back. */
  channel: string;
  ok: true;
  token: string;
  /** RFC 3339; the editor renews through the popup before this passes. */
  expires_at: string;
  project_id: string;
  origin: string;
}

/** Why no grant was minted. The editor shows this and offers to retry. */
export interface InContextDeniedMessage {
  type: typeof IN_CONTEXT_MESSAGE;
  channel: string;
  ok: false;
  /** A stable problem code, or `cancelled` when the person declined. */
  code: string;
  message: string;
}

export type InContextMessage = InContextGrantMessage | InContextDeniedMessage;

/** Whether a value is one of our envelopes for `channel`. */
export function isInContextMessage(value: unknown, channel: string): value is InContextMessage {
  if (typeof value !== "object" || value === null) return false;
  const m = value as Partial<InContextMessage>;
  return m.type === IN_CONTEXT_MESSAGE && m.channel === channel && typeof m.ok === "boolean";
}
