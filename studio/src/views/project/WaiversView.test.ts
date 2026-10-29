import { flushPromises, type VueWrapper } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";
import { createFakeQuality, waiver, type FakeQuality } from "../../test/fake-quality";
import { locale, mountProjectScreen } from "../../test/project";
import WaiversView from "./WaiversView.vue";

let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

const LOCALES = [locale("en", true), locale("de")];

const term = waiver({
  fingerprint: "f_0000000000000001",
  reason: "“Login” is the German term our brand guide prescribes.",
  accepts: { layer: "terminology", code: "term_forbidden", locale: "de", key: "auth.login" },
});
const visual = waiver({
  fingerprint: "f_0000000000000002",
  reason: "Until the checkout redesign lands.",
  scope: "branch",
  ref: "feat/checkout",
  accepts: { layer: "visual", code: "text-clipped" },
  expires_at: "2099-01-01T00:00:00Z",
});
const gone = waiver({ fingerprint: "f_0000000000000003", reason: "Nobody remembers why." });
const past = waiver({ fingerprint: "f_0000000000000004", reason: "Fixed properly.", active: false, revoked_at: "2026-09-18T08:00:00Z" });

async function screen(port: FakeQuality, roles?: Parameters<typeof mountProjectScreen>[1]["roles"], path = "/t/t/p/p/quality/waivers") {
  wrapper = await mountProjectScreen(WaiversView, { quality: port, locales: LOCALES, path, ...(roles ? { roles } : {}) });
  await flushPromises();
  return wrapper;
}

const rows = (w: VueWrapper) => w.findAll("[data-testid=waiver]");
const text = (w: VueWrapper, testid: string) => w.get(`[data-testid=${testid}]`).text();

describe("WaiversView", () => {
  it("lists every waiver, standing and past, with its reason, reach, author and expiry", async () => {
    const w = await screen(createFakeQuality({ waivers: [term, visual, gone, past] }));
    expect(rows(w)).toHaveLength(4);
    const first = rows(w)[0]!;
    expect(first.text()).toContain("“Login” is the German term our brand guide prescribes.");
    expect(first.text()).toContain("Everywhere in this project");
    expect(first.text()).toContain("Stands");
    const second = rows(w)[1]!;
    expect(second.text()).toContain("on feat/checkout only");
    expect(second.text()).toContain("expires");
    // Two carry a fingerprint no stored finding has any more; the table marks both.
    expect(w.findAll("[data-testid=waiver-unexamined]")).toHaveLength(2);
  });

  it("counts the standing and the past apart, and names the two that need a person", async () => {
    const w = await screen(createFakeQuality({ waivers: [term, visual, gone, past] }));
    expect(text(w, "waiver-totals")).toContain("3 standing");
    expect(text(w, "waiver-totals")).toContain("1 revoked or expired");
    expect(text(w, "waiver-unexamined-count")).toContain("1 accepts nothing that is still found");
  });

  it("filters by state, by the layer it accepts and by fingerprint, and keeps the totals about the project", async () => {
    const w = await screen(createFakeQuality({ waivers: [term, visual, gone, past] }), undefined, "/t/t/p/p/quality/waivers?state=active");
    expect(rows(w)).toHaveLength(3);
    expect(text(w, "waiver-matched")).toBe("3 waivers match.");
    // The counts above the table are the project's, not the query's.
    expect(text(w, "waiver-totals")).toContain("1 revoked or expired");

    await w.get("[data-testid=waiver-layer]").setValue("visual");
    await flushPromises();
    expect(rows(w)).toHaveLength(1);
    expect(rows(w)[0]?.text()).toContain("Until the checkout redesign lands.");
  });

  it("says no waiver matches rather than showing the empty-project sentence", async () => {
    const w = await screen(createFakeQuality({ waivers: [term] }), undefined, "/t/t/p/p/quality/waivers?fingerprint=nothing");
    expect(text(w, "waivers-no-matches")).toBe("No waiver matches these filters.");
    expect(w.find("[data-testid=no-waivers]").exists()).toBe(false);
  });

  it("says the project accepts nothing yet when it accepts nothing yet", async () => {
    const w = await screen(createFakeQuality());
    expect(text(w, "no-waivers")).toBe("This project accepts nothing yet.");
  });

  it("revokes a waiver without deleting it: it stays, marked, with its reason", async () => {
    const port = createFakeQuality({ waivers: [term] });
    const w = await screen(port);
    await rows(w)[0]!.get("button").trigger("click");
    await flushPromises();
    expect(text(w, "waivers-status")).toContain("term_forbidden");
    expect(rows(w)).toHaveLength(1);
    expect(rows(w)[0]?.text()).toContain("Revoked");
    expect(rows(w)[0]?.text()).toContain("“Login” is the German term our brand guide prescribes.");
    expect(text(w, "waiver-totals")).toContain("0 standing");
  });

  it("is read-only without the developer role: the list is there, the revoke is not", async () => {
    const w = await screen(createFakeQuality({ waivers: [term] }), ["translator"]);
    expect(text(w, "waivers-read-only")).toContain("needs the developer role");
    expect(rows(w)[0]?.find("button").exists()).toBe(false);
  });
});
