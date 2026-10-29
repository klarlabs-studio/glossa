/** The workspace's workflows (RFC 0006 §2.3): listed, opened, deleted with what that means said first. */
import { flushPromises, type VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";
import { ApiError } from "../../api/errors";
import type { Role } from "../../api/schemas";
import { binding, createFakeWorkflows, definition, type FakeWorkflows } from "../../test/fake-workflows";
import { mountTenantScreen } from "../../test/project";
import WorkflowsView from "./WorkflowsView.vue";

let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

const AT = "2026-09-01T00:00:00Z";
const PROJECTS = { "/v1/tenants/t/projects": { items: [{ id: "p", slug: "portal", name: "Portal", source_locale: "en", settings: { default_syntax: "mf2", review_required: true }, created_at: AT, updated_at: AT }] } };

async function screen(port: FakeWorkflows, roles: Role[] = ["admin"]) {
  wrapper = await mountTenantScreen(WorkflowsView, { workflows: port, path: "/t/t/settings/workflows", roles, responses: PROJECTS });
  return wrapper;
}

describe("WorkflowsView", () => {
  it("says it is loading, then lists definitions by name with their scope", async () => {
    const port = createFakeWorkflows({
      definitions: [definition({ id: "b", name: "vendor-then-four-eyes", version: 3, project_id: "p" }), definition({ id: "a", name: "review" })],
    });
    port.hold.add("definitions");
    const held = await screen(port);
    expect(held.get("[data-testid=workflows-loading]").text()).toBe("Loading workflows…");
    held.unmount();
    port.hold.delete("definitions");

    const w = await screen(port);
    const rows = w.findAll("[data-testid=workflow-list] tbody tr");
    expect(rows.map((r) => r.get("th").text())).toEqual(["review", "vendor-then-four-eyes"]);
    expect(rows[1]!.text()).toContain("v3");
    expect(rows[1]!.text()).toContain("Only Portal");
    expect(rows[0]!.text()).toContain("Every project");
    expect(rows[0]!.get("a").attributes("href")).toBe("/t/t/settings/workflows/a");
    expect(w.get("[data-testid=workflow-create]").attributes("href")).toBe("/t/t/settings/workflows/new");
  });

  it("tells a failed read apart from an empty list", async () => {
    const port = createFakeWorkflows();
    port.fail.definitions = new ApiError(403, "forbidden", "Forbidden.");
    const w = await screen(port);
    expect(w.get("[data-testid=workflows-failed]").attributes("role")).toBe("alert");
    expect(w.find("[data-testid=workflows-empty]").exists()).toBe(false);
    delete port.fail.definitions;
    await w.get("[data-testid=workflows-failed] button").trigger("click");
    await flushPromises();
    expect(w.get("[data-testid=workflows-empty]").text()).toContain("Until one is bound to a project");
  });

  it("offers no writing without workflows.manage, and says who may", async () => {
    const w = await screen(createFakeWorkflows({ definitions: [definition()] }), ["reviewer"]);
    expect(w.get("[data-testid=workflows-read-only]").text()).toContain("needs the owner or admin role");
    expect(w.find("[data-testid=workflow-create]").exists()).toBe(false);
    expect(w.find("[data-testid=workflow-remove]").exists()).toBe(false);
  });

  it("deletes after saying what it means — and what deleting the default means", async () => {
    const port = createFakeWorkflows({ definitions: [definition({ id: "a", name: "review" })], bindings: [binding({ definition_id: "a" })] });
    const w = await screen(port);
    await w.get("[data-testid=workflow-remove]").trigger("click");
    await flushPromises();
    expect(w.get("[data-testid=workflow-remove-default]").text()).toContain("means “no workflow”");
    await w.get("[data-testid=workflow-remove-confirm]").trigger("click");
    await flushPromises();
    expect(port.calls).toContainEqual(["remove", "t", "a"]);
    expect(port.state.bindings).toEqual([]);
    expect(w.get("[data-testid=workflows-status]").text()).toBe("Deleted the workflow review. Its bindings were removed.");
    expect(w.find("[data-testid=workflows-empty]").exists()).toBe(true);
    expect(document.activeElement?.tagName).toBe("H1");
  });
});
