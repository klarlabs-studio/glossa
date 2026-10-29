import { afterEach, describe, expect, it, vi } from "vitest";
import { createRuntime } from "./runtime.js";
import type { Render, RuntimeError, RuntimeOptions } from "./runtime.js";
import { memoryStorage } from "./storage.js";
import type { RuntimeStorage } from "./storage.js";
import { DELIVERY_KEY, EDGE, fakeEdge } from "./testing/edge.js";
import { release, served, text } from "./testing/release.js";
import type { Message } from "./model.js";

const greeting: Message = {
  type: "message",
  declarations: [],
  pattern: ["Hallo ", { type: "expression", arg: { type: "variable", name: "name" } }, "!"],
};

const r1 = release(
  "rel_1",
  1,
  {
    de: { "cart.checkout": text("Zur Kasse"), greeting, "only.de": text("Nur Deutsch") },
    "de-AT": { "cart.checkout": text("Zur Kassa") },
    en: { "cart.checkout": text("Checkout"), "only.en": text("English only") },
    ar: { "cart.checkout": text("الدفع") },
  },
  { fallback: { "de-AT": ["de"], "*": ["en"] }, directions: { ar: "rtl" } },
);
const r2 = release("rel_2", 2, {
  de: { "cart.checkout": text("Zur Kasse v2") },
  en: { "cart.checkout": text("Checkout v2") },
});

