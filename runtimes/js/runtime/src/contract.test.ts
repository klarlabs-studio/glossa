/**
 * The runtime contract's conformance fixtures (runtimes/SPEC.md §7), driven
 * through a fake edge and in-memory storage. Every scenario case and every
 * loading sequence runs; there is no skip list.
 *
 * - scenarios: one runtime per file, loaded from the edge; each case
 *   switches the requested locales, renders, and checks `explain()`.
 * - loading: each step serves the step's edge responses and settles one
 *   load (a fresh runtime on the first step and after a restart, which drops
 *   memory and keeps storage; `refresh()` otherwise), then reads.
 */
import { describe, expect, it } from "vitest";
import { createRuntime } from "./runtime.js";
import type { Runtime, RuntimeError } from "./runtime.js";
import { memoryStorage } from "./storage.js";
import { DELIVERY_KEY, EDGE, fakeEdge } from "./testing/edge.js";
import { fixtures } from "./testing/fixtures.js";
import type { LoadingSequence, Scenario } from "./testing/fixtures.js";

const scenarios = fixtures<Scenario>("scenarios");
const loading = fixtures<LoadingSequence>("loading");

describe("runtimes/testdata/scenarios", () => {
  it("has fixtures", () => expect(scenarios.length).toBeGreaterThan(0));

  describe.each(scenarios)("%s", (_name, sc) => {
    const edge = fakeEdge({
      manifest: { status: 200, etag: '"m"', body: sc.manifest },
      artifacts: sc.artifacts,
    });
    const runtime = (bidiIsolation: "none" | "default") =>
      createRuntime({
        edge: EDGE,
        deliveryKey: DELIVERY_KEY,
        transport: edge.transport,
        storage: memoryStorage(),
        locales: [],
        refreshInterval: 0,
        bidiIsolation,
        // Cases repeat message IDs on purpose; report every miss, not once a minute.
        errorInterval: 0,
      });
    const runtimes = new Map<string, Runtime>();

    it.each(sc.cases.map((c) => [`${JSON.stringify(c.requested)} ${c.id}`, c] as const))(
      "%s",
      async (_case, c) => {
        let rt = runtimes.get(c.bidiIsolation);
        if (!rt) runtimes.set(c.bidiIsolation, (rt = runtime(c.bidiIsolation)));
        await rt.ready;
        const errors: RuntimeError[] = [];
        const off = rt.onError((e) => errors.push(e));
        await rt.setLocales(c.requested);
        const rendered = rt.t(c.id, c.values, { default: c.default });
        off();

        expect(rendered).toBe(c.exp);
        expect(rt.locale).toBe(c.expLocale);
        if (c.expDirection) expect(rt.dir).toBe(c.expDirection);
        const x = rt.explain(c.id);
        expect({ locale: x.locale, chain: x.chain, resolvedFrom: x.resolvedFrom }).toEqual({
          locale: c.expLocale,
          chain: c.expChain,
          resolvedFrom: c.expResolvedFrom,
        });
        // Explaining the requested locales directly answers the same, without switching.
        expect(rt.explain(c.id, c.requested)).toEqual(x);
        expect(x.release).toEqual({
          id: sc.manifest.release.id,
          version: sc.manifest.release.version,
        });
        expect(x.source).toBe(c.expResolvedFrom === null ? "inline" : "network");
        const walked = x.chain.slice(
          0,
          x.resolvedFrom ? x.chain.indexOf(x.resolvedFrom) + 1 : undefined,
        );
        expect(x.steps).toEqual(
          walked.map((locale) => ({
            locale,
            outcome: locale === c.expResolvedFrom ? "found" : "missing",
          })),
        );
        // A message found in the chain renders without errors; a miss is reported.
        expect(errors.map((e) => e.type)).toEqual(
          c.expResolvedFrom === null ? ["missing-message"] : [],
        );
      },
    );
  });
});

describe("runtimes/testdata/loading", () => {
  it("has fixtures", () => expect(loading.length).toBeGreaterThan(0));

  it.each(loading)("%s", async (_name, seq) => {
    const edge = fakeEdge();
    const storage = memoryStorage();
    const errors: string[] = [];
    let rt: Runtime | undefined;
    let lastEtag: string | undefined;

    for (const [i, step] of seq.steps.entries()) {
      const where = `step ${i}: ${step.description}`;
      edge.serve(step.edge);
      const seen = errors.length;
      const requests = edge.requests.length;
      if (!rt || seq.restartBefore?.includes(i)) {
        rt?.dispose();
        rt = createRuntime({
          edge: EDGE,
          deliveryKey: DELIVERY_KEY,
          transport: edge.transport,
          storage,
          publicKeys: seq.publicKeys,
          locales: step.read.requested,
          refreshInterval: 0,
          onError: (e) => errors.push(e.type),
          installationId: seq.installationId,
          rollout: seq.rolloutSupport,
        });
        await rt.ready;
      } else {
        await rt.refresh();
      }
      if (step.edge.manifest.status === 304) {
        const revalidation = edge.requests.slice(requests).find((r) => r.url.endsWith(".json"));
        expect(revalidation?.headers["If-None-Match"], where).toBe(lastEtag);
      }
      // The manifest became active, on either side of a rollout it carries.
      const body = step.edge.manifest.body;
      if (body && [body.release.id, body.rollout?.candidate.release.id].includes(rt.release?.id)) {
        lastEtag = step.edge.manifest.etag;
      }
      if (rt.explain(step.read.id).requested.join() !== step.read.requested.join()) {
        await rt.setLocales(step.read.requested);
      }

      expect(rt.t(step.read.id), where).toBe(step.exp);
      expect(rt.release?.id ?? null, where).toBe(step.expActiveRelease);
      expect(rt.explain(step.read.id).source, where).toBe(step.expSource);
      expect(errors.slice(seen), where).toEqual(step.expErrors);
      if (step.expRollout !== undefined) {
        expect(rt.explain(step.read.id).rollout, where).toEqual(step.expRollout);
      }
    }
  });
});
