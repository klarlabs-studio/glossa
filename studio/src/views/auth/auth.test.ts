import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { defineComponent, type Component } from "vue";
import { createMemoryHistory, createRouter } from "vue-router";
import type { Meta } from "../../api/schemas";
import { resetMeta } from "../../session/meta";
import RegisterView from "./RegisterView.vue";
import ResetPasswordView from "./ResetPasswordView.vue";
import SignInView from "./SignInView.vue";

const api = vi.hoisted(() => ({
  meta: vi.fn(),
  requestMagicLink: vi.fn(),
  signInWithPassword: vi.fn(),
  register: vi.fn(),
  requestPasswordReset: vi.fn(),
  beginPasskeySignIn: vi.fn(),
  establish: vi.fn(),
}));
vi.mock("../../api/endpoints", () => ({
  meta: { get: api.meta },
  auth: {
    requestMagicLink: api.requestMagicLink,
    signInWithPassword: api.signInWithPassword,
    register: api.register,
    requestPasswordReset: api.requestPasswordReset,
    beginPasskeySignIn: api.beginPasskeySignIn,
  },
}));
vi.mock("../../session/session", () => ({ establishSession: api.establish }));

const withEmail: Meta = { sign_in_methods: ["password", "magic_link"], email_delivery: true };
const withoutEmail: Meta = { sign_in_methods: ["passkey", "password"], email_delivery: false };
const session = { person: { id: "p" }, csrf_token: "c", expires_at: "2026-09-20T00:00:00Z" };

const Empty = defineComponent({ template: "<div />" });
let wrapper: VueWrapper | undefined;

async function render(view: Component, meta: Meta): Promise<VueWrapper> {
  api.meta.mockResolvedValue(meta);
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: "/auth/sign-in", name: "sign-in", component: Empty },
      { path: "/auth/register", name: "register", component: Empty },
      { path: "/auth/reset-password", name: "reset-password", component: Empty },
      { path: "/app", name: "home", component: Empty },
    ],
  });
  await router.push("/auth/sign-in");
  wrapper = mount(view, { attachTo: document.body, global: { plugins: [router], stubs: { "kl-theme-toggle": true } } });
  await flushPromises();
  return wrapper;
}

const buttons = (w: VueWrapper) => w.findAll("button").map((b) => b.text());

beforeEach(() => {
  for (const f of Object.values(api)) f.mockReset();
  resetMeta();
  // A browser with WebAuthn.
  vi.stubGlobal("PublicKeyCredential", function PublicKeyCredential() {});
  vi.stubGlobal("navigator", { ...navigator, credentials: { get: vi.fn(), create: vi.fn() } });
});
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  vi.unstubAllGlobals();
});

describe("SignInView", () => {
  it("leads with the emailed link when the server sends email", async () => {
    const w = await render(SignInView, withEmail);
    expect(w.get("button[type=submit]").text()).toBe("Email me a sign-in link");
    expect(w.text()).toContain("Forgot your password?");
    // Passkeys aren't configured on this server: no passkey button.
    expect(buttons(w).some((b) => /passkey/i.test(b))).toBe(false);
  });

  it("without email hides the link and defaults to passkey and password", async () => {
    const w = await render(SignInView, withoutEmail);
    expect(buttons(w)).not.toContain("Email me a sign-in link");
    expect(w.text()).not.toContain("Forgot your password?");
    expect(w.text()).toContain("Sign in with your password or a passkey.");
    expect(buttons(w)).toContain("Sign in with a passkey");
    // The password form is right there, not behind "Other ways".
    expect(w.find("details").exists()).toBe(false);
    api.signInWithPassword.mockResolvedValue(session);
    await w.get("#email").setValue("ada@example.com");
    await w.get("#password").setValue("correct horse battery");
    await w.get("form[data-testid=password-form]").trigger("submit");
    await flushPromises();
    expect(api.signInWithPassword).toHaveBeenCalledWith({ email: "ada@example.com", password: "correct horse battery" });
    expect(api.requestMagicLink).not.toHaveBeenCalled();
    expect(api.establish).toHaveBeenCalledWith(session);
  });
});

describe("RegisterView", () => {
  it("with email, asks to open the verification link", async () => {
    const w = await render(RegisterView, withEmail);
    api.register.mockResolvedValue(undefined);
    await w.get("#email").setValue("ada@example.com");
    await w.get("#password").setValue("correct horse battery");
    await w.get("form").trigger("submit");
    await flushPromises();
    expect(w.text()).toContain("we sent a verification link to ada@example.com");
    expect(api.signInWithPassword).not.toHaveBeenCalled();
  });

  it("without email, signs the new account in with its password", async () => {
    const w = await render(RegisterView, withoutEmail);
    api.register.mockResolvedValue(undefined);
    api.signInWithPassword.mockResolvedValue(session);
    expect(w.text()).not.toContain("verification link");
    await w.get("#email").setValue("ada@example.com");
    await w.get("#password").setValue("correct horse battery");
    await w.get("form").trigger("submit");
    await flushPromises();
    expect(api.signInWithPassword).toHaveBeenCalledWith({ email: "ada@example.com", password: "correct horse battery" });
    expect(api.establish).toHaveBeenCalledWith(session);
  });
});

describe("ResetPasswordView", () => {
  it("says reset by email isn't available without email", async () => {
    const w = await render(ResetPasswordView, withoutEmail);
    expect(w.find("form").exists()).toBe(false);
    expect(w.text()).toContain("This server doesn't send email");
  });

  it("offers the reset link with email", async () => {
    const w = await render(ResetPasswordView, withEmail);
    expect(w.find("form").exists()).toBe(true);
  });
});
