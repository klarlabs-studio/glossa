/**
 * The vendored Unicode MessageFormat suite against the runtime interpreter.
 *
 * The runtime has no parser, so each `src` is parsed with the reference parser
 * (through @klarlabs-studio/glossa-messageformat, test-only) into the canonical data model,
 * which is exactly what release artifacts carry. The runtime then interprets
 * it and must produce `exp`, `expParts` and `expErrors`.
 *
 * Syntax and data model errors are compile-time: the server rejects those
 * messages before they reach an artifact, so here we only check that the
 * reference parser rejects them too.
 */
import { parseMF2 } from "@klarlabs-studio/glossa-messageformat";
import { caseKind, caseName, paramValues, suiteCases } from "@klarlabs-studio/glossa-messageformat/testing";
import type { SuiteCase } from "@klarlabs-studio/glossa-messageformat/testing";
import { describe, expect, it } from "vitest";
import { format, formatToParts } from "./index.js";
import type { MessageError } from "./index.js";
import { testFunctions } from "./testing/test-functions.js";

/**
 * Cases the runtime deliberately doesn't pass, keyed by `file: src`, with the
 * reason. Keep this list short and every entry justified.
 */
const skip = new Map<string, string>([]);

const cases = suiteCases();
const compileTime = cases.filter((tc) =>
  ["syntax-error", "data-model-error"].includes(caseKind(tc)),
);
const runtime = cases.filter((tc) => !compileTime.includes(tc));

const key = (tc: SuiteCase) => `${tc.file}: ${tc.src}`;

function run(tc: SuiteCase, toParts: boolean) {
  const errors: MessageError[] = [];
  const msg = parseMF2(tc.src);
  const opts = {
    bidiIsolation: tc.bidiIsolation ?? "default",
    functions: testFunctions,
    onError: (e: MessageError) => errors.push(e),
  } as const;
  const values = paramValues(tc.params);
  const out = toParts
    ? formatToParts(msg, tc.locale, values, opts)
    : format(msg, tc.locale, values, opts);
  return { out, errors: errors.map((e) => e.type) };
}

describe("Unicode MessageFormat suite (runtime)", () => {
  for (const file of [...new Set(runtime.map((tc) => tc.file))]) {
    describe(file, () => {
      for (const tc of runtime.filter((c) => c.file === file)) {
        const reason = skip.get(key(tc));
        const test = reason ? it.skip : it;
        test(`${caseName(tc)}${reason ? ` — skipped: ${reason}` : ""}`, () => {
          const expErrors = (tc.expErrors ?? []).map((e) => e.type);
          const res = run(tc, false);
          if (tc.exp !== undefined) expect(res.out).toBe(tc.exp);
          expect(res.errors).toEqual(expErrors);
          if (tc.expParts) {
            const parts = run(tc, true);
            expect(parts.out).toMatchObject(tc.expParts);
            expect(parts.errors).toEqual(expErrors);
          }
        });
      }
    });
  }

  describe("compile-time errors are rejected before reaching a runtime", () => {
    it.each(compileTime.map((tc) => [`${tc.file}: ${caseName(tc)}`, tc] as const))(
      "%s",
      (_n, tc) => {
        expect(() => parseMF2(tc.src)).toThrow();
      },
    );
  });

  it("keeps the skip list honest", () => {
    const known = new Set(runtime.map(key));
    for (const k of skip.keys()) expect(known, `stale skip entry ${k}`).toContain(k);
  });
});
