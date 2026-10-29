import { describe, expect, it } from "vitest";
import { diffWords, tokenize } from "./diff";

describe("tokenize", () => {
  it("keeps placeholders' braces and punctuation apart", () => {
    expect(tokenize("Hi {name}!")).toEqual(["Hi", " ", "{", "name", "}", "!"]);
  });
  it("returns nothing for empty text", () => {
    expect(tokenize("")).toEqual([]);
  });
});

describe("diffWords", () => {
  it("reports equal text as one segment", () => {
    expect(diffWords("Pay now", "Pay now")).toEqual([{ op: "equal", text: "Pay now" }]);
  });

  it("marks a replaced placeholder", () => {
    expect(diffWords("You have {count} files", "You have {total} files")).toEqual([
      { op: "equal", text: "You have {" },
      { op: "delete", text: "count" },
      { op: "insert", text: "total" },
      { op: "equal", text: "} files" },
    ]);
  });

  it("handles pure insertions and deletions", () => {
    expect(diffWords("", "New")).toEqual([{ op: "insert", text: "New" }]);
    expect(diffWords("Old", "")).toEqual([{ op: "delete", text: "Old" }]);
    expect(diffWords("Save", "Save draft")).toEqual([
      { op: "equal", text: "Save" },
      { op: "insert", text: " draft" },
    ]);
  });

  it("reassembles both sides", () => {
    const before = "Checkout: pay {amount} today.";
    const after = "Pay {amount} now, securely.";
    const segs = diffWords(before, after);
    expect(segs.filter((s) => s.op !== "insert").map((s) => s.text).join("")).toBe(before);
    expect(segs.filter((s) => s.op !== "delete").map((s) => s.text).join("")).toBe(after);
  });
});
