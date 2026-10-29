import { flushPromises, type DOMWrapper, type VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";
import { createFakeKnowledge, type FakeKnowledge } from "../../test/fake-knowledge";
import { locale, mountProjectScreen } from "../../test/project";
import StyleGuidesView from "./StyleGuidesView.vue";
import TermbaseView from "./TermbaseView.vue";

let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

const locales = [locale("de"), locale("en", true)];
const button = (root: { findAll(s: string): DOMWrapper<Element>[] }, name: string) => {
  const b = root.findAll("button").find((x) => x.text() === name || x.attributes("aria-label") === name);
  if (!b) throw new Error(`no button ${name}`);
  return b;
};
const dialog = (w: VueWrapper) => w.get("dialog[open]");

describe("TermbaseView", () => {
  const screen = async (k: FakeKnowledge, roles: Array<"developer" | "translator"> = ["developer"]) =>
    (wrapper = await mountProjectScreen(TermbaseView, { knowledge: k, locales, roles, path: "/t/t/p/p/terms" }));

  it("adds a project concept with terms per locale, the source locale first", async () => {
    const k = createFakeKnowledge();
    const w = await screen(k);
    expect(w.text()).toContain("No concepts yet.");
    await button(w, "New concept").trigger("click");
    await flushPromises();
    const d = dialog(w);
    await d.get("#c-def").setValue("Shared environment");
    expect(d.findAll("[data-testid=term-row] select").map((s) => (s.element as HTMLSelectElement).value).filter((v) => v === "en" || v === "de")).toEqual(["en", "de"]);
    await d.get("[aria-label='Term 1']").setValue("workspace");
    await d.get("[aria-label='Term 2']").setValue("Arbeitsbereich");
    await button(d, "Add term").trigger("click");
    await d.get("[aria-label='Locale 3']").setValue("de");
    await d.get("[aria-label='Term 3']").setValue("Workspace");
    await d.get("[aria-label='Status 3']").setValue("forbidden");
    await d.get("[aria-label='Part of speech 3']").setValue("noun");
    await d.get("form").trigger("submit");
    await flushPromises();
    const [, body] = k.calls.find((c) => c[0] === "createConcept")!;
    expect(body).toEqual({
      project_id: "p",
      definition: "Shared environment",
      domain: "",
      note: "",
      terms: [
        { locale: "en", text: "workspace", status: "preferred", case_sensitive: false },
        { locale: "de", text: "Arbeitsbereich", status: "preferred", case_sensitive: false },
        { locale: "de", text: "Workspace", status: "forbidden", case_sensitive: false, part_of_speech: "noun" },
      ],
    });
    expect(w.get("[data-testid=termbase-status]").text()).toBe("Concept added.");
    const card = w.get("[data-testid=concepts]");
    expect(card.text()).toContain("workspace");
    expect(card.text()).toContain("This project");
    expect(card.text()).toContain("Forbidden");
  });

  it("edits with the concept's ETag and shows its history", async () => {
    const k = createFakeKnowledge();
    await k.createConcept("t", { definition: "Old", terms: [{ locale: "en", text: "invoice", case_sensitive: false }] }, "x");
    const w = await screen(k);
    await button(w, "Edit invoice").trigger("click");
    await flushPromises();
    const d = dialog(w);
    expect((d.get("#c-scope").element as HTMLSelectElement).disabled).toBe(true);
    await d.get("#c-def").setValue("A bill");
    await d.get("form").trigger("submit");
    await flushPromises();
    expect(k.calls.find((c) => c[0] === "replaceConcept")!.slice(1)).toEqual(["concept_1", expect.objectContaining({ definition: "A bill" }), '"1"']);
    await button(w, "History of invoice").trigger("click");
    await flushPromises();
    expect(dialog(w).text()).toContain("v2 · Updated · you");
    expect(dialog(w).text()).toContain("v1 · Created");
  });

  it("is read-only without knowledge.write", async () => {
    const k = createFakeKnowledge();
    await k.createConcept("t", { terms: [{ locale: "en", text: "invoice", case_sensitive: false }] }, "x");
    const w = await screen(k, ["translator"]);
    expect(w.text()).toContain("Changing it needs the developer, admin or owner role.");
    expect(w.findAll("button").map((b) => b.text())).not.toContain("New concept");
    expect(w.findAll("button").map((b) => b.text())).not.toContain("Edit");
  });
});

describe("StyleGuidesView", () => {
  const screen = async (k: FakeKnowledge) => (wrapper = await mountProjectScreen(StyleGuidesView, { knowledge: k, locales, roles: ["developer"], path: "/t/t/p/p/style" }));

  it("creates a locale guide with structured fields and a rule", async () => {
    const k = createFakeKnowledge();
    const w = await screen(k);
    await button(w, "New style guide").trigger("click");
    await flushPromises();
    const d = dialog(w);
    await d.findAll("input[type=radio]").find((r) => (r.element as HTMLInputElement).value === "locale")!.setValue(true);
    await d.get("#sg-locale").setValue("de");
    await d.get("#sg-name").setValue("German");
    await d.get("#sg-register").setValue("informal");
    await d.get("#sg-pronoun").setValue("du");
    await d.get("#sg-tone").setValue("friendly, direct");
    await d.get("#sg-serial_comma").setValue("no");
    await button(d, "Add rule").trigger("click");
    await d.get("#rule-title-0").setValue("Imperative CTAs");
    await d.get("#rule-good-0").setValue("Speichern\nLöschen");
    await d.get("form").trigger("submit");
    await flushPromises();
    const [, body] = k.calls.find((c) => c[0] === "createStyleGuide")!;
    expect(body).toEqual({
      project_id: "p",
      locale: "de",
      name: "German",
      fields: { formality: { register: "informal", pronoun: "du" }, tone: ["friendly", "direct"], punctuation: { serial_comma: false } },
      rules: [{ id: "imperative-ctas", title: "Imperative CTAs", good: ["Speichern", "Löschen"] }],
    });
    const list = w.get("[data-testid=style-guides]").text();
    expect(list).toContain("German");
    expect(list).toContain("Demo · de");
    expect(list).toContain("informal “du”");
    expect(list).toContain("Imperative CTAs");
  });

  it("refuses an invalid rule ID before sending", async () => {
    const k = createFakeKnowledge();
    const w = await screen(k);
    await button(w, "New style guide").trigger("click");
    await flushPromises();
    const d = dialog(w);
    await button(d, "Add rule").trigger("click");
    await d.get("#rule-id-0").setValue("Bad ID");
    await d.get("form").trigger("submit");
    await flushPromises();
    expect(d.text()).toContain("“Bad ID” isn't a valid rule ID.");
    expect(k.calls.some((c) => c[0] === "createStyleGuide")).toBe(false);
  });

  it("lists versions and previews the effective style", async () => {
    const k = createFakeKnowledge();
    const g = await k.createStyleGuide("t", { name: "Workspace", fields: { tone: ["concise"] } }, "x");
    await k.replaceStyleGuide("t", g.value.id, { name: "Workspace", fields: { tone: ["concise", "warm"] } }, g.etag!);
    const w = await screen(k);
    await flushPromises();
    expect(w.get("[data-testid=style-pane]").text()).toContain("concise, warm");
    await button(w, "History of Workspace").trigger("click");
    await flushPromises();
    expect(dialog(w).text()).toContain("v2 · Updated");
    expect(dialog(w).text()).toContain("Tone: concise, warm");
  });
});
