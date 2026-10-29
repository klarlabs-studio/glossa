/**
 * One workflow instance and its transition log (RFC 0006 §2.5): each
 * event with its guards, its actions and what became of them, who caused
 * it, and a sentence for `ignored` and `refused`.
 */
import type { VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";
import { ApiError } from "../../api/errors";
import { createFakeWorkflows, definition, instance, transition } from "../../test/fake-workflows";
import { mountProjectScreen } from "../../test/project";
import InstanceView from "./InstanceView.vue";

let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

async function screen(workflows: ReturnType<typeof createFakeWorkflows>, path = "/t/t/p/p/workflow/instances/wi1") {
  wrapper = await mountProjectScreen(InstanceView, { workflows, path });
  return wrapper;
}

const LOG = [
  transition({ seq: 1, event: "translation.outdated", from: "current", to: "reviewing", actor: "system:workflow", actions: [{ name: "back_to_review", outcome: "done" }] }),
  transition({ seq: 2, event: "approval.granted", from: "reviewing", to: "reviewing", outcome: "refused", actor: "person:me", guards: [{ guard: "one_approval", passed: true }], actions: [{ name: "approve", outcome: "refused", detail: "translations.review needed in de" }] }),
  transition({ seq: 3, event: "translation.revised", from: "reviewing", to: "reviewing", outcome: "ignored", actor: "token:abcdef123456" }),
];

describe("InstanceView", () => {
  it("says it is loading", async () => {
    const wf = createFakeWorkflows({ instances: [instance()] });
    wf.hold.add("instance");
    const w = await screen(wf);
    expect(w.get("[data-testid=instance-loading]").text()).toBe("Loading the instance…");
  });

  it("says when the instance can't be read", async () => {
    const wf = createFakeWorkflows();
    const w = await screen(wf);
    expect(w.get("[data-testid=instance-failed]").attributes("role")).toBe("alert");
    expect(w.get("[data-testid=instance-failed]").text()).toContain("This instance could not be read.");
  });

  it("shows the instance with its workflow version, linked to that version", async () => {
    const wf = createFakeWorkflows({ definitions: [definition({ id: "wd1", name: "review", version: 4 })], instances: [instance({ definition_version: 2 })] });
    const w = await screen(wf, "/t/t/p/p/workflow/instances/wi1?key=checkout.pay");
    expect(w.get("h1").text()).toBe("Workflow instance: checkout.pay");
    const def = w.get("[data-testid=instance-definition-link]");
    expect(def.text()).toBe("review v2");
    expect(def.attributes("href")).toBe("/t/t/settings/workflows/wd1?version=2");
    expect(w.get("[data-testid=instance-state]").text()).toBe("reviewing");
    expect(w.get("[data-testid=instance-status]").text()).toBe("Active");
    expect(w.get("[data-testid=instance-workspace-link]").attributes("href")).toBe("/t/t/p/p/translate?locale=de&key=checkout.pay");
  });

  it("links a release request's instance to the request", async () => {
    const wf = createFakeWorkflows({ instances: [instance({ subject: "release_request", subject_id: "rr1", locale: undefined })] });
    const w = await screen(wf);
    expect(w.get("[data-testid=instance-request-link]").attributes("href")).toBe("/t/t/p/p/releases/requests/rr1");
  });

  it("lists every transition with its move, guards, actions, actor and a sentence for its outcome", async () => {
    const wf = createFakeWorkflows({ instances: [instance()], transitions: new Map([["wi1", LOG]]) });
    const w = await screen(wf);
    const items = w.findAll("[data-testid=transition]");
    expect(items.map((t) => t.attributes("data-outcome"))).toEqual(["applied", "refused", "ignored"]);

    expect(items[0]!.get("h3").text()).toBe("#1 translation.outdated");
    expect(items[0]!.get("[data-testid=transition-move]").text()).toBe("current → reviewing");
    expect(items[0]!.get("[data-testid=transition-actions]").text()).toContain("back_to_review");
    expect(items[0]!.get("[data-testid=transition-actor]").text()).toBe("Caused by system:workflow");

    expect(items[1]!.get("[data-testid=transition-outcome]").text()).toBe("Refused");
    expect(items[1]!.get("[data-testid=transition-move]").text()).toBe("stayed in reviewing");
    expect(items[1]!.get("[data-testid=transition-explain]").text()).toContain("refused for permission");
    expect(items[1]!.get("[data-testid=transition-guards]").text()).toContain("one_approval: passed");
    expect(items[1]!.get("[data-testid=transition-actions]").text()).toContain("refused — translations.review needed in de");
    expect(items[1]!.get("[data-testid=transition-actor]").text()).toBe("Caused by you");

    expect(items[2]!.get("[data-testid=transition-explain]").text()).toContain("the world moved on");
    expect(items[2]!.get("[data-testid=transition-actor]").text()).toBe("Caused by API token abcdef12");
  });

  it("shows the instance when only the log fails, and says the log failed", async () => {
    const wf = createFakeWorkflows({ instances: [instance()] });
    wf.fail.transitions = new ApiError(500, "internal", "Broke.");
    const w = await screen(wf);
    expect(w.find("[data-testid=instance-facts]").exists()).toBe(true);
    expect(w.get("[data-testid=instance-log-failed]").text()).toContain("The transition log could not be read.");
  });

  it("says when nothing has happened yet", async () => {
    const wf = createFakeWorkflows({ instances: [instance()] });
    const w = await screen(wf);
    expect(w.get("[data-testid=instance-log-empty]").text()).toBe("No events yet.");
  });
});
