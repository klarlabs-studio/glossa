import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { defineComponent } from "vue";
import { createMemoryHistory, createRouter, type Router } from "vue-router";
import { setCsrfToken } from "../../api/client";
import { apiDevice, DEVICE } from "../../api/device";
import { installGuards, routes } from "../../router";
import { refreshSession } from "../../session/session";
import { deviceAuthorization, fakeDevice, type FakeDevice } from "../../test/fake-device";
import DeviceView from "./DeviceView.vue";

/**
 * Device sign-in (RFC 0006 §7.2, RFC 8628). What matters: the request is
 * shown — with the warning — before anything can be approved, the
 * decision sent is the one clicked, an unknown code reads as one plain
 * message, and an anonymous visitor comes back to the code after
 * signing in.
 */

const Empty = defineComponent({ template: "<div />" });
let wrapper: VueWrapper | undefined;

async function open(port: FakeDevice, query: Record<string, string> = {}): Promise<{ w: VueWrapper; router: Router }> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: "/device", name: "device", component: Empty },
      { path: "/auth/sign-in", name: "sign-in", component: Empty },
    ],
  });
  await router.push({ path: "/device", query });
  wrapper = mount(DeviceView, {
    attachTo: document.body,
    global: { plugins: [router], provide: { [DEVICE as symbol]: port }, stubs: { "kl-theme-toggle": true } },
  });
  await flushPromises();
  return { w: wrapper, router };
}

const button = (w: VueWrapper, text: string) => w.findAll("button").find((b) => b.text() === text)!;

async function enter(w: VueWrapper, raw: string): Promise<void> {
  await w.get("#device-code").setValue(raw);
  await w.get("form").trigger("submit");
  await flushPromises();
}

afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

describe("DeviceView", () => {
  let port: FakeDevice;

  beforeEach(() => {
    port = fakeDevice(deviceAuthorization());
  });

  it("offers an accessible, unautocompleted code field and looks nothing up on its own", async () => {
    const { w } = await open(port);
    const input = w.get("#device-code");
    expect(w.get("label[for=device-code]").text()).toBe("Device code");
    expect(input.attributes("autocomplete")).toBe("off");
    expect(input.classes()).toContain("mono");
    expect(document.activeElement).toBe(input.element);
    expect(port.calls).toHaveLength(0);
  });

  it("formats the code as XXXX-XXXX while it is typed", async () => {
    const { w } = await open(port);
    const input = w.get("#device-code");
    await input.setValue("bcdfg");
    expect((input.element as HTMLInputElement).value).toBe("BCDF-G");
    await input.setValue("bcdf ghjk");
    expect((input.element as HTMLInputElement).value).toBe("BCDF-GHJK");
  });

  it("fills in ?code= and looks it up straight away", async () => {
    const { w } = await open(port, { code: "bcdf-ghjk" });

    expect(port.calls).toEqual([["lookup", "BCDFGHJK"]]);
    expect(w.get("[data-testid=device-client]").text()).toBe("glossa CLI on build-01");
    expect(w.text()).toContain("BCDF-GHJK");
    expect(w.findAll("time").map((t) => t.attributes("datetime"))).toEqual(["2026-09-20T10:00:00Z", "2026-09-20T10:15:00Z"]);
    expect(w.get("[data-testid=device-warning]").text()).toContain("signs this device in as you, with all your access");
    // Focus moves to what is being asked.
    expect(document.activeElement).toBe(w.get("#device-request").element);
    // Nothing is decided until a button is pressed.
    expect(port.decided.size).toBe(0);
  });

  it("approves with `approved` and says the device is signed in", async () => {
    const { w } = await open(port);
    await enter(w, "bcdfghjk");
    await button(w, "Approve").trigger("click");
    await flushPromises();

    expect(port.calls.at(-1)).toEqual(["decide", "BCDFGHJK", "approved"]);
    expect(port.decided.get("BCDFGHJK")).toBe("approved");
    const outcome = w.get("[data-testid=device-outcome]");
    expect(outcome.text()).toBe("Device signed in — you can return to your terminal.");
    expect(w.get("h1").text()).toBe("Device signed in");
    expect(document.activeElement).toBe(outcome.element);
    expect(w.findAll("button")).toHaveLength(0);
  });

  it("denies with `denied` and says so", async () => {
    const { w } = await open(port, { code: "BCDF-GHJK" });
    await button(w, "Deny").trigger("click");
    await flushPromises();

    expect(port.calls.at(-1)).toEqual(["decide", "BCDFGHJK", "denied"]);
    expect(port.decided.get("BCDFGHJK")).toBe("denied");
    expect(w.get("h1").text()).toBe("Sign-in denied");
    expect(w.get("[data-testid=device-outcome]").text()).toContain("Sign-in denied");
  });

  it("shows one plain message for an unknown, expired or used code", async () => {
    const { w } = await open(port, { code: "ZZZZ-ZZZZ" });

    expect(w.get('[role="alert"]').text()).toBe("This code is unknown, expired, or already used.");
    expect(w.find("[data-testid=device-client]").exists()).toBe(false);
    expect(document.activeElement).toBe(w.get("#device-code").element);
  });

  it("goes back to the field when the code was decided elsewhere meanwhile", async () => {
    const { w } = await open(port, { code: "BCDF-GHJK" });
    port.pending.clear();
    await button(w, "Approve").trigger("click");
    await flushPromises();

    expect(w.get('[role="alert"]').text()).toBe("This code is unknown, expired, or already used.");
    expect(w.find("[data-testid=device-outcome]").exists()).toBe(false);
    expect(w.find("#device-code").exists()).toBe(true);
  });

  it("says when it is being rate limited", async () => {
    port.failWith = { status: 429, code: "rate_limited" };
    const { w } = await open(port, { code: "BCDF-GHJK" });
    expect(w.get('[role="alert"]').text()).toBe("Too many requests. Wait a moment and try again.");
  });

  it("shows a generic failure", async () => {
    port.failWith = { status: 0, code: "network_error" };
    const { w } = await open(port, { code: "BCDF-GHJK" });
    expect(w.get('[role="alert"]').text()).toContain("could not be reached");
  });

  it("refuses a code that can't be one before asking the server", async () => {
    const { w } = await open(port);
    await enter(w, "ABCD-1234");

    expect(port.calls).toHaveLength(0);
    expect(w.get("#device-code").attributes("aria-invalid")).toBe("true");
    expect(w.get("#device-code-error").text()).toContain("eight consonants");
  });

  it("sends a session that ended to sign in, and back to the code", async () => {
    port.failWith = { status: 401, code: "unauthenticated" };
    const { router } = await open(port, { code: "BCDF-GHJK" });
    await flushPromises();

    expect(router.currentRoute.value.name).toBe("sign-in");
    expect(router.currentRoute.value.query.next).toBe("/device?code=BCDF-GHJK");
  });
});

