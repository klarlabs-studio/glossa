import { describe, expect, it } from "vitest";
import { formatUserCode, isUserCode, normalizeUserCode } from "./device-code";

describe("device user codes", () => {
  it("normalizes case, hyphens and whitespace away", () => {
    expect(normalizeUserCode("bcdf-ghjk")).toBe("BCDFGHJK");
    expect(normalizeUserCode(" BCDF GHJK ")).toBe("BCDFGHJK");
    expect(normalizeUserCode("bc-df--gh jk")).toBe("BCDFGHJK");
    expect(normalizeUserCode("")).toBe("");
  });

  it("formats as XXXX-XXXX while typing", () => {
    expect(formatUserCode("b")).toBe("B");
    expect(formatUserCode("bcdf")).toBe("BCDF");
    expect(formatUserCode("bcdfg")).toBe("BCDF-G");
    expect(formatUserCode("BCDF-")).toBe("BCDF");
    expect(formatUserCode("bcdfghjk")).toBe("BCDF-GHJK");
    expect(formatUserCode("BCDF-GHJK")).toBe("BCDF-GHJK");
  });

  it("drops anything past the eighth character", () => {
    expect(formatUserCode("BCDFGHJKLM")).toBe("BCDF-GHJK");
  });

  it("accepts only eight characters of the consonant alphabet", () => {
    expect(isUserCode("BCDF-GHJK")).toBe(true);
    expect(isUserCode("bcdfghjk")).toBe(true);
    expect(isUserCode("WXZB-CDFG")).toBe(true);
    // Vowels and digits are never issued.
    expect(isUserCode("ABCD-EFGH")).toBe(false);
    expect(isUserCode("BCDF-GHJ1")).toBe(false);
    // Too short, too long.
    expect(isUserCode("BCDF-GHJ")).toBe(false);
    expect(isUserCode("BCDF-GHJKL")).toBe(false);
  });
});
