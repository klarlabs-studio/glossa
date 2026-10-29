import { beforeEach, describe, expect, it, vi } from "vitest";
import { AUTHORIZE_PATH, GRANT_MESSAGE, GrantError, popupGrant } from "./grant.js";

/**
 * The in-context grant, from the product's side (RFC 0004 §5.2).
 *
 * Everything here runs against a stub window: no real popup is ever
 * opened, so the tests are the same in CI as on a laptop. What they pin
 * is the part that has to be right — which messages are believed and
 * which are ignored — plus renewal and the single-flight behaviour.
 */

const STUDIO = "https://studio.glossa.test";
const APP = "https://preview.example.test";

interface Stub {
  win: Window;
  /** The URLs window.open was asked for. */
  opened: string[];
  /** Deliver a message event to the listeners the provider installed. */
  send(data: unknown, over?: { origin?: string; source?: unknown }): void;
  /** Make the popup report itself closed. */
  close(): void;
  /** Whether a listener is still installed (nothing leaks after settling). */
  listening(): boolean;
  blockPopups: boolean;
}

function stubWindow(): Stub {
  const listeners = new Set<(e: MessageEvent) => void>();
  const popup = { closed: false, close: vi.fn() };
  const stub: Stub = {
    opened: [],
    blockPopups: false,
    win: {
      location: { origin: APP },
      open: (url: string) => {
        stub.opened.push(url);
        return stub.blockPopups ? null : popup;
      },
      addEventListener: (type: string, fn: (e: MessageEvent) => void) => {
        if (type === "message") listeners.add(fn);
      },
      removeEventListener: (type: string, fn: (e: MessageEvent) => void) => {
        if (type === "message") listeners.delete(fn);
      },
    } as unknown as Window,
    send(data, over = {}) {
      const event = { data, origin: over.origin ?? STUDIO, source: "source" in over ? over.source : popup };
      for (const fn of [...listeners]) fn(event as MessageEvent);
    },
    close() {
      popup.closed = true;
    },
    listening: () => listeners.size > 0,
  };
  return stub;
}

const NOW = Date.UTC(2026, 8, 20, 12, 0, 0);

/**
 * The error a call rejects with, with the handler attached before the
 * rejection can happen — otherwise a synchronous `send` settles the
 * promise before `expect(...).rejects` is looking, and Node reports an
 * unhandled rejection for something the test does handle.
 */
const rejection = (p: Promise<unknown>): Promise<GrantError> =>
  p.then(
    () => {
      throw new Error("expected a rejection");
    },
    (e: GrantError) => e,
  );

function grantFor(stub: Stub, now = () => NOW) {
  let n = 0;
  return popupGrant({
    studio: STUDIO,
    tenant: "ten_1",
    project: "prj_1",
    window: stub.win,
    nonce: () => `nonce-${++n}`,
    now,
  });
}

const granted = (over: Record<string, unknown> = {}) => ({
  type: GRANT_MESSAGE,
  channel: "nonce-1",
  ok: true,
  token: "glossa_ctx_" + "t".repeat(43),
  expires_at: new Date(NOW + 15 * 60_000).toISOString(),
  project_id: "prj_1",
  origin: APP,
  ...over,
});

