/**
 * Staged rollout (runtimes/SPEC.md §1.4). The cohort function is checked
 * against `runtimes/testdata/rollout/cohorts.json`, which
 * `runtimes/testdata/gen/generate.py` computes from the SPEC's formula with
 * code shared with no runtime: every installation id, every vector, every
 * candidate count. The loading behaviour is the shared `loading/rollout-*`
 * sequences (contract.test.ts); these cases cover what they can't: the
 * installation id's lifecycle, the switch, and bundled catalogs.
 */
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import type { Manifest } from "./manifest.js";
import { createRuntime } from "./runtime.js";
import type { PersistedRelease, RuntimeOptions } from "./runtime.js";
import { memoryStorage } from "./storage.js";
import { DELIVERY_KEY, EDGE, fakeEdge } from "./testing/edge.js";
import { runtimeTestdata } from "./testing/fixtures.js";
import { release, served, text } from "./testing/release.js";
import { cohort } from "./verify.js";

interface CohortTable {
  salt: string;
  expCandidates: Record<string, number>;
  vectors: Array<{ salt: string; key: string; cohort: number; note: string }>;
  installations: Array<[string, number]>;
}

const table = JSON.parse(
  readFileSync(`${runtimeTestdata}rollout/cohorts.json`, "utf8"),
) as CohortTable;

describe("runtimes/testdata/rollout/cohorts.json", () => {
  it("has 10,000 installations", () => expect(table.installations).toHaveLength(10000));

  it.each(table.vectors.map((v) => [v.note, v] as const))("%s", async (_note, v) => {
    expect(await cohort(v.salt, v.key)).toBe(v.cohort);
  });

  it("agrees with the generator on every installation id and every candidate count", async () => {
    const got = await Promise.all(table.installations.map(([id]) => cohort(table.salt, id)));
    const disagree = table.installations.filter(([, want], i) => got[i] !== want);
    expect(disagree).toEqual([]);
    for (const [percent, n] of Object.entries(table.expCandidates)) {
      expect(got.filter((c) => c < Number(percent) * 100).length, `${percent} %`).toBe(n);
    }
  });
});

const stable = release("rel_1", 1, { en: { hello: text("Hello v1") } });
const candidate = release("rel_2", 2, { en: { hello: text("Hello v2") } });
const SALT = table.salt;

const withRollout = (percent: number, salt = SALT, id = "ro_test"): Manifest => {
  const { release: r, locales, fallback, artifacts } = candidate.manifest;
  return {
    ...stable.manifest,
    rollout: { id, percent, salt, candidate: { release: r, locales, fallback, artifacts } },
  };
};

const both = { ...stable.artifacts, ...candidate.artifacts };
const IN = table.installations.find(([, c]) => c < 1000)![0];
const OUT = table.installations.find(([, c]) => c >= 1000)![0];

function setup(body: Manifest, opts: RuntimeOptions = {}) {
  const edge = fakeEdge({ manifest: { status: 200, etag: '"m"', body }, artifacts: both });
  const create = (more: RuntimeOptions = {}) =>
    createRuntime({
      edge: EDGE,
      deliveryKey: DELIVERY_KEY,
      transport: edge.transport,
      locales: "en",
      refreshInterval: 0,
      ...opts,
      ...more,
    });
  return { edge, create };
}

