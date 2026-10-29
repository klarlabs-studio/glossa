import { parseMF2 } from "@klarlabs-studio/glossa-messageformat";
import { describe, expect, it } from "vitest";
import { createRuntime } from "./runtime.js";
import type { RuntimeError, RuntimeOptions } from "./runtime.js";
import { memoryStorage } from "./storage.js";
import { release, text } from "./testing/release.js";

const bundle = (m: Parameters<typeof release>[2]) => {
  const r = release("rel_1", 1, m);
  return { manifest: r.manifest, artifacts: r.parsed };
};
const bundled = bundle({ de: { known: text("Bekannt") } });
const make = (o: RuntimeOptions = {}) =>
  createRuntime({
    locales: "de",
    bundled,
    storage: memoryStorage(),
    refreshInterval: 0,
    bidiIsolation: "none",
    ...o,
  });

describe("inline default syntax (parseDefault)", () => {
  it("renders the default literally unless opted in", () => {
    expect(make().t("nope", { name: "Ada" }, { default: "Hallo {$name}" })).toBe("Hallo {$name}");
  });

  it("formats the default with the call's values when opted in", () => {
    const rt = make({ parseDefault: parseMF2 });
    expect(rt.t("nope", { name: "Ada" }, { default: "Hallo {$name}" })).toBe("Hallo Ada");
  });

  it("formats the default before any release is active", () => {
    const rt = createRuntime({ locales: "de", storage: null, parseDefault: parseMF2, bidiIsolation: "none" });
    expect(rt.t("nope", { n: 3 }, { default: "{$n} Stück" })).toBe("3 Stück");
  });

  it("leaves a catalog message alone", () => {
    const rt = make({ parseDefault: parseMF2 });
    expect(rt.t("known", {}, { default: "Anders" })).toBe("Bekannt");
  });

  it("renders a default that does not parse literally and reports it", () => {
    const errors: RuntimeError[] = [];
    const rt = make({ parseDefault: parseMF2, onError: (e) => errors.push(e) });
    expect(rt.t("nope", {}, { default: "Hallo {$" })).toBe("Hallo {$");
    expect(errors.some((e) => e.type === "format" && e.messageId === "nope")).toBe(true);
  });
});

describe("has(id)", () => {
  it("tells a missing message from one whose text equals its id", () => {
    const rt = createRuntime({
      locales: "de",
      storage: null,
      bundled: bundle({ de: { same: text("same"), known: text("Bekannt") } }),
    });
    expect(rt.has("known")).toBe(true);
    expect(rt.has("same")).toBe(true);
    expect(rt.t("same")).toBe("same");
    expect(rt.has("nope")).toBe(false);
  });

  it("is false before a release is active, and reports no error", () => {
    const errors: RuntimeError[] = [];
    const rt = createRuntime({ locales: "de", storage: null, onError: (e) => errors.push(e) });
    expect(rt.has("x")).toBe(false);
    expect(errors).toEqual([]);
  });
});
