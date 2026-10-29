/**
 * Glossa's own runtime cases (`messageformat/testdata/glossa/runtime-format.json`):
 * precompiled data model + locale + params → the reference formatter's output.
 */
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { paramValues, testdataDir } from "@klarlabs-studio/glossa-messageformat/testing";
import type { SuiteParam } from "@klarlabs-studio/glossa-messageformat/testing";
import { describe, expect, it } from "vitest";
import { format, formatToParts } from "./index.js";
import type { Message } from "./index.js";

interface FixtureTest {
  description: string;
  locale: string;
  message: Message;
  params?: SuiteParam[];
  bidiIsolation?: "default" | "none";
  exp: string;
  expParts?: unknown[];
  expErrors?: Array<{ type: string }>;
}

const fixture = JSON.parse(
  readFileSync(join(testdataDir, "glossa/runtime-format.json"), "utf8"),
) as {
  tests: FixtureTest[];
};

describe("glossa/runtime-format.json", () => {
  it.each(
    fixture.tests.map((t) => [`${t.description} ${JSON.stringify(t.params ?? [])}`, t] as const),
  )("%s", (_name, t) => {
    const errors: string[] = [];
    const opts = {
      bidiIsolation: t.bidiIsolation ?? "default",
      onError: (e: { type: string }) => errors.push(e.type),
    };
    const values = paramValues(t.params);
    expect(format(t.message, t.locale, values, opts)).toBe(t.exp);
    expect(errors).toEqual((t.expErrors ?? []).map((e) => e.type));
    if (t.expParts) expect(formatToParts(t.message, t.locale, values, opts)).toEqual(t.expParts);
  });
});
