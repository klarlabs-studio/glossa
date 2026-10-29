/**
 * The organization's vendors (RFC 0006 §3.3): listed with their members
 * counted, added only with `vendors.manage`.
 */
import { flushPromises, type VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";
import { ApiError } from "../../api/errors";
import type { Role } from "../../api/schemas";
import { createFakeDirectory, member, vendor, type FakeDirectory } from "../../test/fake-directory";
import { mountTenantScreen } from "../../test/project";
import VendorsView from "./VendorsView.vue";

let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

async function screen(directory: FakeDirectory, roles: Role[] = ["admin"]) {
  wrapper = await mountTenantScreen(VendorsView, { directory, roles, path: "/t/t/settings/vendors" });
  return wrapper;
}

describe("VendorsView", () => {
  it("tells a failed read apart from no vendors", async () => {
    const dir = createFakeDirectory();
    dir.fail.vendors = new ApiError(0, "network_error", "x");
    const w = await screen(dir);
    expect(w.get("[data-testid=vendors-failed]").text()).toContain("The vendors could not be read");
    expect(w.find("[data-testid=vendors-empty]").exists()).toBe(false);
  });

  it("says so when there are none", async () => {
    const w = await screen(createFakeDirectory());
    expect(w.get("[data-testid=vendors-empty]").text()).toBe("No vendors yet.");
  });

  it("lists vendors with locales and members counted, linked to each", async () => {
    const dir = createFakeDirectory({
      vendors: [vendor()],
      members: [member({ id: "m1", vendor_id: "v1", visibility: "assigned" }), member({ id: "m2", vendor_id: "v1", visibility: "assigned" }), member({ id: "m3" })],
    });
    const w = await screen(dir);
    const row = w.get("[data-vendor=v1]");
    expect(row.text()).toContain("Lingua GmbH");
    expect(row.text()).toContain("de, fr");
    expect(row.get("[data-testid=vendor-members]").text()).toBe("2");
    expect(row.get("a").attributes("href")).toBe("/t/t/settings/vendors/v1");
  });

  it("says member counts are unknown rather than zero when members can't be read", async () => {
    const dir = createFakeDirectory({ vendors: [vendor()] });
    dir.fail.members = new ApiError(403, "forbidden", "No.");
    const w = await screen(dir);
    expect(w.find("[data-testid=vendors-members-failed]").exists()).toBe(true);
    expect(w.get("[data-testid=vendor-members]").text()).toBe("—");
  });

  it("is read-only without vendors.manage", async () => {
    const w = await screen(createFakeDirectory(), ["developer"]);
    expect(w.find("[data-testid=vendor-create]").exists()).toBe(false);
    expect(w.find("[data-testid=vendors-read-only]").exists()).toBe(true);
  });

  it("adds a vendor and opens it", async () => {
    const dir = createFakeDirectory();
    const w = await screen(dir);
    await w.get("#vnd-name").setValue("Lingua GmbH");
    await w.get("#vnd-locales").setValue("de, fr-CA");
    await w.get("[data-testid=vendor-create]").trigger("submit");
    await flushPromises();
    const call = dir.calls.find((c) => c[0] === "createVendor")!;
    expect(call[2]).toEqual({ name: "Lingua GmbH", locales: ["de", "fr-CA"] });
    expect(w.vm.$route.name).toBe("vendor");
  });

  it("shows a refused vendor as its sentence", async () => {
    const dir = createFakeDirectory();
    dir.refuse.createVendor = new ApiError(400, "invalid_vendor_contact", "Bad.");
    const w = await screen(dir);
    await w.get("#vnd-name").setValue("X");
    await w.get("[data-testid=vendor-create]").trigger("submit");
    await flushPromises();
    expect(w.get("[data-testid=vendor-create-error]").text()).toBe("The contact is at most 200 characters.");
  });
});
