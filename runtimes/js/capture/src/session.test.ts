import { afterEach, describe, expect, it } from "vitest";
import { createRuntime } from "@klarlabs-studio/glossa-runtime";
import type { Runtime } from "@klarlabs-studio/glossa-runtime";
import { createApp, defineComponent, h, nextTick } from "vue";
import { GlossaText, createGlossa as createVueGlossa, useGlossa as useVueGlossa } from "@klarlabs-studio/glossa-vue";
import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { GlossaProvider, T, createGlossa as createReactGlossa, useGlossa } from "@klarlabs-studio/glossa-react";
import "@klarlabs-studio/glossa-elements";

import { END, START, hasMarkers, strip } from "./markers.js";
import { collectRegions } from "./regions.js";
import { digest, startCapture, stripMarkers } from "./session.js";
import type { CaptureSession } from "./session.js";
import { fixture } from "./testing/release.js";
import { schemaErrors } from "./testing/schema.js";

const runtime = (locales = "de"): Runtime =>
  createRuntime({ bundled: fixture, locales, environment: "preview", storage: null });

let session: CaptureSession | undefined;

afterEach(() => {
  session?.stop();
  session = undefined;
  document.body.innerHTML = "";
});

const settle = async () => {
  for (let i = 0; i < 5; i++) await new Promise((r) => setTimeout(r, 0));
};

describe("startCapture", () => {
  it("marks t() strings with their index in the render log; equal renders share an entry", () => {
    const rt = runtime();
    session = startCapture(rt);
    const a = rt.t("profile.save");
    const b = rt.t("settings.save");
    const c = rt.t("profile.save");
    const d = rt.t("cart.total", { amount: 12 });
    const e = rt.t("cart.total", { amount: 13 });
    expect([a, b, c, d, e].map(strip)).toEqual([
      "Speichern",
      "Speichern",
      "Speichern",
      "Summe: 12",
      "Summe: 13",
    ]);
    expect(a).toMatch(new RegExp(`^${START}.+Speichern${END}$`, "u"));
    expect(a).toBe(c);
    expect(a).not.toBe(b);
    expect(session.renders).toEqual([
      { id: "profile.save", locale: "de", digest: digest(undefined) },
      { id: "settings.save", locale: "de", digest: digest(undefined) },
      { id: "cart.total", locale: "de", digest: digest({ amount: 12 }) },
      { id: "cart.total", locale: "de", digest: digest({ amount: 13 }) },
    ]);
    expect(JSON.stringify(session.renders)).not.toContain("12");
  });

  it("digests values stably, including BigInts, and survives unserializable ones", () => {
    expect(digest({ a: 1 })).toBe(digest({ a: 1 }));
    expect(digest({ a: 1 })).not.toBe(digest({ a: 2 }));
    expect(digest({ a: 1n })).toMatch(/^[0-9a-f]{8}$/);
    const cyclic: Record<string, unknown> = {};
    cyclic.self = cyclic;
    expect(digest(cyclic)).toBe("!");
  });

  it("shares one log across runtimes (islands), and stops on all of them", () => {
    const de = runtime("de");
    const ar = runtime("ar");
    session = startCapture([de, ar]);
    de.t("profile.save");
    ar.t("profile.save");
    expect(session.renders.map((r) => [r.id, r.locale])).toEqual([
      ["profile.save", "de"],
      ["profile.save", "ar"],
    ]);
    session.stop();
    expect([de.hooked, ar.hooked]).toEqual([false, false]);
    expect(hasMarkers(de.t("profile.save"))).toBe(false);
  });

  it("adds runtimes created after it started to the same log; not after stop", () => {
    const de = runtime("de");
    session = startCapture([]);
    session.add(de);
    const ar = runtime("ar");
    session.add(ar);
    de.t("profile.save");
    ar.t("profile.save");
    expect(session.renders.map((r) => r.locale)).toEqual(["de", "ar"]);
    session.stop();
    const late = runtime("de");
    session.add(late);
    expect([de.hooked, ar.hooked, late.hooked]).toEqual([false, false, false]);
  });
});

