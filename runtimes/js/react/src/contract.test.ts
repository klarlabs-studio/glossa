// @vitest-environment jsdom
/**
 * The runtime contract's conformance fixtures (runtimes/SPEC.md §7), rendered
 * through React: every scenario case and every loading step is read from the
 * DOM of a mounted component, both as `useGlossa().t()` and as `<T>`, so a
 * locale switch or a newly activated release only passes if it re-renders.
 * There is no skip list.
 */
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { act, createElement as h } from "react";
import type { ReactNode } from "react";
import { createRoot } from "react-dom/client";
import type { Root as ReactRoot } from "react-dom/client";
import { memoryStorage } from "@klarlabs-studio/glossa-runtime";
import type { RuntimeError } from "@klarlabs-studio/glossa-runtime";

import { GlossaProvider, T, createGlossa, useGlossa } from "./index.js";
import type { Glossa, UseGlossa } from "./index.js";
import { DELIVERY_KEY, EDGE, fakeEdge } from "./testing/edge.js";
import { fixtures } from "./testing/fixtures.js";
import type { LoadingSequence, Scenario } from "./testing/fixtures.js";

const scenarios = fixtures<Scenario>("scenarios");
const loading = fixtures<LoadingSequence>("loading");

beforeAll(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
});

interface Read {
  id: string;
  values?: Record<string, unknown>;
  default?: string;
}

/** The hook's latest result, for actions (`setLocales`) and `explain`. */
let hook: UseGlossa;

/** Renders `read` both ways, under `lang` and `dir` from the hook. */
function Probe({ read }: { read: Read }): ReactNode {
  hook = useGlossa();
  const { t, locale, dir } = hook;
  return h(
    "output",
    { lang: locale, dir },
    h("span", { className: "t" }, t(read.id, read.values, { default: read.default })),
    h("span", { className: "T" }, h(T, { id: read.id, values: read.values }, read.default)),
  );
}

/** A mounted provider whose probe can be pointed at another message. */
function mount(glossa: Glossa) {
  const host = document.createElement("div");
  document.body.append(host);
  const root: ReactRoot = createRoot(host);
  const show = (read: Read) =>
    act(() => root.render(h(GlossaProvider, { glossa }, h(Probe, { read }))));
  const text = (cls: "t" | "T") => host.querySelector(`.${cls}`)!.textContent;
  const output = () => host.querySelector("output")!;
  return { show, text, output, unmount: () => act(() => root.unmount()) };
}

describe("runtimes/testdata/scenarios through React", () => {
  it("has fixtures", () => expect(scenarios.length).toBeGreaterThan(0));

  describe.each(scenarios)("%s", (_name, sc) => {
    const apps = new Map<string, ReturnType<typeof mount> & { glossa: Glossa }>();
    const errors: RuntimeError[] = [];
    const app = (bidiIsolation: "none" | "default") => {
      let a = apps.get(bidiIsolation);
      if (!a) {
        const edge = fakeEdge({
          manifest: { status: 200, etag: '"m"', body: sc.manifest },
          artifacts: sc.artifacts,
        });
        const glossa = createGlossa({
          edge: EDGE,
          deliveryKey: DELIVERY_KEY,
          transport: edge.transport,
          storage: memoryStorage(),
          locales: [],
          refreshInterval: 0,
          bidiIsolation,
          errorInterval: 0,
          onError: (e) => errors.push(e),
        });
        a = { ...mount(glossa), glossa };
        apps.set(bidiIsolation, a);
      }
      return a;
    };

    it.each(sc.cases.map((c) => [`${JSON.stringify(c.requested)} ${c.id}`, c] as const))(
      "%s",
      async (_case, c) => {
        const a = app(c.bidiIsolation);
        await act(() => a.glossa.runtime.ready);
        a.show({ id: c.id, values: c.values, default: c.default });
        await act(() => hook.setLocales(c.requested));
        const seen = errors.length;
        a.show({ id: c.id, values: c.values, default: c.default });

        expect(a.text("t")).toBe(c.exp);
        expect(a.text("T")).toBe(c.exp);
        expect(a.output().lang).toBe(c.expLocale);
        if (c.expDirection) expect(a.output().dir).toBe(c.expDirection);
        const x = hook.explain(c.id);
        expect({ locale: x.locale, chain: x.chain, resolvedFrom: x.resolvedFrom }).toEqual({
          locale: c.expLocale,
          chain: c.expChain,
          resolvedFrom: c.expResolvedFrom,
        });
        expect(hook.release).toEqual({
          id: sc.manifest.release.id,
          version: sc.manifest.release.version,
        });
        // A message found in the chain renders without errors; a miss is reported.
        expect(new Set(errors.slice(seen).map((e) => e.type))).toEqual(
          new Set(c.expResolvedFrom === null ? ["missing-message"] : []),
        );
      },
    );

    afterAll(() => {
      for (const a of apps.values()) {
        a.unmount();
        a.glossa.runtime.dispose();
      }
    });
  });
});

describe("runtimes/testdata/loading through React", () => {
  it("has fixtures", () => expect(loading.length).toBeGreaterThan(0));

  it.each(loading)("%s", async (_name, seq) => {
    const edge = fakeEdge();
    const storage = memoryStorage();
    const errors: string[] = [];
    let current: (ReturnType<typeof mount> & { glossa: Glossa }) | undefined;

    for (const [i, step] of seq.steps.entries()) {
      const where = `step ${i}: ${step.description}`;
      edge.serve(step.edge);
      const seen = errors.length;
      const read = { id: step.read.id };
      if (!current || seq.restartBefore?.includes(i)) {
        // A restart drops memory and keeps storage: a new runtime and a new root.
        if (current) {
          current.unmount();
          current.glossa.runtime.dispose();
        }
        const glossa = createGlossa({
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
        current = { ...mount(glossa), glossa };
        current.show(read);
        await act(() => glossa.runtime.ready);
      } else {
        current.show(read);
        await act(() => current!.glossa.runtime.refresh());
      }
      if (hook.explain(read.id).requested.join() !== step.read.requested.join()) {
        await act(() => hook.setLocales(step.read.requested));
      }

      expect(current.text("t"), where).toBe(step.exp);
      expect(current.text("T"), where).toBe(step.exp);
      expect(hook.release?.id ?? null, where).toBe(step.expActiveRelease);
      expect(hook.explain(read.id).source, where).toBe(step.expSource);
      expect(errors.slice(seen), where).toEqual(step.expErrors);
      if (step.expRollout !== undefined) {
        expect(hook.explain(read.id).rollout, where).toEqual(step.expRollout);
      }
    }
    current?.unmount();
    current?.glossa.runtime.dispose();
  });
});
