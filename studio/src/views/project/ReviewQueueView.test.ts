import { flushPromises, type VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it, type Mock } from "vitest";
import type { AISuggestion } from "../../api/intelligence-schemas";
import { createFakeIntelligence, suggestion as baseSuggestion, suggestionSource, type FakeIntelligence } from "../../test/fake-intelligence";
import { ApiError } from "../../api/errors";
import { assignment, createFakeWork, type FakeWork } from "../../test/fake-work";
import { locale, mountProjectScreen, type ScreenOptions } from "../../test/project";
import ReviewQueueView from "./ReviewQueueView.vue";

let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

const locales = [locale("en", true), locale("de"), locale("fr")];
/** Each item carries its message's current source, as the server embeds it. */
const suggestion = (over: Partial<AISuggestion>) => baseSuggestion({ source: suggestionSource(over.message_key!, { text: `source of ${over.message_key}` }), ...over });

function queue(): FakeIntelligence {
  const i = createFakeIntelligence();
  i.state.suggestions.push(
    suggestion({ id: "ok", message_key: "billing.invoice", score: 0.86, action: "approve_recommended", message: "Rechnung herunterladen" }),
    suggestion({ id: "risky", message_key: "workspace.delete", score: 0.23, risk_tags: ["term_forbidden"], message: "Diesen Workspace löschen" }),
    suggestion({ id: "legal", message_key: "terms.accept", score: 0.49, risk_tags: ["legal"] }),
    suggestion({ id: "fr", message_key: "menu.home", locale: "fr", score: 0.9, action: "approve_recommended", message: "Accueil" }),
  );
  return i;
}

async function screen(i: FakeIntelligence, options: Partial<ScreenOptions> = {}) {
  wrapper = await mountProjectScreen(ReviewQueueView, {
    intelligence: i,
    work: createFakeWork(),
    locales,
    roles: ["owner"],
    path: "/t/t/p/p/review",
    ...options,
  });
  return wrapper;
}

const press = async (key: string, init: KeyboardEventInit = {}, target: EventTarget = window) => {
  (target as HTMLElement).dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true, ...init }));
  await flushPromises();
};
const options = (w: VueWrapper) => w.findAll("[role=option]");

