import { flushPromises, type VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createFakeReleases } from "../../test/fake-releases";
import { mountProjectScreen } from "../../test/project";
import DeliveryKeys from "./DeliveryKeys.vue";

let w: VueWrapper | undefined;
afterEach(() => {
  w?.unmount();
  w = undefined;
});

const signingKeys = [
  { key_id: "k_2026a", algorithm: "Ed25519" as const, public_key: "pubA", active: true },
  { key_id: "k_2025", algorithm: "Ed25519" as const, public_key: "pubOld", active: false },
];

describe("DeliveryKeys", () => {
  it("creates a key, shows it in full once with snippets, then lists it masked", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    const port = createFakeReleases({ signingKeys });
    w = await mountProjectScreen(DeliveryKeys, { port, path: "/t/t/p/p/settings" });
    expect(w.text()).toContain("No delivery keys yet.");

    await w.get("#dk-name").setValue("web");
    await w.get("form").trigger("submit");
    await flushPromises();
    const created = port.state.keys[0]!;
    expect(port.calls.find((c) => c[0] === "createDeliveryKey")?.[2]).toBe("web");
    // A new key reads production only until someone says otherwise.
    expect(created.scope).toEqual({ environments: ["production"], branches: false });

    const d = w.get("dialog[open]");
    expect(d.get("h2").text()).toBe("Delivery key “web”");
    expect((d.get("[data-testid=created-key]").element as HTMLInputElement).value).toBe(created.key);
    await d.findAll("button").find((b) => b.text() === "Copy Delivery key")!.trigger("click");
    await flushPromises();
    expect(writeText).toHaveBeenCalledWith(created.key);
    expect(d.text()).toContain("Copied.");

    // Snippets for every runtime, pinned to the active signing key only.
    const tabs = d.findAll("[role=tab]").map((t) => t.text());
    expect(tabs).toEqual(["JavaScript", "Vue", "Web components", "Go"]);
    const js = d.get("[data-testid=snippet-runtime]").text();
    expect(js).toContain(created.key);
    expect(js).toContain('environment: "production"');
    expect(js).toContain('keyId: "k_2026a"');
    expect(js).not.toContain("k_2025");
    await d.get("#dk-env").setValue("staging");
    expect(d.get("[data-testid=snippet-go]").text()).toContain('Environment: "staging"');
    expect(d.get("[data-testid=snippet-elements]").text()).toContain(`delivery-key="${created.key}"`);

    // Arrow keys move between the tabs.
    await d.get("[role=tab]").trigger("keydown", { key: "End" });
    await flushPromises();
    expect(d.findAll("[role=tab]")[3]!.attributes("aria-selected")).toBe("true");

    await d.findAll("button").find((b) => b.text() === "Done")!.trigger("click");
    await flushPromises();
    expect(w.find("dialog[open]").exists()).toBe(false);
    const row = w.get("[data-testid=delivery-keys] tbody tr");
    expect(row.text()).toContain("web");
    expect(row.text()).toContain("glossa_pk_KKKK…");
    expect(row.text()).not.toContain(created.key);
    expect(row.text()).toContain("Active");
  });

  it("revokes only after a confirmation that says what happens", async () => {
    const port = createFakeReleases();
    await port.createDeliveryKey({ tenant: "t", project: "p" }, "web", undefined, "i1");
    w = await mountProjectScreen(DeliveryKeys, { port, path: "/t/t/p/p/settings" });
    await w.findAll("button").find((b) => b.text() === "Revoke web")!.trigger("click");
    await flushPromises();
    const d = w.get("dialog[open]");
    expect(d.get("h2").text()).toBe("Revoke “web”?");
    expect(d.text()).toContain("stop receiving new releases from the edge within about 30 seconds");
    expect(port.calls.some((c) => c[0] === "revokeDeliveryKey")).toBe(false);
    await d.findAll("button").find((b) => b.text() === "Revoke key")!.trigger("click");
    await flushPromises();
    expect(port.calls.some((c) => c[0] === "revokeDeliveryKey")).toBe(true);
    expect(w.get("[role=status]").text()).toBe("web is revoked.");
    expect(w.get("[data-testid=delivery-keys] tbody tr").text()).toContain("Revoked");
    expect(w.findAll("button").some((b) => b.text().startsWith("Revoke"))).toBe(false);
  });

  it("creates a preview key and changes what a key reads", async () => {
    const port = createFakeReleases();
    await port.createDeliveryKey({ tenant: "t", project: "p" }, "web", undefined, "i1");
    w = await mountProjectScreen(DeliveryKeys, { port, path: "/t/t/p/p/settings" });
    expect(w.get("[data-testid=key-scope]").text()).toBe("production");

    // A preview key: branch previews, and no production.
    await w.get("#dk-name").setValue("previews");
    const boxes = w.get("[data-testid=new-key-scope]").findAll("input[type=checkbox]");
    await boxes.find((b) => (b.element as HTMLInputElement).value === "production")!.setValue(false);
    await boxes.find((b) => (b.element as HTMLInputElement).value === "preview")!.setValue(true);
    await w.get("[data-testid=new-key-preview]").setValue(true);
    await w.get("form").trigger("submit");
    await flushPromises();
    expect(port.state.keys[1]!.scope).toEqual({ environments: ["preview"], branches: true });
    await w.findAll("button").find((b) => b.text() === "Done")!.trigger("click");
    await flushPromises();
    expect(w.findAll("[data-testid=key-scope]")[1]!.text()).toBe("preview + branch previews");

    // Changing a key's scope keeps the key itself.
    const key = port.state.keys[0]!.key;
    await w.findAll("button").find((b) => b.text() === "Change scope web")!.trigger("click");
    await flushPromises();
    const d = w.get("dialog[open]");
    expect(d.get("h2").text()).toBe("What “web” reads");
    await d.get("[data-testid=scope-edit-preview]").setValue(true);
    await d.findAll("button").find((b) => b.text() === "Save")!.trigger("click");
    await flushPromises();
    expect(port.state.keys[0]!.key).toBe(key);
    expect(port.state.keys[0]!.scope).toEqual({ environments: ["production"], branches: true });
    expect(w.get("[role=status]").text()).toBe("web now reads production + branch previews.");
  });

  it("refuses a key that reads nothing", async () => {
    const port = createFakeReleases();
    w = await mountProjectScreen(DeliveryKeys, { port, path: "/t/t/p/p/settings" });
    await w.get("#dk-name").setValue("nothing");
    const boxes = w.get("[data-testid=new-key-scope]").findAll("input[type=checkbox]");
    await boxes.find((b) => (b.element as HTMLInputElement).value === "production")!.setValue(false);
    await flushPromises();
    expect(w.text()).toContain("Choose at least one environment, or branch previews.");
    expect((w.get("button[type=submit]").element as HTMLButtonElement).disabled).toBe(true);
  });

  it("lists keys without create or revoke for members who can't publish", async () => {
    const port = createFakeReleases();
    await port.createDeliveryKey({ tenant: "t", project: "p" }, "web", undefined, "i1");
    w = await mountProjectScreen(DeliveryKeys, { port, roles: ["translator"], path: "/t/t/p/p/settings" });
    expect(w.get("[data-testid=delivery-keys]").text()).toContain("web");
    expect(w.find("#dk-name").exists()).toBe(false);
    expect(w.findAll("button").some((b) => b.text().startsWith("Revoke"))).toBe(false);
  });
});
