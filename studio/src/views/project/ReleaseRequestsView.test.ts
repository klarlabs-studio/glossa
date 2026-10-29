/**
 * A project's release requests (RFC 0006 §5.1): newest first, filtered by
 * environment and state in the address, and a failed read says so rather
 * than showing an empty list.
 */
import { flushPromises, type VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";
import { ApiError } from "../../api/errors";
import { createFakeReleaseOps, request, type FakeReleaseOps } from "../../test/fake-release-ops";
import { createFakeReleases, type FakeReleases } from "../../test/fake-releases";
import { mountProjectScreen } from "../../test/project";
import ReleaseRequestsView from "./ReleaseRequestsView.vue";

let w: VueWrapper | undefined;
afterEach(() => {
  w?.unmount();
  w = undefined;
});

const p = { tenant: "t", project: "p" };

/** v1 published to staging; three requests: a pending promote, a forced one, a denied one. */
async function setup(): Promise<{ releases: FakeReleases; ops: FakeReleaseOps }> {
  const releases = createFakeReleases({ author: "person:dev" });
  await releases.publish(p, { environment: "staging" }, "k1");
  const ops = createFakeReleaseOps(releases);
  const rel = releases.state.releases[0]!;
  ops.addRequest(request({ id: "rr3", release_id: rel.id, environment: "staging", state: "denied", created_at: "2026-09-18T08:00:00Z" }));
  ops.addRequest(request({ id: "rr2", release_id: rel.id, forced: true, force_reason: "Hotfix", created_at: "2026-09-19T08:00:00Z" }));
  ops.addRequest(request({ id: "rr1", release_id: rel.id, created_at: "2026-09-20T08:00:00Z" }));
  return { releases, ops };
}

async function screen(releases: FakeReleases, ops: FakeReleaseOps, query = "") {
  w = await mountProjectScreen(ReleaseRequestsView, { port: releases, releaseOps: ops, path: `/t/t/p/p/releases/requests${query}` });
  await flushPromises();
  return w;
}
const lastQuery = (ops: FakeReleaseOps) => ops.calls.filter((c) => c[0] === "releaseRequests").at(-1)![2];
const rows = () => w!.findAll("[data-testid=requests-list] tbody tr");

describe("ReleaseRequestsView", () => {
  it("says it is loading, then lists every request with its release, state and a forced marker", async () => {
    const { releases, ops } = await setup();
    ops.hold.add("releaseRequests");
    await screen(releases, ops);
    expect(w!.find("[data-testid=requests-loading]").exists()).toBe(true);
    w!.unmount();

    ops.hold.clear();
    await screen(releases, ops);
    expect(rows().map((r) => r.attributes("data-request"))).toEqual(["rr1", "rr2", "rr3"]);
    const first = rows()[0]!;
    expect(first.get("a").text()).toBe("v1 to production");
    expect(first.get("[data-testid=request-state]").text()).toBe("Pending");
    expect(first.find("[data-testid=request-forced]").exists()).toBe(false);
    expect(rows()[1]!.find("[data-testid=request-forced]").exists()).toBe(true);
    expect(rows()[2]!.get("[data-testid=request-state]").text()).toBe("Denied");
    expect(first.get("time").attributes("datetime")).toBe("2026-09-20T08:00:00Z");
    expect(first.get("a").attributes("href")).toBe("/t/t/p/p/releases/requests/rr1");
  });

  it("filters by environment and by state, and keeps the filters in the address", async () => {
    const { releases, ops } = await setup();
    await screen(releases, ops);
    expect(lastQuery(ops)).toEqual({});

    await w!.get("[data-testid=filter-environment]").setValue("staging");
    await flushPromises();
    expect(lastQuery(ops)).toEqual({ environment: "staging" });
    expect(rows().map((r) => r.attributes("data-request"))).toEqual(["rr3"]);

    await w!.get("[data-testid=filter-state]").setValue("pending");
    await flushPromises();
    expect(lastQuery(ops)).toEqual({ environment: "staging", state: "pending" });
    expect(w!.find("[data-testid=requests-empty]").exists()).toBe(true);

    await w!.get("[data-testid=filter-environment]").setValue("");
    await flushPromises();
    expect(lastQuery(ops)).toEqual({ state: "pending" });
    expect(rows().map((r) => r.attributes("data-request"))).toEqual(["rr1", "rr2"]);
  });

  it("opens with the filters of a link it was given, and ignores a state it does not know", async () => {
    const { releases, ops } = await setup();
    await screen(releases, ops, "?environment=production&state=pending");
    expect(lastQuery(ops)).toEqual({ environment: "production", state: "pending" });
    expect((w!.get("[data-testid=filter-environment]").element as HTMLSelectElement).value).toBe("production");
    w!.unmount();

    await screen(releases, ops, "?state=nonsense");
    expect(lastQuery(ops)).toEqual({});
    expect(rows()).toHaveLength(3);
  });

  it("tells a failed read apart from an empty list, and retries", async () => {
    const { releases, ops } = await setup();
    ops.fail.releaseRequests = new ApiError(0, "network_error", "x");
    await screen(releases, ops);
    expect(w!.get("[data-testid=requests-failed]").text()).toContain("could not be read");
    expect(w!.find("[data-testid=requests-empty]").exists()).toBe(false);
    delete ops.fail.releaseRequests;
    await w!.get("[data-testid=requests-failed] button").trigger("click");
    await flushPromises();
    expect(rows()).toHaveLength(3);
  });

  it("says plainly that a project with no requests has none", async () => {
    const releases = createFakeReleases();
    await screen(releases, createFakeReleaseOps(releases));
    expect(w!.find("[data-testid=requests-empty]").exists()).toBe(true);
  });
});
