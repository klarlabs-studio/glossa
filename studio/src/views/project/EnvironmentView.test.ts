/**
 * One environment (RFC 0006 §5): the approval requirement and its
 * settings, and the staged rollout — start, advance under `If-Match`,
 * complete, and an abort that sends no tag so a stale one can never slow
 * it down.
 */
import { flushPromises, type VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";
import type { Role } from "../../api/schemas";
import { createFakeReleaseOps, rollout, type FakeReleaseOps } from "../../test/fake-release-ops";
import { createFakeReleases, type FakeReleases } from "../../test/fake-releases";
import { mountProjectScreen } from "../../test/project";
import EnvironmentView from "./EnvironmentView.vue";

let w: VueWrapper | undefined;
afterEach(() => {
  w?.unmount();
  w = undefined;
});

const p = { tenant: "t", project: "p" };

/** v1 serves production; v2 (published to staging) is the candidate. */
async function setup(): Promise<{ releases: FakeReleases; ops: FakeReleaseOps; v1: string; v2: string }> {
  const releases = createFakeReleases();
  await releases.publish(p, { environment: "production" }, "k1");
  await releases.publish(p, { environment: "staging" }, "k2");
  const idOf = (version: number) => releases.state.releases.find((r) => r.version === version)!.id;
  const [v1, v2] = [idOf(1), idOf(2)] as [string, string];
  return { releases, ops: createFakeReleaseOps(releases), v1, v2 };
}

async function screen(releases: FakeReleases, ops: FakeReleaseOps, roles: Role[] = ["owner"], environment = "production") {
  w = await mountProjectScreen(EnvironmentView, { port: releases, releaseOps: ops, roles, path: `/t/t/p/p/releases/environments/${environment}` });
  await flushPromises();
  return w;
}
const click = async (sel: string) => {
  await w!.get(sel).trigger("click");
  await flushPromises();
};
const submit = async (sel: string) => {
  await w!.get(sel).trigger("submit");
  await flushPromises();
};
const text = (sel: string) => w!.get(sel).text();
const call = (ops: FakeReleaseOps, method: string) => ops.calls.filter((c) => c[0] === method).at(-1)!;

/** A running rollout of v2 over v1 at 10%, as another person started it. */
function running(ops: FakeReleaseOps, v1: string, v2: string): void {
  ops.state.rollouts.set("production", [{ rollout: rollout({ release_id: v2, stable_release_id: v1 }), etag: 1 }]);
}

describe("EnvironmentView", () => {
  it("says it is loading, then tells a failed read apart and retries", async () => {
    const { releases, ops } = await setup();
    await screen(releases, ops, ["owner"], "nowhere");
    expect(text("[data-testid=env-failed]")).toContain("could not be read");
    expect(w!.find("[data-testid=env-serving]").exists()).toBe(false);
  });

  it("shows what it serves and, with approval required, the requirement and a way to its requests", async () => {
    const { releases, ops, v1 } = await setup();
    Object.assign(releases.state.envs.get("production")!.env, { approval: { n: 2, from: { role: "reviewer" }, distinct_from_requester: true } });
    await screen(releases, ops);
    expect(text("h1")).toBe("Environment production");
    expect(text("[data-testid=env-serving]")).toBe("v1");
    expect(releases.state.envs.get("production")!.env.current_release_id).toBe(v1);
    expect(text("[data-testid=env-approval]")).toContain("2 approvals");
    expect(w!.get("[data-testid=env-approval] a").attributes("href")).toBe("/t/t/p/p/releases/requests?environment=production");
  });

  it("is read-only for someone who cannot publish: no settings, start, advance, complete or abort", async () => {
    const { releases, ops, v1, v2 } = await setup();
    running(ops, v1, v2);
    await screen(releases, ops, ["translator"]);
    expect(w!.find("[data-testid=env-read-only]").exists()).toBe(true);
    expect(w!.find("[data-testid=env-settings]").exists()).toBe(false);
    expect(w!.find("[data-testid=rollout-active]").exists()).toBe(true);
    expect(w!.find("[data-testid=rollout-advance]").exists()).toBe(false);
    expect(w!.find("[data-testid=rollout-abort]").exists()).toBe(false);
    expect(w!.find("[data-testid=rollout-complete]").exists()).toBe(false);
  });

  describe("approval settings", () => {
    it("requires approvals: saved under the environment's ETag, together with its policy", async () => {
      const { releases, ops } = await setup();
      await screen(releases, ops);
      const before = String(releases.state.envs.get("production")!.etag);
      await click("[data-testid=env-settings]");
      await w!.get("dialog[open] [data-testid=approval-on]").setValue(true);
      await w!.get("dialog[open] [data-testid=approval-n]").setValue("2");
      await w!.get("dialog[open] #policy-form").trigger("submit");
      await flushPromises();
      const [, , name, settings, etag] = call(ops, "configureEnvironment") as [string, unknown, string, { approval: unknown; clear_approval?: boolean }, string];
      expect(name).toBe("production");
      expect(etag).toBe(before);
      expect(settings.approval).toEqual({ n: 2, from: { role: "reviewer" }, distinct_from_requester: true });
      expect(settings.clear_approval).toBeUndefined();
      expect(text("[data-testid=env-approval]")).toContain("2 approvals");
      expect(text("[data-testid=env-status]")).toContain("production");
    });

    it("clears the requirement it had", async () => {
      const { releases, ops } = await setup();
      Object.assign(releases.state.envs.get("production")!.env, { approval: { n: 1, from: { role: "reviewer" }, distinct_from_requester: true } });
      await screen(releases, ops);
      await click("[data-testid=env-settings]");
      await w!.get("dialog[open] [data-testid=approval-off]").setValue(true);
      await w!.get("dialog[open] #policy-form").trigger("submit");
      await flushPromises();
      expect((call(ops, "configureEnvironment")[3] as { clear_approval?: boolean }).clear_approval).toBe(true);
      expect(releases.state.envs.get("production")!.env.approval).toBeUndefined();
      expect(text("[data-testid=env-approval]")).not.toContain("approvals");
    });

    it("explains, instead of offering a refused change, to someone without workflows.manage", async () => {
      const { releases, ops } = await setup();
      await screen(releases, ops, ["developer"]);
      await click("[data-testid=env-settings]");
      expect(text("dialog[open] [data-testid=approval-needs-manage]")).toContain("workflows.manage");
      expect(w!.get("dialog[open] [data-testid=approval-on]").attributes("disabled")).toBeDefined();
    });
  });

  describe("rollouts", () => {
    it("starts one: the candidate, its share, and an idempotency key; the running rollout replaces the form", async () => {
      const { releases, ops, v1, v2 } = await setup();
      await screen(releases, ops);
      expect(w!.find("[data-testid=rollout-none]").exists()).toBe(true);
      await w!.get("[data-testid=rollout-start] select").setValue(v2);
      await w!.get("#ro-start-percent").setValue("5");
      await submit("[data-testid=rollout-start]");
      const [, , env, input, key] = call(ops, "startRollout") as [string, unknown, string, Record<string, unknown>, string];
      expect(env).toBe("production");
      expect(input).toEqual({ release_id: v2, percent: 5 });
      expect(key).toMatch(/\S/);
      expect(w!.find("[data-testid=rollout-start]").exists()).toBe(false);
      expect(text("[data-testid=rollout-candidate]")).toBe("v2");
      expect(text("[data-testid=rollout-stable]")).toBe("v1");
      expect(w!.get("[data-testid=rollout-meter]").attributes("value")).toBe("5");
      expect(releases.state.envs.get("production")!.env.current_release_id).toBe(v1);
    });

    it("sends a forced start only with a reason", async () => {
      const { releases, ops, v2 } = await setup();
      await screen(releases, ops);
      await w!.get("[data-testid=rollout-start] select").setValue(v2);
      await w!.get("[data-testid=rollout-force]").setValue(true);
      expect(w!.get("[data-testid=rollout-start] button[type=submit]").attributes("disabled")).toBeDefined();
      await w!.get("#ro-force-reason").setValue("Hotfix");
      await submit("[data-testid=rollout-start]");
      expect(call(ops, "startRollout")[3]).toEqual({ release_id: v2, percent: 10, force: true, force_reason: "Hotfix" });
      expect(w!.find("[data-testid=rollout-forced]").exists()).toBe(true);
    });

    it("says approval is required instead of starting where a release needs approving", async () => {
      const { releases, ops, v2 } = await setup();
      Object.assign(releases.state.envs.get("production")!.env, { approval: { n: 1, from: { role: "reviewer" }, distinct_from_requester: true } });
      await screen(releases, ops);
      await w!.get("[data-testid=rollout-start] select").setValue(v2);
      await submit("[data-testid=rollout-start]");
      expect(w!.find("[data-testid=rollout-needs-approval]").exists()).toBe(true);
      expect(w!.find("[data-testid=rollout-active]").exists()).toBe(false);
    });

    it("advances under the rollout's current ETag", async () => {
      const { releases, ops, v1, v2 } = await setup();
      running(ops, v1, v2);
      await screen(releases, ops);
      expect(w!.get("[data-testid=rollout-meter]").attributes("value")).toBe("10");
      await w!.get("#ro-percent").setValue("50");
      await submit("[data-testid=rollout-advance]");
      expect(call(ops, "advance").slice(2)).toEqual(["production", "ro1", 50, '"1"']);
      expect(w!.get("[data-testid=rollout-meter]").attributes("value")).toBe("50");
      expect(text("[data-testid=env-status]")).toContain("50");

      // The ETag moved: the next advance carries the new one.
      await w!.get("#ro-percent").setValue("100");
      await submit("[data-testid=rollout-advance]");
      expect(call(ops, "advance").slice(2)).toEqual(["production", "ro1", 100, '"2"']);
    });

    it("reads the rollout again when someone else moved it, and does not apply the stale change", async () => {
      const { releases, ops, v1, v2 } = await setup();
      running(ops, v1, v2);
      await screen(releases, ops);
      const entry = ops.state.rollouts.get("production")![0]!;
      entry.rollout = { ...entry.rollout, percent: 25 };
      entry.etag = 7;
      await w!.get("#ro-percent").setValue("50");
      await submit("[data-testid=rollout-advance]");
      expect(text("[data-testid=env-status]")).toMatch(/changed|moved|meanwhile/i);
      expect(entry.rollout.percent).toBe(25);
      expect(w!.get("[data-testid=rollout-meter]").attributes("value")).toBe("25");
      // ...and the form now carries the fresh tag.
      await w!.get("#ro-percent").setValue("50");
      await submit("[data-testid=rollout-advance]");
      expect(call(ops, "advance").slice(2)).toEqual(["production", "ro1", 50, '"7"']);
    });

    it("completes after confirming: the pointer moves to the candidate", async () => {
      const { releases, ops, v1, v2 } = await setup();
      running(ops, v1, v2);
      await screen(releases, ops);
      await click("[data-testid=rollout-complete]");
      await click("dialog[open] [data-testid=rollout-confirm]");
      expect(call(ops, "complete").slice(2)).toEqual(["production", "ro1", '"1"']);
      expect(releases.state.envs.get("production")!.env.current_release_id).toBe(v2);
      expect(text("[data-testid=env-serving]")).toBe("v2");
      expect(w!.find("[data-testid=rollout-active]").exists()).toBe(false);
      expect(text("[data-testid=rollout-history]")).toContain("Completed");
    });

    it("aborts instantly after confirming, sending no ETag, even when the tag it holds is stale", async () => {
      const { releases, ops, v1, v2 } = await setup();
      running(ops, v1, v2);
      await screen(releases, ops);
      ops.state.rollouts.get("production")![0]!.etag = 9; // moved since it was read
      await click("[data-testid=rollout-abort]");
      await click("dialog[open] [data-testid=rollout-confirm]");
      const abort = call(ops, "abort");
      expect(abort.slice(2, 4)).toEqual(["production", "ro1"]);
      expect(abort[4]).toBeUndefined();
      expect(releases.state.envs.get("production")!.env.current_release_id).toBe(v1);
      expect(w!.find("[data-testid=rollout-active]").exists()).toBe(false);
      expect(text("[data-testid=rollout-history]")).toContain("Aborted");
    });

    it("does not end anything the person backs out of", async () => {
      const { releases, ops, v1, v2 } = await setup();
      running(ops, v1, v2);
      await screen(releases, ops);
      await click("[data-testid=rollout-abort]");
      await w!.get("dialog[open] button.btn:not([data-testid])").trigger("click");
      await flushPromises();
      expect(ops.calls.some((c) => c[0] === "abort" || c[0] === "complete")).toBe(false);
      expect(w!.find("[data-testid=rollout-active]").exists()).toBe(true);
    });
  });
});
