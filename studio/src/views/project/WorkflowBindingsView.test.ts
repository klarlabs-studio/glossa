/**
 * A project's workflow bindings (RFC 0006 §2.3): shown in the order the
 * server tries them, bound and unbound by whoever manages workflows, and
 * "which applies?" answered by the server.
 */
import { flushPromises, type VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";
import { ApiError } from "../../api/errors";
import type { Role } from "../../api/schemas";
import { binding, createFakeWorkflows, definition, type FakeWorkflows } from "../../test/fake-workflows";
import { locale, mountProjectScreen } from "../../test/project";
import WorkflowBindingsView from "./WorkflowBindingsView.vue";

let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

const LOCALES = [locale("en", true), locale("de"), locale("fr")];

async function screen(port: FakeWorkflows, roles: Role[] = ["admin"]) {
  wrapper = await mountProjectScreen(WorkflowBindingsView, { workflows: port, roles, locales: LOCALES, path: "/t/t/p/p/workflow" });
  return wrapper;
}

const rows = (w: VueWrapper, subject = "translation") => w.findAll(`[data-testid=bindings-${subject}] tbody tr`);

describe("WorkflowBindingsView", () => {
  it("says so when nothing is bound: no workflow, M4's behaviour", async () => {
    const w = await screen(createFakeWorkflows({ definitions: [definition()] }));
    expect(w.get("[data-testid=bindings-translation] [data-testid=bindings-none]").text()).toBe("No binding: this project's translations run under no workflow.");
    expect(w.get("[data-testid=bindings-instances]").attributes("href")).toBe("/t/t/p/p/workflow/instances");
  });

  it("tells a failed read apart, and reads again", async () => {
    const port = createFakeWorkflows();
    port.fail.bindings = new ApiError(500, "internal", "Something broke.");
    const w = await screen(port);
    expect(w.get("[data-testid=bindings-failed]").text()).toContain("which workflow applies here is not known");
    delete port.fail.bindings;
    await w.get("[data-testid=bindings-failed] button").trigger("click");
    await flushPromises();
    expect(w.find("[data-testid=bindings-failed]").exists()).toBe(false);
  });

  it("lists bindings in the order they are tried: more specific first, the later of a tie first", async () => {
    const port = createFakeWorkflows({
      definitions: [definition({ id: "wd1", name: "review" }), definition({ id: "wd2", name: "legal" }), definition({ id: "wd3", name: "ship", subject: "release_request" })],
      bindings: [
        binding({ id: "all", definition_id: "wd1", position: 1 }),
        binding({ id: "de", definition_id: "wd2", position: 2, locales: ["de"] }),
        binding({ id: "de-checkout", definition_id: "wd1", position: 3, locales: ["de"], namespace: "checkout" }),
        binding({ id: "fr", definition_id: "wd1", position: 4, locales: ["fr"] }),
        binding({ id: "rr", definition_id: "wd3", subject: "release_request", position: 5 }),
      ],
    });
    const w = await screen(port);
    expect(rows(w).map((r) => r.attributes("data-binding"))).toEqual(["de-checkout", "fr", "de", "all"]);
    const first = rows(w)[0]!.findAll("th,td").map((c) => c.text());
    expect(first.slice(0, 5)).toEqual(["1", "review", "de", "checkout", "2 fields"]);
    expect(rows(w)[3]!.text()).toContain("Every locale");
    expect(rows(w)[3]!.text()).toContain("Nothing (the fallback)");
    expect(rows(w)[0]!.get("a").attributes("href")).toBe("/t/t/settings/workflows/wd1");
    expect(rows(w, "release_request").map((r) => r.attributes("data-binding"))).toEqual(["rr"]);
  });

  it("asks the server which applies, and marks the binding it matched", async () => {
    const port = createFakeWorkflows({
      definitions: [definition({ id: "wd1", name: "review", version: 4 }), definition({ id: "wd2", name: "legal" })],
      bindings: [binding({ id: "all", definition_id: "wd1", position: 1 }), binding({ id: "de", definition_id: "wd2", position: 2, locales: ["de"] })],
    });
    const w = await screen(port);
    await w.get("#rs-locale").setValue("fr");
    await w.get("[data-testid=resolve-ask]").trigger("click");
    await flushPromises();
    expect(port.calls).toContainEqual(["resolve", { tenant: "t", project: "p" }, { subject: "translation", locale: "fr" }]);
    expect(w.get("[data-testid=resolve-answer]").text()).toContain("Runs under review, starting on version 4.");
    expect(w.get("[data-testid=resolve-answer]").text()).toContain("Matched the binding tried 2nd.");
    expect(w.get('[data-binding="all"]').attributes("aria-current")).toBe("true");

    port.state.bindings = [];
    await w.get("[data-testid=resolve-ask]").trigger("click");
    await flushPromises();
    expect(w.get("[data-testid=resolve-answer]").text()).toContain("No binding fits: no instance would be created.");
  });

  it("binds a definition to some locales and a namespace, and announces it", async () => {
    const port = createFakeWorkflows({ definitions: [definition({ id: "wd2", name: "legal" })] });
    const w = await screen(port);
    await w.get("[data-testid=bind-definition]").setValue("wd2");
    await w.get('input[type=checkbox][value="de"]').setValue(true);
    await w.get("#bd-ns").setValue("checkout");
    await w.get("[data-testid=bind-submit]").trigger("click");
    await flushPromises();
    expect(port.calls).toContainEqual(["bind", { tenant: "t", project: "p" }, { definition_id: "wd2", locales: ["de"], namespace: "checkout" }]);
    expect(w.get("[data-testid=bindings-status]").text()).toBe("Bound legal. New work that fits it starts under its latest version.");
    expect(rows(w).map((r) => r.attributes("data-binding"))).toHaveLength(1);
  });

  it("says a duplicate selector in a sentence", async () => {
    const port = createFakeWorkflows({ definitions: [definition({ id: "wd1" })], bindings: [binding({ definition_id: "wd1" })] });
    const w = await screen(port);
    await w.get("[data-testid=bind-definition]").setValue("wd1");
    await w.get("[data-testid=bind-submit]").trigger("click");
    await flushPromises();
    expect(w.get("[data-testid=bindings-add]").text()).toContain("A binding for exactly these locales and this namespace exists already.");
  });

  it("unbinds, announces it and returns focus to the heading", async () => {
    const port = createFakeWorkflows({ definitions: [definition({ id: "wd1", name: "review" })], bindings: [binding({ id: "b1", definition_id: "wd1" })] });
    const w = await screen(port);
    await w.get("[data-testid=binding-remove]").trigger("click");
    await flushPromises();
    expect(port.state.bindings).toEqual([]);
    expect(w.get("[data-testid=bindings-status]").text()).toContain("Unbound review.");
    expect(document.activeElement?.tagName).toBe("H1");
  });

  it("is read-only without workflows.manage", async () => {
    const port = createFakeWorkflows({ definitions: [definition()], bindings: [binding()] });
    const w = await screen(port, ["developer"]);
    expect(w.get("[data-testid=bindings-read-only]").text()).toContain("need the owner or admin role");
    expect(w.find("[data-testid=binding-remove]").exists()).toBe(false);
    expect(w.find("[data-testid=bindings-add]").exists()).toBe(false);
    // Asking which applies is reading, so it stays.
    expect(w.find("[data-testid=bindings-resolve]").exists()).toBe(true);
  });
});
