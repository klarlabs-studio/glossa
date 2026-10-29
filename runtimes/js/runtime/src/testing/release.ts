/**
 * Build releases for tests the way the publisher does (runtimes/SPEC.md §1):
 * artifacts are serialized once, and the manifest names them by the SHA-256
 * of those exact bytes. Test-only.
 */
import { createHash } from "node:crypto";
import type { Artifact, Manifest } from "../manifest.js";
import type { Message } from "../model.js";

export const text = (s: string): Message => ({ type: "message", declarations: [], pattern: [s] });

export interface TestRelease {
  manifest: Manifest;
  /** sha256 → exact artifact bytes, as the edge serves them. */
  artifacts: Record<string, string>;
  /** sha256 → parsed artifact, as a build would bundle them. */
  parsed: Record<string, Artifact>;
  /** locale → sha256 of its default artifact. */
  sha: Record<string, string>;
}

export function release(
  id: string,
  version: number,
  catalogs: Record<string, Record<string, Message>>,
  opts: {
    sourceLocale?: string;
    fallback?: Record<string, string[]>;
    directions?: Record<string, "ltr" | "rtl">;
  } = {},
): TestRelease {
  const artifacts: Record<string, string> = {};
  const parsed: Record<string, Artifact> = {};
  const sha: Record<string, string> = {};
  const refs: Manifest["artifacts"] = {};
  for (const [locale, messages] of Object.entries(catalogs)) {
    const a: Artifact = { schema: "glossa.artifact/v1", locale, namespace: "default", messages };
    const bytes = JSON.stringify(a);
    const digest = createHash("sha256").update(bytes, "utf8").digest("hex");
    artifacts[digest] = bytes;
    parsed[digest] = a;
    sha[locale] = digest;
    refs[locale] = { default: { sha256: digest, size: Buffer.byteLength(bytes) } };
  }
  const locales = Object.keys(catalogs);
  const manifest: Manifest = {
    schema: "glossa.manifest/v1",
    project: "prj_test",
    environment: "production",
    release: { id, version, createdAt: "2026-09-19T08:00:00Z" },
    sourceLocale: opts.sourceLocale ?? locales[0]!,
    locales: locales.map((code) => ({ code, direction: opts.directions?.[code] ?? "ltr" })),
    fallback: opts.fallback ?? {},
    artifacts: refs,
  };
  return { manifest, artifacts, parsed, sha };
}

/** What the fake edge serves for a release. */
export const served = (r: TestRelease, etag = `"${r.manifest.release.id}"`) => ({
  manifest: { status: 200 as const, etag, body: r.manifest },
  artifacts: r.artifacts,
});
