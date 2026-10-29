import { describe, expect, it } from "vitest";
import { grantFor } from "../session/permissions";
import {
  canCancelExport,
  canCancelImport,
  canImportAny,
  canOverwrite,
  detectFormat,
  formatFromName,
  importableFormats,
  importProgress,
  localeBlock,
  localeFromName,
  poLanguage,
  pollUntil,
  sha256Hex,
  sniffFormat,
  xliffLanguages,
} from "./integration";

const XLIFF2 = `<?xml version="1.0" encoding="UTF-8"?>
<xliff xmlns="urn:oasis:names:tc:xliff:document:2.0" version="2.1" srcLang="en" trgLang="de"><file id="f1">`;
const XLIFF12 = `<xliff version="1.2"><file source-language="en-US" target-language="pt-BR" datatype="plaintext">`;
const PO = `# German translations
msgid ""
msgstr ""
"Content-Type: text/plain; charset=UTF-8\\n"
"Language: pt_BR\\n"

msgid "Add to cart"
msgstr "In den Warenkorb"
`;

describe("recognizing a file", () => {
  it.each([
    ["shop.de.xlf", "xliff"],
    ["shop.XLIFF", "xliff"],
    ["de.json", "json"],
    ["messages.po", "po"],
    ["template.pot", "po"],
    ["memory.tmx", "tmx"],
    ["glossary.tbx", "tbx"],
    ["notes.txt", undefined],
  ])("%s by its name → %s", (name, f) => {
    expect(formatFromName(name)).toBe(f);
  });

  it("sniffs the content when the name doesn't say", () => {
    expect(sniffFormat(XLIFF2)).toBe("xliff");
    expect(sniffFormat('﻿  {"a": "b"}')).toBe("json");
    expect(sniffFormat(PO)).toBe("po");
    expect(sniffFormat('<?xml version="1.0"?><tmx version="1.4">')).toBe("tmx");
    expect(sniffFormat('<?xml version="1.0"?><martif type="TBX-Basic">')).toBe("tbx");
    expect(sniffFormat("hello")).toBeUndefined();
    expect(detectFormat("export.txt", XLIFF2)).toBe("xliff");
    expect(detectFormat("de.json", XLIFF2)).toBe("json");
  });

  it("reads XLIFF 2 and 1.2 languages", () => {
    expect(xliffLanguages(XLIFF2)).toEqual({ source: "en", target: "de" });
    expect(xliffLanguages(XLIFF12)).toEqual({ source: "en-US", target: "pt-BR" });
    expect(xliffLanguages('<xliff version="2.1" srcLang="en">')).toEqual({ source: "en" });
  });

  it("reads a PO file's Language header as BCP 47", () => {
    expect(poLanguage(PO)).toBe("pt-BR");
    expect(poLanguage('"Language: sr@latin\\n"')).toBe("sr");
    expect(poLanguage('msgid "x"')).toBeUndefined();
  });

  it("finds the project locale a file name names, the longest first", () => {
    const locales = ["en", "de", "de-AT", "pt-BR"];
    expect(localeFromName("de.json", locales)).toBe("de");
    expect(localeFromName("app.de-AT.json", locales)).toBe("de-AT");
    expect(localeFromName("messages_pt_BR.json", locales)).toBe("pt-BR");
    expect(localeFromName("strings.json", locales)).toBeUndefined();
  });
});

describe("who may import what", () => {
  const translator = grantFor({ roles: ["translator"], locales: ["de"] });
  const developer = grantFor({ roles: ["developer"], locales: [] });

  it("mirrors locale-scoped integration.import", () => {
    expect(canImportAny(translator)).toBe(true);
    expect(localeBlock(translator, "de", "en")).toBeUndefined();
    expect(localeBlock(translator, "de-AT", "en")).toBeUndefined();
    expect(localeBlock(translator, "fr", "en")).toBe("scope");
    expect(localeBlock(translator, "en", "en")).toBe("source");
    expect(localeBlock(developer, "en", "en")).toBeUndefined();
    expect(localeBlock(developer, "fr", "en")).toBeUndefined();
  });

  it("keeps TMX, TBX and overwrite to managers", () => {
    expect(importableFormats(translator)).toEqual(["xliff", "json", "po"]);
    expect(importableFormats(developer)).toEqual(["xliff", "json", "po", "tmx", "tbx"]);
    expect(canOverwrite(translator)).toBe(false);
    expect(canOverwrite(developer)).toBe(true);
    expect(canImportAny(grantFor(undefined))).toBe(false);
  });
});

describe("jobs", () => {
  const base = { cancel_requested: false, processed_items: 0, total_items: 0 };
  it("cancels imports until they end, exports only while queued", () => {
    expect(canCancelImport({ ...base, state: "running" } as never)).toBe(true);
    expect(canCancelImport({ ...base, state: "running", cancel_requested: true } as never)).toBe(false);
    expect(canCancelImport({ ...base, state: "succeeded" } as never)).toBe(false);
    expect(canCancelExport({ ...base, state: "queued" } as never)).toBe(true);
    expect(canCancelExport({ ...base, state: "running" } as never)).toBe(false);
  });

  it("knows progress only once the file's size in items is known", () => {
    expect(importProgress({ state: "running", processed_items: 0, total_items: 0 })).toBeUndefined();
    expect(importProgress({ state: "running", processed_items: 250, total_items: 1000 })).toBe(0.25);
    expect(importProgress({ state: "succeeded", processed_items: 0, total_items: 0 })).toBe(1);
  });

  it("polls until done and can be stopped", async () => {
    let n = 0;
    const seen: number[] = [];
    await expect(pollUntil(async () => ++n, (v) => v === 3, (v) => seen.push(v), 0)).resolves.toBe(3);
    expect(seen).toEqual([1, 2, 3]);
    const ctrl = new AbortController();
    const p = pollUntil(async () => 0, () => false, () => ctrl.abort(), 10, ctrl.signal);
    await expect(p).rejects.toThrow("Stopped.");
  });

  it("hashes files like the server (SHA-256, hex)", async () => {
    expect(await sha256Hex(new Blob(["abc"]))).toBe("ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");
  });
});
