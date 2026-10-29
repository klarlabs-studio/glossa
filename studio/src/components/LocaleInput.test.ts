import { mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";
import LocaleInput from "./LocaleInput.vue";

const mountInput = (modelValue: string) => mount(LocaleInput, { props: { id: "loc", label: "Locale", modelValue } });

describe("LocaleInput", () => {
  it("previews the canonical tag, its name and direction", () => {
    const w = mountInput("he_il");
    expect(w.get("#loc-msg").text()).toBe("Adds he-IL — Hebrew (Israel), written right to left.");
    expect(w.get("input").attributes("aria-invalid")).toBeUndefined();
  });

  it("explains an invalid code and marks the field", () => {
    const w = mountInput("de-x-internal");
    expect(w.get("#loc-msg").text()).toContain("private use");
    expect(w.get("#loc-msg").classes()).toContain("field-error");
    expect(w.get("input").attributes("aria-invalid")).toBe("true");
    expect(w.get("input").attributes("aria-describedby")).toBe("loc-msg");
  });

  it("shows the hint while empty", () => {
    expect(mountInput("").get("#loc-msg").text()).toContain("BCP 47");
  });
});
