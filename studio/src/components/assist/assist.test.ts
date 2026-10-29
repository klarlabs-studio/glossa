import { flushPromises, mount } from "@vue/test-utils";
import { afterEach, describe, expect, it, vi } from "vitest";
import { defineComponent, h, ref } from "vue";
import { createMemoryHistory, createRouter } from "vue-router";
import { INTELLIGENCE } from "../../api/intelligence";
import { KNOWLEDGE } from "../../api/knowledge";
import type { Message } from "../../api/schemas";
import { grantFor } from "../../session/permissions";
import { createFakeIntelligence, suggestion, type FakeIntelligence } from "../../test/fake-intelligence";
import { createFakeKnowledge, type FakeKnowledge } from "../../test/fake-knowledge";
import { locale } from "../../test/project";
import FillDialog from "./FillDialog.vue";
import SuggestionPanel from "./SuggestionPanel.vue";
import TmMatches from "./TmMatches.vue";
import { TERM_CHECK_DELAY_MS, useTerminology } from "./useTerminology";

const NOW = "2026-09-19T08:00:00Z";
const message: Message = {
  id: "m1",
  key: "greeting",
  namespace: "default",
  description: "",
  state: "active",
  source: { text: "Create a new workspace", syntax: "mf1", model: { type: "message" }, arguments: [], markup: [] },
  source_revision: 1,
  created_at: NOW,
  updated_at: NOW,
};
const de = locale("de");
const en = locale("en", true);

let wrappers: Array<{ unmount(): void }> = [];
afterEach(() => {
  for (const w of wrappers) w.unmount();
  wrappers = [];
});

function withPorts<T extends object>(component: Parameters<typeof mount>[0], props: T, ports: { k?: FakeKnowledge; i?: FakeIntelligence }) {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: "/t/:tenant/p/:project/review", name: "review", component: { template: "<div />" } }] });
  const w = mount(component, {
    props,
    attachTo: document.body,
    global: { plugins: [router], provide: { [KNOWLEDGE as symbol]: ports.k ?? createFakeKnowledge(), [INTELLIGENCE as symbol]: ports.i ?? createFakeIntelligence() } },
  });
  wrappers.push(w);
  return w;
}
const button = (w: ReturnType<typeof mount>, name: string) => {
  const b = w.findAll("button").find((x) => x.text().startsWith(name));
  if (!b) throw new Error(`no button ${name}`);
  return b;
};

