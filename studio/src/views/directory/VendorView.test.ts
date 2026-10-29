/**
 * One vendor (RFC 0006 §3.3): what its members can and can't see, said
 * plainly; its details under the ETag; its people with their project
 * scope; inviting them as `assigned` translators; and the refusals as
 * sentences.
 */
import { flushPromises, type VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";
import { ApiError } from "../../api/errors";
import type { Role } from "../../api/schemas";
import { createFakeDirectory, member, vendor, type FakeDirectory } from "../../test/fake-directory";
import { project } from "../../test/fake-work";
import { mountTenantScreen } from "../../test/project";
import VendorView from "./VendorView.vue";

let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

const PROJECTS = { "/v1/tenants/t/projects": { items: [project("p1", "Portal"), project("p2", "Checkout")] } };

async function screen(directory: FakeDirectory, roles: Role[] = ["admin"]) {
  wrapper = await mountTenantScreen(VendorView, { directory, roles, path: "/t/t/settings/vendors/v1", responses: PROJECTS });
  return wrapper;
}
const seeded = () =>
  createFakeDirectory({
    vendors: [vendor()],
    members: [
      member({ id: "mv", display_name: "Vera", email: "vera@lingua.example", vendor_id: "v1", visibility: "assigned", locales: ["de"], projects: ["p1"] }),
      member({ id: "mo", display_name: "Own", vendor_id: undefined }),
    ],
  });
const status = (w: VueWrapper) => w.get("[data-testid=vendor-status]").text();

describe("VendorView", () => {
  it("says when the vendor can't be read", async () => {
    const w = await screen(createFakeDirectory());
    expect(w.get("[data-testid=vendor-failed]").text()).toContain("The vendor could not be read.");
  });

  it("says plainly what the vendor's members can and can't see", async () => {
    const w = await screen(seeded());
    const vis = w.get("[data-testid=vendor-visibility]");
    expect(vis.get("h2").text()).toBe("What the vendor's members can see");
    expect(vis.text()).toContain("completed within the last 30 days");
    expect(vis.text()).toContain("write translations in those units");
    expect(vis.text()).toContain("review, approve, publish, export or import anything");
    expect(vis.text()).toContain("search translation memory");
  });

  it("lists only the vendor's members, with their project scope by name and their visibility", async () => {
    const w = await screen(seeded());
    const rows = w.findAll("[data-testid=vendor-member]");
    expect(rows.map((r) => r.attributes("data-member"))).toEqual(["mv"]);
    expect(rows[0]!.get("[data-testid=member-projects]").text()).toBe("Portal");
    expect(rows[0]!.get("[data-testid=member-visibility]").text()).toBe("Sees assigned work only");
  });

  it("saves details under the vendor's ETag", async () => {
    const dir = seeded();
    const w = await screen(dir);
    await w.get("#vnd-contact").setValue("new@lingua.example");
    await w.get("[data-testid=vendor-details]").trigger("submit");
    await flushPromises();
    expect(dir.calls.find((c) => c[0] === "updateVendor")).toEqual(["updateVendor", "t", "v1", { name: "Lingua GmbH", contact: "new@lingua.example", locales: ["de", "fr"] }, '"1"']);
    expect(status(w)).toBe("Saved Lingua GmbH.");
  });

  it("is read-only for someone who can't manage vendors or members", async () => {
    const w = await screen(seeded(), ["developer"]);
    expect(w.find("[data-testid=vendor-read-only]").exists()).toBe(true);
    expect((w.get("#vnd-name").element as HTMLInputElement).disabled).toBe(true);
    expect(w.find("[data-testid=vendor-delete]").exists()).toBe(false);
    expect(w.find("[data-testid=member-scope]").exists()).toBe(false);
    expect(w.find("[data-testid=vendor-invite-needs]").exists()).toBe(true);
  });

  it("refuses to delete a vendor with members, in its own sentence", async () => {
    const w = await screen(seeded());
    await w.get("[data-testid=vendor-delete]").trigger("click");
    await flushPromises();
    await w.get("[data-testid=vendor-delete-confirm]").trigger("click");
    await flushPromises();
    expect(w.get("[data-testid=vendor-delete-error]").text()).toContain("This vendor still has members.");
  });

  it("changes a member's project scope under the member's ETag", async () => {
    const dir = seeded();
    const w = await screen(dir);
    await w.get("[data-testid=member-scope]").trigger("click");
    await flushPromises();
    const boxes = w.findAll("[data-testid=scope-form] input[type=checkbox]");
    await boxes[1]!.setValue(true);
    await w.get("[data-testid=scope-form]").trigger("submit");
    await flushPromises();
    expect(dir.calls.find((c) => c[0] === "restrict")).toEqual(["restrict", "t", "mv", { projects: ["p1", "p2"] }, '"1"']);
    expect(w.get("[data-testid=member-projects]").text()).toBe("Portal, Checkout");
    expect(status(w)).toBe("Saved Vera's project scope.");
  });

  it("takes a member off the vendor, and shows the server's refusal when it says no", async () => {
    const dir = seeded();
    dir.refuse.restrict = new ApiError(400, "vendor_member_visibility", "No.");
    const w = await screen(dir);
    await w.get("[data-testid=member-off]").trigger("click");
    await flushPromises();
    await w.get("[data-testid=off-confirm]").trigger("click");
    await flushPromises();
    expect(w.get("[data-testid=off-error]").text()).toBe("A vendor's member always sees assigned work only.");
    await w.get("[data-testid=off-confirm]").trigger("click");
    await flushPromises();
    expect(dir.calls.filter((c) => c[0] === "restrict").at(-1)![3]).toEqual({ vendor_id: "" });
    expect(w.find("[data-testid=vendor-member]").exists()).toBe(false);
    expect(status(w)).toBe("Vera no longer works for this vendor.");
  });

  it("invites a member as an assigned translator of this vendor", async () => {
    const dir = seeded();
    const w = await screen(dir);
    expect(w.text()).toContain("that is not a choice");
    await w.get("#vnd-inv-email").setValue("new@lingua.example");
    await w.get("#vnd-inv-locales").setValue("de fr");
    await w.findAll("[data-testid=vendor-invite] input[type=checkbox]")[0]!.setValue(true);
    await w.get("[data-testid=vendor-invite]").trigger("submit");
    await flushPromises();
    const call = dir.calls.find((c) => c[0] === "inviteVendorMember")!;
    expect(call[2]).toEqual({ email: "new@lingua.example", vendor_id: "v1", roles: ["translator"], locales: ["de", "fr"], projects: ["p1"] });
    expect(status(w)).toBe("Invited new@lingua.example as a member of this vendor.");
    expect(w.findAll("[data-testid=vendor-member]")).toHaveLength(2);
  });

  it("shows an invitation the server refuses as its sentence", async () => {
    const dir = seeded();
    const w = await screen(dir);
    await w.get("#vnd-inv-email").setValue("vera@lingua.example");
    await w.get("[data-testid=vendor-invite]").trigger("submit");
    await flushPromises();
    expect(w.get("[data-testid=vendor-invite-error]").text()).toBe("That address is already a member of this organization.");
  });
});