describe("the /device route", () => {
  const problem = { type: "about:blank", title: "Unauthorized", status: 401, code: "unauthenticated" };

  function guarded(): Router {
    const router = createRouter({ history: createMemoryHistory(), routes });
    installGuards(router);
    return router;
  }

  it("sends an anonymous visitor to sign in, with the way back", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(JSON.stringify(problem), { status: 401, headers: { "Content-Type": "application/problem+json" } })),
    );
    await refreshSession();
    const router = guarded();
    await router.push("/device?code=BCDF-GHJK");

    expect(router.currentRoute.value.name).toBe("sign-in");
    expect(router.currentRoute.value.query.next).toBe("/device?code=BCDF-GHJK");
  });
});

describe("apiDevice", () => {
  it("decides over POST /v1/auth/device-approvals with the session's CSRF token", async () => {
    const seen: Request[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (req: Request) => {
        seen.push(req);
        return new Response(null, { status: 204 });
      }),
    );
    setCsrfToken("csrf-1");
    await apiDevice.decide("BCDFGHJK", "approved");
    setCsrfToken(undefined);

    expect(seen).toHaveLength(1);
    expect(new URL(seen[0]!.url).pathname).toBe("/v1/auth/device-approvals");
    expect(seen[0]!.method).toBe("POST");
    expect(seen[0]!.headers.get("X-CSRF-Token")).toBe("csrf-1");
    expect(await seen[0]!.json()).toEqual({ user_code: "BCDFGHJK", decision: "approved" });
  });

  it("looks a code up and checks the answer against the contract", async () => {
    const seen: Request[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (req: Request) => {
        seen.push(req);
        return new Response(JSON.stringify(deviceAuthorization()), { status: 200, headers: { "Content-Type": "application/json" } });
      }),
    );
    expect(await apiDevice.lookup("BCDFGHJK")).toEqual(deviceAuthorization());
    expect(new URL(seen[0]!.url).pathname).toBe("/v1/auth/device-authorizations/BCDFGHJK");
  });
});