describe("SuggestionPanel", () => {
  const panel = (i: FakeIntelligence, roles: Array<"owner" | "translator"> = ["owner"], locales: string[] = []) =>
    withPorts(SuggestionPanel, { tenant: "t", projectId: "p", message, target: de, grant: grantFor({ roles, locales }) }, { i });

  it("shows confidence as a band and a number, and why", async () => {
    const i = createFakeIntelligence();
    i.state.suggestions.push(suggestion({ risk_tags: ["legal"], action_note: "Auto-approve is off for production." }));
    const w = panel(i);
    await flushPromises();
    expect(w.get("[data-testid=confidence-band]").text()).toBe("Medium confidence");
    expect(w.get("[data-testid=confidence-score]").text()).toBe("score 0.62");
    expect(w.text()).not.toMatch(/62\s?%/);
    expect(w.text()).toContain("isn't a promise that the text is right");
    expect(w.get("[data-testid=suggestion-action]").text()).toBe("Review required");
    expect(w.text()).toContain("Legal text");
    expect(w.text()).toContain("Auto-approve is off for production.");
    const why = w.get("[data-testid=why]");
    const rows = why.findAll("tbody tr").map((r) => r.text());
    // By impact: origin (−0.20), then the TM match (+0.10), then the self-assessment.
    expect(rows[0]).toContain("Origin");
    expect(rows[0]).toContain("−0.20");
    expect(rows[1]).toContain("Translation-memory match");
    expect(rows[1]).toContain("+0.10");
    const prov = w.get("[data-testid=provenance]").text();
    expect(prov).toContain("anthropic / claude-sonnet-5");
    expect(prov).toContain("translate/v1");
    expect(prov).toContain("g1@2");
    expect(prov).toContain("$0.0021");
  });

  it("accepts as is, and tells the workspace", async () => {
    const i = createFakeIntelligence();
    i.state.suggestions.push(suggestion());
    const w = panel(i);
    await flushPromises();
    await button(w, "Accept").trigger("click");
    await flushPromises();
    expect(i.calls).toContainEqual(["accept", "s1", undefined]);
    expect(w.get("[data-testid=ai-status]").text()).toBe("Accepted: it's now the translation.");
    expect(w.get("[data-testid=suggestion-status]").text()).toBe("Accepted");
    expect(w.emitted("accepted")).toHaveLength(1);
  });

  it("edits then accepts, sending the edited MF2 with Mod+Enter", async () => {
    const i = createFakeIntelligence();
    i.state.suggestions.push(suggestion());
    const w = panel(i);
    await flushPromises();
    await button(w, "Edit").trigger("click");
    await flushPromises();
    const edit = w.get("[data-testid=suggestion-edit]");
    expect(document.activeElement).toBe(edit.element);
    expect((edit.element as HTMLTextAreaElement).value).toBe("Hallo, {$name}!");
    await edit.setValue("Servus, {$name}!");
    await edit.trigger("keydown", { key: "Enter", ctrlKey: true });
    await flushPromises();
    expect(i.calls).toContainEqual(["accept", "s1", { text: "Servus, {$name}!", syntax: "mf2" }]);
    expect(w.text()).toContain("Your edit is accepted as the translation.");
  });

  it("rejects with an optional reason", async () => {
    const i = createFakeIntelligence();
    i.state.suggestions.push(suggestion());
    const w = panel(i);
    await flushPromises();
    await button(w, "Reject…").trigger("click");
    await w.get("#ai-reason").setValue("Wrong register");
    await w.get("form").trigger("submit");
    await flushPromises();
    expect(i.calls).toContainEqual(["reject", "s1", "Wrong register"]);
    expect(w.get("[data-testid=suggestion-status]").text()).toBe("Rejected");
    expect(w.emitted("accepted")).toBeUndefined();
  });

  it("keeps decisions to members who may translate the locale", async () => {
    const i = createFakeIntelligence();
    i.state.suggestions.push(suggestion());
    const w = panel(i, ["translator"], ["fr"]);
    await flushPromises();
    expect(w.findAll("button").map((b) => b.text())).not.toContain("Accept");
    expect(w.text()).toContain("Accepting and rejecting de suggestions needs permission to translate de.");
  });

  it("says when there is no suggestion yet", async () => {
    const w = panel(createFakeIntelligence());
    await flushPromises();
    expect(w.text()).toContain("No AI suggestion for this message yet.");
  });
});

describe("SuggestionPanel for an assigned member", () => {
  it("shows the text and why, hides provenance and cost, and inserts into the draft", async () => {
    const i = createFakeIntelligence();
    i.state.assigned = true;
    i.state.suggestions.push(suggestion());
    const w = withPorts(SuggestionPanel, { tenant: "t", projectId: "p", message, target: de, grant: grantFor({ roles: ["translator"], locales: ["de"] }), canInsert: true }, { i });
    await flushPromises();
    expect(i.calls.map((c) => c[0])).toEqual(["unitSuggestions"]);
    expect(w.get("[data-testid=suggestion-text]").text()).toBe("Hallo, {$name}!");
    expect(w.get("[data-testid=confidence-score]").text()).toBe("score 0.62");
    expect(w.find("[data-testid=provenance]").exists()).toBe(false);
    expect(w.text()).not.toContain("anthropic");
    expect(w.text()).not.toContain("$0.0021");
    expect(w.findAll("button").map((b) => b.text())).toEqual(["Insert"]);
    await w.get("[data-testid=suggestion-insert]").trigger("click");
    expect(w.emitted("insert")).toEqual([[{ text: "Hallo, {$name}!", syntax: "mf2" }]]);
  });

  it("is empty for a unit with no suggestion", async () => {
    const i = createFakeIntelligence();
    i.state.assigned = true;
    const w = withPorts(SuggestionPanel, { tenant: "t", projectId: "p", message, target: de, grant: grantFor({ roles: ["translator"], locales: ["de"] }) }, { i });
    await flushPromises();
    expect(w.text()).toContain("No AI suggestion for this message yet.");
  });
});