describe("createRuntime: staged rollout", () => {
  it("puts the installations of the table on the side the generator says", async () => {
    const ids = table.installations.slice(0, 200);
    const { create } = setup(withRollout(10));
    for (const [id, c] of ids) {
      const rt = create({ installationId: id, storage: null });
      await rt.ready;
      expect(rt.release?.id, id).toBe(c < 1000 ? "rel_2" : "rel_1");
      expect(rt.explain("hello").rollout, id).toEqual({
        id: "ro_test",
        percent: 10,
        cohort: c,
        side: c < 1000 ? "candidate" : "stable",
      });
      rt.dispose();
    }
  });

  it("creates a 128-bit installation id once, keeps it with the release, and reuses it", async () => {
    const storage = memoryStorage();
    const { create } = setup(withRollout(50));
    const first = create({ storage });
    await first.ready;
    const record = storage.get(`glossa:${DELIVERY_KEY}:production`) as PersistedRelease;
    expect(record.installation).toMatch(/^[0-9a-f]{32}$/);
    const id = record.installation!;
    expect(first.explain("hello").rollout?.cohort).toBe(await cohort(SALT, id));
    first.dispose();

    const again = create({ storage });
    await again.ready;
    expect((storage.get(`glossa:${DELIVERY_KEY}:production`) as PersistedRelease).installation).toBe(
      id,
    );
    expect(again.explain("hello").rollout).toEqual(first.explain("hello").rollout);
  });

  it("creates no installation id for a manifest without a rollout", async () => {
    const storage = memoryStorage();
    const { create } = setup(stable.manifest);
    await create({ storage }).ready;
    const record = storage.get(`glossa:${DELIVERY_KEY}:production`) as PersistedRelease;
    expect(record.manifest.release.id).toBe("rel_1");
    expect(record.installation).toBeUndefined();
  });

  it("works without storage: an id in memory, no crash", async () => {
    const { create } = setup(withRollout(100));
    const rt = create({ storage: null });
    await rt.ready;
    expect(rt.t("hello")).toBe("Hello v2");
    expect(rt.explain("hello").rollout?.side).toBe("candidate");
  });

  it("with `rollout: false`, ignores the rollout and never fetches the candidate", async () => {
    const { create, edge } = setup(withRollout(100), { rollout: false });
    const rt = create({ installationId: IN, storage: null });
    await rt.ready;
    expect(rt.t("hello")).toBe("Hello v1");
    expect(rt.explain("hello").rollout).toBeNull();
    expect(edge.artifactRequests()).not.toContain(candidate.sha.en);
  });

  it("returns to stable on abort and draws again under a new rollout", async () => {
    const { create, edge } = setup(withRollout(10));
    const rt = create({ installationId: IN, storage: null });
    await rt.ready;
    expect(rt.release?.id).toBe("rel_2");
    edge.serve(served(stable, '"m2"'));
    await rt.refresh();
    expect(rt.release?.id).toBe("rel_1");
    expect(rt.explain("hello").rollout).toBeNull();
    const next = withRollout(100, "b3RoZXItcm9sbG91dC0yMg", "ro_2");
    edge.serve({ manifest: { status: 200, etag: '"m3"', body: next }, artifacts: both });
    await rt.refresh();
    expect(rt.explain("hello").rollout).toMatchObject({
      id: "ro_2",
      percent: 100,
      side: "candidate",
    });
  });

  it("keeps the side when the locales switch", async () => {
    const { create } = setup(withRollout(10));
    const rt = create({ installationId: IN, storage: null });
    await rt.ready;
    await rt.setLocales("en");
    expect(rt.release?.id).toBe("rel_2");
    expect(rt.explain("hello").rollout?.side).toBe("candidate");
  });

  it("decides the side of a bundled manifest's rollout too", async () => {
    const body = withRollout(10);
    const parsed = { ...stable.parsed, ...candidate.parsed };
    const bundled = (installationId: string) =>
      createRuntime({
        bundled: { manifest: body, artifacts: parsed },
        installationId,
        locales: "en",
        storage: null,
      });
    const inside = bundled(IN);
    const outside = bundled(OUT);
    expect(inside.t("hello")).toBe("Hello v1"); // synchronously: the stable view
    await Promise.all([inside.ready, outside.ready]);
    expect(inside.t("hello")).toBe("Hello v2");
    expect(inside.explain("hello").source).toBe("bundled");
    expect(outside.t("hello")).toBe("Hello v1");
  });

  it("ignores an invalid rollout with a schema error", async () => {
    const bad = withRollout(100);
    bad.rollout!.percent = 10.5;
    const { create } = setup(bad);
    const errors: string[] = [];
    const onError = (e: { type: string }) => void errors.push(e.type);
    const rt = create({ installationId: IN, storage: null, onError });
    await rt.ready;
    expect(rt.release?.id).toBe("rel_1");
    expect(rt.explain("hello").rollout).toBeNull();
    expect(errors).toEqual(["schema"]);
  });
});
