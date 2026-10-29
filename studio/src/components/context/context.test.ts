import { flushPromises, mount } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";
import { computed, ref } from "vue";
import { CONTEXT } from "../../api/context";
import type { Message } from "../../api/schemas";
import { capture, createFakeContext, region, usage, type FakeContext } from "../../test/fake-context";
import { locale } from "../../test/project";
import ContextFilters from "./ContextFilters.vue";
import { indexOf, useContextFilter, type ContextFilterState } from "./useContextFilter";
import WhereItAppears from "./WhereItAppears.vue";

const NOW = "2026-09-19T08:00:00Z";
const message: Message = {
  id: "checkout_pay",
  key: "checkout.pay",
  namespace: "default",
  description: "",
  state: "active",
  source: { text: "Pay", syntax: "mf1", model: { type: "message" }, arguments: [], markup: [] },
  source_revision: 1,
  created_at: NOW,
  updated_at: NOW,
};
const de = locale("de");
const en = locale("en", true);

let wrappers: Array<{ unmount(): void }> = [];
afterEach(() => {
  for (const w of wrappers) w.unmount();
  wrappers = [];
});

function pane(port: FakeContext) {
  const w = mount(WhereItAppears, {
    props: { tenant: "t", projectId: "p", message, source: en, target: de, applications: [{ id: "a1", slug: "web", name: "Web", platform: "web", created_at: NOW, updated_at: NOW }] },
    attachTo: document.body,
    global: { provide: { [CONTEXT as symbol]: port } },
  });
  wrappers.push(w);
  return w;
}

