/**
 * One audit entry (RFC 0006 §6.1): every field, the content-free
 * summary, the chain hashes, and "not found" for what the reader can't see.
 */
import { type VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";
import { ApiError } from "../../api/errors";
import type { Role } from "../../api/schemas";
import { createFakeAudit, entry, type FakeAudit } from "../../test/fake-audit";
import { mountTenantScreen } from "../../test/project";
import AuditEntryView from "./AuditEntryView.vue";

let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

async function screen(audit: FakeAudit, sequence: number, roles: Role[] = ["admin"]) {
  wrapper = await mountTenantScreen(AuditEntryView, { audit, roles, path: `/t/t/settings/audit/entries/${sequence}` });
  return wrapper;
}

describe("AuditEntryView", () => {
  it("shows the entry, its summary as recorded, and its place in the chain", async () => {
    const audit = createFakeAudit({ entries: [entry(1), entry(2, { project_id: "prj-1", request_id: "req-9" }), entry(3)] });
    const w = await screen(audit, 2);
    expect(w.get("h1").text()).toBe("Audit entry 2");
    expect(w.get("[data-testid=entry-actor]").text()).toBe("person:0190a1b2-0000-7000-8000-000000000001");
    expect(w.get("[data-testid=entry-action]").text()).toBe("localization.translation.revised");
    const fields = w.get("[data-testid=entry-fields]").text();
    expect(fields).toContain("translation:tr_2");
    expect(fields).toContain("prj-1");
    expect(fields).toContain("req-9");
    expect(w.get("[data-testid=entry-summary]").text()).toContain('"text": "string(len=12)"');
    expect(w.get("[data-testid=entry-prev-hash]").text()).toBe(entry(2).prev_hash);
    expect(w.get("[data-testid=entry-hash]").text()).toBe(entry(2).hash);
    expect(w.get("[data-testid=entry-previous]").attributes("href")).toBe("/t/t/settings/audit/entries/1");
    expect(w.get("[data-testid=entry-next]").attributes("href")).toBe("/t/t/settings/audit/entries/3");
  });

  it("has no previous entry before the first", async () => {
    const w = await screen(createFakeAudit({ entries: [entry(1)] }), 1);
    expect(w.find("[data-testid=entry-previous]").exists()).toBe(false);
  });

  it("says plainly that an entry is not found", async () => {
    const w = await screen(createFakeAudit({ entries: [entry(1)] }), 40);
    expect(w.get("[data-testid=entry-missing]").text()).toContain("no such entry");
    expect(w.find("[data-testid=entry-failed]").exists()).toBe(false);
  });

  it("tells a failed read apart from a missing entry", async () => {
    const audit = createFakeAudit({ entries: [entry(1)] });
    audit.fail.entry = new ApiError(0, "network_error", "x");
    const w = await screen(audit, 1);
    expect(w.find("[data-testid=entry-failed]").exists()).toBe(true);
    expect(w.find("[data-testid=entry-missing]").exists()).toBe(false);
  });

  it("asks the server nothing without audit.read", async () => {
    const audit = createFakeAudit({ entries: [entry(1)] });
    const w = await screen(audit, 1, ["translator"]);
    expect(w.find("[data-testid=audit-no-access]").exists()).toBe(true);
    expect(audit.calls).toEqual([]);
  });
});
