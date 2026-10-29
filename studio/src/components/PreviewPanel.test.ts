import { flushPromises, mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";
import type { Argument } from "../api/schemas";
import { loadFormatter } from "../lib/preview";
import PreviewPanel from "./PreviewPanel.vue";

describe("PreviewPanel", async () => {
  const mf = await loadFormatter();
  const source = mf.parseMF2(".input {$n :number}\n.match $n\none {{{$n} file for {$name}}}\n* {{{$n} files for {$name}}}");
  const target = mf.parseMF2(".input {$n :number}\n.match $n\none {{{$n} Datei für {$name}}}\n* {{{$n} Dateien für {$name}}}");
  const args: Argument[] = [
    { name: "n", type: "number", function: "number", selector: { kind: "plural", keys: ["one", "*"] } },
    { name: "name", type: "string" },
  ];

  const mountPanel = (locale = "de") =>
    mount(PreviewPanel, {
      props: { args, source: { model: source, locale: "en", dir: "ltr" }, target: { model: target, locale, dir: locale === "ar" ? "rtl" : "ltr" } },
    });

  it("renders source and translation with sample values", async () => {
    const w = mountPanel();
    await flushPromises();
    expect(w.get("[data-testid=preview-source]").text()).toBe("3 files for Ada");
    expect(w.get("[data-testid=preview-target]").text()).toBe("3 Dateien für Ada");
    expect(w.get("[data-testid=preview-target]").attributes()).toMatchObject({ lang: "de", dir: "ltr" });
  });

  it("updates as sample values change", async () => {
    const w = mountPanel();
    await flushPromises();
    await w.get("#sample-name").setValue("Kim");
    expect(w.get("[data-testid=preview-target]").text()).toBe("3 Dateien für Kim");
  });

  it("offers every plural category of the target language", async () => {
    const w = mountPanel("ar");
    await flushPromises();
    const buttons = w.findAll(".plurals button").map((b) => b.text().split(" ")[0]);
    expect(buttons.sort()).toEqual(["few", "many", "one", "other", "two", "zero"]);
  });

  it("switches the sample with a plural button", async () => {
    const w = mountPanel();
    await flushPromises();
    const one = w.findAll(".plurals button").find((b) => b.text().startsWith("one"))!;
    await one.trigger("click");
    expect(one.attributes("aria-pressed")).toBe("true");
    expect(w.get("[data-testid=preview-target]").text()).toBe("1 Datei für Ada");
  });

  it("says when there's nothing to preview", async () => {
    const w = mount(PreviewPanel, {
      props: { args: [], source: { model: mf.parseMF2("Hi"), locale: "en", dir: "ltr" }, target: { model: undefined, locale: "de", dir: "ltr" }, notice: "saved text only" },
    });
    await flushPromises();
    expect(w.get("[data-testid=preview-target]").text()).toBe("Nothing to preview yet.");
    expect(w.text()).toContain("saved text only");
  });
});
