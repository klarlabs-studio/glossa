/**
 * The audit log (RFC 0006 §6): entries newest first, filters, paging,
 * and nothing asked of the server without `audit.read`.
 */
import { flushPromises, type VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";
import { ApiError } from "../../api/errors";
import type { Role } from "../../api/schemas";
import { createFakeAudit, entry, type FakeAudit } from "../../test/fake-audit";
import { mountTenantScreen } from "../../test/project";
import AuditLogView from "./AuditLogView.vue";

let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

async function screen(audit: FakeAudit, roles: Role[] = ["admin"]) {
  wrapper = await mountTenantScreen(AuditLogView, { audit, roles, path: "/t/t/settings/audit" });
  return wrapper;
}

const entries = (n: number) => Array.from({ length: n }, (_, i) => entry(i + 1));

describe("AuditLogView", () => {
  it("lists entries newest first, each linked to its detail", async () => {
    const w = await screen(createFakeAudit({ entries: entries(3) }));
    const rows = w.findAll("[data-testid=audit-row]");
    expect(rows.map((r) => r.attributes("data-sequence"))).toEqual(["3", "2", "1"]);
    expect(rows[0]!.text()).toContain("localization.translation.revised");
    expect(rows[0]!.text()).toContain("translation:tr_3");
    expect(rows[0]!.text()).toContain("person:0190a1b2");
    expect(rows[0]!.get("a").attributes("href")).toBe("/t/t/settings/audit/entries/3");
    expect(w.get("[data-testid=audit-count]").text()).toContain("3 entries");
  });

  it("says a failed read apart from an empty log", async () => {
    const audit = createFakeAudit();
    audit.fail.entries = new ApiError(0, "network_error", "x");
    const w = await screen(audit);
    expect(w.get("[data-testid=audit-failed]").text()).toContain("could not be read");
    expect(w.find("[data-testid=audit-empty]").exists()).toBe(false);

    const empty = await screen(createFakeAudit());
    expect(empty.get("[data-testid=audit-empty]").text()).toBe("No entries match.");
  });

  it("pages with Load more and keeps what it has when the next page fails", async () => {
    const audit = createFakeAudit({ entries: entries(7), pageSize: 3 });
    const w = await screen(audit);
    expect(w.findAll("[data-testid=audit-row]")).toHaveLength(3);

    audit.refuse.entries = new ApiError(503, "unavailable", "Down.");
    await w.get("[data-testid=audit-more]").trigger("click");
    await flushPromises();
    expect(w.findAll("[data-testid=audit-row]")).toHaveLength(3);
    expect(w.find("[data-testid=audit-more-failed]").exists()).toBe(true);

    await w.get("[data-testid=audit-more]").trigger("click");
    await flushPromises();
    expect(w.findAll("[data-testid=audit-row]")).toHaveLength(6);
    expect(audit.calls.at(-1)).toEqual(["entries", "t", {}, "3"]);

    await w.get("[data-testid=audit-more]").trigger("click");
    await flushPromises();
    expect(w.findAll("[data-testid=audit-row]")).toHaveLength(7);
    expect(w.find("[data-testid=audit-more]").exists()).toBe(false);
  });

  it("sends the filters it was given, and clears them", async () => {
    const audit = createFakeAudit({
      entries: [entry(1), entry(2, { action: "identity.person.signed_in", source: "direct", actor: "person:b" }), entry(3)],
    });
    const w = await screen(audit);
    await w.get("#af-action").setValue("identity.person.signed_in");
    await w.get("#af-source").setValue("direct");
    await w.get("#af-actor").setValue(" person:b ");
    await w.get("[data-testid=audit-filters]").trigger("submit");
    await flushPromises();
    expect(audit.calls.at(-1)).toEqual(["entries", "t", { action: "identity.person.signed_in", source: "direct", actor: "person:b" }, undefined]);
    expect(w.findAll("[data-testid=audit-row]").map((r) => r.attributes("data-sequence"))).toEqual(["2"]);

    await w.get("#af-action").setValue("nothing.happened");
    await w.get("[data-testid=audit-filters]").trigger("submit");
    await flushPromises();
    expect(w.get("[data-testid=audit-empty]").text()).toBe("No entries match these filters.");

    const clear = w.findAll("button").find((b) => b.text() === "Clear")!;
    await clear.trigger("click");
    await flushPromises();
    expect(audit.calls.at(-1)).toEqual(["entries", "t", {}, undefined]);
    expect(w.findAll("[data-testid=audit-row]")).toHaveLength(3);
  });

  it("turns a local time range into instants, and refuses an inverted one", async () => {
    const audit = createFakeAudit({ entries: entries(2) });
    const w = await screen(audit);
    await w.get("#af-from").setValue("2026-10-02T12:00");
    await w.get("#af-to").setValue("2026-10-02T10:00");
    await w.get("[data-testid=audit-filters]").trigger("submit");
    await flushPromises();
    expect(w.find("[data-testid=audit-range-error]").exists()).toBe(true);
    expect(audit.calls.filter((c) => c[0] === "entries")).toHaveLength(1);

    await w.get("#af-to").setValue("2026-10-02T14:00");
    await w.get("[data-testid=audit-filters]").trigger("submit");
    await flushPromises();
    const f = audit.calls.at(-1)![2] as { from: string; to: string };
    expect(f.from).toBe(new Date("2026-10-02T12:00").toISOString());
    expect(f.to).toBe(new Date("2026-10-02T14:00").toISOString());
  });

  it("asks the server nothing without audit.read", async () => {
    const audit = createFakeAudit({ entries: entries(2) });
    const w = await screen(audit, ["developer"]);
    expect(w.get("[data-testid=audit-no-access]").text()).toContain("audit.read");
    expect(w.find("[data-testid=audit-list]").exists()).toBe(false);
    expect(audit.calls).toEqual([]);
  });

  it("links to the exports only for those who may export", async () => {
    const admin = await screen(createFakeAudit({ entries: entries(1) }), ["admin"]);
    expect(admin.find("[data-testid=audit-exports-link]").exists()).toBe(false);
    admin.unmount();
    const owner = await screen(createFakeAudit({ entries: entries(1) }), ["owner"]);
    expect(owner.get("[data-testid=audit-exports-link]").attributes("href")).toBe("/t/t/settings/audit/exports");
  });
});