describe("WhereItAppears", () => {
  it("groups usages application → route → component with file:line, unlinked while no repository is connected", async () => {
    const port = createFakeContext({
      usages: [
        usage({ key: "checkout.pay", route: "/checkout/payment", component: "PaymentFooter", file: "src/checkout/PaymentFooter.vue", line: 42 }),
        usage({ key: "checkout.pay", route: "/checkout/payment", component: "PrimaryButton", file: "src/ui/PrimaryButton.vue", line: 7, kind: "component" }),
      ],
      captures: [capture("c1", ["checkout.pay"])],
      active: [{ id: "checkout_pay", key: "checkout.pay" }],
    });
    const w = pane(port);
    await flushPromises();
    const usages = w.get("[data-testid=where-usages]");
    expect(usages.text()).toContain("Web");
    expect(usages.text()).toContain("/checkout/payment");
    expect(usages.text()).toContain("PaymentFooter");
    expect(w.findAll("[data-testid=usage]").map((u) => u.text())).toEqual([
      expect.stringContaining("src/checkout/PaymentFooter.vue:42"),
      expect.stringContaining("src/ui/PrimaryButton.vue:7"),
    ]);
    // A repository link needs a Git connection (RFC 0004 §6), which nothing provides yet.
    expect(usages.findAll("a")).toHaveLength(0);
    expect(usages.text()).toContain("t()");
    expect(usages.text()).toContain("<GlossaText>");
  });

  it("crops the screenshot around the message and opens a full-page lightbox", async () => {
    const port = createFakeContext({
      usages: [usage({ key: "checkout.pay" })],
      captures: [capture("c1", ["checkout.pay"], { regions: [region({ box: { x: 600, y: 1200, width: 90, height: 24 } })] })],
      active: [{ id: "checkout_pay", key: "checkout.pay" }],
    });
    const w = pane(port);
    await flushPromises();
    const shot = w.get("[data-testid=capture-shot]");
    // The crop is a window on the page, so the image is wider than the box that shows it.
    expect(shot.get("img").attributes("src")).toBe("/v1/tenants/t/projects/p/captures/c1/image");
    expect(shot.get("img").attributes("alt")).toContain("outlined");
    expect(Number.parseFloat(shot.get("img").attributes("style")!.match(/width:\s*([\d.]+)%/)![1]!)).toBeGreaterThan(100);
    expect(shot.findAll("[data-testid=capture-region]")).toHaveLength(1);
    expect(w.find("dialog[open]").exists()).toBe(false);

    await w.findAll("button").find((b) => b.text() === "Full screenshot")!.trigger("click");
    await flushPromises();
    const d = w.get("dialog[open]");
    expect(d.text()).toContain("/checkout");
    // The lightbox shows the whole page: the image fills the box exactly.
    expect(d.get("[data-testid=capture-shot] img").attributes("style")).toContain("width: 100.0000%");
  });

  it("toggles between the locales a screen was captured in", async () => {
    const port = createFakeContext({
      usages: [usage({ key: "checkout.pay" })],
      captures: [capture("c-en", ["checkout.pay"], { locale: "en" }), capture("c-de", ["checkout.pay"], { locale: "de" })],
      active: [{ id: "checkout_pay", key: "checkout.pay" }],
    });
    const w = pane(port);
    await flushPromises();
    // The target locale's capture leads where there is one.
    const select = w.get("[data-testid=where-locale]");
    expect((select.element as HTMLSelectElement).value).toBe("de");
    expect(w.get("[data-testid=capture-shot] img").attributes("src")).toContain("/captures/c-de/");
    await select.setValue("en");
    await flushPromises();
    expect(w.get("[data-testid=capture-shot] img").attributes("src")).toContain("/captures/c-en/");
  });

  it("falls back to another locale's screenshot and says so", async () => {
    const port = createFakeContext({
      usages: [usage({ key: "checkout.pay" })],
      captures: [capture("c-en", ["checkout.pay"], { locale: "en" }), capture("c-cart", ["checkout.pay"], { locale: "de", route: "/cart" })],
      active: [{ id: "checkout_pay", key: "checkout.pay" }],
    });
    const w = pane(port);
    await flushPromises();
    expect(w.get("[data-testid=where-locale]")).toBeTruthy();
    expect(w.get("[data-testid=locale-fallback]").text()).toContain("No de screenshot of this screen yet; showing en.");
  });

  it("says a message is on a screen but not visible", async () => {
    const port = createFakeContext({
      usages: [usage({ key: "checkout.pay" })],
      captures: [capture("c1", ["checkout.pay"], { regions: [region({ visible: false, box: { x: 0, y: 0, width: 0, height: 0 } })] })],
      active: [{ id: "checkout_pay", key: "checkout.pay" }],
    });
    const w = pane(port);
    await flushPromises();
    expect(w.get("[data-testid=not-visible]").text()).toContain("zero-size or off-screen");
  });

  it("tells the three empty states apart", async () => {
    const nothingUploaded = createFakeContext({ builds: 0, active: [{ id: "checkout_pay", key: "checkout.pay" }] });
    let w = pane(nothingUploaded);
    await flushPromises();
    expect(w.get("[data-testid=where-no-data]").text()).toContain("No usage data yet.");
    expect(w.text()).toContain("glossa extract --upload");

    const unused = createFakeContext({ builds: 2, active: [{ id: "checkout_pay", key: "checkout.pay" }] });
    w = pane(unused);
    await flushPromises();
    expect(w.get("[data-testid=where-unused]").text()).toContain("No current build uses this message.");
    expect(w.find("[data-testid=where-no-data]").exists()).toBe(false);

    const notCaptured = createFakeContext({
      builds: 2,
      usages: [usage({ key: "checkout.pay" })],
      active: [{ id: "checkout_pay", key: "checkout.pay" }],
    });
    w = pane(notCaptured);
    await flushPromises();
    expect(w.get("[data-testid=where-not-captured]").text()).toContain("No screenshot shows this message yet.");
    expect(w.text()).toContain("glossa capture --upload");
  });

  it("reports what the server refuses", async () => {
    const port = createFakeContext({ active: [] });
    const w = pane(port);
    await flushPromises();
    expect(w.text()).toContain("No such message.");
  });
});

// ── the message list's filters ──────────────────────────────────────
function filtering(port: FakeContext, initial: ContextFilterState) {
  const filter = ref(initial);
  const state = useContextFilter({
    tenant: computed(() => "t"),
    projectId: computed(() => "p"),
    filter: computed(() => filter.value),
    port,
  });
  return { filter, ...state };
}

const blank: ContextFilterState = { route: "", component: "", file: "", only: "" };

