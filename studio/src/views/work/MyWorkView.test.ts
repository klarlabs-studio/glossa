/**
 * My work (RFC 0006 §3.1): the assignments given to me, each a batch of
 * units with its state in words, opening into the workspace, and the
 * actions on it with the API's real refusals.
 */
import { flushPromises, type DOMWrapper, type VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";
import { ApiError } from "../../api/errors";
import { assignment, createFakeWork, project, type FakeWork } from "../../test/fake-work";
import { mountTenantScreen } from "../../test/project";
import MyWorkView from "./MyWorkView.vue";

let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

async function screen(work: FakeWork) {
  wrapper = await mountTenantScreen(MyWorkView, { work, path: "/t/t/work", roles: ["translator"], locales: ["de", "fr"] });
  return wrapper;
}
const cards = (w: VueWrapper) => w.findAll("[data-testid=assignment]");
const card = (w: VueWrapper, id: string) => w.get(`[data-assignment="${id}"]`);
const click = async (el: Pick<DOMWrapper<Element>, "trigger">) => {
  await el.trigger("click");
  await flushPromises();
};

describe("MyWorkView", () => {
  it("says it is loading while the work is read, and claims nothing yet", async () => {
    const work = createFakeWork();
    work.hold.add("myAssignments");
    const w = await screen(work);
    expect(w.get("[data-testid=work-loading]").text()).toBe("Loading your work…");
    expect(w.find("[data-testid=work-empty]").exists()).toBe(false);
  });

  it("tells a failed read apart from having no work, and reads again on request", async () => {
    const work = createFakeWork({ assignments: [assignment()] });
    work.fail.myAssignments = new ApiError(503, "unavailable", "The service is unavailable.");
    const w = await screen(work);
    const failed = w.get("[data-testid=work-failed]");
    expect(failed.attributes("role")).toBe("alert");
    expect(failed.text()).toContain("Your work could not be read");
    expect(failed.text()).toContain("The service is unavailable.");
    expect(w.find("[data-testid=work-empty]").exists()).toBe(false);
    delete work.fail.myAssignments;
    await click(failed.get("button"));
    expect(cards(w)).toHaveLength(1);
  });

  it("says so when nothing is assigned", async () => {
    const w = await screen(createFakeWork());
    expect(w.get("[data-testid=work-empty]").text()).toContain("Nothing is assigned to you.");
    expect(w.find("[data-testid=work-failed]").exists()).toBe(false);
  });

  it("groups the units by assignment: project, state in words, unit count, locales and due date, live work first", async () => {
    const work = createFakeWork({
      projects: [project("p1", "Portal"), project("p2", "Ledger")],
      assignments: [
        assignment({ id: "done", state: "done", closed_at: "2026-09-18T10:00:00Z" }),
        assignment({
          id: "late",
          project_id: "p2",
          state: "accepted",
          due_at: "2020-01-01T00:00:00Z",
          units: [
            { message_id: "m1", locale: "fr" },
            { message_id: "m1", locale: "de" },
            { message_id: "m2", locale: "de" },
          ],
        }),
        assignment({ id: "new", due_at: "2099-01-01T00:00:00Z" }),
      ],
    });
    const w = await screen(work);
    expect(w.findAll("[data-testid=work-live] [data-testid=assignment]").map((c) => c.attributes("data-assignment"))).toEqual(["late", "new"]);
    expect(w.findAll("[data-testid=work-finished] [data-testid=assignment]").map((c) => c.attributes("data-assignment"))).toEqual(["done"]);

    const late = card(w, "late");
    expect(late.get("h3").text()).toBe("Ledger");
    expect(late.get("[data-testid=assignment-state]").text()).toBe("Accepted");
    expect(late.get("[data-testid=assignment-units]").text()).toBe("3 units");
    expect(late.get("[data-testid=assignment-locales]").text()).toBe("In de, fr");
    expect(late.get("[data-testid=assignment-overdue]").text()).toMatch(/^Overdue since /);

    const fresh = card(w, "new");
    expect(fresh.get("[data-testid=assignment-state]").text()).toBe("Open");
    expect(fresh.get("[data-testid=assignment-due]").text()).toMatch(/^Due /);
    expect(fresh.get("[data-testid=assignment-due]").attributes("datetime")).toBe("2099-01-01T00:00:00Z");
    expect(card(w, "done").get("[data-testid=assignment-state]").text()).toBe("Done");
  });

  it("opens the workspace scoped to the assignment's units, in its first locale", async () => {
    const w = await screen(createFakeWork({ assignments: [assignment({ id: "as9", units: [{ message_id: "m1", locale: "fr" }] })] }));
    expect(card(w, "as9").get("[data-testid=assignment-open]").attributes("href")).toBe("/t/t/p/p1/translate?assignment=as9&locale=fr");
  });

  it("names a project it cannot read by id, rather than failing the work", async () => {
    const work = createFakeWork({ assignments: [assignment({ project_id: "0123456789ab" })] });
    work.fail.projects = new ApiError(403, "forbidden", "Forbidden.");
    const w = await screen(work);
    expect(cards(w)[0]!.get("h3").text()).toBe("Project 01234567");
  });

  it("accepts, then completes: each result is announced and focus stays on the assignment", async () => {
    const work = createFakeWork({ assignments: [assignment({ id: "a1" })] });
    const w = await screen(work);
    await click(card(w, "a1").get("[data-testid=assignment-accept]"));
    expect(work.calls).toContainEqual(["accept", "t", "a1"]);
    expect(card(w, "a1").get("[data-testid=assignment-state]").text()).toBe("Accepted");
    expect(w.get("[data-testid=work-status]").text()).toBe("Accepted: the assignment in Portal is yours to work on.");
    expect(w.get("[data-testid=work-status]").attributes("role")).toBe("status");
    expect(document.activeElement?.id).toBe("as-h-a1");

    await click(card(w, "a1").get("[data-testid=assignment-complete]"));
    expect(work.calls).toContainEqual(["complete", "t", "a1"]);
    expect(w.get("[data-testid=work-status]").text()).toContain("Marked complete");
    expect(w.findAll("[data-testid=work-finished] [data-testid=assignment]")).toHaveLength(1);
    expect(w.get("[data-testid=work-none-live]").text()).toContain("Nothing left to do.");
    expect(document.activeElement?.id).toBe("as-h-a1");
  });

  it("declines with a reason through a dialog", async () => {
    const work = createFakeWork({ assignments: [assignment({ id: "a1" })] });
    const w = await screen(work);
    await click(card(w, "a1").get("[data-testid=assignment-decline]"));
    await w.get("[data-testid=decline-reason]").setValue("On leave until November");
    await click(w.get("[data-testid=decline-confirm]"));
    expect(work.calls).toContainEqual(["decline", "t", "a1", "On leave until November"]);
    expect(card(w, "a1").get("[data-testid=assignment-state]").text()).toBe("Declined");
    expect(card(w, "a1").text()).toContain("Reason: On leave until November");
  });

  it("explains a state conflict and reads the list again rather than showing a stale action", async () => {
    const work = createFakeWork({ assignments: [assignment({ id: "a1" })] });
    const w = await screen(work);
    // Someone else's change lands between the read and the click.
    work.state.assignments = [assignment({ id: "a1", state: "expired" })];
    await click(card(w, "a1").get("[data-testid=assignment-accept]"));
    expect(w.get("[data-testid=work-status]").text()).toContain("Already moved on");
    expect(card(w, "a1").get("[data-testid=assignment-state]").text()).toBe("Expired");
    expect(card(w, "a1").find("[data-testid=assignment-accept]").exists()).toBe(false);
  });

  it("explains an assignment that is gone", async () => {
    const work = createFakeWork({ assignments: [assignment({ id: "a1" })] });
    const w = await screen(work);
    work.state.assignments = [];
    await click(card(w, "a1").get("[data-testid=assignment-accept]"));
    expect(w.get("[data-testid=work-status]").text()).toContain("Not found: this assignment no longer exists");
    expect(w.find("[data-testid=work-empty]").exists()).toBe(true);
  });

  it("says why taking work on is refused, on the assignment itself", async () => {
    const work = createFakeWork({ assignments: [assignment({ id: "a1" })] });
    work.fail.accept = new ApiError(403, "forbidden", "Forbidden.");
    const w = await screen(work);
    await click(card(w, "a1").get("[data-testid=assignment-accept]"));
    const err = card(w, "a1").get("[data-testid=assignment-error]");
    expect(err.attributes("role")).toBe("alert");
    expect(err.text()).toBe("Not allowed: taking this on needs translating rights in every locale it covers.");
    expect(card(w, "a1").get("[data-testid=assignment-state]").text()).toBe("Open");
  });
});
