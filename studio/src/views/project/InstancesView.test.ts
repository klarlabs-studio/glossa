/**
 * A project's workflow instances (RFC 0006 §2.5): filters that live in
 * the query, the loading / failed / empty states kept apart, and the
 * server without an instance store said as such.
 */
import { flushPromises, type VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";
import { ApiError } from "../../api/errors";
import { createFakeWorkflows, definition, instance } from "../../test/fake-workflows";
import { mountProjectScreen } from "../../test/project";
import InstancesView from "./InstancesView.vue";

let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

async function screen(workflows = createFakeWorkflows(), path = "/t/t/p/p/workflow/instances") {
  wrapper = await mountProjectScreen(InstancesView, { workflows, path });
  return wrapper;
}

describe("InstancesView", () => {
  it("says it is loading, and claims nothing yet", async () => {
    const wf = createFakeWorkflows();
    wf.hold.add("instances");
    const w = await screen(wf);
    expect(w.get("[data-testid=instances-loading]").text()).toBe("Loading instances…");
    expect(w.find("[data-testid=instances-empty]").exists()).toBe(false);
  });

  it("tells a failed read apart from no instances, and reads again", async () => {
    const wf = createFakeWorkflows({ instances: [instance()] });
    wf.fail.instances = new ApiError(0, "network_error", "x");
    const w = await screen(wf);
    expect(w.get("[data-testid=instances-failed]").text()).toContain("could not be read");
    expect(w.find("[data-testid=instances-empty]").exists()).toBe(false);
    delete wf.fail.instances;
    await w.get("[data-testid=instances-failed] button").trigger("click");
    await flushPromises();
    expect(w.findAll("[data-testid=instance-row]")).toHaveLength(1);
  });

  it("says a server without an instance store in its own sentence", async () => {
    const wf = createFakeWorkflows();
    wf.fail.instances = new ApiError(503, "workflow_instances_unavailable", "Unavailable.");
    const w = await screen(wf);
    expect(w.get("[data-testid=instances-failed]").text()).toContain("This server keeps no workflow instances");
  });

  it("says so when nothing matches", async () => {
    const w = await screen();
    expect(w.get("[data-testid=instances-empty]").text()).toContain("No instances match");
  });

  it("lists instances with their workflow by name and version, linking to each", async () => {
    const wf = createFakeWorkflows({
      definitions: [definition({ id: "wd1", name: "vendor-then-four-eyes", version: 3 })],
      instances: [instance({ id: "wi1", definition_version: 2, subject_id: "m12345678" }), instance({ id: "wi2", subject: "release_request", subject_id: "rr12345678", locale: undefined, status: "finished", state: "deployed" })],
    });
    const w = await screen(wf);
    const rows = w.findAll("[data-testid=instance-row]");
    expect(rows).toHaveLength(2);
    expect(rows[0]!.text()).toContain("vendor-then-four-eyes v2");
    expect(rows[0]!.text()).toContain("Message m1234567");
    expect(rows[0]!.text()).toContain("Active");
    expect(rows[1]!.text()).toContain("Release request rr123456");
    expect(rows[1]!.text()).toContain("Finished");
    expect(rows[0]!.get("[data-testid=instance-open]").attributes("href")).toBe("/t/t/p/p/workflow/instances/wi1");
    expect(rows[0]!.get("[data-testid=instance-open]").text()).toContain("instance for Message m1234567");
  });

  it("reads its filters from the query, names the key, and carries it to the instance", async () => {
    const wf = createFakeWorkflows({ instances: [instance({ id: "wi1", locale: "de" }), instance({ id: "wi2", locale: "fr" })] });
    const w = await screen(wf, "/t/t/p/p/workflow/instances?message=checkout.pay&locale=de&status=active");
    expect(wf.calls.find((c) => c[0] === "instances")![2]).toEqual({ status: "active", locale: "de", message: "checkout.pay" });
    expect((w.get("#inst-locale").element as HTMLInputElement).value).toBe("de");
    expect((w.get("#inst-message").element as HTMLInputElement).value).toBe("checkout.pay");
    const rows = w.findAll("[data-testid=instance-row]");
    expect(rows).toHaveLength(1);
    expect(rows[0]!.get("th").text()).toBe("checkout.pay");
    expect(rows[0]!.get("[data-testid=instance-open]").attributes("href")).toBe("/t/t/p/p/workflow/instances/wi1?key=checkout.pay");
  });

  it("puts a submitted filter in the query and reads again; clearing drops it", async () => {
    const wf = createFakeWorkflows({ instances: [instance({ id: "wi1", status: "active" }), instance({ id: "wi2", status: "finished" })] });
    const w = await screen(wf);
    expect(w.findAll("[data-testid=instance-row]")).toHaveLength(2);
    await w.get("#inst-status").setValue("finished");
    await w.get("[data-testid=instances-filters]").trigger("submit");
    await flushPromises();
    expect(w.vm.$route.query).toEqual({ status: "finished" });
    expect(w.findAll("[data-testid=instance-row]").map((r) => r.attributes("data-instance"))).toEqual(["wi2"]);
    await w.get("[data-testid=instances-clear]").trigger("click");
    await flushPromises();
    expect(w.vm.$route.query).toEqual({});
    expect(w.findAll("[data-testid=instance-row]")).toHaveLength(2);
  });

  it("loads more when the server has another page", async () => {
    const wf = createFakeWorkflows({ instances: [instance({ id: "wi1" })] });
    const first = wf.instances.bind(wf);
    let n = 0;
    wf.instances = async (p, q, token) => {
      n++;
      const page = await first(p, q, token);
      return n === 1 ? { items: page.items, next: "t2" } : { items: [instance({ id: "wi9" })], next: undefined };
    };
    const w = await screen(wf);
    await w.get("[data-testid=instances-more]").trigger("click");
    await flushPromises();
    expect(w.findAll("[data-testid=instance-row]").map((r) => r.attributes("data-instance"))).toEqual(["wi1", "wi9"]);
    expect(w.find("[data-testid=instances-more]").exists()).toBe(false);
  });
});