function setup(opts: RuntimeOptions = {}) {
  const edge = fakeEdge(served(r1));
  const errors: RuntimeError[] = [];
  const storage = opts.storage === undefined ? memoryStorage() : opts.storage;
  const create = (more: RuntimeOptions = {}) =>
    createRuntime({
      edge: EDGE,
      deliveryKey: DELIVERY_KEY,
      transport: edge.transport,
      storage,
      locales: ["de-AT"],
      refreshInterval: 0,
      bidiIsolation: "none",
      onError: (e) => errors.push(e),
      ...opts,
      ...more,
    });
  return { edge, errors, storage, create };
}

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("createRuntime: loading", () => {
  it("renders the inline default or the message ID until a release is active", async () => {
    const { create } = setup();
    const rt = create();
    expect(rt.t("cart.checkout", {}, { default: "Zur Kasse!" })).toBe("Zur Kasse!");
    expect(rt.t("cart.checkout")).toBe("cart.checkout");
    expect(rt.t("cart.checkout", {}, { default: "" })).toBe("cart.checkout");
    expect(rt.release).toBeUndefined();
    await rt.ready;
    expect(rt.t("cart.checkout")).toBe("Zur Kassa");
    expect(rt.release).toEqual({ id: "rel_1", version: 1 });
  });

  it("fetches only the artifacts of the active chain, each once", async () => {
    const { create, edge } = setup();
    const rt = create();
    await rt.ready;
    expect(edge.artifactRequests()).toEqual([r1.sha["de-AT"], r1.sha.de, r1.sha.en]);
    await rt.setLocales("en");
    await rt.setLocales("de-AT");
    expect(edge.artifactRequests()).toHaveLength(3);
    await rt.setLocales("ar");
    expect(edge.artifactRequests().slice(3)).toEqual([r1.sha.ar]);
  });

  it("revalidates the manifest with If-None-Match", async () => {
    const { create, edge } = setup();
    const rt = create();
    await rt.ready;
    expect(edge.requests[0]!.headers["If-None-Match"]).toBeUndefined();
    edge.serve({ manifest: { status: 304 }, artifacts: {} });
    await rt.refresh();
    const manifestRequests = edge.requests.filter((r) => r.url.endsWith("/manifest.json"));
    expect(manifestRequests[1]!.headers["If-None-Match"]).toBe('"rel_1"');
    expect(rt.explain("cart.checkout").source).toBe("memory");
  });

  it("reuses cached artifacts whose hash a new release still names", async () => {
    const { create, edge } = setup({ locales: ["en"] });
    const shared = release(
      "rel_1b",
      2,
      {
        en: { "cart.checkout": text("Checkout"), "only.en": text("English only") },
        de: { "cart.checkout": text("Zur Kasse v3") },
      },
      { sourceLocale: "de" },
    );
    expect(shared.sha.en).toBe(r1.sha.en);
    const rt = create();
    await rt.ready;
    edge.serve(served(shared));
    await rt.refresh();
    expect(rt.release?.id).toBe("rel_1b");
    expect(edge.artifactRequests()).toEqual([r1.sha.en, r1.sha.de, shared.sha.de]);
  });

  it("keeps a new release when the locale changes while it loads", async () => {
    const { create, edge } = setup();
    let open!: () => void;
    const gate = new Promise<void>((resolve) => (open = resolve));
    let blocked = 0;
    const rt = create({
      transport: async (url, init) => {
        if (url.includes("/a/") && rt.release) {
          blocked++;
          await gate;
        }
        return edge.transport(url, init);
      },
    });
    await rt.ready;
    edge.serve(served(r2));
    const refreshing = rt.refresh();
    await vi.waitFor(() => expect(blocked).toBe(1));
    const switching = rt.setLocales("en");
    open();
    await Promise.all([refreshing, switching]);
    expect([rt.release?.id, rt.locale]).toEqual(["rel_2", "en"]);
  });

  it("coalesces concurrent refreshes", async () => {
    const { create, edge } = setup();
    const rt = create();
    await rt.ready;
    const before = edge.requests.length;
    await Promise.all([rt.refresh(), rt.refresh(), rt.refresh()]);
    expect(edge.requests.length - before).toBe(1);
  });

  it("activates atomically: a failing artifact keeps the previous release", async () => {
    const { create, edge, errors } = setup();
    const rt = create();
    await rt.ready;
    const seen: string[] = [];
    rt.subscribe(() => seen.push(rt.release!.id));
    edge.serve({ ...served(r2), artifacts: { [r2.sha.en!]: r2.artifacts[r2.sha.en!]! } });
    await rt.refresh();
    expect(rt.release?.id).toBe("rel_1");
    expect(rt.t("cart.checkout")).toBe("Zur Kassa");
    expect(errors.map((e) => e.type)).toEqual(["network"]);
    expect(seen).toEqual([]);
    edge.serve(served(r2));
    await rt.refresh();
    expect(rt.t("cart.checkout")).toBe("Zur Kasse v2");
    expect(seen).toEqual(["rel_2"]);
  });

  it("rejects artifacts whose bytes don't parse as a v1 artifact", async () => {
    const bad = release("rel_bad", 3, { de: {} });
    const bytes = '{"schema":"glossa.artifact/v2","messages":{}}';
    const { createHash } = await import("node:crypto");
    const sha = createHash("sha256").update(bytes).digest("hex");
    bad.manifest.artifacts.de = { default: { sha256: sha, size: bytes.length } };
    const { create, edge, errors } = setup({ locales: ["de"] });
    edge.serve({ ...served(bad), artifacts: { [sha]: bytes } });
    const rt = create();
    await rt.ready;
    expect(rt.release).toBeUndefined();
    expect(errors.map((e) => [e.type, e.releaseId])).toEqual([["schema", "rel_bad"]]);
  });

  it("persists last-good and restarts from it when the edge is down", async () => {
    const { create, edge, errors } = setup();
    await create().ready;
    edge.setOffline(true);
    const rt = create();
    await rt.ready;
    expect(rt.t("cart.checkout")).toBe("Zur Kassa");
    expect(rt.explain("cart.checkout").source).toBe("persisted");
    expect(errors.map((e) => e.type)).toEqual(["network"]);
  });

  it("keeps a persisted release's other locales when a restart persists again", async () => {
    const { create, edge } = setup();
    const first = create();
    await first.ready;
    await first.setLocales("ar");
    edge.serve({ manifest: { status: 304 }, artifacts: {} });
    const second = create();
    await second.ready;
    edge.setOffline(true);
    await second.setLocales("ar");
    expect(second.t("cart.checkout")).toBe("الدفع");
    const third = create({ locales: ["ar"] });
    await third.ready;
    expect(third.t("cart.checkout")).toBe("الدفع");
  });

  it("re-verifies persisted artifacts and falls through on corruption", async () => {
    const storage = memoryStorage();
    const { create, edge, errors } = setup({ storage });
    await create().ready;
    const rec = (await storage.get(`glossa:${DELIVERY_KEY}:production`)) as {
      artifacts: Record<string, string>;
    };
    rec.artifacts[r1.sha.de!] = rec.artifacts[r1.sha.de!]!.replace("Kasse", "Kiste");
    edge.setOffline(true);
    const rt = create();
    await rt.ready;
    expect(rt.release).toBeUndefined();
    expect(errors.map((e) => e.type)).toEqual(["integrity", "network", "network"]);
  });

  it("survives storage that throws", async () => {
    const broken: RuntimeStorage = {
      get: () => Promise.reject(new Error("quota")),
      set: () => {
        throw new Error("quota");
      },
    };
    const { create } = setup({ storage: broken });
    const rt = create();
    await rt.ready;
    expect(rt.t("cart.checkout")).toBe("Zur Kassa");
  });

  it("works without persistence (storage: null) and without a network", async () => {
    const { create, edge } = setup({ storage: null });
    const rt = create();
    await rt.ready;
    expect(rt.t("cart.checkout")).toBe("Zur Kassa");
    const offline = createRuntime({ locales: "de" });
    await offline.ready;
    expect(offline.t("cart.checkout")).toBe("cart.checkout");
    expect(edge.requests.every((r) => r.url.startsWith(EDGE))).toBe(true);
  });

  it("rejects unsigned manifests when keys are configured", async () => {
    const { create, errors } = setup({ publicKeys: [{ keyId: "k", key: "A".repeat(43) }] });
    const rt = create();
    await rt.ready;
    expect(rt.release).toBeUndefined();
    expect(errors.map((e) => e.type)).toEqual(["signature"]);
  });
});

