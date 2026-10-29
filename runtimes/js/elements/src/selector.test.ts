import { afterEach, describe, expect, it } from "vitest";

import "./index.js";
import type { GlossaProvider } from "./glossa-provider.js";
import type { GlossaSelector } from "./glossa-selector.js";
import { autonym, pickBestMatch } from "./glossa-selector.js";
import { release, text } from "./testing/release.js";

const r = release("rel_1", 1, {
  de: { hi: text("Hallo") },
  en: { hi: text("Hello") },
  "pt-BR": { hi: text("Olá") },
});

async function settle(): Promise<void> {
  for (let i = 0; i < 5; i++) await new Promise((res) => setTimeout(res, 0));
}

async function mount(selector: string, browser = "de-DE") {
  const container = document.createElement("div");
  container.innerHTML = `<glossa-provider locale="de">${selector}<glossa-text key="hi">…</glossa-text></glossa-provider>`;
  const provider = container.querySelector("glossa-provider") as GlossaProvider;
  provider.bundled = r;
  provider.options = { storage: null };
  const el = container.querySelector("glossa-selector") as GlossaSelector;
  el.detectImpl = () => browser;
  const events: CustomEvent[] = [];
  el.addEventListener("glossa-locale-change", (e) => events.push(e as CustomEvent));
  document.body.append(container);
  await settle();
  return { provider, el, events, select: el.shadowRoot!.querySelector("select") };
}

const options = (select: HTMLSelectElement | null) =>
  Array.from(select?.options ?? []).map((o) => [o.value, o.text, o.selected]);

afterEach(() => {
  document.body.innerHTML = "";
});

describe("<glossa-selector>", () => {
  it("offers the release's locales, each named in its own language", async () => {
    const { select } = await mount(`<glossa-selector></glossa-selector>`);
    expect(options(select)).toEqual([
      ["de", "Deutsch", true],
      ["en", "English", false],
      ["pt-BR", autonym("pt-BR"), false],
    ]);
    expect(select!.getAttribute("aria-label")).toBe("Language");
    expect(select!.options[0]!.lang).toBe("de");
  });

  it("keeps v0.3's locales and labels attributes", async () => {
    const { select } = await mount(
      `<glossa-selector locales="en,de" labels="English,Deutsch" label="Sprache"></glossa-selector>`,
    );
    expect(options(select)).toEqual([
      ["en", "English", false],
      ["de", "Deutsch", true],
    ]);
    expect(select!.getAttribute("aria-label")).toBe("Sprache");
  });

  it("emits glossa-locale-change on a pick and switches the provider", async () => {
    const { provider, events, select } = await mount(`<glossa-selector></glossa-selector>`);
    select!.value = "en";
    select!.dispatchEvent(new Event("change"));
    expect(events.map((e) => e.detail)).toEqual([{ locale: "en", source: "manual" }]);
    await settle();
    expect(provider.runtime!.locale).toBe("en");
    expect(provider.lang).toBe("en");
  });

  it("leaves the switch to the app when a listener prevents the default", async () => {
    const { provider, el, select } = await mount(`<glossa-selector></glossa-selector>`);
    el.addEventListener("glossa-locale-change", (e) => e.preventDefault());
    select!.value = "en";
    select!.dispatchEvent(new Event("change"));
    await settle();
    expect(provider.runtime!.locale).toBe("de");
  });

  it("suggests the browser language once with source=auto, without switching", async () => {
    const { provider, el, events } = await mount(
      `<glossa-selector auto-detect></glossa-selector>`,
      "en-GB",
    );
    el.maybeAutoDetect();
    expect(events.map((e) => e.detail)).toEqual([{ locale: "en", source: "auto" }]);
    await settle();
    expect(provider.runtime!.locale).toBe("de");
  });

  it("doesn't suggest the current locale or one that isn't offered", async () => {
    for (const browser of ["de-DE", "fr-FR"]) {
      const { events } = await mount(`<glossa-selector auto-detect></glossa-selector>`, browser);
      expect(events).toHaveLength(0);
      document.body.innerHTML = "";
    }
  });

  it("shows the current locale read-only when no locales are known", async () => {
    const container = document.createElement("div");
    container.innerHTML = `<glossa-provider locale="de"><glossa-selector label="Sprache"></glossa-selector></glossa-provider>`;
    (container.firstElementChild as GlossaProvider).options = { storage: null };
    document.body.append(container);
    await settle();
    const el = container.querySelector("glossa-selector")!;
    expect(el.shadowRoot!.querySelector("select")).toBeNull();
    expect(el.shadowRoot!.querySelector("span")!.getAttribute("aria-label")).toBe("Sprache");
  });
});

describe("pickBestMatch", () => {
  it("prefers an exact match, then the primary language", () => {
    expect(pickBestMatch("pt-BR", ["pt", "pt-BR"])).toBe("pt-BR");
    expect(pickBestMatch("en-GB", ["de", "en"])).toBe("en");
    expect(pickBestMatch("fr", ["de", "en"])).toBeUndefined();
  });
});
