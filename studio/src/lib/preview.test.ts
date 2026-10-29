import { describe, expect, it } from "vitest";
import { loadFormatter, parseMF2, renderPreview } from "./preview";

describe("preview", async () => {
  const mf = await loadFormatter();

  it("formats values per locale and marks them", () => {
    const parsed = parseMF2(mf, "Hallo {$name}, du hast {$count :number} Dateien.");
    if (!parsed.ok) throw new Error(parsed.error);
    const p = renderPreview(mf, parsed.model, "de", { name: "Ada", count: 1234.5 });
    expect(p.errors).toEqual([]);
    expect(p.segments).toEqual([
      { kind: "text", text: "Hallo " },
      { kind: "value", text: "Ada" },
      { kind: "text", text: ", du hast " },
      { kind: "value", text: "1.234,5" },
      { kind: "text", text: " Dateien." },
    ]);
  });

  it("selects the plural form of the target locale", () => {
    const parsed = parseMF2(mf, ".input {$n :number}\n.match $n\none {{eine Datei}}\n* {{{$n} Dateien}}");
    if (!parsed.ok) throw new Error(parsed.error);
    const text = (n: number) => renderPreview(mf, parsed.model, "de", { n }).segments.map((s) => s.text).join("");
    expect(text(1)).toBe("eine Datei");
    expect(text(3)).toBe("3 Dateien");
  });

  it("shows markup and reports missing values as fallbacks", () => {
    const parsed = parseMF2(mf, "{#b}Hi{/b} {$who}");
    if (!parsed.ok) throw new Error(parsed.error);
    const p = renderPreview(mf, parsed.model, "en", {});
    expect(p.segments.map((s) => s.kind)).toEqual(["markup", "text", "markup", "text", "fallback"]);
    expect(p.segments[4]).toEqual({ kind: "fallback", text: "{$who}" });
    expect(p.errors.length).toBeGreaterThan(0);
  });

  it("reports MF2 syntax errors", () => {
    const r = parseMF2(mf, "Hello {$name");
    expect(r.ok).toBe(false);
  });
});
