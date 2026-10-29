import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { createMemoryHistory, createRouter } from "vue-router";
import { defineComponent } from "vue";
import { IN_CONTEXT } from "../../api/in-context";
import { IN_CONTEXT_MESSAGE } from "../../lib/in-context-message";
import { fakeInContext, previewOrigin, type FakeInContext } from "../../test/fake-in-context";
import { refreshSession } from "../../session/session";
import AuthorizeView from "./AuthorizeView.vue";

/**
 * The authorization popup (RFC 0004 §5.2). The dangerous parts are the
 * ones tested: nothing is minted before the person confirms, the grant
 * goes to the exact origin that asked and never to "*", and a request
 * that doesn't name a real origin mints nothing at all.
 */

const ME = {
  person: {
    id: "me",
    email: "ada@example.com",
    email_verified: true,
    totp_enabled: false,
    individual_tenant_id: "t",
    created_at: "2026-09-01T00:00:00Z",
  },
  csrf_token: "csrf",
  memberships: [],
};

const ORIGIN = "https://preview.example.com";
const Empty = defineComponent({ template: "<div />" });

interface Posted {
  message: unknown;
  targetOrigin: string;
}

async function open(port: FakeInContext, query: Record<string, string>): Promise<{ w: VueWrapper; posted: Posted[] }> {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      return new Response(JSON.stringify(path === "/v1/me" ? ME : { items: [] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
  await refreshSession();

  // Only `opener` and `close` are stubbed: replacing the whole window
  // would take the DOM's event constructors with it.
  const posted: Posted[] = [];
  Object.defineProperty(window, "opener", {
    configurable: true,
    writable: true,
    value: { postMessage: (message: unknown, targetOrigin: string) => posted.push({ message, targetOrigin }) },
  });
  Object.defineProperty(window, "close", { configurable: true, writable: true, value: vi.fn() });

  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: "/in-context/authorize", name: "in-context-authorize", component: Empty },
      { path: "/auth/sign-in", name: "sign-in", component: Empty },
    ],
  });
  await router.push({ path: "/in-context/authorize", query });
  const w = mount(AuthorizeView, {
    attachTo: document.body,
    global: { plugins: [router], provide: { [IN_CONTEXT as symbol]: port } },
  });
  await flushPromises();
  return { w, posted };
}

const ask = (over: Record<string, string> = {}) => ({
  tenant: "t",
  project: "p",
  origin: ORIGIN,
  channel: "nonce-1",
  ...over,
});

describe("AuthorizeView", () => {
  let port: FakeInContext;

  beforeEach(() => {
    port = fakeInContext();
    port.originList = [previewOrigin()];
  });

  it("says who it will act as and where, and mints nothing until confirmed", async () => {
    const { w, posted } = await open(port, ask());

    expect(w.text()).toContain("ada@example.com");
    expect(w.text()).toContain(ORIGIN);
    // The four things a grant allows, in words.
    expect(w.text()).toContain("write translations");
    expect(w.text()).toContain("cannot publish a release");
    expect(w.text()).toContain("fifteen minutes");

    // Nothing has happened yet.
    expect(port.calls).toHaveLength(0);
    expect(posted).toHaveLength(0);
  });

  it("posts the grant to the exact origin that asked, never to a wildcard", async () => {
    const { w, posted } = await open(port, ask());
    await w.findAll("button").find((b) => b.text() === "Allow editing")!.trigger("click");
    await flushPromises();

    expect(port.calls).toEqual([["mint", "t", "p", ORIGIN]]);
    expect(posted).toHaveLength(1);
    expect(posted[0]!.targetOrigin).toBe(ORIGIN);
    expect(posted[0]!.targetOrigin).not.toBe("*");

    const message = posted[0]!.message as Record<string, unknown>;
    expect(message.type).toBe(IN_CONTEXT_MESSAGE);
    expect(message.channel).toBe("nonce-1");
    expect(message.ok).toBe(true);
    expect(String(message.token)).toMatch(/^glossa_ctx_/);
    expect(message.origin).toBe(ORIGIN);
    expect(message.project_id).toBe("p");
  });

  it("tells the opener when the person declines, and mints nothing", async () => {
    const { w, posted } = await open(port, ask());
    await w.findAll("button").find((b) => b.text() === "Cancel")!.trigger("click");
    await flushPromises();

    expect(port.calls).toHaveLength(0);
    expect(posted).toHaveLength(1);
    const message = posted[0]!.message as Record<string, unknown>;
    expect(message.ok).toBe(false);
    expect(message.code).toBe("cancelled");
    expect(posted[0]!.targetOrigin).toBe(ORIGIN);
  });

  it("refuses a request whose origin is not a plain scheme, host and port", async () => {
    for (const bad of [
      "https://preview.example.com/steal",
      "https://preview.example.com?x=1",
      "javascript:alert(1)",
      "*",
      "",
    ]) {
      const { w, posted } = await open(port, ask({ origin: bad }));
      expect(w.findAll("button")).toHaveLength(0);
      expect(w.text()).toContain("link is incomplete");
      expect(posted).toHaveLength(0);
      expect(port.calls).toHaveLength(0);
    }
  });

  it("refuses a request that names no channel, so an answer can't be mistaken for another's", async () => {
    const { w, posted } = await open(port, ask({ channel: "" }));
    expect(w.findAll("button")).toHaveLength(0);
    expect(posted).toHaveLength(0);
  });

  it("shows the server's refusal and passes its code back to the opener", async () => {
    port.originList = [];
    const { w, posted } = await open(port, ask());
    await w.findAll("button").find((b) => b.text() === "Allow editing")!.trigger("click");
    await flushPromises();

    expect(w.find('[role="alert"]').text()).toContain("not on this project's list");
    expect(posted).toHaveLength(1);
    expect((posted[0]!.message as Record<string, unknown>).ok).toBe(false);
    expect((posted[0]!.message as Record<string, unknown>).code).toBe("origin_not_registered");
  });
});
