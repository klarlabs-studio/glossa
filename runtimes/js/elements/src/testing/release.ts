/**
 * Test releases built the way the publisher builds them (runtimes/SPEC.md §1),
 * and a fake edge serving them. Test-only.
 */
import { createHash } from "node:crypto";
import type {
  Artifact,
  BundledRelease,
  Manifest,
  Message,
  Pattern,
  Transport,
} from "@klarlabs-studio/glossa-runtime";

export const EDGE = "https://edge.test";
export const DELIVERY_KEY = "pk_test";

export const text = (s: string): Message => ({ type: "message", declarations: [], pattern: [s] });

type El = string | { $: string } | { open: string } | { close: string };

const pattern = (els: El[]): Pattern =>
  els.map((p) =>
    typeof p === "string"
      ? p
      : "$" in p
        ? { type: "expression", arg: { type: "variable", name: p.$ } }
        : "open" in p
          ? { type: "markup", kind: "open", name: p.open }
          : { type: "markup", kind: "close", name: p.close },
  );

/** A pattern message from strings, `{ $: "name" }` placeholders and `{ open }`/`{ close }` markup. */
export const msg = (...els: El[]): Message => ({
  type: "message",
  declarations: [],
  pattern: pattern(els),
});

/** `.input {$name :fn} .match $name key {{…}} * {{…}}`; the key `*` is the catch-all. */
export const match = (name: string, fn: string, variants: Record<string, El[]>): Message => ({
  type: "select",
  declarations: [
    {
      type: "input",
      name,
      value: {
        type: "expression",
        arg: { type: "variable", name },
        function: { type: "function", name: fn },
      },
    },
  ],
  selectors: [{ type: "variable", name }],
  variants: Object.entries(variants).map(([key, els]) => ({
    keys: [key === "*" ? { type: "*" } : { type: "literal", value: key }],
    value: pattern(els),
  })),
});

export interface TestRelease extends BundledRelease {
  /** sha256 → exact artifact bytes, as the edge serves them. */
  bytes: Record<string, string>;
}

export function release(
  id: string,
  version: number,
  catalogs: Record<string, Record<string, Message>>,
  opts: { fallback?: Record<string, string[]>; directions?: Record<string, "ltr" | "rtl"> } = {},
): TestRelease {
  const bytes: Record<string, string> = {};
  const artifacts: Record<string, Artifact> = {};
  const refs: Manifest["artifacts"] = {};
  for (const [locale, messages] of Object.entries(catalogs)) {
    const a: Artifact = { schema: "glossa.artifact/v1", locale, namespace: "default", messages };
    const s = JSON.stringify(a);
    const sha = createHash("sha256").update(s, "utf8").digest("hex");
    bytes[sha] = s;
    artifacts[sha] = a;
    refs[locale] = { default: { sha256: sha, size: Buffer.byteLength(s) } };
  }
  const locales = Object.keys(catalogs);
  const manifest: Manifest = {
    schema: "glossa.manifest/v1",
    project: "prj_test",
    environment: "production",
    release: { id, version, createdAt: "2026-09-19T08:00:00Z" },
    sourceLocale: locales[0]!,
    locales: locales.map((code) => ({ code, direction: opts.directions?.[code] ?? "ltr" })),
    fallback: opts.fallback ?? {},
    artifacts: refs,
  };
  return { manifest, artifacts, bytes };
}

/** A fake `glossa-edge` serving `current()`; records the URLs it was asked for. */
export function fakeEdge(current: () => TestRelease | undefined) {
  const requests: string[] = [];
  const respond = (status: number, body = "") => ({
    status,
    headers: { get: () => null },
    text: async () => body,
  });
  const transport: Transport = async (url) => {
    requests.push(url);
    const r = current();
    if (!r) return respond(503);
    if (url === `${EDGE}/v1/${DELIVERY_KEY}/production/manifest.json`) {
      return respond(200, JSON.stringify(r.manifest));
    }
    const sha = /\/a\/([0-9a-f]{64})\.json$/.exec(url)?.[1];
    const body = sha && r.bytes[sha];
    return body ? respond(200, body) : respond(404);
  };
  return { transport, requests };
}
