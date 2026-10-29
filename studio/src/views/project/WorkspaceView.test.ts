/**
 * The workspace opened on one assignment (RFC 0006 §3.1): only its
 * units, only its locales — and, while the assignment is unknown,
 * nothing rather than every message.
 */
import type { VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";
import { ApiError } from "../../api/errors";
import type { Message } from "../../api/schemas";
import { assignment, createFakeWork, type FakeWork } from "../../test/fake-work";
import { locale, mountProjectScreen } from "../../test/project";
import WorkspaceView from "./WorkspaceView.vue";

let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

const AT = "2026-09-19T08:00:00Z";
const message = (id: string, key: string): Message => ({
  id,
  key,
  namespace: "default",
  description: "",
  state: "active",
  source: { text: `Source of ${key}`, syntax: "mf2", model: { type: "message", declarations: [], pattern: [`Source of ${key}`] }, arguments: [], markup: [] },
  source_revision: 1,
  created_at: AT,
  updated_at: AT,
});
const MESSAGES = { items: [message("m1", "a.one"), message("m2", "a.two"), message("m3", "a.three")] };

async function screen(work: FakeWork, query = "?assignment=as1") {
  wrapper = await mountProjectScreen(WorkspaceView, {
    work,
    roles: ["translator"],
    memberLocales: ["de", "fr"],
    locales: [locale("en", true), locale("de"), locale("fr")],
    path: `/t/t/p/p/translate${query}`,
    fetch: (path) => (path === "/v1/tenants/t/projects/p/messages" ? MESSAGES : undefined),
  });
  return wrapper;
}
const count = (w: VueWrapper) => w.findAll(".count[role=status]")[0]!.text();

describe("WorkspaceView scoped to an assignment", () => {
  it("shows only the assignment's units, in its locales", async () => {
    const work = createFakeWork({
      assignments: [assignment({ id: "as1", project_id: "p", state: "accepted", units: [{ message_id: "m1", locale: "de" }, { message_id: "m3", locale: "de" }] })],
    });
    const w = await screen(work);
    expect(work.calls).toContainEqual(["assignment", "t", "as1"]);
    expect(w.get("[data-testid=assignment-scope]").text()).toContain("Working on an assignment: 2 units in de");
    expect(w.get("[data-testid=assignment-scope-state]").text()).toBe("Accepted");
    expect(count(w)).toBe("2 of 3 messages");
    expect(w.findAll("#ws-locale option").map((o) => o.attributes("value"))).toEqual(["de"]);
    expect(w.get("[data-testid=assignment-scope] a").attributes("href")).toBe("/t/t/work");
  });

  it("shows nothing, and says why, when the assignment can't be read", async () => {
    const work = createFakeWork();
    const w = await screen(work);
    const scope = w.get("[data-testid=assignment-scope]");
    expect(scope.attributes("data-phase")).toBe("failed");
    expect(scope.get("[role=alert]").text()).toContain("The assignment could not be read");
    expect(count(w)).toBe("0 of 3 messages");
    expect(w.text()).not.toContain("No messages match");
  });

  it("shows nothing while the assignment loads", async () => {
    const work = createFakeWork();
    work.hold.add("assignment");
    const w = await screen(work);
    expect(w.get("[data-testid=assignment-scope]").attributes("data-phase")).toBe("loading");
    expect(count(w)).toBe("0 of 3 messages");
  });

  it("is the whole catalog without an assignment", async () => {
    const work = createFakeWork();
    work.fail.assignment = new ApiError(500, "internal", "Should not be asked.");
    const w = await screen(work, "");
    expect(w.find("[data-testid=assignment-scope]").exists()).toBe(false);
    expect(count(w)).toBe("3 of 3 messages");
    expect(work.calls.filter((c) => c[0] === "assignment")).toEqual([]);
  });
});

describe("WorkspaceView and the unit's workflow", () => {
  it("links the selected translation's history to its workflow instances (RFC 0006 §8)", async () => {
    const w = await screen(createFakeWork(), "?locale=de&key=a.two");
    const link = w.get("[data-testid=workflow-instances-link]");
    expect(link.text()).toBe("Workflow history of this translation");
    expect(link.attributes("href")).toBe("/t/t/p/p/workflow/instances?message=a.two&locale=de");
  });
});