describe("ReviewQueueView", () => {
  it("lists pending suggestions riskiest first, with the focused one's source", async () => {
    const w = await screen(queue());
    expect(options(w).map((o) => o.get(".key").text())).toEqual(["workspace.delete", "terms.accept", "billing.invoice", "menu.home"]);
    expect(options(w)[0]!.text()).toContain("Low confidence · 0.23");
    expect(options(w)[0]!.text()).toContain("Forbidden term");
    expect(options(w)[0]!.attributes("aria-selected")).toBe("true");
    expect(w.get("[data-testid=review-source]").text()).toBe("source of workspace.delete");
  });

  it("shows each item's source from the queue itself, with no request per item", async () => {
    const w = await screen(queue());
    await press("j");
    expect(w.get("[data-testid=review-source]").text()).toBe("source of terms.accept");
    await press("j");
    expect(w.get("[data-testid=review-source]").text()).toBe("source of billing.invoice");
    const paths = (fetch as unknown as Mock<(r: Request) => Promise<Response>>).mock.calls.map((c) => new URL(c[0].url).pathname);
    expect(paths.filter((p) => p !== "/v1/me")).toEqual([]);
  });

  it("says when the source moved on since the suggestion, and follows a rename", async () => {
    const i = createFakeIntelligence();
    i.state.suggestions.push(
      baseSuggestion({ id: "old", message_key: "cart.checkout", source_revision: 1, source: suggestionSource("checkout.start", { source_revision: 3, text: "Start checkout" }) }),
    );
    const w = await screen(i);
    expect(w.get("[data-testid=review-source]").text()).toBe("Start checkout");
    expect(w.get("[data-testid=review-source-changed]").text()).toContain("source revision 1; the source is now at revision 3");
    expect(w.get("[data-testid=review-detail]").text()).toContain("Now checkout.start.");
    expect(w.get("[data-testid=review-detail] a").attributes("href")).toContain("key=checkout.start");
  });

  it("triages from the keyboard: j/k move, a accepts, r rejects, e edits and Mod+Enter accepts the edit", async () => {
    const i = queue();
    const w = await screen(i);
    await press("j");
    expect(options(w)[1]!.attributes("aria-selected")).toBe("true");
    await press("a");
    expect(i.calls).toContainEqual(["accept", "legal", undefined]);
    expect(w.get("[data-testid=review-status]").text()).toBe("Accepted terms.accept (de).");
    expect(options(w)).toHaveLength(3);
    // The next item takes the decided one's place.
    expect(options(w)[1]!.attributes("aria-selected")).toBe("true");
    await press("k");
    await press("e");
    const edit = w.get("[data-testid=review-edit]");
    expect(document.activeElement).toBe(edit.element);
    // Single keys type while editing.
    await press("a", {}, edit.element);
    expect(i.calls.filter((c) => c[0] === "accept")).toHaveLength(1);
    await edit.setValue("Diesen Arbeitsbereich löschen");
    await press("Enter", { metaKey: true }, edit.element);
    expect(i.calls).toContainEqual(["accept", "risky", { text: "Diesen Arbeitsbereich löschen", syntax: "mf2" }]);
    expect(w.get("[data-testid=review-status]").text()).toBe("Accepted your edit of workspace.delete (de).");
    await press("r");
    expect(i.calls).toContainEqual(["reject", "ok", undefined]);
    expect(options(w).map((o) => o.get(".key").text())).toEqual(["menu.home"]);
  });

  it("Esc cancels an edit without deciding", async () => {
    const i = queue();
    const w = await screen(i);
    await press("e");
    await press("Escape", {}, w.get("[data-testid=review-edit]").element);
    expect(w.find("[data-testid=review-edit]").exists()).toBe(false);
    expect(i.calls.filter((c) => c[0] === "accept" || c[0] === "reject")).toEqual([]);
  });

  it("batch-accepts only what the policy recommends approving", async () => {
    const i = queue();
    const w = await screen(i);
    const batch = w.get("[data-testid=batch-accept]");
    expect(batch.text()).toBe("Accept 2 recommended");
    await batch.trigger("click");
    await flushPromises();
    const confirm = w.get("dialog[open]").findAll("button").find((b) => b.text() === "Accept 2")!;
    await confirm.trigger("click");
    await flushPromises();
    expect(i.calls.filter((c) => c[0] === "accept").map((c) => c[1])).toEqual(["ok", "fr"]);
    expect(w.get("[data-testid=review-status]").text()).toBe("Accepted 2.");
    expect(options(w).map((o) => o.get(".key").text())).toEqual(["workspace.delete", "terms.accept"]);
  });

  it("narrows a scoped translator's queue to their locales", async () => {
    const i = queue();
    const w = await screen(i, { roles: ["translator"], memberLocales: ["de"] });
    expect(i.calls.find((c) => c[0] === "reviewQueue")![1]).toEqual(["de"]);
    expect(options(w).map((o) => o.text())).not.toContain("menu.home");
    expect(w.findAll("#rq-locale option").map((o) => o.text())).toEqual(["All your locales", "de"]);
  });

  it("says when there's nothing to review", async () => {
    const w = await screen(createFakeIntelligence());
    expect(w.get("[data-testid=review-empty]").text()).toContain("Nothing to review.");
    expect(w.get("[data-testid=batch-accept]").attributes("disabled")).toBeDefined();
  });

  describe("assigned to me (RFC 0006 §3.1)", () => {
    function boundWork(): FakeWork {
      const work = createFakeWork({
        assignments: [
          assignment({ id: "live", project_id: "p", state: "accepted", units: [{ message_id: "m-pay", locale: "de" }] }),
          assignment({ id: "old", project_id: "p", state: "done", units: [{ message_id: "m-old", locale: "de" }] }),
        ],
      });
      work.state.resolution.set("de", { bound: true, definition_name: "four-eyes", version: 1 });
      return work;
    }
    function mixedQueue(): FakeIntelligence {
      const i = createFakeIntelligence();
      i.state.suggestions.push(
        suggestion({ id: "pay", message_id: "m-pay", message_key: "checkout.pay", score: 0.3 }),
        suggestion({ id: "old", message_id: "m-old", message_key: "checkout.old", score: 0.4 }),
        suggestion({ id: "other", message_id: "m-other", message_key: "menu.home", score: 0.5 }),
      );
      return i;
    }
    const keys = (w: VueWrapper) => options(w).map((o) => o.get(".key").text());

    it("is not offered where no workflow is bound", async () => {
      const w = await screen(queue());
      expect(w.find("[data-testid=review-mine]").exists()).toBe(false);
    });

    it("asks the workflow resolution per locale of mine", async () => {
      const work = createFakeWork();
      await screen(queue(), { work });
      expect(work.calls.filter((c) => c[0] === "resolveWorkflow").map((c) => c[2])).toEqual(["de", "fr"]);
    });

    it("narrows the queue to the units of my live assignments", async () => {
      const work = boundWork();
      const w = await screen(mixedQueue(), { work });
      expect(keys(w)).toEqual(["checkout.pay", "checkout.old", "menu.home"]);
      const box = w.get("[data-testid=review-mine]");
      expect(box.attributes("aria-describedby")).toBe("rq-mine-hint");
      await box.setValue(true);
      await flushPromises();
      expect(work.calls).toContainEqual(["myAssignments", "t", { project: "p" }]);
      expect(keys(w)).toEqual(["checkout.pay"]);
      expect(w.get("[data-testid=review-detail] h2").text()).toBe("checkout.pay");
    });

    it("says nothing of mine waits, rather than showing the whole queue", async () => {
      const work = boundWork();
      work.state.assignments = [];
      const w = await screen(mixedQueue(), { work, path: "/t/t/p/p/review?mine=1" });
      expect(options(w)).toHaveLength(0);
      expect(w.get("[data-testid=review-mine-empty]").text()).toBe("Nothing to review in your assignments.");
      expect(w.find("[data-testid=review-empty]").exists()).toBe(false);
    });

    it("tells a failed assignment read apart from an empty filter", async () => {
      const work = boundWork();
      work.fail.myAssignments = new ApiError(503, "unavailable", "Unavailable.");
      const w = await screen(mixedQueue(), { work, path: "/t/t/p/p/review?mine=1" });
      expect(options(w)).toHaveLength(0);
      const failed = w.get("[data-testid=review-mine-failed]");
      expect(failed.attributes("role")).toBe("alert");
      expect(failed.text()).toContain("Your assignments could not be read");
      expect(w.find("[data-testid=review-mine-empty]").exists()).toBe(false);
    });

    it("says when it could not tell whether a workflow applies", async () => {
      const work = createFakeWork();
      work.fail.resolveWorkflow = new ApiError(500, "internal", "Broken.");
      const w = await screen(queue(), { work });
      expect(w.find("[data-testid=review-mine]").exists()).toBe(false);
      expect(w.get("[data-testid=review-workflow-unknown]").text()).toContain("could not be read");
      // The queue itself is unaffected.
      expect(options(w)).toHaveLength(4);
    });
  });
});
