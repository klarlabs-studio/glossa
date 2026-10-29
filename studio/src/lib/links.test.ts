import { describe, expect, it } from "vitest";
import { safeNext, tokenFromHash } from "./links";

describe("tokenFromHash", () => {
  it("reads the token from the fragment", () => {
    expect(tokenFromHash("#token=abc_DEF-123")).toBe("abc_DEF-123");
    expect(tokenFromHash("token=x")).toBe("x");
    expect(tokenFromHash("")).toBeUndefined();
    expect(tokenFromHash("#token=" + "a".repeat(300))).toBeUndefined();
  });
});

describe("safeNext", () => {
  it.each([
    ["/t/1/p/2/translate?locale=de", "/t/1/p/2/translate?locale=de"],
    ["//evil.example", undefined],
    ["/\\evil.example", undefined],
    ["https://evil.example", undefined],
    [undefined, undefined],
    [["/a"], undefined],
  ])("%j → %j", (input, want) => {
    expect(safeNext(input)).toBe(want);
  });
});