describe("TmMatches", () => {
  it("shows the score, how the remembered source differs, and inserts the target", async () => {
    const k = createFakeKnowledge();
    k.remember("Create a workspace", "Einen Arbeitsbereich erstellen", { message_key: "workspace.old" }, "Einen Arbeitsbereich erstellen");
    k.sources.set("greeting", "Create a new workspace");
    const w = withPorts(TmMatches, { tenant: "t", projectId: "p", message, source: en, target: de, targetSyntax: "mf1", canInsert: true }, { k });
    await flushPromises();
    // The unit's own read: the key and locale, not the tenant's search.
    expect(k.calls[0]).toEqual(["unitTMMatches", "greeting", "de", expect.objectContaining({ target_syntax: "mf1" })]);
    const m = w.get("[data-testid=tm-match]");
    expect(Number(m.get("[data-testid=tm-score]").text())).toBeGreaterThan(50);
    expect(m.text()).toContain("Fuzzy match");
    expect(m.text()).toContain("From workspace.old");
    expect(m.find("ins").text()).toBe("new");
    expect(m.find("[data-testid=tm-fallback]").exists()).toBe(false);
    await button(w, "Insert").trigger("click");
    const mf1 = { text: "Einen Arbeitsbereich erstellen", syntax: "mf1" };
    expect(w.emitted("insert")).toEqual([[mf1]]);
    const vm = w.vm as unknown as { matchTarget: (n: number) => unknown };
    expect(vm.matchTarget(1)).toEqual(mf1);
    expect(vm.matchTarget(2)).toBeUndefined();
  });

  it("asks for targets in the editor's syntax and says when MF1 couldn't express one", async () => {
    const k = createFakeKnowledge();
    // Markup: MF2 only, so the MF1 lookup falls back to MF2.
    k.remember("Create a new workspace", "Einen {#b}neuen{/b} Arbeitsbereich erstellen");
    k.remember("Create a workspace", "{$n} Arbeitsbereich erstellen", {}, "{n} Arbeitsbereich erstellen");
    k.sources.set("greeting", "Create a new workspace");
    const w = withPorts(TmMatches, { tenant: "t", projectId: "p", message, source: en, target: de, targetSyntax: "mf1", canInsert: true }, { k });
    await flushPromises();
    const [exact, fuzzy] = w.findAll("[data-testid=tm-match]");
    expect(exact!.get(".target").text()).toBe("Einen {#b}neuen{/b} Arbeitsbereich erstellen");
    expect(exact!.get("[data-testid=tm-fallback]").text()).toContain("MF1 can't express this one");
    expect(fuzzy!.get(".target").text()).toBe("{n} Arbeitsbereich erstellen");
    await exact!.findAll("button").find((b) => b.text().startsWith("Insert"))!.trigger("click");
    expect(w.emitted("insert")).toEqual([[{ text: "Einen {#b}neuen{/b} Arbeitsbereich erstellen", syntax: "mf2" }]]);

    // Switching the editor to MF2 asks again, in MF2.
    const props: Record<string, unknown> = { targetSyntax: "mf2" };
    await w.setProps(props);
    await flushPromises();
    expect(k.calls.filter((c) => c[0] === "unitTMMatches").at(-1)![3]).toMatchObject({ target_syntax: "mf2" });
    expect(w.findAll("[data-testid=tm-match]")[1]!.get(".target").text()).toBe("{$n} Arbeitsbereich erstellen");
    expect(w.find("[data-testid=tm-fallback]").exists()).toBe(false);
  });
});

describe("TmMatches for an assigned member", () => {
  it("shows text and score without naming where a match came from", async () => {
    const k = createFakeKnowledge();
    k.remember("Create a workspace", "Einen Arbeitsbereich erstellen", { message_key: "workspace.secret", project_id: "p" }, "Einen Arbeitsbereich erstellen");
    k.sources.set("greeting", "Create a new workspace");
    k.hideKeys = true;
    const w = withPorts(TmMatches, { tenant: "t", projectId: "p", message, source: en, target: de, targetSyntax: "mf1", canInsert: true }, { k });
    await flushPromises();
    const m = w.get("[data-testid=tm-match]");
    expect(m.get(".target").text()).toBe("Einen Arbeitsbereich erstellen");
    expect(m.text()).not.toContain("workspace.secret");
    expect(m.text()).not.toContain("From");
    expect(m.text()).toContain("this project");
  });

  it("says why when the unit is not theirs", async () => {
    const k = createFakeKnowledge();
    const w = withPorts(TmMatches, { tenant: "t", projectId: "p", message, source: en, target: de, targetSyntax: "mf1", canInsert: true }, { k });
    await flushPromises();
    expect(w.find("[role=alert]").exists()).toBe(true);
    expect(w.find("[data-testid=tm-match]").exists()).toBe(false);
  });
});