describe("collectRegions (structure; geometry is in e2e/)", () => {
  it("finds marked text, attributes and component hosts, valid against captures.v1", async () => {
    const rt = runtime();
    session = startCapture(rt);
    document.body.innerHTML = `
      <button id="a"></button><button id="b"></button><button id="c"></button>
      <input id="search"><img id="logo"><input id="submit" type="submit">
      <p id="greeting"></p>
      <glossa-provider><glossa-text key="cart.checkout">Zur Kasse</glossa-text></glossa-provider>`;
    document.querySelector("glossa-provider")!.runtime = rt;
    document.getElementById("a")!.textContent = rt.t("profile.save");
    document.getElementById("b")!.textContent = rt.t("settings.save");
    document.getElementById("c")!.textContent = rt.t("draft.save");
    const search = document.getElementById("search") as HTMLInputElement;
    search.placeholder = rt.t("search.placeholder");
    search.title = rt.t("search.hint");
    (document.getElementById("logo") as HTMLImageElement).alt = rt.t("logo.alt");
    (document.getElementById("submit") as HTMLInputElement).value = rt.t("form.submit");
    document.getElementById("greeting")!.textContent = rt.t("greeting", {
      name: rt.t("user.name"),
    });
    await settle();

    const capture = session.collect();
    expect(schemaErrors(capture)).toEqual([]);
    const keyOf = (i?: number) => capture.renders.find((r) => r.index === i)?.key;
    expect(
      capture.regions.map((r) => [r.kind, r.key ?? keyOf(r.index), r.attribute ?? null]),
    ).toEqual([
      ["text", "profile.save", null],
      ["text", "settings.save", null],
      ["text", "draft.save", null],
      ["attribute", "search.placeholder", "placeholder"],
      ["attribute", "search.hint", "title"],
      ["attribute", "logo.alt", "alt"],
      ["attribute", "form.submit", "value"],
      ["text", "user.name", null],
      ["text", "greeting", null],
      ["element", "cart.checkout", null],
    ]);
    // jsdom has no layout: everything is zero-size, so nothing counts as visible.
    expect(capture.regions.every((r) => !r.visible)).toBe(true);
    expect(capture.renders.every((r) => r.locale === "de")).toBe(true);
  });

  it("skips what captures.v1 can't name: IDs that aren't keys, indexes not in the log", () => {
    const rt = runtime();
    session = startCapture(rt);
    document.body.innerHTML = `<p id="p"></p><span data-glossa-id="Not A Key"></span>`;
    const p = document.getElementById("p")!;
    p.textContent = `${rt.t("Hello world")} ${rt.t("profile.save")}`;
    const capture = session.collect();
    expect(schemaErrors(capture)).toEqual([]);
    expect(capture.renders.map((r) => r.key)).toEqual(["profile.save"]);
    expect(capture.regions).toHaveLength(1);
    expect(collectRegions([], document).regions).toEqual([]);
  });

  it("reports an inline default's locale as und", () => {
    const rt = runtime();
    session = startCapture(rt);
    document.body.innerHTML = `<p id="p"></p>`;
    document.getElementById("p")!.textContent = rt.t("no.such.key", {}, { default: "Standard" });
    const capture = session.collect();
    expect(capture.renders).toEqual([{ index: 0, key: "no.such.key", locale: "und" }]);
    expect(schemaErrors(capture)).toEqual([]);
  });
});

describe("stop", () => {
  it("removes the hook and strips markers from text, attributes, values and shadow roots", () => {
    const rt = runtime();
    session = startCapture(rt);
    document.body.innerHTML = `<p id="p"></p><input id="i"><div id="host"></div>`;
    document.getElementById("p")!.textContent = rt.t("profile.save");
    const input = document.getElementById("i") as HTMLInputElement;
    input.placeholder = rt.t("search.placeholder");
    input.value = rt.t("search.hint");
    const shadow = document.getElementById("host")!.attachShadow({ mode: "open" });
    shadow.innerHTML = `<b title="${rt.t("logo.alt")}">${rt.t("cart.checkout")}</b>`;

    session.stop();
    expect(rt.hooked).toBe(false);
    expect(document.getElementById("p")!.textContent).toBe("Speichern");
    expect(input.placeholder).toBe("Suchen …");
    expect(input.value).toBe("Stichwort eingeben");
    expect(shadow.innerHTML).toBe(`<b title="Glossa-Logo">Zur Kasse</b>`);
    expect(hasMarkers(document.body.innerHTML)).toBe(false);
    session.stop(); // idempotent
  });

  it("stripMarkers leaves unmarked content alone", () => {
    document.body.innerHTML = `<p title="x">Hallo</p>`;
    stripMarkers(document.body);
    expect(document.body.innerHTML).toBe(`<p title="x">Hallo</p>`);
  });
});

describe("with the framework components", () => {
  it("Vue: $t() in text and attributes is marked, <GlossaText> hosts carry the ID", async () => {
    const glossa = createVueGlossa({ runtime: runtime() });
    const App = defineComponent(() => {
      const { t } = useVueGlossa();
      return () =>
        h("form", [
          h("input", { placeholder: t("search.placeholder") }),
          h("button", t("profile.save")),
          h(GlossaText, { id: "cart.checkout" }),
        ]);
    });
    const el = document.createElement("div");
    document.body.append(el);
    const app = createApp(App).use(glossa);
    app.mount(el);
    session = startCapture(glossa.runtime);
    await nextTick();
    const capture = session.collect();
    expect(schemaErrors(capture)).toEqual([]);
    const keyOf = (i?: number) => capture.renders.find((r) => r.index === i)?.key;
    expect(capture.regions.map((r) => [r.kind, r.key ?? keyOf(r.index)])).toEqual([
      ["attribute", "search.placeholder"],
      ["text", "profile.save"],
      ["element", "cart.checkout"],
    ]);
    session.stop();
    await nextTick();
    expect(el.innerHTML).toBe(
      `<form><input placeholder="Suchen …"><button>Speichern</button>Zur Kasse</form>`,
    );
    app.unmount();
  });

  it("React: t() and <T> hosts", async () => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    const glossa = createReactGlossa({ runtime: runtime() });
    const App = () => {
      const { t } = useGlossa();
      return createElement(
        "form",
        null,
        createElement("button", { title: t("search.hint") }, t("profile.save")),
        createElement(T, { id: "cart.checkout" }),
      );
    };
    const el = document.createElement("div");
    document.body.append(el);
    const root = createRoot(el);
    await act(async () => root.render(createElement(GlossaProvider, { glossa }, createElement(App))));
    await act(async () => {
      session = startCapture(glossa.runtime);
    });
    const capture = session!.collect();
    expect(schemaErrors(capture)).toEqual([]);
    const keyOf = (i?: number) => capture.renders.find((r) => r.index === i)?.key;
    expect(capture.regions.map((r) => [r.kind, r.key ?? keyOf(r.index)])).toEqual([
      ["attribute", "search.hint"],
      ["text", "profile.save"],
      ["element", "cart.checkout"],
    ]);
    await act(async () => session!.stop());
    expect(el.innerHTML).toBe(`<form><button title="Stichwort eingeben">Speichern</button>Zur Kasse</form>`);
    root.unmount();
  });
});