describe("createRuntime: bundled catalogs", () => {
  const bundled = { manifest: r1.manifest, artifacts: r1.parsed };

  it("renders bundled catalogs synchronously, before any network", () => {
    const { create } = setup();
    const rt = create({ bundled });
    expect(rt.t("cart.checkout")).toBe("Zur Kassa");
    expect(rt.explain("cart.checkout").source).toBe("bundled");
  });

  it("is replaced by the network release, and is the last resort when offline", async () => {
    const { create, edge } = setup({ storage: null });
    edge.serve(served(r2));
    const rt = create({ bundled });
    await rt.ready;
    expect(rt.t("cart.checkout")).toBe("Zur Kasse v2");
    edge.setOffline(true);
    const offline = create({ bundled });
    await offline.ready;
    expect(offline.explain("cart.checkout").source).toBe("bundled");
  });

  it("wins over an older persisted release, loses to a newer one", async () => {
    const { create, edge } = setup();
    await create().ready;
    edge.setOffline(true);
    const newer = release("rel_9", 9, { de: { "cart.checkout": text("Neu") } });
    const withNewer = create({ bundled: { manifest: newer.manifest, artifacts: newer.parsed } });
    await withNewer.ready;
    expect(withNewer.t("cart.checkout")).toBe("Neu");
    expect(withNewer.explain("cart.checkout").source).toBe("bundled");
    const old = release("rel_0", 0, { de: { "cart.checkout": text("Alt") } });
    const withOlder = create({ bundled: { manifest: old.manifest, artifacts: old.parsed } });
    await withOlder.ready;
    expect(withOlder.t("cart.checkout")).toBe("Zur Kassa");
  });

  it("serves artifacts from the bundle when the edge can't", async () => {
    const { create, edge, errors } = setup({ storage: null });
    edge.serve(served(r2));
    const rt = create({ bundled });
    await rt.ready;
    expect(rt.release?.id).toBe("rel_2");
    edge.serve({ manifest: served(r1).manifest, artifacts: {} });
    await rt.refresh();
    expect(rt.explain("cart.checkout").source).toBe("network");
    expect(rt.t("cart.checkout")).toBe("Zur Kassa");
    expect(errors).toEqual([]); // bundled artifacts come before the network
  });
  it("takes an artifact from the bundle before the network (content-addressed, SPEC §3)", async () => {
    const { create, edge } = setup({ storage: null });
    edge.serve(served(r2));
    const rt = create({ bundled });
    await rt.ready;
    expect(rt.release?.id).toBe("rel_2"); // r1's artifacts are no longer in memory
    const before = edge.artifactRequests().length;
    edge.serve(served(r1)); // a rollback to the bundled release
    await rt.refresh();
    expect(rt.release?.id).toBe("rel_1");
    expect(rt.t("cart.checkout")).toBe("Zur Kassa");
    expect(edge.artifactRequests().slice(before)).toEqual([]);
  });
});

