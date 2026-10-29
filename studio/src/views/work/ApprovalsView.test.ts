/**
 * The approvals inbox (RFC 0006 §3.2): what I may decide, with the text
 * under approval and its author, and the human-only, four-eyes refusals
 * as sentences that say which rule refused.
 */
import { flushPromises, type DOMWrapper, type VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";
import { ApiError } from "../../api/errors";
import type { Role } from "../../api/schemas";
import { approval, createFakeWork, unitText, type FakeWork } from "../../test/fake-work";
import { mountTenantScreen } from "../../test/project";
import ApprovalsView from "./ApprovalsView.vue";

let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

const AT = "2026-09-01T00:00:00Z";
const MEMBERS = {
  "/v1/tenants/t/members": {
    items: [
      { id: "mv", email: "vera@lingua.example", person_id: "vera", display_name: "Vera", status: "active", roles: ["translator"], locales: ["de"], projects: [], visibility: "assigned", created_at: AT, updated_at: AT },
    ],
  },
};

async function screen(work: FakeWork, roles: Role[] = ["reviewer"], locales = ["de"]) {
  wrapper = await mountTenantScreen(ApprovalsView, { work, path: "/t/t/approvals", roles, locales, responses: MEMBERS });
  return wrapper;
}
const texts = (...ts: ReturnType<typeof unitText>[]) => new Map([["p1/de", ts]]);
const cards = (w: VueWrapper) => w.findAll("[data-testid=approval]");
const card = (w: VueWrapper, id: string) => w.get(`[data-approval="${id}"]`);
const click = async (el: Pick<DOMWrapper<Element>, "trigger">) => {
  await el.trigger("click");
  await flushPromises();
};

describe("ApprovalsView", () => {
  it("says it is loading, and claims nothing yet", async () => {
    const work = createFakeWork();
    work.hold.add("approvals");
    const w = await screen(work);
    expect(w.get("[data-testid=approvals-loading]").text()).toBe("Loading approvals…");
    expect(w.find("[data-testid=approvals-empty]").exists()).toBe(false);
  });

  it("tells a failed read apart from an empty inbox", async () => {
    const work = createFakeWork({ approvals: [approval()] });
    work.fail.approvals = new ApiError(0, "network_error", "The server could not be reached.");
    const w = await screen(work);
    expect(w.get("[data-testid=approvals-failed]").text()).toContain("The approvals could not be read");
    expect(w.get("[data-testid=approvals-failed]").text()).toContain("The server could not be reached.");
    expect(w.find("[data-testid=approvals-empty]").exists()).toBe(false);
    delete work.fail.approvals;
    await click(w.get("[data-testid=approvals-failed] button"));
    expect(cards(w)).toHaveLength(1);
  });

  it("says so when nothing waits", async () => {
    const w = await screen(createFakeWork());
    expect(w.get("[data-testid=approvals-empty]").text()).toBe("Nothing is waiting for your approval.");
  });

  it("asks only for pending translation approvals", async () => {
    const work = createFakeWork();
    await screen(work);
    expect(work.calls.find((c) => c[0] === "approvals")![2]).toEqual({ state: "pending", subject: "translation" });
  });

  it("lists what I may decide, with the text under approval and its author", async () => {
    const work = createFakeWork({
      approvals: [
        approval({ id: "role", eligible: { kind: "role", role: "reviewer" }, required: 2, decisions: [{ principal: "person:rita", decision: "granted", at: AT }] }),
        approval({ id: "me", subject_id: "m2", eligible: { kind: "member", id: "m" } }),
        approval({ id: "group", subject_id: "m3", eligible: { kind: "group", id: "g1" } }),
        // Not mine to decide: another member, another role, a group I am not in, a locale outside my scope, one I decided.
        approval({ id: "other-member", eligible: { kind: "member", id: "m-other" } }),
        approval({ id: "other-role", eligible: { kind: "role", role: "admin" } }),
        approval({ id: "other-group", eligible: { kind: "group", id: "g2" } }),
        approval({ id: "fr", locale: "fr" }),
        approval({ id: "decided", required: 2, decisions: [{ principal: "person:me", decision: "granted", at: AT }] }),
      ],
      groups: [
        { id: "g1", name: "de reviewers", members: ["m", "m2"], created_at: AT, updated_at: AT },
        { id: "g2", name: "fr reviewers", members: ["m3"], created_at: AT, updated_at: AT },
      ],
      texts: texts(unitText("m1", "checkout.pay", "Jetzt bezahlen"), unitText("m2", "checkout.back", "Zurück", "person:rita")),
    });
    const w = await screen(work);
    expect(cards(w).map((c) => c.attributes("data-approval"))).toEqual(["role", "me", "group"]);

    const role = card(w, "role");
    expect(role.get("h2").text()).toBe("checkout.pay");
    expect(role.get("[data-testid=approval-state]").text()).toBe("Pending");
    expect(role.get("[data-testid=approval-text]").text()).toBe("Jetzt bezahlen");
    expect(role.get("[data-testid=approval-text]").attributes("lang")).toBe("de");
    expect(role.get("[data-testid=approval-author]").text()).toBe("Written by Vera");
    expect(role.get("[data-testid=approval-progress]").text()).toBe("1 of 2 approvals given");
    expect(role.text()).toContain("Asked of every reviewer");
    expect(role.text()).toContain("Four-eyes: the author of this text can't approve it.");
    expect(card(w, "me").text()).toContain("Asked of you");
    expect(card(w, "group").text()).toContain("Asked of the group de reviewers");
    // A unit without a translation says so, rather than showing an empty box.
    expect(card(w, "group").get("[data-testid=approval-no-text]").text()).toContain("no text yet");
  });

  it("lists group approvals when the groups can't be read, and says why", async () => {
    const work = createFakeWork({ approvals: [approval({ eligible: { kind: "group", id: "g1" } })], texts: texts(unitText("m1", "a.b", "x")) });
    work.fail.groups = new ApiError(403, "forbidden", "Forbidden.");
    const w = await screen(work);
    expect(cards(w)).toHaveLength(1);
    expect(w.get("[data-testid=approvals-groups-unknown]").text()).toContain("Your groups could not be read");
  });

  it("offers no decision on text it could not read", async () => {
    const work = createFakeWork({ approvals: [approval()] });
    work.fail.unitTexts = new ApiError(500, "internal", "Something broke.");
    const w = await screen(work);
    const failed = card(w, "ap1").get("[data-testid=approval-text-failed]");
    expect(failed.attributes("role")).toBe("alert");
    expect(failed.text()).toContain("The text under approval could not be read.");
    expect(failed.text()).toContain("Something broke.");
    expect(card(w, "ap1").get("[data-testid=approval-grant]").attributes("disabled")).toBeDefined();
  });

  it("says plainly when the text is mine, and offers no decision (four-eyes)", async () => {
    const work = createFakeWork({ approvals: [approval()], texts: texts(unitText("m1", "a.b", "Mein Text", "person:me")) });
    const w = await screen(work);
    expect(card(w, "ap1").get("[data-testid=approval-own-text]").text()).toBe("You wrote this text, so someone else must approve it.");
    expect(card(w, "ap1").find("[data-testid=approval-grant]").exists()).toBe(false);
  });

  it("grants with a reason: the result is announced, the item leaves and focus moves to the next", async () => {
    const work = createFakeWork({
      approvals: [approval({ id: "a1" }), approval({ id: "a2", subject_id: "m2" })],
      texts: texts(unitText("m1", "checkout.pay", "Jetzt bezahlen"), unitText("m2", "checkout.back", "Zurück")),
    });
    const w = await screen(work);
    await card(w, "a1").get("[data-testid=approval-reason]").setValue("Matches the glossary");
    await click(card(w, "a1").get("[data-testid=approval-grant]"));
    expect(work.calls).toContainEqual(["decide", "t", "a1", "granted", "Matches the glossary"]);
    expect(w.get("[data-testid=approvals-status]").text()).toBe("Approved checkout.pay (de).");
    expect(w.get("[data-testid=approvals-status]").attributes("role")).toBe("status");
    expect(cards(w).map((c) => c.attributes("data-approval"))).toEqual(["a2"]);
    expect(document.activeElement?.id).toBe("ap-h-a2");

    await click(card(w, "a2").get("[data-testid=approval-deny]"));
    expect(work.calls).toContainEqual(["decide", "t", "a2", "denied", undefined]);
    expect(w.get("[data-testid=approvals-status]").text()).toBe("Denied checkout.back (de).");
    expect(w.find("[data-testid=approvals-empty]").exists()).toBe(true);
    expect(document.activeElement?.tagName).toBe("H1");
  });

  it.each([
    ["own_text", "Not yours to approve: you wrote this text, and four-eyes means someone other than its author approves it."],
    ["not_eligible", "Not yours to approve: this approval was asked of other people, or of this locale's reviewers and you aren't one."],
    ["person_required", "Only a person can approve: an API token or an agent can't, whatever its scopes."],
  ])("shows the %s refusal as its own sentence, on the approval", async (code, sentence) => {
    const work = createFakeWork({ approvals: [approval()], texts: texts(unitText("m1", "a.b", "x")) });
    work.state.refusals.set("ap1", new ApiError(403, code, "Forbidden."));
    const w = await screen(work);
    await click(card(w, "ap1").get("[data-testid=approval-grant]"));
    const err = card(w, "ap1").get("[data-testid=approval-error]");
    expect(err.attributes("role")).toBe("alert");
    expect(err.text()).toBe(sentence);
    expect(err.text()).not.toContain("permission");
    // Still listed: nothing was decided.
    expect(cards(w)).toHaveLength(1);
    expect(document.activeElement?.id).toBe("ap-h-ap1");
  });

  it.each([
    ["approval_closed", "Already decided"],
    ["approval_superseded", "Replaced: a newer approval request"],
  ])("reads the inbox again after %s and says why", async (code, words) => {
    const work = createFakeWork({ approvals: [approval()], texts: texts(unitText("m1", "a.b", "x")) });
    work.state.refusals.set("ap1", new ApiError(409, code, "Conflict."));
    const w = await screen(work);
    work.state.approvals = [];
    await click(card(w, "ap1").get("[data-testid=approval-grant]"));
    expect(w.get("[data-testid=approvals-status]").text()).toContain(words);
    expect(w.find("[data-testid=approvals-empty]").exists()).toBe(true);
  });
});
