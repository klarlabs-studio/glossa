/**
 * The organization's groups (RFC 0006 §4.3): read by anyone who may read
 * members, changed only with `members.manage`, renamed under the group's
 * ETag, and never presented as carrying permissions.
 */
import { flushPromises, type VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";
import { ApiError } from "../../api/errors";
import type { Role } from "../../api/schemas";
import { createFakeDirectory, group, member, type FakeDirectory } from "../../test/fake-directory";
import { mountTenantScreen } from "../../test/project";
import GroupsView from "./GroupsView.vue";

let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

async function screen(directory: FakeDirectory, roles: Role[] = ["admin"]) {
  wrapper = await mountTenantScreen(GroupsView, { directory, roles, path: "/t/t/settings/groups" });
  return wrapper;
}
const seeded = () =>
  createFakeDirectory({
    members: [member({ id: "m1", display_name: "Ana" }), member({ id: "m2", display_name: "Ben", email: "ben@example.com" })],
    groups: [group({ id: "g1", name: "de reviewers", members: ["m1"] })],
  });
const status = (w: VueWrapper) => w.get("[data-testid=groups-status]").text();

describe("GroupsView", () => {
  it("says it is loading", async () => {
    const dir = createFakeDirectory();
    dir.hold.add("groups");
    const w = await screen(dir);
    expect(w.get("[data-testid=groups-loading]").text()).toBe("Loading groups…");
  });

  it("tells a failed read apart from no groups", async () => {
    const dir = createFakeDirectory();
    dir.fail.groups = new ApiError(500, "internal", "Broke.");
    const w = await screen(dir);
    expect(w.get("[data-testid=groups-failed]").text()).toContain("The groups could not be read");
    expect(w.find("[data-testid=groups-empty]").exists()).toBe(false);
  });

  it("explains that groups carry no permissions, and lists members by name", async () => {
    const w = await screen(seeded());
    expect(w.text()).toContain("Groups carry no permissions");
    const g = w.get("[data-group=g1]");
    expect(g.get("h2").text()).toBe("de reviewers");
    expect(g.get("[data-testid=group-members]").text()).toContain("Ana");
  });

  it("is read-only without members.manage", async () => {
    const w = await screen(seeded(), ["developer"]);
    expect(w.get("[data-testid=groups-read-only]").text()).toContain("needs the admin or owner role");
    expect(w.find("[data-testid=group-create]").exists()).toBe(false);
    expect(w.find("[data-testid=group-rename]").exists()).toBe(false);
    expect(w.find("[data-testid=group-add]").exists()).toBe(false);
  });

  it("creates a group with an idempotency key, announces it and moves focus to it", async () => {
    const dir = seeded();
    const w = await screen(dir);
    await w.get("#grp-new").setValue("legal");
    await w.get("[data-testid=group-create]").trigger("submit");
    await flushPromises();
    const call = dir.calls.find((c) => c[0] === "createGroup")!;
    expect(call[2]).toBe("legal");
    expect(typeof call[3]).toBe("string");
    expect(status(w)).toBe("Created the group legal.");
    expect(document.activeElement?.textContent).toBe("legal");
  });

  it("shows a refused name as its sentence", async () => {
    const dir = seeded();
    dir.refuse.createGroup = new ApiError(400, "invalid_group_name", "Bad.");
    const w = await screen(dir);
    await w.get("#grp-new").setValue("x");
    await w.get("[data-testid=group-create]").trigger("submit");
    await flushPromises();
    expect(w.get("[data-testid=group-create-error]").text()).toBe("A group's name is 1 to 100 characters.");
  });

  it("adds and removes members", async () => {
    const dir = seeded();
    const w = await screen(dir);
    await w.get("#grp-add-g1").setValue("m2");
    await w.get("[data-testid=group-add]").trigger("submit");
    await flushPromises();
    expect(dir.state.groups[0]!.members).toEqual(["m1", "m2"]);
    expect(status(w)).toBe("Added Ben to de reviewers.");
    const removes = w.findAll("[data-testid=group-remove]");
    expect(removes[0]!.text()).toBe("Remove Ana from the group");
    await removes[0]!.trigger("click");
    await flushPromises();
    expect(dir.state.groups[0]!.members).toEqual(["m2"]);
    expect(status(w)).toBe("Removed Ana from de reviewers.");
  });

  it("renames under the group's current ETag", async () => {
    const dir = seeded();
    const w = await screen(dir);
    await w.get("[data-testid=group-rename]").trigger("click");
    await flushPromises();
    expect(document.activeElement?.id).toBe("grp-rename-g1");
    await w.get("#grp-rename-g1").setValue("German reviewers");
    await w.get("[data-testid=group-rename-form]").trigger("submit");
    await flushPromises();
    expect(dir.calls.find((c) => c[0] === "renameGroup")).toEqual(["renameGroup", "t", "g1", "German reviewers", '"1"']);
    expect(w.get("[data-group=g1] h2").text()).toBe("German reviewers");
    expect(status(w)).toBe("Renamed the group to German reviewers.");
  });

  it("deletes after confirming", async () => {
    const dir = seeded();
    const w = await screen(dir);
    await w.get("[data-testid=group-delete]").trigger("click");
    await flushPromises();
    expect(w.text()).toContain("Its members stay members.");
    await w.get("[data-testid=group-delete-confirm]").trigger("click");
    await flushPromises();
    expect(dir.state.groups).toEqual([]);
    expect(status(w)).toBe("Deleted the group de reviewers.");
    expect(w.find("[data-testid=groups-empty]").exists()).toBe(true);
  });
});