describe("createRuntime: resolution", () => {
  it("walks the fallback chain and formats with the locale the message was found in", async () => {
    const { create } = setup();
    const rt = create();
    await rt.ready;
    expect(rt.t("only.de")).toBe("Nur Deutsch");
    expect(rt.t("only.en")).toBe("English only");
    expect(rt.t("greeting", { name: "Lina" })).toBe("Hallo Lina!");
    expect(rt.parts("greeting", { name: "Lina" }).map((p) => p.type)).toEqual([
      "text",
      "string",
      "text",
    ]);
    expect(rt.parts("nope", {}, { default: "Nein" })).toEqual([{ type: "text", value: "Nein" }]);
  });

  it("exposes the active locale and its direction", async () => {
    const { create } = setup();
    const rt = create({ locales: "ar_EG" });
    await rt.ready;
    expect(rt.locale).toBe("ar");
    expect(rt.dir).toBe("rtl");
    await rt.setLocales(["de-AT"]);
    expect([rt.locale, rt.dir]).toEqual(["de-AT", "ltr"]);
  });

  it("exposes the active release's locales, for locale pickers", async () => {
    const { create } = setup();
    const rt = create();
    expect(rt.availableLocales).toEqual([]);
    await rt.ready;
    expect(rt.availableLocales).toEqual([
      { code: "de", direction: "ltr" },
      { code: "de-AT", direction: "ltr" },
      { code: "en", direction: "ltr" },
      { code: "ar", direction: "rtl" },
    ]);
  });

  it("notifies subscribers on activation until they unsubscribe", async () => {
    const { create } = setup();
    const rt = create();
    const seen: Array<string | undefined> = [];
    const off = rt.subscribe(() => seen.push(rt.locale));
    await rt.ready;
    await rt.setLocales("en");
    off();
    await rt.setLocales("de");
    expect(seen).toEqual(["de-AT", "en"]);
  });

  it("uses navigator.languages when no locales are given", async () => {
    vi.stubGlobal("navigator", { languages: ["en-GB", "de"] });
    const { create } = setup();
    const rt = create({ locales: undefined });
    await rt.ready;
    expect(rt.locale).toBe("en");
  });
});

describe("createRuntime: onRender (RFC 0004 §3.1)", () => {
  const bundled = { manifest: r1.manifest, artifacts: r1.parsed };

  it("shows every t() render to the hook, which may decorate it", () => {
    const { create } = setup();
    const rt = create({ bundled });
    const seen: Render[] = [];
    const off = rt.onRender((r) => {
      seen.push(r);
      return `[${r.output}]`;
    });
    expect(rt.t("cart.checkout")).toBe("[Zur Kassa]");
    expect(rt.t("greeting", { name: "Lina" })).toBe("[Hallo Lina!]");
    expect(rt.t("nope", {}, { default: "Nein" })).toBe("[Nein]");
    expect(seen).toEqual([
      { id: "cart.checkout", locale: "de-AT", values: undefined, output: "Zur Kassa" },
      { id: "greeting", locale: "de", values: { name: "Lina" }, output: "Hallo Lina!" },
      { id: "nope", locale: undefined, values: {}, output: "Nein" },
    ]);
    off();
    expect(rt.t("cart.checkout")).toBe("Zur Kassa");
  });

  it("leaves parts() alone: components mark their host elements instead", () => {
    const { create } = setup();
    const rt = create({ bundled });
    const hook = vi.fn(() => "x");
    rt.onRender(hook);
    expect(rt.parts("cart.checkout")).toEqual([{ type: "text", value: "Zur Kassa" }]);
    expect(hook).not.toHaveBeenCalled();
  });

  it("chains hooks in order, keeps the output of a hook that returns nothing", () => {
    const { create } = setup();
    const rt = create({ bundled });
    rt.onRender(() => undefined);
    rt.onRender((r) => `<${r.output}>`);
    rt.onRender((r) => `(${r.output})`);
    expect(rt.t("cart.checkout")).toBe("(<Zur Kassa>)");
  });

  it("contains a hook that throws", () => {
    const { create } = setup();
    const rt = create({ bundled });
    rt.onRender(() => {
      throw new Error("bug");
    });
    expect(rt.t("cart.checkout")).toBe("Zur Kassa");
  });

  it("reports whether a hook is installed and notifies subscribers when one comes or goes", () => {
    const { create } = setup();
    const rt = create({ bundled });
    const seen: boolean[] = [];
    rt.subscribe(() => seen.push(rt.hooked));
    expect(rt.hooked).toBe(false);
    const off = rt.onRender(() => undefined);
    expect(rt.hooked).toBe(true);
    off();
    expect(rt.hooked).toBe(false);
    expect(seen).toEqual([true, false]);
  });

  it("reports the active manifest's environment, so a capture can refuse production", async () => {
    const { create } = setup();
    const rt = create();
    expect(rt.environment).toBeUndefined(); // nothing active yet
    await rt.ready;
    expect(rt.environment).toBe("production");
    const staging = { manifest: { ...r1.manifest, environment: "staging" }, artifacts: r1.parsed };
    expect(create({ bundled: staging, edge: undefined, environment: "staging" }).environment).toBe(
      "staging",
    );
  });

  it("adds itself to the page's registry, hooked by a capture session that put it there", () => {
    vi.stubGlobal("document", { addEventListener() {}, removeEventListener() {} });
    const { create } = setup();
    const g = globalThis as Record<symbol, unknown[]>;
    const key = Symbol.for("glossa.runtimes");
    const seen: unknown[] = [];
    const registry: unknown[] = [];
    Object.defineProperty(registry, "push", {
      configurable: true,
      value: (rt: unknown) => (seen.push(rt), Array.prototype.push.call(registry, rt)),
    });
    g[key] = registry;
    const rt = create({ bundled });
    expect(seen).toEqual([rt]);
    expect(registry).toEqual([rt]);
    rt.dispose();
    expect(registry).toEqual([]);
    delete g[key];
    vi.unstubAllGlobals();
  });
});

