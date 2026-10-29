/**
 * Validate capture-script output against runtimes/testdata/schemas/captures.v1.schema.json
 * (with usages.v1, which it references), wrapped in a minimal capture
 * document the way `glossa capture` wraps it, and probe findings against
 * finding.v1.schema.json, completed the way the ingest completes them.
 * Test-only.
 */
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { Ajv2020 } from "ajv/dist/2020.js";

import type { ProbeFinding } from "../probes.js";
import type { Capture } from "../regions.js";

const schemas = join(import.meta.dirname, "../../../../testdata/schemas");
const schema = (name: string) =>
  JSON.parse(readFileSync(join(schemas, name), "utf8")) as Record<string, unknown>;

const ajv = new Ajv2020({ allErrors: true, strict: false });
ajv.addFormat("uri", (s: string) => URL.canParse(s));
ajv.addFormat("uuid", /^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i);
ajv.addSchema(schema("usages.v1.schema.json"));
// captures.v1 refers to finding.v1: a capture's `findings` are that shape
// minus the two members the page cannot know (RFC 0005 §5).
ajv.addSchema(schema("finding.v1.schema.json"));
const validate = ajv.compile(schema("captures.v1.schema.json"));
const validateFinding = ajv.getSchema("https://glossa.dev/schemas/finding/v1.json")!;

/** A capture the server could have minted, for the locus a probe leaves open. */
export const CAPTURE = "0192f5c2-0000-7000-8000-00000000c0de";

/**
 * A probe finding as the server stores it: the ingest computes the
 * fingerprint (only it knows the catalog message ID) and fills in
 * `locus.capture` (only it knows the capture). Everything else is what the
 * page wrote.
 */
export const stored = (p: ProbeFinding, capture = CAPTURE): Record<string, unknown> =>
  // Through JSON, because that is how it reaches the server: the absent
  // fields are absent on the wire, not present and undefined.
  JSON.parse(
    JSON.stringify({
      ...p,
      fingerprint: "f_0123456789abcdef",
      locus: { ...p.locus, ...(p.locus.region ? { capture } : {}) },
    }),
  ) as Record<string, unknown>;

/** The finding.v1 errors for `p` once the ingest completed it, or `[]`. */
export function findingErrors(p: ProbeFinding): string[] {
  return validateFinding(stored(p))
    ? []
    : (validateFinding.errors ?? []).map((e) => `${e.instancePath} ${e.message ?? ""}`);
}

/**
 * The captures.v1 document around one capture's `renders`, `regions` and —
 * where the probe pass ran — its findings, as `glossa capture` wraps them.
 *
 * The findings go through JSON for the reason `stored` does: an absent
 * `subject` is absent on the wire, not present and `undefined`, and the
 * schema is about the wire.
 */
export const document = (c: Capture & { probes?: ProbeFinding[] }) => ({
  schema: "glossa.captures/v1",
  application: "web",
  commit: "9f2c1e7a4b3d5c6e8f0a1b2c3d4e5f6a7b8c9d0e",
  branch: "main",
  tool: { name: "glossa", version: "0.0.0" },
  captures: [
    {
      route: "/",
      url: "http://localhost:4173/",
      viewport: { width: 1280, height: 800 },
      locale: "de",
      image: { sha256: "0".repeat(64), width: 1280, height: 800 },
      renders: c.renders,
      regions: c.regions,
      ...(c.probes ? { findings: JSON.parse(JSON.stringify(c.probes)) as unknown[] } : {}),
    },
  ],
});

/** The schema errors for `c`, or `[]`. */
export function schemaErrors(c: Capture & { probes?: ProbeFinding[] }): string[] {
  return validate(document(c))
    ? []
    : (validate.errors ?? []).map((e) => `${e.instancePath} ${e.message ?? ""}`);
}
