import { readFileSync } from "node:fs";
import { join } from "node:path";
import { Ajv } from "ajv";
import { describe, expect, it } from "vitest";
import { buildTests, serialize } from "../scripts/runtime-format-cases.ts";
import type { FixtureTest } from "../scripts/runtime-format-cases.ts";
import { format, formatToParts, parseMF2 } from "./index.js";
import { messageSchemaPath, testdataDir } from "./testing/index.js";

const path = join(testdataDir, "glossa/runtime-format.json");
const committed = JSON.parse(readFileSync(path, "utf8")) as {
  generatedWith: { icu: string; cldr: string };
  tests: FixtureTest[];
};
const regenerated = buildTests({ parseMF2, format, formatToParts });
const sameLocaleData =
  committed.generatedWith.icu === process.versions.icu &&
  committed.generatedWith.cldr === process.versions.cldr;

describe("glossa/runtime-format.json", () => {
  it("carries messages valid against message.schema.json", () => {
    const validate = new Ajv({ strict: false }).compile(
      JSON.parse(readFileSync(messageSchemaPath, "utf8")) as object,
    );
    for (const t of committed.tests) expect(validate(t.message), t.description).toBe(true);
  });

  it("matches its case list (regenerate with `pnpm generate:runtime-format`)", () => {
    const strip = (tests: FixtureTest[]) =>
      tests.map(({ description, locale, src, syntax, message, params, bidiIsolation }) => ({
        description,
        locale,
        src,
        syntax,
        message,
        params,
        bidiIsolation,
      }));
    expect(strip(regenerated)).toEqual(strip(committed.tests));
  });

  // Formatted output depends on CLDR data, so only compare it with the same ICU.
  it.skipIf(!sameLocaleData)("matches the reference formatter's output", () => {
    const current = JSON.parse(serialize({ tests: regenerated })) as { tests: FixtureTest[] };
    expect(current.tests).toEqual(committed.tests);
  });
});
