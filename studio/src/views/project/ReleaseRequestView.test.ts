/**
 * One release request (RFC 0006 §5.1): what it deploys where, the gate's
 * verdict and any force shown to the approver, every decision with who
 * and when, and the human-only, four-eyes decision — the requester is
 * told why there is nothing for them to decide.
 */
import { flushPromises, type VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";
import { ApiError } from "../../api/errors";
import type { Role } from "../../api/schemas";
import { createFakeReleaseOps, request, type FakeReleaseOps } from "../../test/fake-release-ops";
import { createFakeReleases, type FakeReleases } from "../../test/fake-releases";
import { createFakeWorkflows, instance } from "../../test/fake-workflows";
import { mountProjectScreen } from "../../test/project";
import ReleaseRequestView from "./ReleaseRequestView.vue";

let w: VueWrapper | undefined;
afterEach(() => {
  w?.unmount();
  w = undefined;
});

const p = { tenant: "t", project: "p" };

/** v1 published to staging, then a promote into production held for approval. */
async function setup(over: Parameters<typeof request>[0] = {}): Promise<{ releases: FakeReleases; ops: FakeReleaseOps; id: string }> {
  const releases = createFakeReleases({ author: "person:dev" });
  await releases.publish(p, { environment: "staging" }, "k1");
  const ops = createFakeReleaseOps(releases);
  const rel = releases.state.releases[0]!;
  ops.addRequest(request({ release_id: rel.id, ...over }));
  return { releases, ops, id: over.id ?? "rr1" };
}

async function screen(releases: FakeReleases, ops: FakeReleaseOps, id = "rr1", roles: Role[] = ["reviewer"], workflows = createFakeWorkflows()) {
  w = await mountProjectScreen(ReleaseRequestView, { port: releases, releaseOps: ops, workflows, roles, path: `/t/t/p/p/releases/requests/${id}` });
  return w;
}
const click = async (sel: string) => {
  await w!.get(sel).trigger("click");
  await flushPromises();
};

describe("ReleaseRequestView", () => {
  it("says it is loading, then tells a failed read apart and retries", async () => {
    const { releases, ops } = await setup();
    ops.hold.add("releaseRequest");
    await screen(releases, ops);
    expect(w!.get("[data-testid=request-loading]").text()).toBe("Loading the release request…");
    w!.unmount();

    ops.hold.clear();
    ops.fail.releaseRequest = new ApiError(0, "network_error", "x");
    await screen(releases, ops);
    expect(w!.get("[data-testid=request-failed]").text()).toContain("The release request could not be read.");
    delete ops.fail.releaseRequest;
    await click("[data-testid=request-failed] button");
    expect(w!.get("h1").text()).toBe("v1 to production");
  });

  it("shows what it deploys, the gate's verdict, the force and its reason, the requirement and every decision", async () => {
    const { releases, ops } = await setup({ forced: true, force_reason: "Hotfix for the checkout", gate: { met: false, unmet: ["de: 3 translations not approved"] } });
    const a = ops.ask("rr1");
    ops.state.approvals.set("rr1", { ...a, decisions: [{ principal: "person:rita", decision: "granted", reason: "Looks right", at: "2026-09-20T09:00:00Z" }] });
    await screen(releases, ops);
    expect(w!.get("h1").text()).toBe("v1 to production");
    expect(w!.get("[data-testid=request-state]").text()).toBe("Pending");
    expect(w!.text()).toContain("Promote v1 to production");
    expect(w!.get("[data-testid=request-forced]").text()).toContain("Force never bypasses approval");
    expect(w!.get("[data-testid=request-forced]").text()).toContain("Their reason: Hotfix for the checkout");
    expect(w!.get("[data-testid=request-gate]").text()).toContain("de: 3 translations not approved");
    expect(w!.get("[data-testid=request-requirement]").text()).toBe("Requirement: 2 approvals from reviewers, never the requester.");
    expect(w!.get("[data-testid=request-progress]").text()).toBe("1 of 2 approvals given");
    const decision = w!.get("[data-testid=request-decisions] li");
    expect(decision.text()).toContain("Approved");
    expect(decision.text()).toContain("“Looks right”");
    expect(decision.get("time").attributes("datetime")).toBe("2026-09-20T09:00:00Z");
  });

  it("approves with a reason; the last approval deploys it", async () => {
    const { releases, ops } = await setup({ approval: { n: 1, from: { role: "reviewer" }, distinct_from_requester: true } });
    ops.ask("rr1");
    await screen(releases, ops);
    await w!.get("[data-testid=request-reason]").setValue("Checked the German");
    await click("[data-testid=request-approve]");
    expect(ops.calls).toContainEqual(["decide", p, "rr1", "granted", "Checked the German"]);
    expect(w!.get("[data-testid=request-status]").text()).toBe("Approved v1 to production. It deploys once enough people have approved it.");
    expect(w!.get("[data-testid=request-state]").text()).toBe("Deployed");
    expect(releases.state.envs.get("production")!.env.current_release_id).toBe(releases.state.releases[0]!.id);
    expect(w!.find("[data-testid=request-decide]").exists()).toBe(false);
  });

  it("denies: nothing deploys", async () => {
    const { releases, ops } = await setup();
    ops.ask("rr1");
    await screen(releases, ops);
    await click("[data-testid=request-deny]");
    expect(w!.get("[data-testid=request-state]").text()).toBe("Denied");
    expect(releases.state.envs.get("production")!.env.current_release_id).toBeUndefined();
  });

  it("tells the requester why there is nothing to decide (four-eyes)", async () => {
    const { releases, ops } = await setup({ requester: "person:me" });
    ops.ask("rr1");
    await screen(releases, ops);
    expect(w!.get("[data-testid=request-own]").text()).toContain("You asked for this release");
    expect(w!.find("[data-testid=request-approve]").exists()).toBe(false);
  });

  it("explains a missing approvals.decide instead of offering a refused button", async () => {
    const { releases, ops } = await setup();
    ops.ask("rr1");
    await screen(releases, ops, "rr1", ["developer"]);
    expect(w!.get("[data-testid=request-cannot-decide]").text()).toContain("approvals.decide");
    expect(w!.find("[data-testid=request-approve]").exists()).toBe(false);
  });

  it("says the workflow hasn't asked yet, and reads the approval again on retry", async () => {
    const { releases, ops } = await setup();
    await screen(releases, ops);
    expect(w!.get("[data-testid=request-not-asked]").text()).toContain("hasn't asked for approvals yet");
    await click("[data-testid=request-approve]");
    const alert = w!.get("[data-testid=request-not-requested]");
    expect(alert.attributes("role")).toBe("alert");
    expect(alert.text()).toContain("Try again in a moment.");
    ops.ask("rr1");
    await click("[data-testid=request-not-requested] button");
    expect(w!.get("[data-testid=request-progress]").text()).toBe("0 of 2 approvals given");
  });

  it.each([
    ["own_text", "you requested this release"],
    ["not_eligible", "must be approved by other people"],
    ["person_required", "Only a person can approve a release"],
  ])("renders the %s refusal as a release sentence, not a translation one", async (code, words) => {
    const { releases, ops } = await setup();
    ops.ask("rr1");
    ops.fail.decide = new ApiError(403, code, "Forbidden.");
    await screen(releases, ops);
    await click("[data-testid=request-approve]");
    const err = w!.get("[data-testid=request-error]");
    expect(err.text()).toContain(words);
    expect(err.text()).not.toContain("text");
  });

  it("reads the request again when it closed meanwhile", async () => {
    const { releases, ops } = await setup();
    ops.ask("rr1");
    await screen(releases, ops);
    Object.assign(releases.state.requests[0]!, { state: "withdrawn", decided_by: "person:dev", reason: "Wrong release" });
    await click("[data-testid=request-approve]");
    expect(w!.get("[data-testid=request-status]").text()).toContain("closed meanwhile");
    expect(w!.get("[data-testid=request-state]").text()).toBe("Withdrawn");
    expect(w!.get("[data-testid=request-closed]").text()).toContain("Reason: Wrong release");
  });

  it("withdraws a pending request after confirming, with a reason", async () => {
    const { releases, ops } = await setup();
    await screen(releases, ops, "rr1", ["developer"]);
    await click("[data-testid=request-withdraw]");
    await w!.get("dialog[open] [data-testid=withdraw-reason]").setValue("Wrong release");
    await w!.get("dialog[open] form").trigger("submit");
    await flushPromises();
    expect(ops.calls).toContainEqual(["withdraw", p, "rr1", "Wrong release"]);
    expect(w!.get("[data-testid=request-status]").text()).toBe("Withdrawn: the request is closed and nothing was deployed.");
    expect(w!.find("[data-testid=request-withdraw]").exists()).toBe(false);
  });

  it("offers no withdrawal without releases.publish", async () => {
    const { releases, ops } = await setup();
    await screen(releases, ops, "rr1", ["reviewer"]);
    expect(w!.find("[data-testid=request-withdraw]").exists()).toBe(false);
  });

  it("links the workflow instance that carries the request", async () => {
    const { releases, ops } = await setup();
    const workflows = createFakeWorkflows({ instances: [instance({ id: "wi9", subject: "release_request", subject_id: "rr1", state: "pending" })] });
    await screen(releases, ops, "rr1", ["reviewer"], workflows);
    const link = w!.get("[data-testid=request-instances] a");
    expect(link.text()).toBe("Workflow instance, now in “pending”");
    expect(link.attributes("href")).toBe("/t/t/p/p/workflow/instances/wi9");
  });
});
