import { flushPromises, type DOMWrapper, type VueWrapper } from "@vue/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Router } from "vue-router";
import { forgetFiles, polling } from "../../lib/integration";
import { createFakeIntegration } from "../../test/fake-integration";
import { mountTenantScreen } from "../../test/project";
import KnowledgeFilesView from "./KnowledgeFilesView.vue";
import KnowledgeImportJobView from "./KnowledgeImportJobView.vue";

let wrapper: VueWrapper | undefined;
beforeEach(() => {
  polling.ms = 0;
  forgetFiles();
});
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

const TMX = `<?xml version="1.0"?><tmx version="1.4"><header srclang="en"/><body>
<tu><tuv xml:lang="en"><seg>Pay</seg></tuv><tuv xml:lang="de"><seg>Zahlen</seg></tuv></tu></body></tmx>`;
const until = (check: () => void) => vi.waitFor(check, { timeout: 1000, interval: 5 });
const button = (root: { findAll(s: string): DOMWrapper<Element>[] }, name: string) => {
  const b = root.findAll("button, a").find((x) => x.text().replace(/\s+/g, " ").trim().startsWith(name));
  if (!b) throw new Error(`no button ${name}`);
  return b;
};

describe("KnowledgeFilesView", () => {
  it("imports a TMX file into the workspace's memory on its own route and lands on its results", async () => {
    const fake = createFakeIntegration();
    fake.resultsFor = () => [{ kind: "tm_unit", key: "", locale: "de", status: "created", line: 2, column: 30, ref: "tu[1]" }];
    wrapper = await mountTenantScreen(KnowledgeFilesView, { integration: fake, path: "/t/t/settings/knowledge" });
    const w = wrapper;
    expect(w.get("h1").text()).toBe("Translation memory & termbase");
    expect(fake.calls.filter((c) => c[0] === "knowledgeImports").map((c) => c[1])).toEqual(["tm", "termbase"]);
    const input = w.get("#kn-file");
    Object.defineProperty(input.element, "files", { value: [new File([TMX], "vendor.tmx")], configurable: true });
    await input.trigger("change");
    await flushPromises();
    expect(w.get("[data-testid=knowledge-file]").text()).toContain("into the workspace's translation memory");
    expect((w.get("input[name=kn-mode][value=dry_run]").element as HTMLInputElement).checked).toBe(true);
    await w.get("form").trigger("submit");
    await until(() => expect((w.vm.$router as Router).currentRoute.value.name).toBe("workspace-import-job"));
    expect(fake.calls.find((c) => c[0] === "createKnowledgeImport")!.slice(1, 3)).toEqual(["tm", { mode: "dry_run", file_name: "vendor.tmx" }]);
    expect(fake.imports[0]!.project_id).toBeUndefined();
  });

  it("asks before overwriting, and refuses what isn't TMX or TBX", async () => {
    const fake = createFakeIntegration();
    wrapper = await mountTenantScreen(KnowledgeFilesView, { integration: fake, path: "/t/t/settings/knowledge" });
    const w = wrapper;
    const input = w.get("#kn-file");
    Object.defineProperty(input.element, "files", { value: [new File(['{"a": "b"}'], "de.json")], configurable: true });
    await input.trigger("change");
    await flushPromises();
    expect(w.get("[role=alert]").text()).toBe("That isn't a TMX or TBX file. Catalogs are imported in a project.");
    Object.defineProperty(input.element, "files", { value: [new File(["<tbx/>"], "terms.tbx")], configurable: true });
    await input.trigger("change");
    await flushPromises();
    await w.get("input[name=kn-mode][value=overwrite]").setValue(true);
    await w.get("form").trigger("submit");
    expect(w.get("dialog[open]").text()).toContain("Overwrite with this file?");
    await button(w.get("dialog[open]"), "Overwrite").trigger("click");
    await until(() => expect(fake.calls.find((c) => c[0] === "createKnowledgeImport")?.slice(1, 3)).toEqual(["termbase", { mode: "overwrite", file_name: "terms.tbx" }]));
  });

  it("mirrors the permissions: a translator can export, not import", async () => {
    wrapper = await mountTenantScreen(KnowledgeFilesView, { integration: createFakeIntegration(), roles: ["translator"], path: "/t/t/settings/knowledge" });
    expect(wrapper.get("[data-testid=knowledge-no-import]").text()).toContain("needs the manage permission");
    expect(wrapper.find("#kn-file").exists()).toBe(false);
    expect(wrapper.find("input[name=kn-export-kind]").exists()).toBe(true);
  });

  it("exports the whole memory narrowed by locales, downloads it, and lists it", async () => {
    const fake = createFakeIntegration();
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
    URL.createObjectURL = vi.fn(() => "blob:studio/1");
    URL.revokeObjectURL = vi.fn();
    wrapper = await mountTenantScreen(KnowledgeFilesView, { integration: fake, path: "/t/t/settings/knowledge" });
    const w = wrapper;
    await w.get("#kn-source").setValue("en");
    await w.get("#kn-target").setValue("de_at");
    await button(w, "Add").trigger("click");
    await w.get("#kn-target").setValue("fr");
    await button(w, "Add").trigger("click");
    expect(w.findAll("[data-testid=knowledge-targets] li").map((l) => l.find(".mono").text())).toEqual(["de-AT", "fr"]);
    await button(w.get("[data-testid=knowledge-targets]"), "×").trigger("click");
    await w.findAll("form")[1]!.trigger("submit");
    await until(() => expect(w.find("[data-testid=knowledge-export-ready]").exists()).toBe(true));
    expect(fake.calls.find((c) => c[0] === "createKnowledgeExport")!.slice(1, 3)).toEqual(["tm", { options: { locales: ["fr"], source_locale: "en" } }]);
    await button(w.get("[data-testid=knowledge-export]"), "Download").trigger("click");
    await flushPromises();
    expect(click).toHaveBeenCalledOnce();
    expect(w.get("[data-testid=knowledge-exports]").text()).toContain("TMX 1.4b");

    await w.get("input[name=kn-export-kind][value=termbase]").setValue(true);
    expect(w.find("#kn-source").exists()).toBe(false);
    await w.findAll("form")[1]!.trigger("submit");
    await until(() => expect(fake.calls.find((c) => c[0] === "createKnowledgeExport" && c[1] === "termbase")?.[2]).toEqual({}));
  });
});

describe("KnowledgeImportJobView", () => {
  it("shows a workspace import's results with where each item is, and links back", async () => {
    const fake = createFakeIntegration();
    fake.resultsFor = () => [
      { kind: "concept", key: "c1", status: "conflict", code: "concept_differs", line: 5, column: 3, ref: "conceptEntry[1]" },
    ];
    let j = await fake.createKnowledgeImport("t", "termbase", { mode: "merge", file_name: "terms.tbx" }, "k");
    j = await fake.upload("t", j, new File(["<tbx/>"], "terms.tbx"));
    while (j.state !== "succeeded") j = await fake.importJob("t", j.id);
    wrapper = await mountTenantScreen(KnowledgeImportJobView, { integration: fake, path: `/t/t/settings/knowledge/imports/${j.id}` });
    const row = wrapper.get("[data-testid=import-results] tr[data-status=conflict]");
    expect(row.text()).toContain("line 5, column 3");
    expect(row.get(".ref").text()).toBe("conceptEntry[1]");
    expect(row.text()).toContain("The termbase's concept differs; the stored one is kept.");
    expect(wrapper.get("a").attributes("href")).toBe("/t/t/settings/knowledge");
  });
});
