/**
 * The in-context grant, from the product's side (RFC 0004 §5.2).
 *
 * The editor runs on the product's own page, so it has no Studio session
 * to use. It opens a popup on Studio instead — `/in-context/authorize` —
 * and Studio, which does have the session, posts a short-lived token
 * back. The token stays in this closure: never `localStorage`, never
 * `sessionStorage`, never a cookie, never the URL. When it is close to
 * expiring the next request opens the popup again.
 *
 * Both ends check each other, and neither trusts the other's word for
 * who it is:
 *
 *   - Studio posts to the exact origin that asked, never `"*"`.
 *   - This side accepts a message only from Studio's own origin, only
 *     while a popup it opened is outstanding, and only when the message
 *     carries a `channel` nonce this request generated. A stale answer,
 *     a second popup's answer, or an unrelated `postMessage` from the
 *     same origin is ignored.
 *
 * A popup is a user gesture in every browser that matters, so the flow
 * only ever starts from a click or a keystroke — which it does: the
 * editor asks for a token when someone opens the panel.
 */

/** The envelope Studio posts back. Studio keeps the same constant. */
export const GRANT_MESSAGE = "glossa.in-context-grant";

/** Where Studio serves the authorization popup. */
export const AUTHORIZE_PATH = "/in-context/authorize";

/** How long to wait for the person to decide before giving up. */
const POPUP_TIMEOUT = 3 * 60_000;

/**
 * How long before expiry a grant is renewed. A save that starts just
 * inside the window must not finish just outside it.
 */
const RENEW_BEFORE = 60_000;

/** How often to notice the popup was closed without answering. */
const CLOSED_POLL = 400;

export interface GrantOptions {
  /** Studio's origin, which serves the popup and receives nothing else. */
  studio: string;
  tenant: string;
  project: string;
  /** Defaults to the global window. */
  window?: Window;
  /** Defaults to `crypto.randomUUID()`. */
  nonce?: () => string;
  /** Defaults to `Date.now()`. */
  now?: () => number;
}

interface Held {
  token: string;
  /** Epoch milliseconds. */
  expires: number;
}

/**
 * Thrown when no grant was obtained. `code` is Studio's problem code,
 * `cancelled` when the person declined, `popup_blocked` when the browser
 * refused to open the window, or `timeout`.
 */
export class GrantError extends Error {
  constructor(
    readonly code: string,
    message: string,
  ) {
    super(message);
    this.name = "GrantError";
  }
}

/** A token provider the overlay can call on every request. */
export interface GrantProvider {
  (): Promise<string>;
  /** Forget the held grant, so the next call asks again (after a 401). */
  invalidate(): void;
  /** Whether a grant is held and still good. */
  readonly held: boolean;
}

/**
 * A token provider backed by Studio's popup. One popup at a time: calls
 * that arrive while one is open wait for the same answer.
 */
export function popupGrant(options: GrantOptions): GrantProvider {
  const win = options.window ?? (globalThis as { window?: Window }).window;
  const studio = new URL(options.studio).origin;
  const nonce = options.nonce ?? (() => globalThis.crypto.randomUUID());
  const now = options.now ?? (() => Date.now());

  let held: Held | undefined;
  let asking: Promise<string> | undefined;

  const fresh = (): string | undefined =>
    held && held.expires - now() > RENEW_BEFORE ? held.token : undefined;

  const provider = (async (): Promise<string> => {
    const have = fresh();
    if (have) return have;
    held = undefined;
    return (asking ??= ask().finally(() => (asking = undefined)));
  }) as GrantProvider;

  function ask(): Promise<string> {
    if (!win) return Promise.reject(new GrantError("no_window", "the editor needs a browser window"));
    const channel = nonce();
    const url = new URL(AUTHORIZE_PATH, studio);
    url.searchParams.set("tenant", options.tenant);
    url.searchParams.set("project", options.project);
    url.searchParams.set("origin", win.location.origin);
    url.searchParams.set("channel", channel);

    // A named window would be reused across attempts and could be
    // navigated by anything else that knows the name; "_blank" is a
    // fresh one every time.
    const popup = win.open(url.href, "_blank", "popup=yes,width=460,height=620");
    if (!popup) {
      return Promise.reject(
        new GrantError("popup_blocked", "allow pop-ups from this site to sign in to the editor"),
      );
    }

    return new Promise<string>((resolve, reject) => {
      let settled = false;
      const finish = (fn: () => void) => {
        if (settled) return;
        settled = true;
        win.removeEventListener("message", onMessage);
        clearInterval(closedTimer);
        clearTimeout(timeoutTimer);
        fn();
      };

      function onMessage(event: MessageEvent): void {
        // Three checks, all of them necessary: the origin (only Studio
        // ever sends this), the popup we opened (not some other frame on
        // Studio's origin), and the nonce (this request's answer, not an
        // earlier one's).
        if (event.origin !== studio) return;
        if (event.source !== null && event.source !== popup) return;
        const data = event.data as Record<string, unknown> | null;
        if (typeof data !== "object" || data === null) return;
        if (data.type !== GRANT_MESSAGE || data.channel !== channel) return;

        if (data.ok !== true) {
          const code = typeof data.code === "string" ? data.code : "denied";
          const message = typeof data.message === "string" ? data.message : "editing was not allowed";
          finish(() => reject(new GrantError(code, message)));
          return;
        }
        const expires = typeof data.expires_at === "string" ? Date.parse(data.expires_at) : NaN;
        if (typeof data.token !== "string" || Number.isNaN(expires)) {
          finish(() => reject(new GrantError("malformed_grant", "Studio sent an answer we don't understand")));
          return;
        }
        const token = data.token;
        held = { token, expires };
        finish(() => resolve(token));
      }

      win.addEventListener("message", onMessage);

      // The person may close the window instead of answering. There is
      // no event for that across origins, so it is noticed by polling.
      const closedTimer = setInterval(() => {
        if (popup.closed) {
          finish(() => reject(new GrantError("cancelled", "the sign-in window was closed")));
        }
      }, CLOSED_POLL);

      const timeoutTimer = setTimeout(() => {
        finish(() => {
          popup.close(); // a window this page opened is this page's to close
          reject(new GrantError("timeout", "signing in to the editor took too long"));
        });
      }, POPUP_TIMEOUT);
    });
  }

  provider.invalidate = () => {
    held = undefined;
  };
  Object.defineProperty(provider, "held", { get: () => fresh() !== undefined });
  return provider;
}
