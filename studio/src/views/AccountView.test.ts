import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { defineComponent } from "vue";
import { createMemoryHistory, createRouter } from "vue-router";
import { setCsrfToken } from "../api/client";
import { resetMeta } from "../session/meta";
import AccountView from "./AccountView.vue";

const Empty = defineComponent({ template: "<div />" });
const json = (body: unknown, status = 200) =>
  new Response(body === undefined ? null : JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

let passkeys = [
  { id: "cred-1", name: "MacBook Touch ID", created_at: "2026-09-01T10:00:00Z", last_used_at: "2026-09-18T10:00:00Z" },
  { id: "cred-2", name: "Phone", created_at: "2026-09-02T10:00:00Z" },
];
const requests: Request[] = [];
let wrapper: VueWrapper | undefined;

async function render(): Promise<VueWrapper> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: "/account", name: "account", component: Empty },
      { path: "/auth/sign-in", name: "sign-in", component: Empty },
    ],
  });
  await router.push("/account");
  wrapper = mount(AccountView, { attachTo: document.body, global: { plugins: [router] } });
  await flushPromises();
  return wrapper;
}

beforeEach(() => {
  resetMeta();
  setCsrfToken("csrf-9");
  requests.length = 0;
  passkeys = passkeys.slice(0, 2);
  // A browser with WebAuthn.
  vi.stubGlobal("PublicKeyCredential", function PublicKeyCredential() {});
  vi.stubGlobal("navigator", { ...navigator, credentials: { get: vi.fn(), create: vi.fn() } });
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      requests.push(req);
      const path = new URL(req.url).pathname;
      if (path === "/v1/meta") return json({ sign_in_methods: ["password"], email_delivery: false });
      if (path === "/v1/me/passkeys") return json({ items: passkeys });
      if (req.method === "DELETE" && path.startsWith("/v1/me/passkeys/")) {
        passkeys = passkeys.filter((p) => `/v1/me/passkeys/${p.id}` !== path);
        return new Response(null, { status: 204 });
      }
      return json({ type: "x", title: "nope", status: 404, code: "not_found" }, 404);
    }),
  );
});
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  setCsrfToken(undefined);
  vi.unstubAllGlobals();
});

describe("AccountView passkeys", () => {
  it("lists every passkey from the server, with its last use", async () => {
    const w = await render();
    const rows = w.findAll("[data-testid=passkeys] tbody tr");
    expect(rows.map((r) => r.get("th").text())).toEqual(["MacBook Touch ID", "Phone"]);
    expect(rows[1]!.text()).toContain("never");
    // Passkeys aren't configured here: nothing can be added, the list stays manageable.
    expect(w.find("#passkey-name").exists()).toBe(false);
    expect(w.text()).toContain("Passkeys aren't configured on this server");
  });

  it("removes one after confirming, with the CSRF token", async () => {
    const w = await render();
    await w.get("button[aria-label='Remove passkey Phone']").trigger("click");
    await flushPromises();
    const dialog = w.get("dialog[open]");
    expect(dialog.text()).toContain("Remove passkey “Phone”?");
    await dialog.findAll("button").find((b) => b.text() === "Remove passkey")!.trigger("click");
    await flushPromises();
    const del = requests.find((r) => r.method === "DELETE")!;
    expect(new URL(del.url).pathname).toBe("/v1/me/passkeys/cred-2");
    expect(del.headers.get("X-CSRF-Token")).toBe("csrf-9");
    expect(w.findAll("[data-testid=passkeys] tbody tr")).toHaveLength(1);
    expect(w.text()).toContain('Passkey "Phone" removed.');
  });

  it("says when there are none", async () => {
    passkeys = [];
    const w = await render();
    expect(w.get("[data-testid=no-passkeys]").text()).toBe("No passkeys yet.");
  });
});