describe("useTerminology", () => {
  it("recognizes the source's terms and checks the draft, debounced, the latest winning", async () => {
    vi.useFakeTimers();
    const k = createFakeKnowledge();
    await k.createConcept("t", { terms: [{ locale: "en", text: "workspace", case_sensitive: false }, { locale: "de", text: "Arbeitsbereich", case_sensitive: false }, { locale: "de", text: "Workspace", status: "forbidden", case_sensitive: true }] }, "k");
    const draft = ref<{ text: string; syntax: "mf1" | "mf2" }>({ text: "", syntax: "mf1" });
    let api: ReturnType<typeof useTerminology> | undefined;
    const Host = defineComponent({
      setup() {
        api = useTerminology({ tenant: ref("t"), projectId: ref("p"), message: ref(message), sourceLocale: ref("en"), targetLocale: ref("de"), draft });
        return () => h("div");
      },
    });
    withPorts(Host, {}, { k });
    await vi.runAllTimersAsync();
    expect(api!.recognition.value?.hits.map((x) => x.text)).toEqual(["workspace"]);
    expect(api!.recognition.value?.hits[0]!.targets?.map((t) => t.text)).toEqual(["Arbeitsbereich", "Workspace"]);

    draft.value = { text: "Einen neuen W", syntax: "mf1" };
    await vi.advanceTimersByTimeAsync(50);
    draft.value = { text: "Einen neuen Workspace erstellen", syntax: "mf1" };
    await vi.advanceTimersByTimeAsync(TERM_CHECK_DELAY_MS + 10);
    const checks = k.calls.filter((c) => c[0] === "checkTerminology");
    expect(checks).toHaveLength(1);
    expect(checks[0]![1]).toMatchObject({ target: "Einen neuen Workspace erstellen", syntax: "mf1", project_id: "p" });
    expect(api!.findings.value.map((f) => f.code)).toEqual(["term_missing", "term_forbidden"]);

    // An MF2 draft (a TM match) against an MF1 source is checked as plain text.
    draft.value = { text: "Einen neuen Arbeitsbereich erstellen", syntax: "mf2" };
    await vi.advanceTimersByTimeAsync(TERM_CHECK_DELAY_MS + 10);
    expect(k.calls.filter((c) => c[0] === "checkTerminology").at(-1)![1]).not.toHaveProperty("syntax");
    expect(api!.findings.value).toEqual([]);
    vi.useRealTimers();
  });
});

