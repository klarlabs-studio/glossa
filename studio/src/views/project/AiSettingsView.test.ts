import { flushPromises, type DOMWrapper, type VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";
import { createFakeIntelligence, type FakeIntelligence } from "../../test/fake-intelligence";
import { locale, mountProjectScreen } from "../../test/project";
import AiSettingsView from "./AiSettingsView.vue";

let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

const locales = [locale("en", true), locale("de"), locale("ja")];
const button = (root: { findAll(s: string): DOMWrapper<Element>[] }, name: string) => {
  const b = root.findAll("button").find((x) => x.text() === name || x.attributes("aria-label") === name);
  if (!b) throw new Error(`no button ${name}`);
  return b;
};
const screen = async (i: FakeIntelligence, roles: Array<"owner" | "developer"> = ["owner"]) =>
  (wrapper = await mountProjectScreen(AiSettingsView, { intelligence: i, locales, roles, path: "/t/t/p/p/ai" }));
const status = (w: VueWrapper) => w.get("[data-testid=ai-settings-status]").text();

describe("AiSettingsView", () => {
  it("keeps consent off until someone confirms it, explaining what is sent", async () => {
    const i = createFakeIntelligence();
    const w = await screen(i);
    const consent = w.get("[data-testid=consent]");
    expect(consent.get("[data-testid=consent-state]").text()).toBe("Sending text to AI providers is off.");
    expect(consent.text()).toContain("Namespaces tagged sensitive are never sent");
    await button(consent, "Allow sending text…").trigger("click");
    await flushPromises();
    expect(i.calls.some((c) => c[0] === "updateSettings")).toBe(false);
    await button(w.get("dialog[open]"), "Allow").trigger("click");
    await flushPromises();
    // Never saved: If-Match "0" creates them only while still unsaved.
    expect(i.calls.find((c) => c[0] === "updateSettings")).toEqual(["updateSettings", { provider_consent: true }, '"0"']);
    expect(consent.get("[data-testid=consent-state]").text()).toBe("Sending text to AI providers is allowed.");
    expect(consent.text()).toContain("Changed by you");
    await button(consent, "Stop sending text").trigger("click");
    await flushPromises();
    expect(i.calls.filter((c) => c[0] === "updateSettings").at(-1)).toEqual(["updateSettings", { provider_consent: false }, '"1"']);
  });

  it("loses a first save to someone else's with 412, reloads and says so", async () => {
    const i = createFakeIntelligence();
    const w = await screen(i);
    // Someone saves the budget while this screen still shows the defaults.
    await i.updateSettings("t", { monthly_budget_micro_usd: 1_000_000 }, '"0"');
    await w.get("#ai-budget").setValue("12.50");
    await button(w, "Save budget").trigger("click");
    await flushPromises();
    expect(i.calls.filter((c) => c[0] === "updateSettings").at(-1)).toEqual(["updateSettings", { monthly_budget_micro_usd: 12_500_000, max_concurrent_jobs: 4 }, '"0"']);
    expect(w.text()).toContain("Someone else changed these settings meanwhile.");
    expect(i.state.settings.monthly_budget_micro_usd).toBe(1_000_000);
    // Reloaded: the next save is based on their version.
    await w.get("#ai-budget").setValue("12.50");
    await button(w, "Save budget").trigger("click");
    await flushPromises();
    expect(i.calls.filter((c) => c[0] === "updateSettings").at(-1)![2]).toBe('"1"');
    expect(status(w)).toBe("Budget saved.");
  });

  it("creates the project's own routing policy with If-Match \"0\" while it inherits", async () => {
    const i = createFakeIntelligence();
    const w = await screen(i);
    const card = w.get("[data-testid=routing]");
    await card.get("form").trigger("submit");
    await flushPromises();
    expect(i.calls.find((c) => c[0] === "putProjectRouting")![2]).toBe('"0"');
    await card.get("form").trigger("submit");
    await flushPromises();
    expect(i.calls.filter((c) => c[0] === "putProjectRouting").at(-1)![2]).toBe('"1"');
  });

  it("adds a provider with a write-only key and never shows it", async () => {
    const i = createFakeIntelligence();
    const w = await screen(i);
    await button(w, "Add provider").trigger("click");
    await flushPromises();
    const d = w.get("dialog[open]");
    expect(d.text()).toContain("Stored sealed, never shown again");
    expect(d.get("#pv-key").attributes("type")).toBe("password");
    await d.get("#pv-name").setValue("mistral-eu");
    await d.get("#pv-kind").setValue("openai_compatible");
    await d.get("#pv-url").setValue("https://api.mistral.ai/v1");
    await d.get("#pv-models").setValue("mistral-large, mistral-small");
    await d.get("#pv-key").setValue("sk-secret");
    await d.get("form").trigger("submit");
    await flushPromises();
    expect(i.calls.find((c) => c[0] === "createProvider")![1]).toEqual({
      name: "mistral-eu",
      kind: "openai_compatible",
      base_url: "https://api.mistral.ai/v1",
      models: ["mistral-large", "mistral-small"],
      enabled: true,
      api_key: "sk-secret",
    });
    const row = w.get("tr[data-provider=mistral-eu]");
    expect(row.text()).toContain("Key stored (sealed)");
    expect(w.html()).not.toContain("sk-secret");

    // Editing never shows the key: an empty field keeps it, and removing it is explicit.
    await button(w, "Edit mistral-eu").trigger("click");
    await flushPromises();
    const e = w.get("dialog[open]");
    expect((e.get("#pv-key").element as HTMLInputElement).value).toBe("");
    await e.get("form").trigger("submit");
    await flushPromises();
    expect(i.calls.find((c) => c[0] === "updateProvider")![2]).not.toHaveProperty("api_key");
    expect(i.state.keys.get(i.state.providers[0]!.id)).toBe("sk-secret");
  });

  it("saves the budget in integer micro-USD and shows the spend against it", async () => {
    const i = createFakeIntelligence();
    i.state.spend.push({ id: "x", task: "translate", provider: "anthropic", model: "claude-sonnet-5", usage: { input_tokens: 900, output_tokens: 40 }, cost_micro_usd: 2_120, priced: true, occurred_at: new Date().toISOString() });
    const w = await screen(i);
    await w.get("#ai-budget").setValue("12.50");
    await button(w, "Save budget").trigger("click");
    await flushPromises();
    expect(i.calls.find((c) => c[0] === "updateSettings")![1]).toEqual({ monthly_budget_micro_usd: 12_500_000, max_concurrent_jobs: 4 });
    expect(status(w)).toBe("Budget saved.");
    expect(w.get("[data-testid=budget-spent]").text()).toBe("$0.0021 of $12.50 spent this month");
    expect(w.get("[data-testid=spend-chart]").find("svg").exists()).toBe(true);

    await w.get("#ai-budget").setValue("twelve");
    await button(w, "Save budget").trigger("click");
    expect(w.text()).toContain("Enter an amount in dollars");
    expect(i.calls.filter((c) => c[0] === "updateSettings")).toHaveLength(1);
  });

  it("tags namespaces, picks auto-translate locales and warns when auto-approve goes on", async () => {
    const i = createFakeIntelligence();
    const w = await screen(i);
    const card = w.get("[data-testid=project-policy]");
    await card.get("#pp-ns").setValue("legal");
    await button(card, "Add namespace").trigger("click");
    await card.get("[aria-label='Legal: legal']").setValue(true);
    await card.findAll("input[type=checkbox]").find((c) => (c.element as HTMLInputElement).value === "ja")!.setValue(true);
    expect(card.text()).not.toContain("without anyone reading it");
    const auto = card.get("[data-testid=auto-approve]");
    expect((auto.element as HTMLInputElement).checked).toBe(false);
    await auto.setValue(true);
    expect(card.text()).toContain("Auto-approved text becomes an approved translation without anyone reading it");
    await card.get("#pp-envs").setValue("staging, production");
    await card.get("form").trigger("submit");
    await flushPromises();
    expect(i.calls.find((c) => c[0] === "updateProjectSettings")![1]).toEqual({
      namespace_tags: { legal: ["legal"] },
      auto_translate_locales: ["ja"],
      review: { recommend_min: 0.75, auto_approve: true, auto_approve_min: 0.92, auto_approve_environments: ["staging", "production"], force_review: [] },
    });
    expect(status(w)).toBe("Project policy saved.");
  });

  it("edits the routing policy per task with fallbacks", async () => {
    const i = createFakeIntelligence();
    const w = await screen(i);
    const card = w.get("[data-testid=routing]");
    expect(card.get("[data-testid=routing-source]").text()).toBe("The default policy applies (Anthropic).");
    await button(card, "Add fallback").trigger("click");
    const models = card.findAll("[aria-label='Model 2']");
    await models[0]!.setValue("claude-haiku-4-5-20251001");
    await card.get("form").trigger("submit");
    await flushPromises();
    expect(i.calls.find((c) => c[0] === "putProjectRouting")![1]).toEqual({
      rules: [
        {
          task: "translate",
          routes: [
            { provider: "anthropic", model: "claude-sonnet-5", max_tokens: 8192 },
            { provider: "anthropic", model: "claude-haiku-4-5-20251001", max_tokens: 4096 },
          ],
        },
      ],
    });
  });

  it("shows acceptance per locale and is read-only without intelligence.manage", async () => {
    const w = await screen(createFakeIntelligence(), ["developer"]);
    expect(w.text()).toContain("Changing them needs the admin or owner role.");
    expect(w.findAll("button").map((b) => b.text())).not.toContain("Add provider");
    expect(w.findAll("button").map((b) => b.text())).not.toContain("Allow sending text…");
    const de = w.get("[data-testid=ai-metrics] tr[data-locale=de]").text();
    expect(de).toContain("75%");
    expect(de).toContain("4.0 (10%)");
  });
});
