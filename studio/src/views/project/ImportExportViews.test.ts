import { flushPromises, type DOMWrapper, type VueWrapper } from "@vue/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Router } from "vue-router";
import type { ImportJob, ImportResult } from "../../api/integration-schemas";
import { forgetFiles, polling, rememberFile } from "../../lib/integration";
import { createFakeIntegration, type FakeIntegration } from "../../test/fake-integration";
import { createFakeKnowledge } from "../../test/fake-knowledge";
import { locale, mountProjectScreen, type ScreenOptions } from "../../test/project";
import ImportExportView from "./ImportExportView.vue";
import ImportJobView from "./ImportJobView.vue";
import ImportView from "./ImportView.vue";
import TermbaseView from "./TermbaseView.vue";

let wrapper: VueWrapper | undefined;
beforeEach(() => {
  polling.ms = 0;
  forgetFiles();
});
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

const locales = [locale("en", true), locale("de"), locale("fr")];
const xliff = (target: string) => `<?xml version="1.0" encoding="UTF-8"?>
<xliff xmlns="urn:oasis:names:tc:xliff:document:2.0" version="2.1" srcLang="en" trgLang="${target}"><file id="f1">
<unit id="checkout.title"><segment state="translated"><source>Checkout</source><target>Kasse</target></segment></unit>
</file></xliff>`;
const PO = `msgid ""
msgstr ""
"Language: de\\n"

msgid "Add to cart"
msgstr "In den Warenkorb"
`;

async function screen(component: Parameters<typeof mountProjectScreen>[0], fake: FakeIntegration, options: Partial<ScreenOptions> = {}) {
  wrapper = await mountProjectScreen(component, { integration: fake, locales, roles: ["developer"], path: "/t/t/p/p/files/import", ...options });
  return wrapper;
}
const router = (w: VueWrapper) => w.vm.$router as Router;
const button = (root: { findAll(s: string): DOMWrapper<Element>[] }, name: string) => {
  const b = root.findAll("button, a").find((x) => x.text().replace(/\s+/g, " ").trim().startsWith(name));
  if (!b) throw new Error(`no button ${name}`);
  return b;
};
async function pick(w: VueWrapper, selector: string, file: File): Promise<void> {
  const input = w.get(selector);
  Object.defineProperty(input.element, "files", { value: [file], configurable: true });
  await input.trigger("change");
  await flushPromises();
}
const until = (check: () => void) => vi.waitFor(check, { timeout: 1000, interval: 5 });

/** Run an import through the fake to its end, as the server would. */
async function finished(fake: FakeIntegration, file: File, over: Partial<Parameters<FakeIntegration["createImport"]>[1]> = {}): Promise<ImportJob> {
  let j = await fake.createImport("t", { project_id: "p", format: "xliff", mode: "dry_run", file_name: file.name, ...over }, "k");
  j = await fake.upload("t", j, file);
  while (j.state === "queued" || j.state === "running") j = await fake.importJob("t", j.id);
  fake.calls.length = 0;
  return j;
}

const conflictResults = (): Array<Omit<ImportResult, "seq">> => [
  { kind: "translation", key: "checkout.title", locale: "de", status: "created", line: 2, column: 1, ref: "#/f=f1/u=checkout.title" },
  { kind: "translation", key: "cart.items", locale: "de", status: "conflict", code: "approved_translation_conflict", line: 3, column: 5, ref: "#/f=f1/u=cart.items" },
  { kind: "translation", key: "bad key", locale: "de", status: "invalid", code: "invalid_message_key", line: 4 },
];