describe("FillDialog", () => {
  const dialog = (i: FakeIntelligence, keys?: string[], select: "missing" | "outdated" | "missing_or_outdated" = "missing") =>
    withPorts(FillDialog, { open: true, tenant: "t", projectId: "p", locale: "de", namespace: "checkout", select, keys }, { i });

  it("previews what the fill would do, queues it on confirmation, follows its jobs and reports when they settle", async () => {
    const i = createFakeIntelligence();
    i.pending = [suggestion({ id: "a", message_key: "a" }), suggestion({ id: "b", message_key: "b" })];
    const w = dialog(i, ["a", "b"]);
    await flushPromises();
    expect(w.text()).toContain("Of the 2 messages your search shows in checkout, those selected below get an AI suggestion in de.");
    // Nothing is queued before the person confirms the preview.
    expect(i.calls.find((c) => c[0] === "previewFill")![1]).toEqual({ locales: ["de"], select: "missing", namespace: "checkout", keys: ["a", "b"] });
    expect(i.calls.some((c) => c[0] === "createFill")).toBe(false);
    const preview = w.get("[data-testid=fill-preview]");
    expect(preview.get("[data-testid=fill-plan-messages]").text()).toBe("2 messages to fill:");
    expect(preview.get("[data-testid=fill-plan]").text()).toContain("2 call an AI provider");
    expect(preview.get("details").text()).toContain("Show the 2 keys (de)");
    expect(preview.findAll("details li").map((li) => li.text())).toEqual(["a", "b"]);
    expect(preview.get("[data-testid=fill-cost]").text()).toBe("Estimated cost $0.0020; at most $0.02, which the budget reserves while jobs run.");
    await button(w, "Fill 2 messages").trigger("click");
    await flushPromises();
    expect(i.calls.find((c) => c[0] === "createFill")![1]).toEqual({ locales: ["de"], select: "missing", namespace: "checkout", keys: ["a", "b"] });
    expect(w.get("[data-testid=fill-status]").text()).toBe("0 of 2 done…");
    // One job gets a suggestion, the other's provider fails.
    i.pending = [i.pending[0]!];
    i.settle();
    await new Promise((r) => setTimeout(r, 1100));
    await flushPromises();
    expect(w.get("[data-testid=fill-status]").text()).toBe("Done: 1 suggested, 0 skipped, 1 failed.");
    expect(w.text()).toContain("1 × the provider failed");
    expect(w.emitted("settled")).toHaveLength(1);
    expect(w.findAll("a").map((a) => a.text())).toContain("Open the review queue");
  });

  it("selects by translation state, and previews again when the selection changes", async () => {
    const i = createFakeIntelligence();
    i.pending = [suggestion({ id: "a", message_key: "a" })];
    const w = dialog(i, undefined, "outdated");
    await flushPromises();
    expect((w.get("input[value=outdated]").element as HTMLInputElement).checked).toBe(true);
    expect(i.calls.filter((c) => c[0] === "previewFill").map((c) => (c[1] as { select: string }).select)).toEqual(["outdated"]);
    await w.get("input[value=missing_or_outdated]").setValue(true);
    await flushPromises();
    expect(i.calls.filter((c) => c[0] === "previewFill").map((c) => (c[1] as { select: string }).select)).toEqual(["outdated", "missing_or_outdated"]);
    await button(w, "Fill 1 message").trigger("click");
    await flushPromises();
    const body = i.calls.find((c) => c[0] === "createFill")![1];
    expect(body).toEqual({ locales: ["de"], select: "missing_or_outdated", namespace: "checkout" });
    expect(body).not.toHaveProperty("keys");
  });

  it("shows TM reuse, existing jobs and refusals by reason, and flags unpriced models", async () => {
    const i = createFakeIntelligence();
    i.pending = ["a", "b", "c", "d", "e"].map((k) => suggestion({ id: k, message_key: k }));
    i.plan = { existing: 1, tm_exact: 2, provider: 1, refused: { budget_exceeded: 1 }, skipped: { up_to_date: 3 }, cost: { estimated_micro_usd: 900, max_micro_usd: 30_000, unpriced: true } };
    const w = dialog(i);
    await flushPromises();
    const plan = w.get("[data-testid=fill-plan]").findAll("li").map((li) => li.text());
    expect(plan).toEqual([
      "1 calls an AI provider",
      "2 reuse an exact translation-memory match — no provider call",
      "1 already has a job, reused rather than run again",
      "1 won't reach a provider: over this month's AI budget",
    ]);
    expect(w.text()).toContain("Skipped: 3 up to date.");
    expect(w.text()).toContain("A routed model has no price");
  });

  it("says up front when jobs will do little, and when there's nothing to fill", async () => {
    const i = createFakeIntelligence();
    i.warnings = ["provider_consent_off", "no_budget"];
    i.pending = [suggestion({ id: "a", message_key: "a" })];
    const w = dialog(i);
    await flushPromises();
    const warnings = w.get("[data-testid=fill-warnings]").text();
    expect(warnings).toContain("only exact translation-memory matches are reused");
    expect(warnings).toContain("The monthly AI budget is 0");
    expect(w.get("[data-testid=fill-plan]").text()).toContain("1 won't reach a provider: sending text to AI providers is off");

    const empty = dialog(createFakeIntelligence());
    await flushPromises();
    expect(empty.get("[data-testid=fill-plan-empty]").text()).toBe("Nothing to fill: no message in scope has a translation in the selected state.");
    expect(empty.get("[data-testid=fill-start]").attributes("disabled")).toBeDefined();
  });
});
