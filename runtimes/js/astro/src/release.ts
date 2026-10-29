/**
 * The release a build renders with (Node, build time): a release object, a
 * directory written by `glossa pull --release`, or the release the edge
 * serves right now. Directory and edge releases load through
 * `@klarlabs-studio/glossa-runtime` itself, so they're verified exactly like in a browser
 * (schema, environment, signature when keys are set, every artifact's
 * SHA-256). A release that doesn't load completely fails the build: a static
 * site must not silently ship its inline defaults.
 *
 * Directory layout, mirroring the edge's paths:
 *
 *     <dir>/manifest.json
 *     <dir>/a/<sha256>.json
 */
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { createRuntime } from "@klarlabs-studio/glossa-runtime";
import type {
  Artifact,
  BundledRelease,
  PersistedRelease,
  PublicKey,
  RuntimeError,
  Transport,
} from "@klarlabs-studio/glossa-runtime";

export interface ReleaseSource {
  edge?: string;
  deliveryKey?: string;
  /** Default `production`. */
  environment?: string;
  publicKeys?: readonly PublicKey[];
  /** For tests and custom fetching; default `fetch`. */
  transport?: Transport;
}

const DIR_EDGE = "glossa-dir:";
const DIR_KEY = "local";

/** Serve a `glossa pull --release` directory as if it were the edge. */
export function directoryTransport(dir: string): Transport {
  return async (url) => {
    const path = url.slice(`${DIR_EDGE}/v1/${DIR_KEY}/`.length);
    const file = path.startsWith("a/") ? join(dir, path) : join(dir, "manifest.json");
    try {
      const body = await readFile(file, "utf8");
      return { status: 200, headers: { get: () => null }, text: async () => body };
    } catch {
      return { status: 404, headers: { get: () => null }, text: async () => "" };
    }
  };
}

/** Load every locale of the current release through a runtime and return it as a bundle. */
async function collect(src: ReleaseSource & { edge: string; deliveryKey: string }) {
  let saved: PersistedRelease | undefined;
  const errors: RuntimeError[] = [];
  const rt = createRuntime({
    ...src,
    locales: [],
    storage: { get: () => undefined, set: (_k, v) => void (saved = v as PersistedRelease) },
    refreshInterval: 0,
    errorInterval: 0,
    onError: (e) => errors.push(e),
  });
  try {
    await rt.ready;
    for (const l of rt.availableLocales) await rt.setLocales(l.code);
  } finally {
    rt.dispose();
  }
  const why = () => errors.map((e) => `${e.type}: ${e.detail}`).join("; ") || "nothing loaded";
  if (!saved) throw new Error(`[glossa] couldn't load the release from ${src.edge}: ${why()}`);
  const { manifest } = saved;
  const artifacts: Record<string, Artifact> = {};
  for (const [locale, namespaces] of Object.entries(manifest.artifacts)) {
    for (const ref of Object.values(namespaces)) {
      const bytes = saved.artifacts[ref.sha256];
      if (bytes === undefined) {
        throw new Error(
          `[glossa] release ${manifest.release.id} is incomplete (${locale}): ${why()}`,
        );
      }
      artifacts[ref.sha256] = JSON.parse(bytes) as Artifact;
    }
  }
  return { manifest, artifacts };
}

/** The current release of a `glossa pull --release` directory. */
export function readRelease(dir: string, src: Omit<ReleaseSource, "edge" | "deliveryKey"> = {}) {
  return collect({
    ...src,
    edge: DIR_EDGE,
    deliveryKey: DIR_KEY,
    transport: directoryTransport(dir),
  });
}

/** The release the edge serves for `environment` right now. */
export function fetchRelease(src: ReleaseSource & { edge: string; deliveryKey: string }) {
  return collect(src);
}

/** The release to build with, from whichever source is configured; undefined when none is. */
export async function loadRelease(
  release: string | BundledRelease | undefined,
  src: ReleaseSource,
): Promise<BundledRelease | undefined> {
  if (release && typeof release === "object") return release;
  if (typeof release === "string") return readRelease(release, src);
  if (src.edge && src.deliveryKey)
    return fetchRelease({ ...src, edge: src.edge, deliveryKey: src.deliveryKey });
  return undefined;
}
