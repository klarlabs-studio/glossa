/**
 * End-to-end agreement between the Go server and the browser runtime.
 *
 * `messageformat/testdata/glossa/mf1-to-mf2.json` holds the canonical models
 * the Go kernel produces from ICU MF1 source, each with sample outputs taken
 * from the MF1 reference implementation. The runtime must render the
 * Go-produced model to the same string, so a message authored in MF1, stored
 * by the server and shipped in a release reads exactly as its author meant.
 */
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { paramValues, testdataDir } from "@klarlabs-studio/glossa-messageformat/testing";
import type { SuiteParam } from "@klarlabs-studio/glossa-messageformat/testing";
import { describe, expect, it } from "vitest";
import { format } from "./index.js";
import type { Message } from "./index.js";

interface Sample {
  params?: SuiteParam[];
  exp: string;
  /** MF2 output where it knowingly differs from MF1 (null: MF2 reports errors). */
  mf2Exp?: string | null;
}

interface ConversionCase {
  description: string;
  locale: string;
  exp?: Message;
  samples?: Sample[];
}

const fixture = JSON.parse(
  readFileSync(join(testdataDir, "glossa/mf1-to-mf2.json"), "utf8"),
) as { tests: ConversionCase[] };

/** `mf1:` fallback functions have no standard MF2 equivalent; runtimes can't format them. */
const usesMF1Function = (m: Message) => JSON.stringify(m).includes('"name":"mf1:');

const samples = fixture.tests.flatMap((c) =>
  c.exp && !usesMF1Function(c.exp)
    ? (c.samples ?? [])
        .filter((s) => s.mf2Exp !== null)
        .map((s, i) => [`${c.description} #${i + 1}`, c.locale, c.exp as Message, s] as const)
    : [],
);

/**
 * CLDR releases disagree on U+202F NARROW NO-BREAK SPACE vs U+0020 in some
 * formats (English times before AM/PM), so the Node running the suite may
 * differ from the one that recorded the samples. That is locale data, not
 * conversion, so the comparison ignores the difference.
 */
const cldrSpaces = (s: string) => s.replaceAll("\u202f", " ");

describe("Go-converted MF1 messages render like MF1", () => {
  it("covers the fixture", () => {
    expect(samples.length).toBeGreaterThan(50);
  });

  it.each(samples)("%s", (_name, locale, message, sample) => {
    const errors: string[] = [];
    const out = format(message, locale, paramValues(sample.params), {
      bidiIsolation: "none",
      onError: (e) => errors.push(e.type),
    });
    expect(errors).toEqual([]);
    expect(cldrSpaces(out)).toBe(cldrSpaces(sample.mf2Exp ?? sample.exp));
  });
});