describe("createRuntime: override (RFC 0004 §5.3)", () => {
  const preview = { ...r1.manifest, environment: "preview" };
  const bundled = { manifest: preview, artifacts: r1.parsed };
  const edited = text("Jetzt bezahlen");

  it("renders the override through t() and parts() until it's cleared, and notifies subscribers", () => {
    const { create } = setup();
    const rt = create({ bundled, environment: "preview", locales: ["de"] });
    let notified = 0;
    rt.subscribe(() => notified++);
    expect(rt.override("cart.checkout", "de", edited)).toBe(true);
    expect(rt.t("cart.checkout")).toBe("Jetzt bezahlen");
    expect(rt.parts("cart.checkout")).toEqual([{ type: "text", value: "Jetzt bezahlen" }]);
    expect(rt.t("only.de")).toBe("Nur Deutsch");
    expect(rt.override("cart.checkout", "de")).toBe(true);
    expect(rt.t("cart.checkout")).toBe("Zur Kasse");
    expect(notified).toBe(2);
  });

  it("formats the override with values, and in the locale it was set for", () => {
    const { create } = setup();
    const rt = create({ bundled, environment: "preview", locales: ["de-AT"] });
    rt.override("greeting", "de", {
      type: "message",
      declarations: [],
      pattern: ["Servus ", { type: "expression", arg: { type: "variable", name: "name" } }],
    });
    expect(rt.t("greeting", { name: "Lina" })).toBe("Servus Lina");
    // de-AT has its own cart.checkout, which a de override doesn't replace.
    rt.override("cart.checkout", "de", edited);
    expect(rt.t("cart.checkout")).toBe("Zur Kassa");
  });

  it("fills a message the locale doesn't have yet, and explain agrees", () => {
    const { create } = setup();
    const rt = create({ bundled, environment: "preview", locales: ["de-AT"] });
    const hooked: Render[] = [];
    rt.onRender((r) => void hooked.push(r));
    rt.override("only.de", "de_at", text("Nur Österreich"));
    expect(rt.t("only.de")).toBe("Nur Österreich");
    expect(hooked.at(-1)?.locale).toBe("de-AT");
    expect(rt.explain("only.de").resolvedFrom).toBe("de-AT");
  });

  it("refuses in production", () => {
    const { create } = setup();
    const rt = create({ bundled: { manifest: r1.manifest, artifacts: r1.parsed } });
    const listener = vi.fn();
    rt.subscribe(listener);
    expect(rt.override("cart.checkout", "de-AT", edited)).toBe(false);
    expect(rt.t("cart.checkout")).toBe("Zur Kassa");
    expect(listener).not.toHaveBeenCalled();
  });
});