describe("ImportView", () => {
  it("recognizes an XLIFF file, starts with a dry run and lands on its results", async () => {
    const fake = createFakeIntegration();
    const w = await screen(ImportView, fake);
    const file = new File([xliff("de")], "de.xlf");
    await pick(w, "#imp-file", file);
    expect((w.get("#imp-format").element as HTMLSelectElement).value).toBe("xliff");
    expect(w.text()).toContain("Recognized as XLIFF 2.1.");
    expect(w.get("[data-testid=xliff-target]").text()).toBe("The file says de (trgLang). Choose another locale to import its translations as that one.");
    expect((w.get("#imp-xliff-locale").element as HTMLSelectElement).value).toBe("de");
    expect((w.get("input[name=imp-mode][value=dry_run]").element as HTMLInputElement).checked).toBe(true);
    expect(w.text()).toContain("Nothing changes.");
    await w.get("form").trigger("submit");
    await until(() => expect(router(w).currentRoute.value.name).toBe("import-job"));
    expect(fake.calls.find((c) => c[0] === "createImport")![1]).toEqual({
      format: "xliff",
      mode: "dry_run",
      file_name: "de.xlf",
      project_id: "p",
      options: { locale: "de" },
    });
    expect(router(w).currentRoute.value.params.job).toBe("import_1");
    expect(fake.files.get("import_1")).toContain("<target>Kasse</target>");
  });

  it("imports an XLIFF file as the locale the member chooses when the project lacks its trgLang, or it names none", async () => {
    const fake = createFakeIntegration();
    const w = await screen(ImportView, fake);
    await pick(w, "#imp-file", new File([xliff("de-CH")], "shop.xlf"));
    expect(w.get("[data-testid=xliff-target]").text()).toContain("The file says de-CH (trgLang), which isn't a locale of this project");
    expect((w.get("#imp-xliff-locale").element as HTMLSelectElement).value).toBe("");
    expect(w.findAll("#imp-xliff-locale option").map((o) => o.text())).toEqual(["Choose a locale…", "de", "fr"]);
    expect(w.get("[data-testid=import-problem]").text()).toBe("Choose the locale to import the file's translations as.");
    expect(w.get("button[type=submit]").attributes("disabled")).toBeDefined();
    await w.get("#imp-xliff-locale").setValue("de");
    expect(w.find("[data-testid=import-problem]").exists()).toBe(false);
    await w.get("form").trigger("submit");
    await until(() => expect(fake.calls.find((c) => c[0] === "createImport")?.[1]).toMatchObject({ format: "xliff", options: { locale: "de" } }));

    wrapper?.unmount();
    const w2 = await screen(ImportView, createFakeIntegration());
    await pick(w2, "#imp-file", new File([xliff("fr").replace(' trgLang="fr"', "")], "no-target.xlf"));
    expect(w2.get("[data-testid=xliff-target]").text()).toBe("The file names no target locale (trgLang): choose the locale its translations are in.");
    expect(w2.get("[data-testid=import-problem]").text()).toBe("Choose the locale to import the file's translations as.");
  });

  it("mirrors the locale scope: a translator can't import other locales, the source, TMX or overwrite", async () => {
    const fake = createFakeIntegration();
    const w = await screen(ImportView, fake, { roles: ["translator"], memberLocales: ["de"] });
    await pick(w, "#imp-file", new File([xliff("fr")], "fr.xlf"));
    expect(w.get("[data-testid=import-problem]").text()).toBe("You can't import fr: it's outside the locales you may translate.");
    expect(w.findAll("#imp-xliff-locale option").map((o) => [o.text(), o.attributes("disabled") !== undefined])).toEqual([
      ["Choose a locale…", true],
      ["de", false],
      ["fr — outside your locales", true],
    ]);
    expect(w.get("button[type=submit]").attributes("disabled")).toBeDefined();
    expect(w.get("input[name=imp-mode][value=overwrite]").attributes("disabled")).toBeDefined();
    const formats = w.findAll("#imp-format option").map((o) => [o.text(), o.attributes("disabled") !== undefined]);
    expect(formats).toContainEqual(["TMX 1.4b — needs the manage permission", true]);

    await pick(w, "#imp-file", new File(['{"checkout.title": "Kasse"}'], "strings.json"));
    const opts = w.findAll("#imp-json-locale option").map((o) => [o.text(), o.attributes("disabled") !== undefined]);
    expect(opts).toEqual([
      ["en — source catalog — needs the manage permission", true],
      ["de", false],
      ["fr — outside your locales", true],
    ]);
    expect((w.get("#imp-json-locale").element as HTMLSelectElement).value).toBe("de");
    await w.get("form").trigger("submit");
    await until(() => expect(fake.calls.some((c) => c[0] === "upload")).toBe(true));
    expect(fake.calls.find((c) => c[0] === "createImport")![1]).toEqual({
      format: "json",
      mode: "dry_run",
      file_name: "strings.json",
      project_id: "p",
      options: { locale: "de", syntax: "mf1", state: "needs_review" },
    });
  });

  it("asks before overwriting", async () => {
    const fake = createFakeIntegration();
    const w = await screen(ImportView, fake);
    await pick(w, "#imp-file", new File(['{"a": "b"}'], "de.json"));
    expect((w.get("#imp-json-locale").element as HTMLSelectElement).value).toBe("de");
    await w.get("input[name=imp-mode][value=overwrite]").setValue(true);
    expect(w.get("button[type=submit]").text()).toBe("Overwrite…");
    await w.get("form").trigger("submit");
    const dialog = w.get("dialog[open]");
    expect(dialog.text()).toContain("Overwrite with this file?");
    expect(dialog.text()).toContain("approved translations and changed source text included");
    expect(fake.calls.some((c) => c[0] === "createImport")).toBe(false);
    await button(dialog, "Overwrite").trigger("click");
    await until(() => expect(fake.calls.find((c) => c[0] === "createImport")?.[1]).toMatchObject({ mode: "overwrite" }));
  });

  it("reads a PO file's Language header, and imports TMX into the project (the workspace's is elsewhere)", async () => {
    const fake = createFakeIntegration();
    const w = await screen(ImportView, fake);
    await pick(w, "#imp-file", new File([PO], "messages.po"));
    expect(w.get("#imp-po-locale option").text()).toBe("From the file's Language header (de)");
    expect((w.get("#imp-state").element as HTMLSelectElement).value).toBe("approved");
    await w.get("#imp-ns").setValue("shop");
    await w.get("form").trigger("submit");
    await until(() => expect(fake.calls.find((c) => c[0] === "createImport")?.[1]).toMatchObject({ format: "po", options: { state: "approved", namespace: "shop" } }));

    wrapper?.unmount();
    const fake2 = createFakeIntegration();
    const w2 = await screen(ImportView, fake2);
    await pick(w2, "#imp-file", new File(['<?xml version="1.0"?><tmx version="1.4"><body/></tmx>'], "memory.tmx"));
    expect(w2.get("[data-testid=knowledge-scope]").text()).toContain("Imports into this project's translation memory.");
    expect(w2.get("[data-testid=knowledge-scope] a").attributes("href")).toBe("/t/t/settings/knowledge");
    await w2.get("form").trigger("submit");
    await until(() =>
      expect(fake2.calls.find((c) => c[0] === "createImport")?.[1]).toEqual({ format: "tmx", mode: "dry_run", file_name: "memory.tmx", project_id: "p" }),
    );
  });
});