describe("popupGrant", () => {
  let stub: Stub;

  beforeEach(() => {
    vi.useRealTimers();
    stub = stubWindow();
  });

  it("opens Studio's authorize page with this page's origin and a nonce", async () => {
    const grant = grantFor(stub);
    const p = grant();
    expect(stub.opened).toHaveLength(1);
    const url = new URL(stub.opened[0]!);
    expect(url.origin).toBe(STUDIO);
    expect(url.pathname).toBe(AUTHORIZE_PATH);
    expect(url.searchParams.get("tenant")).toBe("ten_1");
    expect(url.searchParams.get("project")).toBe("prj_1");
    expect(url.searchParams.get("origin")).toBe(APP);
    expect(url.searchParams.get("channel")).toBe("nonce-1");
    // The token is never in the URL.
    expect(url.search).not.toContain("glossa_ctx_");

    stub.send(granted());
    await expect(p).resolves.toMatch(/^glossa_ctx_/);
    expect(stub.listening()).toBe(false);
  });

  it("ignores a message from any origin but Studio's", async () => {
    const grant = grantFor(stub);
    const p = grant();

    stub.send(granted({ token: "stolen" }), { origin: "https://evil.example.test" });
    stub.send(granted({ token: "stolen" }), { origin: "http://studio.glossa.test" });
    stub.send(granted({ token: "stolen" }), { origin: "null" });
    expect(stub.listening()).toBe(true);

    stub.send(granted());
    await expect(p).resolves.toMatch(/^glossa_ctx_/);
  });

  it("ignores a message from another window on Studio's origin", async () => {
    const grant = grantFor(stub);
    const p = grant();

    stub.send(granted({ token: "stolen" }), { source: { other: true } });
    expect(stub.listening()).toBe(true);

    stub.send(granted());
    await expect(p).resolves.toMatch(/^glossa_ctx_/);
  });

  it("ignores an answer to a different request, or a shape it doesn't know", async () => {
    const grant = grantFor(stub);
    const p = grant();

    stub.send(granted({ channel: "nonce-2", token: "stolen" })); // another popup's answer
    stub.send({ type: "something.else", channel: "nonce-1", ok: true, token: "stolen" });
    stub.send("glossa_ctx_plain_string");
    stub.send(null);
    expect(stub.listening()).toBe(true);

    stub.send(granted());
    await expect(p).resolves.toMatch(/^glossa_ctx_/);
  });

  it("refuses an answer that says it granted but carries no usable grant", async () => {
    for (const bad of [
      granted({ token: undefined }),
      granted({ token: 42 }),
      granted({ expires_at: undefined }),
      granted({ expires_at: "not a date" }),
    ]) {
      const s = stubWindow();
      const grant = grantFor(s);
      const failed = rejection(grant());
      s.send(bad);
      const e = await failed;
      expect(e).toBeInstanceOf(GrantError);
      expect(e.code).toBe("malformed_grant");
    }
  });

  it("passes a refusal through with Studio's code", async () => {
    const grant = grantFor(stub);
    const failed = rejection(grant());
    stub.send({
      type: GRANT_MESSAGE,
      channel: "nonce-1",
      ok: false,
      code: "origin_not_registered",
      message: "not on the list",
    });
    const e = await failed;
    expect(e.code).toBe("origin_not_registered");
    expect(e.message).toBe("not on the list");
    expect(grant.held).toBe(false);
  });

  it("says so when the browser blocks the popup", async () => {
    stub.blockPopups = true;
    const grant = grantFor(stub);
    expect((await rejection(grant())).code).toBe("popup_blocked");
  });

  it("notices the window being closed without an answer", async () => {
    vi.useFakeTimers();
    const grant = grantFor(stub);
    const failed = rejection(grant());
    stub.close();
    await vi.advanceTimersByTimeAsync(1000);
    expect((await failed).code).toBe("cancelled");
    expect(stub.listening()).toBe(false);
    vi.useRealTimers();
  });

  it("holds the grant in memory and opens no second popup while it is good", async () => {
    let clock = NOW;
    const grant = grantFor(stub, () => clock);
    const p = grant();
    stub.send(granted());
    const token = await p;

    // Thirteen minutes later it is still used as it is.
    clock = NOW + 13 * 60_000;
    await expect(grant()).resolves.toBe(token);
    expect(stub.opened).toHaveLength(1);
    expect(grant.held).toBe(true);
  });

  it("renews through the popup before the grant expires, not after", async () => {
    let clock = NOW;
    const grant = grantFor(stub, () => clock);
    const p = grant();
    stub.send(granted());
    await p;

    // Inside the last minute the next call asks again, so a request that
    // starts now cannot finish with an expired grant.
    clock = NOW + 14 * 60_000 + 30_000;
    expect(grant.held).toBe(false);
    const again = grant();
    expect(stub.opened).toHaveLength(2);
    stub.send(granted({ channel: "nonce-2", token: "glossa_ctx_" + "u".repeat(43) }));
    await expect(again).resolves.toMatch(/^glossa_ctx_u/);
  });

  it("opens one popup for calls that overlap", async () => {
    const grant = grantFor(stub);
    const a = grant();
    const b = grant();
    const c = grant();
    expect(stub.opened).toHaveLength(1);
    stub.send(granted());
    const [ta, tb, tc] = await Promise.all([a, b, c]);
    expect(ta).toBe(tb);
    expect(tb).toBe(tc);
  });

  it("asks again after invalidate, which is what a 401 triggers", async () => {
    const grant = grantFor(stub);
    const p = grant();
    stub.send(granted());
    await p;
    expect(grant.held).toBe(true);

    grant.invalidate();
    expect(grant.held).toBe(false);
    const again = grant();
    expect(stub.opened).toHaveLength(2);
    stub.send(granted({ channel: "nonce-2" }));
    await expect(again).resolves.toMatch(/^glossa_ctx_/);
  });

  it("gives up rather than waiting forever", async () => {
    vi.useFakeTimers();
    const grant = grantFor(stub);
    const failed = rejection(grant());
    await vi.advanceTimersByTimeAsync(3 * 60_000 + 1000);
    expect((await failed).code).toBe("timeout");
    expect(stub.listening()).toBe(false);
    vi.useRealTimers();
  });
});