describe("createRuntime: explain", () => {
  it("reports the chain, the steps taken, the release and the source", async () => {
    const { create } = setup();
    const rt = create();
    await rt.ready;
    expect(rt.explain("only.de")).toEqual({
      id: "only.de",
      requested: ["de-AT"],
      locale: "de-AT",
      chain: ["de-AT", "de", "en"],
      resolvedFrom: "de",
      release: { id: "rel_1", version: 1 },
      source: "network",
      steps: [
        { locale: "de-AT", outcome: "missing" },
        { locale: "de", outcome: "found" },
      ],
      rollout: null,
    });
    expect(rt.explain("nope")).toMatchObject({ resolvedFrom: null, source: "inline" });
  });

  it("explains other locales without loading anything", async () => {
    const { create, edge } = setup();
    const rt = create();
    await rt.ready;
    const before = edge.requests.length;
    expect(rt.explain("cart.checkout", ["ar"])).toMatchObject({
      requested: ["ar"],
      locale: "ar",
      chain: ["ar", "en", "de"],
      resolvedFrom: "en",
      steps: [
        { locale: "ar", outcome: "not-loaded" },
        { locale: "en", outcome: "found" },
      ],
    });
    expect(edge.requests.length).toBe(before);
    expect(rt.locale).toBe("de-AT");
  });

  it("explains an empty runtime", () => {
    const rt = createRuntime({ locales: "de" });
    expect(rt.explain("x")).toEqual({
      id: "x",
      requested: ["de"],
      locale: null,
      chain: [],
      resolvedFrom: null,
      release: null,
      source: "inline",
      steps: [],
      rollout: null,
    });
  });
});

describe("createRuntime: errors", () => {
  it("reports missing messages and format errors with context, never throwing", async () => {
    const broken: Message = { type: "select", declarations: [], selectors: [], variants: [] };
    const bad = release("rel_f", 1, {
      de: {
        greeting,
        broken,
        fn: {
          type: "message",
          declarations: [],
          pattern: [{ type: "expression", function: { type: "function", name: "nope" } }],
        },
      },
    });
    const { create, edge, errors } = setup({ locales: ["de"] });
    edge.serve(served(bad));
    const rt = create();
    await rt.ready;
    expect(rt.t("greeting")).toBe("Hallo {$name}!");
    expect(rt.t("fn")).toBe("{:nope}");
    expect(rt.t("broken")).toBe("{�}");
    expect(rt.t("gone")).toBe("gone");
    expect(errors).toEqual([
      {
        type: "format",
        detail: "unresolved-variable $name",
        messageId: "greeting",
        locale: "de",
        releaseId: "rel_f",
      },
      {
        type: "format",
        detail: "unknown-function :nope",
        messageId: "fn",
        locale: "de",
        releaseId: "rel_f",
      },
      {
        type: "format",
        detail: "bad-message �",
        messageId: "broken",
        locale: "de",
        releaseId: "rel_f",
      },
      {
        type: "missing-message",
        detail: "not in de",
        messageId: "gone",
        locale: "de",
        releaseId: "rel_f",
      },
    ]);
  });

  it("rate-limits repeats of the same error", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    const { create, errors } = setup({ errorInterval: 1000 });
    const rt = create();
    await rt.ready;
    for (let i = 0; i < 5; i++) rt.t("gone");
    rt.t("also.gone");
    expect(errors.map((e) => e.messageId)).toEqual(["gone", "also.gone"]);
    vi.advanceTimersByTime(1001);
    rt.t("gone");
    expect(errors).toHaveLength(3);
  });

  it("isolates listeners that throw, and supports late listeners", async () => {
    const { create } = setup({
      onError: () => {
        throw new Error("listener bug");
      },
    });
    const rt = create();
    const late: string[] = [];
    const off = rt.onError((e) => late.push(e.type));
    rt.subscribe(() => {
      throw new Error("subscriber bug");
    });
    await rt.ready;
    expect(rt.t("gone")).toBe("gone");
    off();
    rt.t("gone.too");
    expect(late).toEqual(["missing-message"]);
  });

  it("rejects a manifest for another environment as a schema error", async () => {
    const staging = release("rel_s", 5, { de: { "cart.checkout": text("Staging") } });
    const manifest = { ...staging.manifest, environment: "staging" };
    const { create, edge, errors } = setup();
    edge.serve(served({ ...staging, manifest }));
    const rt = create();
    await rt.ready;
    expect(rt.release).toBeUndefined();
    expect(errors.map((e) => [e.type, e.releaseId])).toEqual([["schema", "rel_s"]]);
    const bundledStaging = { manifest, artifacts: staging.parsed };
    expect(create({ bundled: bundledStaging, edge: undefined }).release).toBeUndefined();
    const configured = create({ bundled: bundledStaging, edge: undefined, environment: "staging" });
    expect(configured.t("cart.checkout")).toBe("Staging");
  });

  it("drops a message that isn't a data-model message: schema error, then it resolves as missing", async () => {
    const odd = release(
      "rel_m",
      1,
      {
        de: { "cart.checkout": { type: "bogus" } as unknown as Message, ok: text("Gut") },
        en: { "cart.checkout": text("Checkout") },
      },
      { fallback: { "*": ["en"] } },
    );
    const { create, edge, errors } = setup({ locales: ["de"] });
    edge.serve(served(odd));
    const rt = create();
    await rt.ready;
    expect(rt.release?.id).toBe("rel_m");
    expect(rt.t("cart.checkout")).toBe("Checkout");
    expect(rt.explain("cart.checkout").resolvedFrom).toBe("en");
    expect(rt.t("ok")).toBe("Gut");
    expect(errors).toEqual([
      expect.objectContaining({ type: "schema", messageId: "cart.checkout", releaseId: "rel_m" }),
    ]);
    const bundledOdd = { manifest: odd.manifest, artifacts: odd.parsed };
    expect(create({ bundled: bundledOdd, edge: undefined }).t("cart.checkout")).toBe("Checkout");
  });

  it("never renders an empty string: it falls through to the inline default or the ID", async () => {
    const empty = release("rel_e", 1, {
      de: { blank: { type: "message", declarations: [], pattern: [] } },
    });
    const { create, edge } = setup({ locales: ["de"] });
    edge.serve(served(empty));
    const rt = create();
    await rt.ready;
    expect(rt.t("blank")).toBe("blank");
    expect(rt.t("blank", {}, { default: "Leer" })).toBe("Leer");
    expect(rt.parts("blank", {}, { default: "Leer" })).toEqual([{ type: "text", value: "Leer" }]);
  });

  it("reports a non-JSON manifest as a schema error", async () => {
    const { create, edge, errors } = setup();
    edge.serve({ manifest: { status: 200, etag: '"x"', body: undefined as never }, artifacts: {} });
    await create().ready;
    expect(errors.map((e) => e.type)).toEqual(["schema"]);
  });
});