describe("ImportJobView", () => {
  it("summarizes a dry run, explains its conflict with where it is, filters, and applies it for real", async () => {
    const fake = createFakeIntegration();
    fake.resultsFor = conflictResults;
    const file = new File([xliff("de")], "de.xlf");
    const dry = await finished(fake, file);
    rememberFile(dry.id, file);
    const w = await screen(ImportJobView, fake, { path: `/t/t/p/p/files/imports/${dry.id}` });
    expect(w.get("h1").text()).toBe("Import of de.xlf");
    expect(w.get("[data-testid=dry-run-notice]").text()).toContain("Dry run: nothing changed.");
    const tiles = w.findAll("[data-testid=import-summary] .tile").map((t) => t.text());
    expect(tiles).toEqual(["Created1", "Updated0", "Unchanged0", "Conflict1", "Invalid1"]);
    const conflict = w.get("[data-testid=import-results] tr[data-status=conflict]");
    expect(conflict.text()).toContain("cart.items");
    expect(conflict.text()).toContain("line 3, column 5");
    expect(conflict.get(".ref").text()).toBe("#/f=f1/u=cart.items");
    expect(w.get("tr[data-status=created]").text()).toContain("line 2, column 1");
    expect(conflict.text()).toContain("An approved translation differs; the approved one is kept.");
    expect(w.get("tr[data-status=invalid]").text()).toContain("line 4");

    await w.get("#res-status").setValue("conflict");
    await flushPromises();
    expect(fake.calls.filter((c) => c[0] === "importResults").at(-1)![2]).toEqual({ status: "conflict" });
    expect(w.findAll("[data-testid=import-results] tbody tr")).toHaveLength(1);

    await button(w, "Apply this import").trigger("click");
    await until(() => expect(router(w).currentRoute.value.params.job).toBe("import_2"));
    expect(fake.calls.find((c) => c[0] === "createImport")![1]).toEqual({ format: "xliff", mode: "merge", file_name: "de.xlf", project_id: "p" });
    expect(fake.files.get("import_2")).toBe(xliff("de"));
  });

  it("after a reload, applies only the very file the dry run checked", async () => {
    const fake = createFakeIntegration();
    const dry = await finished(fake, new File([xliff("de")], "de.xlf"));
    const w = await screen(ImportJobView, fake, { path: `/t/t/p/p/files/imports/${dry.id}` });
    expect(w.find("#apply-file").exists()).toBe(true);
    await pick(w, "#apply-file", new File([xliff("fr")], "de.xlf"));
    await until(() => expect(w.text()).toContain("That isn't the file this dry run checked"));
    expect(fake.calls.some((c) => c[0] === "createImport")).toBe(false);
    await pick(w, "#apply-file", new File([xliff("de")], "de.xlf"));
    await until(() => expect(fake.calls.find((c) => c[0] === "createImport")?.[1]).toMatchObject({ mode: "merge" }));
  });

  it("says when the server reused an earlier import, and why a job failed", async () => {
    const fake = createFakeIntegration();
    fake.resultsFor = conflictResults;
    const file = new File([xliff("de")], "de.xlf");
    const first = await finished(fake, file, { mode: "merge" });
    const again = await finished(fake, file, { mode: "merge" });
    expect(again.reused_job_id).toBe(first.id);
    const w = await screen(ImportJobView, fake, { path: `/t/t/p/p/files/imports/${again.id}` });
    const notice = w.get("[data-testid=reused-notice]");
    expect(notice.text()).toContain("nothing was applied again");
    expect(notice.get("a").attributes("href")).toBe(`/t/t/p/p/files/imports/${first.id}`);
    expect(w.findAll("[data-testid=import-results] tbody tr")).toHaveLength(3);
    wrapper?.unmount();

    fake.imports.splice(0, 1, { ...first, state: "failed", failure_code: "invalid_file", failure_message: "line 3: unexpected end" });
    const w2 = await screen(ImportJobView, fake, { path: `/t/t/p/p/files/imports/${first.id}` });
    expect(w2.get("[role=alert]").text()).toBe("The import failed: the file is malformed (line 3: unexpected end).");
  });
});

