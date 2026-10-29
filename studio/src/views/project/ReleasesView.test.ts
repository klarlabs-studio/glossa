import { flushPromises, type DOMWrapper, type VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";
import { createFakeReleases, type FakeReleases } from "../../test/fake-releases";
import { mountProjectScreen } from "../../test/project";
import ReleasesView from "./ReleasesView.vue";

const catalog = () => ({
  en: { "app.title": "Demo", greeting: "Hello, {name}!" },
  de: { greeting: "Hallo, {name}!" },
});

let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

async function screen(port: FakeReleases, roles?: Parameters<typeof mountProjectScreen>[1]["roles"]) {
  wrapper = await mountProjectScreen(ReleasesView, { port, ...(roles ? { roles } : {}) });
  return wrapper;
}

const card = (w: VueWrapper, env: string) => w.get(`[data-testid=env-${env}]`);
const button = (root: { findAll(selector: string): DOMWrapper<Element>[] }, name: string | RegExp) => {
  const b = root.findAll("button").find((x) => (typeof name === "string" ? x.text() === name : name.test(x.text())));
  if (!b) throw new Error(`no button ${name}`);
  return b;
};
const dialog = (w: VueWrapper) => w.get("dialog[open]");

async function publish(w: VueWrapper, env: string, note = ""): Promise<void> {
  await button(w, "Publish release").trigger("click");
  await flushPromises();
  const d = dialog(w);
  await d.get("#pub-env").setValue(env);
  if (note) await d.get("#pub-note").setValue(note);
  await d.get("form").trigger("submit");
  await flushPromises();
}

describe("ReleasesView", () => {
  it("shows every environment with what it serves and its policy", async () => {
    const port = createFakeReleases({ catalog: catalog() });
    const w = await screen(port);
    for (const env of ["development", "preview", "staging", "production"]) {
      expect(card(w, env).text()).toContain("Nothing published here yet");
    }
    expect(card(w, "production").text()).toContain("Ships: Approved; outdated included");
    expect(card(w, "development").text()).toContain("Draft, Needs review, Approved");
    expect(w.text()).toContain("Nothing published yet.");
  });

  it("publishes: a dry run shows what would ship and change first, then the result", async () => {
    const port = createFakeReleases({ catalog: catalog() });
    const w = await screen(port);
    await button(w, "Publish release").trigger("click");
    await flushPromises();
    let preview = dialog(w).get("[data-testid=publish-preview]");
    expect(preview.text()).toContain("Nothing is published to development yet");
    expect(preview.get("[data-testid=dry-run-summary]").text()).toMatch(/^2 messages in 2 locales · 2 new artifacts to upload · \d+ B$/);
    expect(preview.get("tr[data-locale=en]").text()).toContain("+2");
    expect(preview.get("tr[data-locale=de]").text()).toContain("+1");
    expect(port.calls.filter((c) => c[0] === "previewPublish").map((c) => c[2])).toEqual(["development"]);
    expect(port.state.releases).toHaveLength(0);
    await button(dialog(w), "Cancel").trigger("click");
    await flushPromises();

    await publish(w, "development", "First cut");
    expect(dialog(w).text()).toContain("Published v1 to development.");
    expect(dialog(w).text()).toContain("Everything is new in this environment.");
    expect(port.calls.find((c) => c[0] === "publish")?.[2]).toEqual({ environment: "development", note: "First cut" });
    await button(dialog(w), "Done").trigger("click");
    await flushPromises();
    expect(card(w, "development").get("[data-testid=env-version]").text()).toBe("Serving v1");
    expect(card(w, "development").text()).toContain("Published by you");
    expect(w.get("[data-testid=release-list]").text()).toContain("First cut");

    // The next publish names what development serves now, then the diff.
    port.catalog.de = { greeting: "Hallo, {name}!", "app.title": "Demo" };
    await button(w, "Publish release").trigger("click");
    await flushPromises();
    await dialog(w).get("#pub-env").setValue("development");
    await flushPromises();
    preview = dialog(w).get("[data-testid=publish-preview]");
    expect(preview.text()).toContain("development ships translations that are: Draft, Needs review, Approved; outdated included.");
    expect(preview.text()).toContain("Compared with v1, which development serves now:");
    // Before publishing: de adds app.title, nothing else changes.
    expect(preview.get("tr[data-locale=de]").text()).toContain("+1");
    expect(preview.get("tr[data-locale=en]").text()).not.toContain("+");
    await dialog(w).get("form").trigger("submit");
    await flushPromises();
    expect(dialog(w).text()).toContain("Changes since v1, per locale");
    expect(dialog(w).get("tr[data-locale=de]").text()).toContain("+1");
  });

  it("says nothing would change when the environment already ships the catalog", async () => {
    const port = createFakeReleases({ catalog: catalog() });
    const w = await screen(port);
    await publish(w, "staging");
    await button(dialog(w), "Done").trigger("click");
    await flushPromises();
    await button(w, "Publish release").trigger("click");
    await flushPromises();
    await dialog(w).get("#pub-env").setValue("staging");
    await flushPromises();
    expect(dialog(w).get("[data-testid=dry-run-unchanged]").text()).toBe("Nothing would change: staging already ships this text.");
  });

  it("lists every problem and refuses to publish a catalog that can't be released", async () => {
    const port = createFakeReleases({ catalog: catalog() });
    port.problems = [
      { code: "not_releasable", detail: "namespace too long", key: "legal.terms" },
      { code: "not_releasable", detail: "namespace too long", key: "legal.privacy" },
    ];
    const w = await screen(port);
    await button(w, "Publish release").trigger("click");
    await flushPromises();
    const problems = dialog(w).get("[data-testid=dry-run-problems]");
    expect(problems.text()).toContain("2 problems to fix first");
    expect(problems.findAll("li").map((li) => li.text())).toEqual(["legal.terms  namespace too long", "legal.privacy  namespace too long"]);
    expect(button(dialog(w), "Publish to development").attributes("disabled")).toBeDefined();
  });

  it("reuses the Idempotency-Key when the same publish is retried", async () => {
    const port = createFakeReleases({ catalog: catalog() });
    const original = port.publish.bind(port);
    let first = true;
    port.publish = async (...args) => {
      port.calls.push(["publish-attempt", args[2]]);
      if (first) {
        first = false;
        throw new Error("network");
      }
      return original(...args);
    };
    const w = await screen(port);
    await publish(w, "staging");
    expect(dialog(w).text()).toContain("network");
    await dialog(w).get("form").trigger("submit");
    await flushPromises();
    const keys = port.calls.filter((c) => c[0] === "publish-attempt").map((c) => c[1]);
    expect(keys).toHaveLength(2);
    expect(keys[0]).toBe(keys[1]);
  });

  it("refuses to promote a release built with drafts into production and says why", async () => {
    const port = createFakeReleases({ catalog: catalog() });
    const w = await screen(port);
    await publish(w, "development");
    await button(dialog(w), "Done").trigger("click");
    await flushPromises();

    await button(card(w, "production"), /^Promote/).trigger("click");
    await flushPromises();
    await dialog(w).get("#prm-release").setValue(port.state.releases[0]!.id);
    await flushPromises();
    const summary = dialog(w).get("[data-testid=promote-summary]");
    expect(summary.text()).toContain("production: nothing → v1");
    expect(dialog(w).get("[data-testid=promote-ineligible]").text()).toContain("built with Draft, Needs review translations");
    expect(button(dialog(w), "Promote v1 to production").attributes("disabled")).toBeDefined();
  });

  it("promotes with the pointer move and per-locale diff named, then rolls back", async () => {
    const port = createFakeReleases({ catalog: catalog() });
    const w = await screen(port);
    await publish(w, "staging");
    await button(dialog(w), "Done").trigger("click");
    await flushPromises();
    const v1 = port.state.releases[0]!.id;

    // v1 into production.
    await button(card(w, "production"), /^Promote/).trigger("click");
    await flushPromises();
    await dialog(w).get("#prm-release").setValue(v1);
    await flushPromises();
    expect(button(dialog(w), "Promote v1 to production").attributes("disabled")).toBeUndefined();
    await dialog(w).get("form").trigger("submit");
    await flushPromises();
    expect(w.get("[data-testid=releases-status]").text()).toBe("production now serves v1.");

    // v2 (one more German message) into production: the dialog names the change.
    port.catalog.de = { ...port.catalog.de, "app.title": "Demo-App" };
    await publish(w, "staging");
    await button(dialog(w), "Done").trigger("click");
    await flushPromises();
    const v2 = port.state.releases[0]!.id;
    await button(card(w, "production"), /^Promote/).trigger("click");
    await flushPromises();
    await dialog(w).get("#prm-release").setValue(v2);
    await flushPromises();
    expect(dialog(w).get("[data-testid=promote-summary]").text()).toContain("production: v1 → v2");
    expect(dialog(w).get("[data-testid=promote-summary] tr[data-locale=de]").text()).toContain("+1");
    await dialog(w).get("form").trigger("submit");
    await flushPromises();
    expect(card(w, "production").get("[data-testid=env-version]").text()).toBe("Serving v2");

    // Rollback preselects v1, names the move and sends the release explicitly.
    await button(card(w, "production"), /^Roll back/).trigger("click");
    await flushPromises();
    const rb = dialog(w).get("[data-testid=rollback-summary]");
    expect(rb.text()).toContain("production: v2 → v1");
    expect(rb.get("tr[data-locale=de]").text()).toContain("−1");
    await dialog(w).get("form").trigger("submit");
    await flushPromises();
    expect(port.calls.find((c) => c[0] === "rollback")?.slice(2)).toEqual(["production", v1]);
    expect(card(w, "production").get("[data-testid=env-version]").text()).toBe("Serving v1");
    expect(card(w, "production").text()).toContain("Rolled back by you");

    // History lists the three moves, newest first.
    await button(card(w, "production"), /^History/).trigger("click");
    await flushPromises();
    const rows = dialog(w).findAll("[data-testid=deployments] tbody tr").map((r) => r.text());
    expect(rows).toHaveLength(3);
    expect(rows[0]).toContain("Rolled back");
    expect(rows[0]).toContain("from v2");
    expect(rows[2]).toContain("Promoted");
  });

  it("says when an environment has nothing to roll back to", async () => {
    const port = createFakeReleases({ catalog: catalog() });
    const w = await screen(port);
    await publish(w, "development");
    await button(dialog(w), "Done").trigger("click");
    await flushPromises();
    await button(card(w, "development"), /^Roll back/).trigger("click");
    await flushPromises();
    expect(dialog(w).text()).toContain("development hasn't served an earlier release");
    expect(dialog(w).findAll("button").map((b) => b.text())).toEqual(["Cancel"]);
  });

  it("changes an environment's policy with its ETag", async () => {
    const port = createFakeReleases({ catalog: catalog() });
    const w = await screen(port);
    await button(card(w, "development"), /^Policy/).trigger("click");
    await flushPromises();
    const d = dialog(w);
    const boxes = d.findAll("input[type=checkbox]");
    await boxes[0]!.setValue(false); // draft
    await boxes[1]!.setValue(false); // needs review
    await d.get("form").trigger("submit");
    await flushPromises();
    expect(port.calls.find((c) => c[0] === "updatePolicy")?.slice(2)).toEqual([
      "development",
      { states: ["approved"], include_outdated: true },
      "1",
    ]);
    expect(card(w, "development").text()).toContain("Ships: Approved; outdated included");
  });

  it("keeps the policy dialog from saving without a review state", async () => {
    const port = createFakeReleases();
    const w = await screen(port);
    await button(card(w, "production"), /^Policy/).trigger("click");
    await flushPromises();
    await dialog(w).get("input[type=checkbox][value=approved]").setValue(false);
    expect(dialog(w).text()).toContain("Choose at least one review state.");
    expect(button(dialog(w), "Save policy").attributes("disabled")).toBeDefined();
  });

  it("is read-only without releases.publish", async () => {
    const port = createFakeReleases({ catalog: catalog() });
    await port.publish({ tenant: "t", project: "p" }, { environment: "development" }, "k1");
    const w = await screen(port, ["translator"]);
    expect(w.find("[data-testid=releases-read-only]").exists()).toBe(true);
    const labels = w.findAll("button").map((b) => b.text());
    expect(labels.some((l) => /Publish|Promote|Roll back|Policy/.test(l))).toBe(false);
    expect(labels.some((l) => l.startsWith("History"))).toBe(true);
    expect(card(w, "development").get("[data-testid=env-version]").text()).toBe("Serving v1");
  });

  it("opens the publish dialog with p", async () => {
    const w = await screen(createFakeReleases());
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "p", bubbles: true, cancelable: true }));
    await flushPromises();
    expect(dialog(w).text()).toContain("Publish a release");
  });
});
