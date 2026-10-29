/**
 * The workflow editor (RFC 0006 §8, §14 decision 12): the document as
 * text with the server's findings placed on its lines, the chart drawn
 * beside it, immutable versions, and a save that never silently
 * replaces someone else's.
 */
import { flushPromises, type VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";
import { ApiError } from "../../api/errors";
import type { Role } from "../../api/schemas";
import type { WorkflowDocument } from "../../api/workflows-schemas";
import { CHART_RENDERER, type RenderChart } from "../../lib/mermaid";
import { formatDocument } from "../../lib/workflow";
import { createFakeWorkflows, definition, reviewDocument, type FakeWorkflows } from "../../test/fake-workflows";
import { mountTenantScreen } from "../../test/project";
import WorkflowEditorView from "./WorkflowEditorView.vue";

let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

const drawn: string[] = [];
const fakeRender: RenderChart = async (_id, source) => {
  drawn.push(source);
  return `<svg data-testid="fake-svg"><text>${source.split("\n").length}</text></svg>`;
};

async function screen(port: FakeWorkflows, path = "/t/t/settings/workflows/wd1", roles: Role[] = ["admin"]) {
  wrapper = await mountTenantScreen(WorkflowEditorView, {
    workflows: port,
    path,
    roles,
    props: { lintDelay: 0, chartDelay: 0 },
    provide: { [CHART_RENDERER as symbol]: fakeRender },
  });
  await flushPromises();
  return wrapper;
}

async function type(w: VueWrapper, text: string) {
  await w.get("[data-testid=editor-text]").setValue(text);
  await new Promise((r) => setTimeout(r, 0));
  await flushPromises();
}

const textOf = (w: VueWrapper) => (w.get("[data-testid=editor-text]").element as HTMLTextAreaElement).value;
const withName = (doc: WorkflowDocument, extra: Record<string, unknown>) => formatDocument({ ...doc, ...extra });

describe("WorkflowEditorView", () => {
  it("opens the latest version as text, with the chart drawn and as a table", async () => {
    const port = createFakeWorkflows({ definitions: [definition({ version: 1 })] });
    const w = await screen(port);
    expect(w.get("h1").text()).toBe("Workflow review");
    expect(JSON.parse(textOf(w))).toEqual(reviewDocument());
    expect(w.get("[data-testid=chart-svg]").html()).toContain("fake-svg");
    expect(w.get("[data-testid=chart-svg]").attributes("aria-hidden")).toBe("true");
    expect(drawn.at(-1)).toContain("s_reviewing --> s_done : approval.granted [one_approval] / approve");
    const rows = w.findAll("[data-testid=chart-table] tbody tr").map((r) => r.findAll("th,td").map((c) => c.text()));
    expect(rows).toContainEqual(["reviewing", "approval.granted", "one_approval", "approve", "done"]);
    expect(w.get("[data-testid=chart-finals]").text()).toBe("Final states: done.");
    expect(w.find("[data-testid=editor-dirty]").exists()).toBe(false);
    expect(w.get("[data-testid=editor-save]").attributes("disabled")).toBeDefined();
  });

  it("tells a failed read apart, and reads again", async () => {
    const port = createFakeWorkflows({ definitions: [definition()] });
    port.fail.definition = new ApiError(0, "network_error", "The server could not be reached.");
    const w = await screen(port);
    expect(w.get("[data-testid=editor-failed]").text()).toContain("This workflow could not be read.");
    delete port.fail.definition;
    await w.get("[data-testid=editor-failed] button").trigger("click");
    await flushPromises();
    expect(w.find("[data-testid=editor-text]").exists()).toBe(true);
  });

  it("checks a moment after typing and places the findings on their lines", async () => {
    const port = createFakeWorkflows({ definitions: [definition()] });
    port.lintWith = () => ({
      valid: false,
      findings: [
        { rule: "unknown-primitive", severity: "error", path: "guards.one_approval", message: "No guard is called approvals_at_lest." },
        { rule: "statechart", severity: "warning", state: "done", message: "Final state has transitions." },
        { rule: "note", severity: "info", message: "A note about the whole document." },
      ],
    });
    const w = await screen(port);
    const doc = reviewDocument();
    const text = withName(doc, { guards: { one_approval: { use: "approvals_at_lest", n: 1 } } });
    await type(w, text);

    expect(port.calls.some((c) => c[0] === "lint")).toBe(true);
    expect(w.get("[data-testid=editor-findings-summary]").text()).toBe("2 findings: a save would be refused.");
    const items = w.findAll("[data-testid=editor-finding]");
    expect(items.map((i) => i.attributes("data-severity"))).toEqual(["error", "warning", "info"]);
    const guardLine = text.split("\n").findIndex((l) => l.includes('"one_approval": {')) + 1;
    expect(items[0]!.get("[data-testid=editor-goto]").text()).toBe(`Go to line ${guardLine}`);
    expect(items[2]!.text()).toContain("the whole document");
    // The gutter marks the line; the list is what assistive technology reads.
    const mark = w.get(`.gutter [data-line="${guardLine}"]`);
    expect(mark.classes()).toContain("mark-error");
    expect(w.get(".gutter").attributes("aria-hidden")).toBe("true");
    expect(w.get("[data-testid=editor-text]").attributes("aria-invalid")).toBe("true");

    // Going to the line puts the caret there.
    await items[0]!.get("[data-testid=editor-goto]").trigger("click");
    const area = w.get("[data-testid=editor-text]").element as HTMLTextAreaElement;
    expect(document.activeElement).toBe(area);
    expect(area.value.slice(area.selectionStart, area.selectionEnd)).toContain('"one_approval": {');
  });

  it("says where the text stops being JSON, and keeps the last chart marked out of date", async () => {
    const port = createFakeWorkflows({ definitions: [definition()] });
    const w = await screen(port);
    const lints = port.calls.filter((c) => c[0] === "lint").length;
    await type(w, '{\n  "schema": "glossa.workflow/v1",\n  "name": \n}');
    expect(w.get("[data-testid=editor-parse]").text()).toMatch(/This isn't valid JSON/);
    expect(w.get("[data-testid=editor-parse]").attributes("role")).toBe("alert");
    expect(w.find("[data-testid=chart-stale]").exists()).toBe(true);
    expect(w.find("[data-testid=chart-svg]").exists()).toBe(true);
    // Nothing that doesn't parse is sent to the server.
    expect(port.calls.filter((c) => c[0] === "lint").length).toBe(lints);
    expect(w.get("[data-testid=editor-save]").attributes("disabled")).toBeDefined();
  });

  it("saves the next version with the version edited as If-Match, and lists it", async () => {
    const port = createFakeWorkflows({ definitions: [definition({ version: 1 })] });
    const w = await screen(port);
    await type(w, withName(reviewDocument(), { guards: { one_approval: { use: "approvals_at_least", n: 2, distinct_from_author: true } } }));
    expect(w.get("[data-testid=editor-dirty]").text()).toBe("Unsaved changes");
    expect(w.get("[data-testid=editor-save]").text()).toBe("Save version 2");
    await w.get("[data-testid=editor-save]").trigger("click");
    await flushPromises();
    const save = port.calls.find((c) => c[0] === "save")!;
    expect(save[4]).toBe('"1"');
    expect(w.get("[data-testid=editor-status]").text()).toBe("Saved review as version 2. Running work stays on the version it started with.");
    expect(w.find("[data-testid=editor-dirty]").exists()).toBe(false);
    expect(w.findAll("[data-testid=editor-versions] li").map((l) => l.attributes("data-version"))).toEqual(["2", "1"]);
  });

  it("shows a refused save's findings inline, and stores nothing", async () => {
    const port = createFakeWorkflows({ definitions: [definition()] });
    const w = await screen(port);
    await type(w, withName(reviewDocument(), { chart: { id: "x", initial: "a", states: { a: { id: "a", type: "atomic" } } } }));
    port.lintWith = () => ({ valid: false, findings: [{ rule: "statechart", severity: "error", state: "a", message: "a is a dead end that is not final." }] });
    await w.get("[data-testid=editor-save]").trigger("click");
    await flushPromises();
    expect(w.get("[data-testid=editor-status]").text()).toBe("Not saved: 1 finding to fix first. Nothing was stored.");
    expect(w.get("[data-testid=editor-finding]").text()).toContain("a is a dead end that is not final.");
    expect(document.activeElement?.id).toBe("wf-findings-h");
    expect(port.state.definitions[0]!.version).toBe(1);
    expect(w.find("[data-testid=editor-dirty]").exists()).toBe(true);
  });

  it("keeps my text when another save overtook mine, and lets me choose", async () => {
    const port = createFakeWorkflows({ definitions: [definition({ version: 1 })] });
    const w = await screen(port);
    const mine = withName(reviewDocument(), { guards: { one_approval: { use: "approvals_at_least", n: 3, distinct_from_author: true } } });
    await type(w, mine);
    // Someone else saves version 2 meanwhile.
    await port.save("t", "wd1", reviewDocument(), '"1"');
    await w.get("[data-testid=editor-save]").trigger("click");
    await flushPromises();
    expect(w.get("[data-testid=editor-overtaken]").attributes("role")).toBe("alert");
    expect(textOf(w)).toBe(mine);

    await w.get("[data-testid=editor-keep-mine]").trigger("click");
    await flushPromises();
    expect(port.state.definitions[0]!.version).toBe(3);
    expect(w.find("[data-testid=editor-overtaken]").exists()).toBe(false);
    expect(w.get("[data-testid=editor-status]").text()).toContain("version 3");
  });

  it("refuses to save a renamed document, and says why", async () => {
    const port = createFakeWorkflows({ definitions: [definition()] });
    const w = await screen(port);
    await type(w, withName(reviewDocument(), { name: "review-2" }));
    expect(w.get("[data-testid=editor-renamed]").text()).toContain("The name changed from review to review-2");
    expect(w.get("[data-testid=editor-save]").attributes("disabled")).toBeDefined();
  });

  it("reads an old version read-only, and edits from it into a new version", async () => {
    const port = createFakeWorkflows({ definitions: [definition({ version: 1 })] });
    await port.save("t", "wd1", { ...reviewDocument(), guards: { one_approval: { use: "approvals_at_least", n: 2, distinct_from_author: true } } }, '"1"');
    const w = await screen(port, "/t/t/settings/workflows/wd1?version=1");
    expect(w.get("[data-testid=editor-viewing]").text()).toContain("You are reading version 1; the latest is version 2.");
    expect(w.get("[data-testid=editor-text]").attributes("readonly")).toBeDefined();
    expect(JSON.parse(textOf(w))).toEqual(reviewDocument());
    expect(w.find("[data-testid=editor-save]").exists()).toBe(false);

    await w.get("[data-testid=editor-restore]").trigger("click");
    await flushPromises();
    expect(w.find("[data-testid=editor-viewing]").exists()).toBe(false);
    expect(JSON.parse(textOf(w))).toEqual(reviewDocument());
    expect(w.get("[data-testid=editor-save]").text()).toBe("Save version 3");
    expect(w.get("[data-testid=editor-status]").text()).toBe("Version 1 is in the editor. Save it to make it the latest.");
  });

  it("is read-only without workflows.manage", async () => {
    const port = createFakeWorkflows({ definitions: [definition()] });
    const w = await screen(port, "/t/t/settings/workflows/wd1", ["developer"]);
    expect(w.get("[data-testid=editor-read-only]").text()).toContain("needs the owner or admin role");
    expect(w.get("[data-testid=editor-text]").attributes("readonly")).toBeDefined();
    expect(w.find("[data-testid=editor-save]").exists()).toBe(false);
    expect(w.find("[data-testid=editor-check]").exists()).toBe(false);
  });

  it("creates version 1 from a starter, scoped to a project if chosen, and opens it", async () => {
    const port = createFakeWorkflows();
    const w = await screen(port, "/t/t/settings/workflows/new");
    expect(w.get("h1").text()).toBe("New workflow");
    expect(JSON.parse(textOf(w))).toMatchObject({ schema: "glossa.workflow/v1", name: "my-workflow", subject: "translation" });
    await w.get("#wf-subject").setValue("release_request");
    expect(JSON.parse(textOf(w))).toMatchObject({ subject: "release_request" });
    const doc = JSON.parse(textOf(w)) as WorkflowDocument;
    await type(w, withName(doc, { name: "ship-it" }));
    await w.get("[data-testid=editor-save]").trigger("click");
    await flushPromises();
    const create = port.calls.find((c) => c[0] === "create")!;
    expect((create[2] as WorkflowDocument).name).toBe("ship-it");
    expect(create[3]).toBeUndefined();
    expect(port.state.definitions).toHaveLength(1);
    expect(w.get("[data-testid=editor-status]").text()).toBe("Saved ship-it as version 1.");
    // Now the editor of the new definition.
    expect(w.get("h1").text()).toBe("Workflow ship-it");
  });

  it("says a taken name in a sentence", async () => {
    const port = createFakeWorkflows({ definitions: [definition({ name: "my-workflow" })] });
    const w = await screen(port, "/t/t/settings/workflows/new");
    await w.get("[data-testid=editor-save]").trigger("click");
    await flushPromises();
    expect(w.text()).toContain("A workflow with this name exists already.");
  });
});
