/**
 * A fake `glossa-edge` (runtimes/SPEC.md §2) behind the runtime's transport,
 * test-only. It answers the manifest and artifact paths from whatever it was
 * last told to serve, and records every request.
 */
import type { Transport } from "../runtime.js";
import type { EdgeManifest } from "./fixtures.js";

export const EDGE = "https://edge.test";
export const DELIVERY_KEY = "pk_test";

export interface EdgeState {
  manifest: EdgeManifest;
  artifacts: Record<string, string>;
}

export function fakeEdge(initial: EdgeState = { manifest: { status: 503 }, artifacts: {} }) {
  let current = initial;
  let offline = false;
  const requests: Array<{ url: string; headers: Record<string, string> }> = [];
  const respond = (status: number, body = "", etag?: string) => ({
    status,
    headers: { get: (name: string) => (name.toLowerCase() === "etag" ? (etag ?? null) : null) },
    text: async () => body,
  });
  const transport: Transport = async (url, init) => {
    requests.push({ url, headers: { ...init.headers } });
    if (offline) throw new TypeError("fetch failed");
    if (url === `${EDGE}/v1/${DELIVERY_KEY}/production/manifest.json`) {
      const m = current.manifest;
      return m.status === 200 ? respond(200, JSON.stringify(m.body), m.etag) : respond(m.status);
    }
    const sha = new RegExp(`^${EDGE}/v1/${DELIVERY_KEY}/a/([0-9a-f]{64})\\.json$`).exec(url)?.[1];
    const body = sha === undefined ? undefined : current.artifacts[sha];
    return body === undefined ? respond(404) : respond(200, body);
  };
  return {
    transport,
    requests,
    serve(state: EdgeState) {
      current = state;
    },
    setOffline(value: boolean) {
      offline = value;
    },
    /** Artifact hashes requested so far, in order. */
    artifactRequests: () =>
      requests.flatMap((r) => /\/a\/([0-9a-f]{64})\.json$/.exec(r.url)?.[1] ?? []),
  };
}
