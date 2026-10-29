import { mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";
import type { MessageRow } from "../lib/workspace";
import MessageList from "./MessageList.vue";

const rows: MessageRow[] = Array.from({ length: 10_000 }, (_, i) => ({
  id: `m${i}`,
  key: `screen.item_${String(i).padStart(5, "0")}`,
  text: `Item ${i}`,
  namespace: "default",
  status: i % 3 === 0 ? "missing" : i % 3 === 1 ? "outdated" : "translated",
}));

const mountList = (active = 0) =>
  mount(MessageList, { props: { rows, active, label: "Messages", sourceLang: "en", sourceDir: "ltr" }, attachTo: document.body });

describe("MessageList", () => {
  it("renders only a window of ten thousand rows", () => {
    const w = mountList();
    const options = w.findAll("[role=option]");
    expect(options.length).toBeGreaterThan(5);
    expect(options.length).toBeLessThan(60);
    expect(w.get("[role=listbox]").attributes("aria-label")).toBe("Messages");
    expect(options[0]!.attributes("aria-setsize")).toBe("10000");
    w.unmount();
  });

  it("points aria-activedescendant at the active row and marks it selected", () => {
    const w = mountList(2);
    const box = w.get("[role=listbox]");
    expect(box.attributes("aria-activedescendant")).toBe("message-option-2");
    expect(w.get("#message-option-2").attributes("aria-selected")).toBe("true");
    expect(w.get("#message-option-0").attributes("aria-selected")).toBe("false");
    w.unmount();
  });

  it("shows missing and outdated badges, not translated ones", () => {
    const w = mountList();
    expect(w.get("#message-option-0").text()).toContain("Missing");
    expect(w.get("#message-option-1").text()).toContain("Outdated");
    expect(w.get("#message-option-2").text()).not.toMatch(/Missing|Outdated|Translated/);
    w.unmount();
  });

  it("moves with the listbox keys and selects on click", async () => {
    const w = mountList(5);
    const box = w.get("[role=listbox]");
    await box.trigger("keydown", { key: "ArrowDown" });
    await box.trigger("keydown", { key: "ArrowUp" });
    await box.trigger("keydown", { key: "Home" });
    await box.trigger("keydown", { key: "End" });
    await w.get("#message-option-3").trigger("click");
    expect(w.emitted("select")).toEqual([[6], [4], [0], [9999], [3]]);
    w.unmount();
  });
});