describe("createRuntime: background refresh", () => {
  it("refreshes on the injected timer and stops on dispose", async () => {
    let tick: (() => void) | undefined;
    const cancel = vi.fn();
    const { create, edge } = setup();
    const rt = create({
      refreshInterval: 60_000,
      timer: (f, ms) => {
        expect(ms).toBe(60_000);
        tick = f;
        return cancel;
      },
    });
    await rt.ready;
    edge.serve(served(r2));
    tick!();
    await rt.refresh();
    expect(rt.release?.id).toBe("rel_2");
    rt.dispose();
    expect(cancel).toHaveBeenCalledOnce();
  });

  it("refreshes when the page becomes visible", async () => {
    const listeners = new Map<string, () => void>();
    const doc = {
      visibilityState: "hidden",
      addEventListener: (type: string, f: () => void) => listeners.set(type, f),
      removeEventListener: (type: string) => listeners.delete(type),
    };
    vi.stubGlobal("document", doc);
    const { create, edge } = setup();
    const rt = create();
    await rt.ready;
    edge.serve(served(r2));
    const before = edge.requests.length;
    listeners.get("visibilitychange")!();
    expect(edge.requests.length).toBe(before);
    doc.visibilityState = "visible";
    listeners.get("visibilitychange")!();
    await rt.refresh();
    expect(rt.release?.id).toBe("rel_2");
    rt.dispose();
    expect(listeners.size).toBe(0);
  });

  it("uses a default unref'd interval that dispose clears", async () => {
    vi.useFakeTimers();
    const { create, edge } = setup();
    const rt = create({ refreshInterval: undefined });
    await vi.waitFor(() => expect(rt.release?.id).toBe("rel_1"));
    edge.serve(served(r2));
    await vi.advanceTimersByTimeAsync(300_000);
    await vi.waitFor(() => expect(rt.release?.id).toBe("rel_2"));
    rt.dispose();
    expect(vi.getTimerCount()).toBe(0);
  });
});
