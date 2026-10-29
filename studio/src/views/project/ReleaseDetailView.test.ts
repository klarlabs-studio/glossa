import { flushPromises, type VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";
import { createFakeReleases } from "../../test/fake-releases";
import { mountProjectScreen } from "../../test/project";
import ReleaseDetailView from "./ReleaseDetailView.vue";

const p = { tenant: "t", project: "p" };
let w: VueWrapper | undefined;
afterEach(() => {
  w?.unmount();
  w = undefined;
});

describe("ReleaseDetailView", () => {
  it("shows per-locale counts and the diff to the previous release", async () => {
    const port = createFakeReleases({ catalog: { en: { a: "A", b: "B" }, de: { a: "A-de" } } });
    await port.publish(p, { environment: "staging" }, "k1");
    port.catalog = { en: { a: "A", b: "B" }, de: { a: "A-de!", b: "B-de" }, fr: { a: "A-fr" } };
    const v2 = await port.publish(p, { environment: "staging", note: "German fixes" }, "k2");
    await port.promote(p, "production", v2.id);

    w = await mountProjectScreen(ReleaseDetailView, { port, path: `/t/t/p/p/releases/${v2.id}` });
    expect(w.get("h1").text()).toBe("Release v2");
    expect(w.text()).toContain("German fixes");
    expect(w.findAll(".pill-accent").map((x) => x.text())).toEqual(["Serving staging", "Serving production"]);
    expect(w.text()).toContain("Compared with v1");

    const row = (l: string) => w!.get(`[data-testid=release-locales] tr[data-locale=${l}]`).findAll("td").map((td) => td.text());
    // messages, outdated, added, changed, removed
    expect(row("en")).toEqual(["2", "0", "0", "0", "0"]);
    expect(row("de")).toEqual(["2", "0", "+1", "1", "0"]);
    expect(row("fr")).toEqual(["1", "0", "+1", "0", "0"]);
    expect(w.get("[data-testid=release-locales] details").text()).toContain("de: Show 2 message IDs");
  });

  it("offers promotion from the release, without the actions for read-only members", async () => {
    const port = createFakeReleases();
    const r = await port.publish(p, { environment: "staging" }, "k1");
    w = await mountProjectScreen(ReleaseDetailView, { port, path: `/t/t/p/p/releases/${r.id}` });
    const promote = w.findAll("button").find((b) => b.text().startsWith("Promote"));
    await promote!.trigger("click");
    await flushPromises();
    await w.get("dialog[open] #prm-env").setValue("production");
    await flushPromises();
    await w.get("dialog[open] form").trigger("submit");
    await flushPromises();
    expect(w.get("[role=status]").text()).toBe("production now serves v1.");
    w.unmount();

    w = await mountProjectScreen(ReleaseDetailView, { port, roles: ["reviewer"], path: `/t/t/p/p/releases/${r.id}` });
    expect(w.findAll("button").some((b) => b.text().startsWith("Promote"))).toBe(false);
  });
});