describe("useContextFilter", () => {
  const port = () =>
    createFakeContext({
      builds: 1,
      usages: [
        usage({ key: "checkout.pay", message_id: "m1", route: "/checkout", component: "PaymentFooter", file: "src/Pay.vue" }),
        usage({ key: "cart.total", message_id: "m2", route: "/cart", component: "Cart", file: "src/Cart.vue" }),
      ],
      captures: [capture("c1", ["checkout.pay"])],
      active: [
        { id: "m1", key: "checkout.pay" },
        { id: "m2", key: "cart.total" },
        { id: "m3", key: "legal.terms" },
      ],
    });

  it("allows every message while nothing is filtered", async () => {
    const f = filtering(port(), blank);
    await flushPromises();
    expect(f.allowed.value).toBeUndefined();
  });

  it("resolves a route through the server", async () => {
    const f = filtering(port(), { ...blank, route: "/cart" });
    await flushPromises();
    expect([...f.allowed.value!]).toEqual(["m2"]);
  });

  it("combines filters", async () => {
    const f = filtering(port(), { ...blank, route: "/cart", component: "PaymentFooter" });
    await flushPromises();
    expect([...f.allowed.value!]).toEqual([]);
  });

  it("finds the unused messages", async () => {
    const f = filtering(port(), { ...blank, only: "unused" });
    await flushPromises();
    expect([...f.allowed.value!]).toEqual(["m3"]);
  });

  it("finds the used messages no screenshot shows, asking per message", async () => {
    const p = port();
    const f = filtering(p, { ...blank, only: "uncaptured" });
    await flushPromises();
    expect([...f.allowed.value!]).toEqual(["m2"]);
    expect(f.probe.value).toEqual({ done: 2, total: 2, capped: false });
    expect(p.calls.filter(([name]) => name === "messageCaptures")).toHaveLength(2);
  });

  it("loads the choices from the project's current usages", async () => {
    const p = port();
    const f = filtering(p, blank);
    await f.loadIndex();
    expect(f.index.value).toMatchObject({ routes: ["/cart", "/checkout"], components: ["Cart", "PaymentFooter"], files: ["src/Cart.vue", "src/Pay.vue"], hasData: true });
    // Once per project.
    await f.loadIndex();
    expect(p.calls.filter(([name]) => name === "usages")).toHaveLength(1);
  });

  it("knows an empty index from a project that never uploaded anything", async () => {
    expect(indexOf([], false).hasData).toBe(false);
    expect(indexOf([usage({ key: "a" })], true).used.size).toBe(1);
  });
});

describe("ContextFilters", () => {
  const index = indexOf([usage({ key: "checkout.pay", route: "/checkout", component: "Pay", file: "src/Pay.vue" })], true);

  it("stays closed until it is opened, then offers the project's routes, components and files", async () => {
    const w = mount(ContextFilters, {
      props: { filter: blank, index: undefined, loading: false, indexError: null, error: null, probe: undefined, matched: undefined },
      attachTo: document.body,
    });
    wrappers.push(w);
    expect(w.emitted("open")).toBeUndefined();
    await w.get("[data-testid=ctx-toggle]").trigger("click");
    expect(w.emitted("open")).toHaveLength(1);
    await w.setProps({ index });
    expect(w.get("#ws-route").text()).toContain("/checkout");
    expect(w.get("#ws-component").text()).toContain("Pay");
    expect(w.get("#ws-file").text()).toContain("src/Pay.vue");
    await w.get("[data-testid=ctx-coverage]").setValue("unused");
    expect(w.emitted("update:filter")!.at(-1)).toEqual([{ ...blank, only: "unused" }]);
  });

  it("opens itself for a bookmarked view and says when there is nothing to filter by", async () => {
    const w = mount(ContextFilters, {
      props: { filter: { ...blank, route: "/checkout" }, index: indexOf([], false), loading: false, indexError: null, error: null, probe: undefined, matched: 3 },
      attachTo: document.body,
    });
    wrappers.push(w);
    await flushPromises();
    expect(w.emitted("open")).toHaveLength(1);
    expect(w.get("[data-testid=ctx-no-data]").text()).toContain("No usage data yet");
  });
});
