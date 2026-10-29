import { mkdtemp, mkdir, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

import { fetchRelease, loadRelease, readRelease } from "./release.js";
import { DELIVERY_KEY, EDGE, fakeEdge, release, text } from "./testing/release.js";
import type { TestRelease } from "./testing/release.js";

const r = release("rel_7", 7, {
  de: { "cart.checkout": text("Zur Kasse") },
  en: { "cart.checkout": text("Checkout") },
});

/** Write a release the way `glossa pull --release` lays it out. */
async function pulled(rel: TestRelease, tamper?: string): Promise<string> {
  const dir = await mkdtemp(join(tmpdir(), "glossa-release-"));
  await mkdir(join(dir, "a"));
  await writeFile(join(dir, "manifest.json"), JSON.stringify(rel.manifest));
  for (const [sha, bytes] of Object.entries(rel.bytes)) {
    await writeFile(
      join(dir, "a", `${sha}.json`),
      sha === tamper ? bytes.replace("}}", "} }") : bytes,
    );
  }
  return dir;
}

describe("loading the release at build time", () => {
  it("reads a glossa pull --release directory, verified, with every locale", async () => {
    const got = await readRelease(await pulled(r));
    expect(got.manifest.release.id).toBe("rel_7");
    expect(got.artifacts).toEqual(r.artifacts);
  });

  it("fails the build on an artifact that doesn't match its hash", async () => {
    const en = await pulled(r, r.manifest.artifacts.en!.default!.sha256);
    await expect(readRelease(en)).rejects.toThrow(/incomplete \(en\).*integrity/);
    const de = await pulled(r, r.manifest.artifacts.de!.default!.sha256);
    await expect(readRelease(de)).rejects.toThrow(/couldn't load.*integrity/);
  });

  it("fails the build on a release for another environment", async () => {
    await expect(readRelease(await pulled(r), { environment: "staging" })).rejects.toThrow(
      /couldn't load.*schema: manifest is for environment production, not staging/,
    );
  });

  it("fetches the edge's current release", async () => {
    const edge = fakeEdge(() => r);
    const got = await fetchRelease({
      edge: EDGE,
      deliveryKey: DELIVERY_KEY,
      transport: edge.transport,
    });
    expect(got.artifacts).toEqual(r.artifacts);
    await expect(
      fetchRelease({
        edge: EDGE,
        deliveryKey: DELIVERY_KEY,
        transport: fakeEdge(() => undefined).transport,
      }),
    ).rejects.toThrow(/couldn't load the release from https:\/\/edge.test: network/);
  });

  it("picks the configured source, or none", async () => {
    expect(await loadRelease(r, {})).toBe(r);
    expect((await loadRelease(await pulled(r), {}))?.manifest.release.id).toBe("rel_7");
    const transport = fakeEdge(() => r).transport;
    expect(
      (await loadRelease(undefined, { edge: EDGE, deliveryKey: DELIVERY_KEY, transport }))
        ?.artifacts,
    ).toEqual(r.artifacts);
    expect(await loadRelease(undefined, {})).toBeUndefined();
  });
});
