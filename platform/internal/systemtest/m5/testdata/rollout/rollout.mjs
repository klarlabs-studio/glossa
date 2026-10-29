// The JS side of RFC 0006 §12.4: one @klarlabs-studio/glossa per installation
// id, each loading through a transport that serves the edge's real
// answers (fetched once, then from memory), and the release each one
// activated.
//
//   node rollout.mjs <input.json> <output.json> <runtimes/js/glossa>
//
// input:  { edgeURL, deliveryKey, environment, ids: [...], rollout: bool,
//           manifest?: "<manifest JSON served instead of the edge's>" }
// output: { "<installation id>": "<active release id>" | null, ... }
//
// The installation id and the switch that turns rollout support off are
// @klarlabs-studio/glossa's `installationId` and `rollout` options (SPEC §1.4,
// RFC 0006 wave 2). `storage: null` keeps the runtime from persisting an id
// of its own; the one given is used as is.
import { readFileSync, writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
import { join } from "node:path";

const [, , inPath, outPath, runtimeDir] = process.argv;
const input = JSON.parse(readFileSync(inPath, "utf8"));
const { createRuntime } = await import(pathToFileURL(join(runtimeDir, "dist", "runtime", "index.js")).href);

const cache = new Map();
const transport = async (url) => {
  if (input.manifest && url.endsWith("/manifest.json")) {
    return respond({ status: 200, etag: '"m5-probe"', body: input.manifest });
  }
  if (!cache.has(url)) {
    const r = await fetch(url);
    cache.set(url, { status: r.status, etag: r.headers.get("etag"), body: await r.text() });
  }
  return respond(cache.get(url));
};
function respond(c) {
  return {
    status: c.status,
    headers: { get: (n) => (n.toLowerCase() === "etag" ? c.etag : null) },
    text: async () => c.body,
  };
}

const out = {};
for (const id of input.ids) {
  const rt = createRuntime({
    edge: input.edgeURL,
    deliveryKey: input.deliveryKey,
    environment: input.environment,
    storage: null,
    transport,
    refreshInterval: 0,
    installationId: id,
    rollout: input.rollout,
  });
  await rt.ready;
  out[id] = rt.release?.id ?? null;
  rt.dispose();
}
writeFileSync(outPath, JSON.stringify(out));