describe("ImportExportView", () => {
  it("lists imports and exports with state, counts and requester, and cancels a running import", async () => {
    const fake = createFakeIntegration();
    fake.resultsFor = conflictResults;
    await finished(fake, new File([xliff("de")], "de.xlf"), { mode: "merge" });
    let running = await fake.createImport("t", { project_id: "p", format: "json", file_name: "fr.json", options: { locale: "fr" } }, "k2");
    running = await fake.upload("t", running, new File(["{}"], "fr.json"));
    await fake.importJob("t", running.id);
    fake.hold = true;
    const w = await screen(ImportExportView, fake, { path: "/t/t/p/p/files" });
    const rows = w.findAll("[data-testid=import-jobs] tbody tr");
    expect(rows.map((r) => r.find("td").text())).toEqual(["fr.json", "de.xlf"]);
    expect(rows[1]!.text()).toContain("Done");
    expect(rows[1]!.text()).toContain("1 created, 0 updated, 1 conflicts, 1 invalid");
    expect(rows[1]!.text()).toContain("you");
    expect(rows[0]!.text()).toContain("Running");
    await button(rows[0]!, "Cancel").trigger("click");
    await flushPromises();
    expect(fake.calls.some((c) => c[0] === "cancelImport")).toBe(true);
    expect(w.get("[data-testid=files-status]").text()).toBe("Stopping after the current batch…");
    expect(w.text()).toContain("No exports yet.");
  });

  it("exports JSON for two locales and chosen namespaces, and downloads the zip", async () => {
    const fake = createFakeIntegration();
    fake.namespaceList = [
      { name: "checkout", active_messages: 12, obsolete_messages: 0 },
      { name: "default", active_messages: 1, obsolete_messages: 2 },
      { name: "landing", active_messages: 3, obsolete_messages: 0 },
    ];
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
    URL.createObjectURL = vi.fn(() => "blob:studio/1");
    URL.revokeObjectURL = vi.fn();
    const w = await screen(ImportExportView, fake, { path: "/t/t/p/p/files" });
    await button(w, "Export").trigger("click");
    const dialog = w.get("dialog[open]");
    expect((dialog.get("#exp-format").element as HTMLSelectElement).value).toBe("json");
    const namespaces = dialog.get("[data-testid=export-namespaces]");
    expect(namespaces.findAll("label").map((l) => l.text())).toEqual(["checkout (12 messages)", "default (1 message)"]);
    await button(namespaces, "Load more").trigger("click");
    await flushPromises();
    expect(namespaces.findAll("label").map((l) => l.text())).toEqual(["checkout (12 messages)", "default (1 message)", "landing (3 messages)"]);
    expect(dialog.findAll("input[type=checkbox]:checked").map((c) => (c.element as HTMLInputElement).value)).toEqual(["en", "de", "fr", "approved"]);
    await namespaces.get("input[value=checkout]").setValue(true);
    await namespaces.get("input[value=landing]").setValue(true);
    await dialog.get("input[type=checkbox][value=fr]").setValue(false);
    await dialog.get("input[type=checkbox][value=needs_review]").setValue(true);
    await dialog.get("form").trigger("submit");
    await until(() => expect(w.find("[data-testid=export-ready]").exists()).toBe(true));
    expect(fake.calls.find((c) => c[0] === "createExport")![1]).toEqual({
      project_id: "p",
      format: "json",
      options: { locales: ["en", "de"], namespaces: ["checkout", "landing"], states: ["approved", "needs_review"], layout: "flat", syntax: "mf1" },
    });
    expect(w.get("[data-testid=export-ready]").text()).toContain("demo.json.zip is ready");
    await button(w.get("dialog[open]"), "Download").trigger("click");
    await flushPromises();
    expect(fake.calls.some((c) => c[0] === "download")).toBe(true);
    expect(click).toHaveBeenCalledOnce();
    expect((click.mock.contexts[0] as HTMLAnchorElement).download).toBe("demo.json.zip");
    expect(w.get("[data-testid=export-saved]").text()).toBe("Saved demo.json.zip.");
  });

  it("exports the project's translation memory, points to the workspace's, and opens the wizard with i", async () => {
    const fake = createFakeIntegration();
    const w = await screen(ImportExportView, fake, { path: "/t/t/p/p/files" });
    await button(w, "Export translation memory (TMX)").trigger("click");
    const dialog = w.get("dialog[open]");
    expect(dialog.get("h2").text()).toBe("Export translation memory");
    expect(dialog.get("[data-testid=knowledge-scope] a").attributes("href")).toBe("/t/t/settings/knowledge");
    expect(dialog.find("[data-testid=export-namespaces]").exists()).toBe(false);
    await dialog.get("#exp-source").setValue("en");
    await dialog.get("form").trigger("submit");
    await until(() => expect(fake.calls.find((c) => c[0] === "createExport")?.[1]).toEqual({ format: "tmx", project_id: "p", options: { source_locale: "en" } }));
    await button(w.get("dialog[open]"), "Close").trigger("click");
    await flushPromises();

    window.dispatchEvent(new KeyboardEvent("keydown", { key: "i", bubbles: true, cancelable: true }));
    await flushPromises();
    expect(router(w).currentRoute.value.name).toBe("import");
  });
});

describe("TermbaseView", () => {
  it("exports the termbase as TBX", async () => {
    const fake = createFakeIntegration();
    const w = await screen(TermbaseView, fake, { knowledge: createFakeKnowledge(), path: "/t/t/p/p/terms" });
    await button(w, "Export termbase (TBX)").trigger("click");
    const dialog = w.get("dialog[open]");
    expect(dialog.get("h2").text()).toBe("Export termbase");
    await dialog.get("form").trigger("submit");
    await until(() => expect(fake.calls.find((c) => c[0] === "createExport")?.[1]).toEqual({ format: "tbx", project_id: "p" }));
  });
});
