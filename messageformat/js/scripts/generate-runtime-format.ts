/**
 * Regenerates `messageformat/testdata/glossa/runtime-format.json` from the
 * reference formatter. Run through the package script:
 *
 *   pnpm --filter @klarlabs-studio/glossa-messageformat generate:runtime-format
 */
import { writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
import * as api from "../src/index.js";
import { buildTests, fixtureComment, serialize } from "./runtime-format-cases.ts";

const out = fileURLToPath(new URL("../../testdata/glossa/runtime-format.json", import.meta.url));

const fixture = {
  $comment: fixtureComment,
  generatedWith: {
    messageformat: (
      createRequire(import.meta.url)("messageformat/package.json") as { version: string }
    ).version,
    icu: process.versions.icu,
    cldr: process.versions.cldr,
  },
  tests: buildTests(api),
};

writeFileSync(out, serialize(fixture));
console.log(`wrote ${fixture.tests.length} tests to ${out}`);
