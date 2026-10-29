/**
 * Audit exports (RFC 0006 §6.2): start one, watch the job, download both
 * files, and see the published key and the offline verify command.
 */
import { flushPromises, type VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";
import { ApiError } from "../../api/errors";
import type { Role } from "../../api/schemas";
import { createFakeAudit, doneJob, entry, job, type FakeAudit } from "../../test/fake-audit";
import { mountTenantScreen } from "../../test/project";
import AuditExportsView from "./AuditExportsView.vue";

let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

async function screen(audit: FakeAudit, roles: Role[] = ["owner"], pollMs = 5) {
  wrapper = await mountTenantScreen(AuditExportsView, { audit, roles, path: "/t/t/settings/audit/exports", props: { pollMs } });
  return wrapper;
}

const wait = (ms: number) => new Promise((r) => setTimeout(r, ms));
const chain = (n: number) => Array.from({ length: n }, (_, i) => entry(i + 1));

describe("AuditExportsView", () => {
  it("is for those with audit.export, and asks nothing of anyone else", async () => {
    const audit = createFakeAudit();
    const w = await screen(audit, ["admin"]);
    expect(w.get("[data-testid=audit-export-no-access]").text()).toContain("audit.export");
    expect(w.find("[data-testid=audit-export-form]").exists()).toBe(false);
    expect(audit.calls).toEqual([]);
  });

  it("lists jobs with both files to download, and the key that signed them", async () => {
    const w = await screen(createFakeAudit({ jobs: [doneJob()] }));
    const row = w.get("[data-job=aej-1]");
    expect(row.get("[data-testid=audit-export-state]").text()).toContain("Ready");
    expect(row.text()).toContain("Sequences 1–12");
    expect(row.get("[data-testid=audit-export-key]").text()).toContain("audit-2026");
    expect(row.get("[data-testid=audit-export-key]").text()).toContain("which the deployment publishes");
    const links = row.findAll("[data-testid=audit-export-download]");
    expect(links.map((l) => l.attributes("data-file"))).toEqual(["entries.jsonl", "manifest.json"]);
    expect(links[0]!.attributes("href")).toBe("/v1/tenants/t/audit-export-jobs/aej-1/file");
    expect(links[1]!.attributes("href")).toBe("/v1/tenants/t/audit-export-jobs/aej-1/manifest");
    expect(links[0]!.attributes("aria-label")).toBe("Download entries.jsonl (4.0 KB)");
  });

  it("shows the published key id and the offline verify command", async () => {
    const w = await screen(createFakeAudit());
    const ids = w.findAll("[data-testid=audit-key-id]").map((n) => n.text());
    expect(ids).toEqual(["audit-2026", "audit-2025"]);
    expect(w.get("[data-testid=audit-verify]").text()).toContain("active");
    expect(w.get("[data-testid=audit-verify]").text()).toContain("retired");
    expect(w.get("[data-testid=audit-save-keys]").text()).toBe(`curl -fsS ${window.location.origin}/.well-known/glossa-audit-keys.json > audit-keys.json`);
    expect(w.get("[data-testid=audit-verify-command]").text()).toBe("glossa audit verify ./audit-export --public-key audit-keys.json");
  });

  it("warns when an export was signed by a key the deployment does not publish", async () => {
    const w = await screen(createFakeAudit({ jobs: [doneJob({ key_id: "audit-lost" })] }));
    expect(w.get("[data-testid=audit-export-key] p").text()).toContain("unknown_key");
  });

  it("starts a time-range export and follows the job until its files are ready", async () => {
    const audit = createFakeAudit({ entries: chain(12) });
    const w = await screen(audit);
    await w.get("#ae-from").setValue("2026-10-01T00:00");
    await w.get("#ae-to").setValue("2026-10-02T00:00");
    await w.get("[data-testid=audit-export-form]").trigger("submit");
    await flushPromises();

    const call = audit.calls.find((c) => c[0] === "createExport")!;
    expect(call[2]).toEqual({ from: new Date("2026-10-01T00:00").toISOString(), to: new Date("2026-10-02T00:00").toISOString() });
    expect(String(call[3])).not.toBe("");
    expect(w.find("[data-testid=audit-export-started]").exists()).toBe(true);
    expect(w.get("[data-testid=audit-export-state]").text()).toContain("Queued");

    audit.advance();
    await wait(30);
    await flushPromises();
    expect(w.get("[data-testid=audit-export-state]").text()).toContain("Running");

    audit.advance();
    await wait(30);
    await flushPromises();
    expect(w.get("[data-testid=audit-export-state]").text()).toContain("Ready");
    expect(w.findAll("[data-testid=audit-export-download]")).toHaveLength(2);
  });

  it("starts a sequence export, with the last sequence optional", async () => {
    const audit = createFakeAudit({ entries: chain(12) });
    const w = await screen(audit);
    await w.get("input[value=sequence]").setValue(true);
    await w.get("#ae-first").setValue("3");
    await w.get("[data-testid=audit-export-form]").trigger("submit");
    await flushPromises();
    expect(audit.calls.find((c) => c[0] === "createExport")![2]).toEqual({ first_sequence: 3 });

    await w.get("#ae-last").setValue("9");
    await w.get("[data-testid=audit-export-form]").trigger("submit");
    await flushPromises();
    expect(audit.calls.filter((c) => c[0] === "createExport").at(-1)![2]).toEqual({ first_sequence: 3, last_sequence: 9 });
  });

  it("refuses a range that cannot be right before asking the server", async () => {
    const audit = createFakeAudit({ entries: chain(3) });
    const w = await screen(audit);
    await w.get("[data-testid=audit-export-form]").trigger("submit");
    expect(w.get("[data-testid=audit-export-error]").text()).toContain("start and a later end");
    await w.get("input[value=sequence]").setValue(true);
    await w.get("#ae-first").setValue("5");
    await w.get("#ae-last").setValue("2");
    await w.get("[data-testid=audit-export-form]").trigger("submit");
    expect(w.get("[data-testid=audit-export-error]").text()).toContain("must not be smaller");
    expect(audit.calls.some((c) => c[0] === "createExport")).toBe(false);
  });

  it("explains the server's refusals in words", async () => {
    const audit = createFakeAudit({ entries: chain(3) });
    const w = await screen(audit);
    await w.get("#ae-from").setValue("2026-01-01T00:00");
    await w.get("#ae-to").setValue("2026-06-01T00:00");
    await w.get("[data-testid=audit-export-form]").trigger("submit");
    await flushPromises();
    expect(w.get("[data-testid=audit-export-error]").text()).toContain("at most 31 days");
  });

  it("says a deployment without an audit key can't export, instead of offering a form", async () => {
    const audit = createFakeAudit({ keys: null });
    const w = await screen(audit);
    expect(w.get("[data-testid=audit-export-unavailable]").text()).toContain("GLOSSA_AUDIT_SIGNING_KEY");
    expect(w.find("[data-testid=audit-export-form]").exists()).toBe(false);
    expect(w.find("[data-testid=audit-no-keys]").exists()).toBe(true);
  });

  it("learns the same from the create call when the key document could not be read", async () => {
    const audit = createFakeAudit({ unavailable: true });
    audit.fail.keys = new ApiError(0, "network_error", "x");
    const w = await screen(audit);
    expect(w.find("[data-testid=audit-keys-failed]").exists()).toBe(true);
    await w.get("#ae-from").setValue("2026-10-01T00:00");
    await w.get("#ae-to").setValue("2026-10-02T00:00");
    await w.get("[data-testid=audit-export-form]").trigger("submit");
    await flushPromises();
    expect(w.find("[data-testid=audit-export-unavailable]").exists()).toBe(true);
  });

  it("shows why a job failed, and says to export by sequence", async () => {
    const w = await screen(createFakeAudit({ jobs: [job({ state: "failed", failure_code: "range_not_contiguous", failure_message: "x" })] }));
    expect(w.get("[data-testid=audit-export-failure]").text()).toContain("Export by sequence");
  });

  it("does not offer files the retention period deleted", async () => {
    const w = await screen(createFakeAudit({ jobs: [doneJob({ files_deleted_at: "2026-10-12T00:00:00Z" })] }));
    expect(w.find("[data-testid=audit-export-download]").exists()).toBe(false);
    expect(w.get("[data-job=aej-1]").text()).toContain("deleted after the retention period");
  });

  it("tells a failed read apart from no exports", async () => {
    const audit = createFakeAudit();
    audit.fail.exportJobs = new ApiError(0, "network_error", "x");
    const w = await screen(audit);
    expect(w.find("[data-testid=audit-exports-failed]").exists()).toBe(true);
    expect(w.find("[data-testid=audit-exports-empty]").exists()).toBe(false);
    w.unmount();
    const empty = await screen(createFakeAudit());
    expect(empty.get("[data-testid=audit-exports-empty]").text()).toBe("No exports yet.");
  });
});
