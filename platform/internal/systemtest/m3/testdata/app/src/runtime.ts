// Written by `go generate ./internal/systemtest/m3/...` — change the generator, not this file.
// The page's one runtime. The release is built in the page from the
// generated catalogs, so the app renders with no network and no server:
// ?env= picks the manifest's environment (preview by default), which is
// what the capture's production refusal and the overlay loader's guard
// read.
import { createRuntime } from "@klarlabs-studio/glossa";
import type { Artifact, BundledRelease, Manifest, Message } from "@klarlabs-studio/glossa";

import catalogs from "./catalogs.json";

const LOCALES = Object.keys(catalogs as Record<string, unknown>).sort();

function release(environment: string): BundledRelease {
  const artifacts: Record<string, Artifact> = {};
  const refs: Manifest["artifacts"] = {};
  LOCALES.forEach((locale, n) => {
    // Bundled artifacts are trusted as shipped; the digest only has to
    // match the manifest's.
    const sha = String(n + 1).repeat(64).slice(0, 64);
    const messages = (catalogs as Record<string, Record<string, Message>>)[locale]!;
    artifacts[sha] = { schema: "glossa.artifact/v1", locale, namespace: "default", messages };
    refs[locale] = { default: { sha256: sha, size: 1 } };
  });
  return {
    manifest: {
      schema: "glossa.manifest/v1",
      project: "prj_m3fixture",
      environment,
      release: { id: "rel_m3fixture", version: 1, createdAt: "2026-09-20T08:00:00Z" },
      sourceLocale: "de",
      locales: LOCALES.map((code) => ({ code, direction: "ltr" as const })),
      fallback: { "*": ["de"] },
      artifacts: refs,
    },
    artifacts,
  };
}

const query = new URLSearchParams(globalThis.location?.search ?? "");
export const environment = query.get("env") ?? "preview";
export const locale = query.get("lang") ?? "de";
export const runtime = createRuntime({
  bundled: release(environment),
  environment,
  locales: locale,
  storage: null,
});
