/**
 * The runtime contract (runtimes/SPEC.md §3–§6): load a release from memory,
 * persisted last-good, the edge or the bundle; resolve the locale and its
 * fallback chain; format; explain. Nothing here throws into application
 * code: failures fall through to the next source and go to the error channel.
 */
import { formatToParts, partsToString } from "./format.js";
import type { FormatOptions, Part } from "./format.js";
import { canonicalLocales, fallbackChain, lookupLocale, navigatorLanguages } from "./locale.js";
import type { Artifact, Manifest, ManifestLocale } from "./manifest.js";
import type { Message } from "./model.js";
import { webStorage } from "./storage.js";
import type { RuntimeStorage } from "./storage.js";
import { checkManifest, cohort as cohortOf, hex, sha256Hex, verifySignature } from "./verify.js";
import type { PublicKey } from "./verify.js";

/** Where the rendered text came from (SPEC §6). */
export type Source = "memory" | "persisted" | "network" | "bundled" | "inline";

/** An entry on the error channel (SPEC §6). */
export interface RuntimeError {
  type: "network" | "integrity" | "signature" | "schema" | "format" | "missing-message";
  detail: string;
  messageId?: string;
  locale?: string;
  releaseId?: string;
}

/** The subset of a `fetch` Response the runtime reads. */
export interface TransportResponse {
  status: number;
  headers: { get(name: string): string | null };
  text(): Promise<string>;
}

/** Fetches a URL; `fetch` itself is one. Rejections count as network errors. */
export type Transport = (
  url: string,
  init: { headers: Record<string, string> },
) => Promise<TransportResponse>;

/** A release shipped with the build (`glossa pull --release`), artifacts keyed by SHA-256. */
export interface BundledRelease {
  manifest: Manifest;
  artifacts: Record<string, Artifact>;
}

/** What storage holds: the last release that loaded and verified completely. */
export interface PersistedRelease {
  etag?: string;
  manifest: Manifest;
  /** SHA-256 → the artifact's exact bytes. */
  artifacts: Record<string, string>;
  /** The installation id (SPEC §1.4), once a rollout has needed one. */
  installation?: string;
}

export interface RuntimeOptions {
  /** Edge origin, e.g. `https://edge.example.com`. With `deliveryKey`, enables network loading. */
  edge?: string;
  /** The project's publishable delivery key. */
  deliveryKey?: string;
  /** Default `"production"`. */
  environment?: string;
  /** Requested locales, most preferred first, e.g. `resolveLocales(…)`. Default: the browser's. */
  locales?: string | readonly string[];
  /** Catalogs shipped with the build: rendered synchronously, and the last resort offline. */
  bundled?: BundledRelease;
  /** Trusted signing keys. When set, manifests without a valid signature are rejected. */
  publicKeys?: readonly PublicKey[];
  /** Where last-good persists. Default: `localStorage` in browsers; `null` disables it. */
  storage?: RuntimeStorage | null;
  /** Default: `fetch`. */
  transport?: Transport;
  /** Background manifest refresh in ms. Default 5 minutes; `0` disables it. */
  refreshInterval?: number;
  /** Schedules the background refresh and returns a cancel function. Default: `setInterval`. */
  timer?: (tick: () => void, ms: number) => () => void;
  /** Bidi isolation of placeholders; the MF2 default (`"default"`) isolates them. */
  bidiIsolation?: FormatOptions["bidiIsolation"];
  /** Custom MF2 functions, merged over the built-ins. */
  functions?: FormatOptions["functions"];
  /**
   * Opt-in: parse the inline `default` of `t()`/`parts()` as a message (e.g. `parseMF2` from
   * `@klarlabs-studio/glossa/messageformat`) and format it with the call's values, so a
   * fallback like `"Hello {$name}"` interpolates. Absent (the default), the inline default is
   * rendered literally. A default that fails to parse or format renders literally and is
   * reported on the error channel.
   */
  parseDefault?: (source: string) => Message;
  /** Error channel listener; more can be added with `onError()`. */
  onError?: (error: RuntimeError) => void;
  /** An identical error is reported at most once per this many ms. Default 60 s. */
  errorInterval?: number;
  /**
   * Staged rollout support (SPEC §1.4), on by default. `false` ignores a
   * manifest's `rollout`: always the stable release, never a candidate fetch.
   */
  rollout?: boolean;
  /**
   * This installation's cohort key under a staged rollout. Default: a random
   * 128-bit id, created the first time a rollout is read and kept in
   * `storage` (in memory without one).
   */
  installationId?: string;
}

/** A staged rollout as `explain()` reports it (SPEC §6). */
export interface RolloutInfo {
  id: string;
  percent: number;
  /** This installation's cohort, 0–9999. */
  cohort: number;
  /**
   * The active side. `stable` with `cohort < percent × 100` is a candidate
   * that failed to activate.
   */
  side: "stable" | "candidate";
}

export interface TranslateOptions {
  /** Inline default rendered when no locale in the chain has the message. */
  default?: string;
}

/** What an `onRender` hook sees: one `t()` render (RFC 0004 §3.1). */
export interface Render {
  id: string;
  /** The locale the message resolved from; undefined when the inline default or the ID rendered. */
  locale: string | undefined;
  /**
   * The values, as given. A hook that keeps a log digests them (the capture
   * module does); the runtime doesn't, to keep this extension point under
   * 100 bytes.
   */
  values: Record<string, unknown> | undefined;
  /** The rendered string. */
  output: string;
}

/**
 * Sees every `t()` render and may decorate it: a returned string replaces the
 * output. Only capture and editor sessions install one (RFC 0004 §5.1).
 */
export type RenderHook = (render: Render) => string | void;

export interface ExplainStep {
  locale: string;
  outcome: "found" | "missing" | "not-loaded";
}

/** Why a message renders the way it does (SPEC §6). */
export interface Explanation {
  id: string;
  requested: string[];
  locale: string | null;
  chain: string[];
  /** `null` when the inline default (or the message ID) is used. */
  resolvedFrom: string | null;
  release: { id: string; version: number } | null;
  source: Source;
  steps: ExplainStep[];
  /** The active manifest's staged rollout; `null` without a valid one, or with `rollout: false`. */
  rollout: RolloutInfo | null;
}

export interface Runtime {
  /** Settles when the first load (persisted, then network) is done. Never rejects. */
  readonly ready: Promise<void>;
  /** The active locale, once a release is active. */
  readonly locale: string | undefined;
  /** The active locale's direction, for `dir` attributes. */
  readonly dir: "ltr" | "rtl";
  readonly release: { id: string; version: number } | undefined;
  /**
   * The active release's environment, from its manifest (covered by the
   * signature when `publicKeys` are set); undefined until one is active. The
   * overlay loader refuses to start unless every runtime on the page reports
   * one that isn't `production` (RFC 0004 §5.1), and a capture session refuses
   * a page that reports `production` (RFC 0004 §10).
   */
  readonly environment: string | undefined;
  /** The locales the active release offers (its manifest's `locales`), e.g. for a locale picker. */
  readonly availableLocales: readonly ManifestLocale[];
  /** Render a message as a string. */
  t(id: string, values?: Record<string, unknown>, opts?: TranslateOptions): string;
  /** Render a message as parts (text, markup, bidi isolates, fallbacks, values). */
  parts(id: string, values?: Record<string, unknown>, opts?: TranslateOptions): Part[];
  /** Whether the active release has `id` in its locale chain: no formatting, no error report, no `explain()` cost. */
  has(id: string): boolean;
  /** How `id` resolves for the active (or the given) locales. Loads nothing. */
  explain(id: string, locales?: string | readonly string[]): Explanation;
  /** Switch the requested locales; the switch is atomic once their artifacts are loaded. */
  setLocales(locales: string | readonly string[]): Promise<void>;
  /** Revalidate the manifest now. Concurrent calls share one request. */
  refresh(): Promise<void>;
  /** Called after every activation (new release or locale) and when an `onRender` hook comes or goes, e.g. to re-render. */
  subscribe(listener: () => void): () => void;
  /**
   * Install a render hook (RFC 0004 §3.1): capture and editor sessions only.
   * Subscribers are notified, so the page re-renders with (and, after the
   * returned unsubscribe, without) the hook.
   */
  onRender(hook: RenderHook): () => void;
  /** Whether an `onRender` hook is installed; components mark their host elements only then. */
  readonly hooked: boolean;
  /**
   * Live preview for the in-product editor (RFC 0004 §5.3), never in
   * production: render `model` for `id` in `locale` instead of the release's
   * message, until it's cleared (no `model`). Subscribers are notified, so
   * the page re-renders. The locale must be on the active fallback chain to
   * show. Returns `false`, and changes nothing, when the runtime's
   * environment is `production`.
   */
  override(id: string, locale: string, model?: Message): boolean;
  /** Listen on the error channel. */
  onError(listener: (error: RuntimeError) => void): () => void;
  /** Stop background refresh and drop listeners. */
  dispose(): void;
}

interface Active {
  /** The active view (SPEC §1.4): the manifest, or its candidate view. */
  m: Manifest;
  /** The manifest as served, `rollout` included. */
  raw: Manifest;
  r: RolloutInfo | null;
  etag?: string;
  source: Source;
  requested: string[];
  locale: string;
  chain: string[];
  catalogs: Array<[string, Record<string, Message>]>;
}

/** 128 bits from a cryptographically secure source, as 32 lowercase hex digits. */
const randomId = () => hex(crypto.getRandomValues(new Uint8Array(16)));

const list = (l: string | readonly string[]) => canonicalLocales(typeof l === "string" ? [l] : l);

const shas = (m: Manifest, locale: string) =>
  Object.values((Object.hasOwn(m.artifacts, locale) && m.artifacts[locale]) || {}).map(
    (a) => a.sha256,
  );

/** The active locale and its fallback chain for `requested` (SPEC §4). */
const plan = (m: Manifest, requested: string[]): [string, string[]] => {
  const locale =
    lookupLocale(
      requested,
      m.locales.map((l) => l.code),
    ) ?? m.sourceLocale;
  return [locale, fallbackChain(locale, m)];
};

/** Create a runtime. Everything is optional: with nothing configured it renders inline defaults. */
export function createRuntime(o: RuntimeOptions = {}): Runtime {
  const keys = o.publicKeys ?? [];
  const transport: Transport = o.transport ?? ((url, init) => fetch(url, init));
  const base = o.edge && o.deliveryKey && `${o.edge.replace(/\/+$/, "")}/v1/${o.deliveryKey}`;
  const env = o.environment ?? "production";
  const storeKey = `glossa:${o.deliveryKey}:${env}`;
  let storage = o.storage;
  if (storage === undefined && "document" in globalThis) {
    try {
      storage = webStorage();
    } catch {
      // No localStorage (sandboxed frame, disabled storage): memory only.
    }
  }
  const cache = new Map<string, { a: Artifact; text?: string }>();
  const subscribers = new Set<() => void>();
  const listeners = new Set<(e: RuntimeError) => void>(o.onError ? [o.onError] : []);
  const hooks = new Set<RenderHook>();
  /** Editor overrides (never in production), keyed `locale id`. */
  const edits = new Map<string, Message>();
  let from: string | undefined;
  const reported = new Map<string, number>();
  let requested = list(o.locales ?? navigatorLanguages() ?? []);
  let state: Active | undefined;
  let record: Promise<PersistedRelease | undefined> | undefined;
  let queue: Promise<unknown> = Promise.resolve();
  let inflight: Promise<void> | undefined;
  let iid: string | undefined;

  const call = <A extends unknown[]>(fs: Iterable<(...a: A) => void>, ...args: A) => {
    for (const f of fs) {
      try {
        f(...args);
      } catch {
        // A listener's bug must not break loading or rendering.
      }
    }
  };

  const emit = (e: RuntimeError) => {
    const key = JSON.stringify(e);
    const now = Date.now();
    if (now - (reported.get(key) ?? -Infinity) < (o.errorInterval ?? 60_000)) return;
    if (reported.size > 999) reported.clear();
    reported.set(key, now);
    call(listeners, e);
  };

  /** Activations run one at a time, so the last one to commit is the last one asked for. */
  const run = (task: () => Promise<unknown>) =>
    (queue = queue.then(task).catch(() => {})) as Promise<void>;

  const persisted = () =>
    (record ??= (async () => {
      try {
        return ((await storage?.get(storeKey)) ?? undefined) as PersistedRelease | undefined;
      } catch {
        return undefined;
      }
    })());

  /** Persist manifest `m` as served, with the artifacts of its active view `v`. */
  const persist = async (m: Manifest, v: Manifest, etag?: string) => {
    const old = (await persisted())?.artifacts ?? {};
    const artifacts: Record<string, string> = {};
    for (const l of Object.keys(v.artifacts)) {
      for (const s of shas(v, l)) {
        const text = cache.get(s)?.text ?? old[s];
        if (text !== undefined) artifacts[s] = text;
      }
    }
    const r: PersistedRelease = { etag, manifest: m, artifacts, installation: iid };
    record = Promise.resolve(r);
    try {
      await storage?.set(storeKey, r);
    } catch {
      // Quota or storage errors: this process keeps working from memory.
    }
  };

  /** GET a URL. 200 → body and ETag, 304 → no body, anything else → a network error. */
  const get = async (url: string, headers: Record<string, string> = {}, releaseId?: string) => {
    try {
      const res = await transport(url, { headers });
      if (res.status === 304) return {};
      if (res.status !== 200) throw `HTTP ${res.status}`;
      return { text: await res.text(), etag: res.headers.get("etag") ?? undefined };
    } catch (e) {
      emit({ type: "network", detail: `${url}: ${String(e)}`, releaseId });
      return undefined;
    }
  };

  /**
   * Cache an artifact, keeping only the messages that are MF2 data-model
   * messages. Each other one is a `schema` error and resolves as missing
   * (SPEC §3), so one bad message doesn't block the release.
   */
  const keep = (sha: string, a: Artifact, releaseId: string, text?: string) => {
    const messages: Record<string, Message> = {};
    for (const [id, m] of Object.entries(a.messages)) {
      const x = m as unknown as Record<string, unknown> | null;
      if (
        x?.type === "message"
          ? Array.isArray(x.pattern)
          : x?.type === "select" && Array.isArray(x.selectors) && Array.isArray(x.variants)
      ) {
        messages[id] = m;
      } else {
        const detail = "not an MF2 data-model message";
        emit({ type: "schema", detail, messageId: id, locale: a.locale, releaseId });
      }
    }
    cache.set(sha, { a: { ...a, messages }, text });
  };

  /** Verify an artifact's bytes against its hash and cache it (SPEC §1.3). */
  const accept = async (sha: string, text: string | undefined, releaseId: string) => {
    if (text === undefined) return false;
    if ((await sha256Hex(text)) !== sha) {
      emit({ type: "integrity", detail: `artifact ${sha} doesn't match its hash`, releaseId });
      return false;
    }
    try {
      const a = JSON.parse(text) as Artifact;
      if (!/^glossa\.artifact\/v1\b/.test(a.schema) || typeof a.messages !== "object") throw 0;
      keep(sha, a, releaseId, text);
      return true;
    } catch {
      emit({ type: "schema", detail: `artifact ${sha} isn't a v1 artifact`, releaseId });
      return false;
    }
  };

  /**
   * One artifact. Artifacts are content-addressed, so any source with the
   * hash will do: memory, persisted, bundled, and the network only for hashes
   * found nowhere else (SPEC §3).
   */
  const load = async (sha: string, releaseId: string) => {
    if (cache.has(sha)) return true;
    if (await accept(sha, (await persisted())?.artifacts?.[sha], releaseId)) return true;
    const b = o.bundled?.artifacts[sha];
    if (b) {
      keep(sha, b, releaseId);
      return true;
    }
    return (
      !!base && accept(sha, (await get(`${base}/a/${sha}.json`, {}, releaseId))?.text, releaseId)
    );
  };

  /** A locale's messages from the cache, namespaces merged; undefined if one isn't loaded. */
  const catalog = (m: Manifest, locale: string) => {
    const parts = shas(m, locale).map((s) => cache.get(s)?.a.messages);
    return parts.includes(undefined)
      ? undefined
      : (Object.assign({}, ...parts) as Record<string, Message>);
  };

  /** Make view `m` of manifest `raw` active for `req`, if everything its chain needs is cached. */
  const commit = (
    m: Manifest,
    source: Source,
    etag?: string,
    req = requested,
    raw = m,
    r: RolloutInfo | null = null,
  ) => {
    const [locale, chain] = plan(m, req);
    const catalogs: Active["catalogs"] = [];
    for (const l of chain) {
      const c = catalog(m, l);
      if (!c) return false;
      catalogs.push([l, c]);
    }
    state = { m, raw, r, etag, source, requested: req, locale, chain, catalogs };
    const keep = new Set(Object.keys(m.artifacts).flatMap((l) => shas(m, l)));
    for (const s of cache.keys()) if (!keep.has(s)) cache.delete(s);
    call(subscribers);
    return true;
  };

  /**
   * This installation's cohort key (SPEC §1.4): the option, or the id kept with
   * the last-good release, or a new one, created the first time a rollout is
   * read and persisted with the release it activates.
   */
  const installation = async () =>
    (iid ||= o.installationId || (await persisted())?.installation || randomId());

  /**
   * Verify a manifest, load every artifact its active chain needs, then switch
   * to it in one step (SPEC §3: atomic activation). On any failure the
   * previous release keeps serving. The requested locales are pinned for the
   * whole activation; a `setLocales()` meanwhile queues its own activation.
   * Under a staged rollout an installation in the candidate tries the
   * candidate view first and, if it can't be activated, the stable view of
   * the same manifest (SPEC §1.4).
   */
  const activate = async (m: Manifest, source: Source, etag?: string) => {
    const req = requested;
    let problem = checkManifest(m, env);
    let type: RuntimeError["type"] = "schema";
    if (!problem && keys.length && m !== state?.raw) {
      problem = await verifySignature(m, keys);
      type = "signature";
    }
    if (problem) {
      emit({ type, detail: problem, releaseId: (m as Partial<Manifest> | null)?.release?.id });
      return false;
    }
    let r: RolloutInfo | null = null;
    const ro = o.rollout !== false && m.rollout;
    if (ro) {
      const { id, percent, salt, candidate: c } = ro;
      // `percent >>> 0 === percent`: an integer, and not negative.
      if (
        id &&
        /^[\w-]{22}$/.test(salt) &&
        percent >>> 0 === percent &&
        percent < 101 &&
        c?.release &&
        c.locales &&
        c.fallback &&
        c.artifacts
      ) {
        const cohort = await cohortOf(salt, await installation());
        r = { id, percent, cohort, side: "candidate" };
        if (cohort < percent * 100) {
          const { release, locales, fallback, artifacts } = c;
          const v = { ...m, release, locales, fallback, artifacts };
          problem = checkManifest(v);
          if (problem) emit({ type: "schema", detail: problem, releaseId: c.release.id });
          else if (await take(m, v, source, etag, req, r)) return true;
        }
        r = { ...r, side: "stable" };
      } else {
        emit({ type: "schema", detail: "invalid rollout", releaseId: m.release.id });
      }
    }
    return take(m, m, source, etag, req, r);
  };

  /** Load every artifact view `v` of manifest `m` needs for `req`, then activate it. */
  const take = async (
    m: Manifest,
    v: Manifest,
    source: Source,
    etag: string | undefined,
    req: string[],
    r: RolloutInfo | null,
  ) => {
    const needed = plan(v, req)[1].flatMap((l) => shas(v, l));
    const loaded = await Promise.all(needed.map((s) => load(s, v.release.id)));
    if (loaded.includes(false) || !commit(v, source, etag, req, m, r)) return false;
    if (source !== "bundled") await persist(m, v, etag);
    return true;
  };

  const cycle = async (initial: boolean) => {
    let activated = false;
    if (initial) {
      const r = await persisted();
      const bundledIsNewer =
        !!o.bundled && o.bundled.manifest.release.version > (r?.manifest?.release?.version ?? 0);
      if (r?.manifest && !bundledIsNewer)
        activated = await activate(r.manifest, "persisted", r.etag);
      // The bundle went active synchronously, as its stable view; its rollout
      // (SPEC §1.4) needs the cohort, which is computed asynchronously.
      else if (o.bundled?.manifest.rollout)
        activated = await activate(o.bundled.manifest, "bundled");
    }
    if (base) {
      const headers: Record<string, string> = state?.etag ? { "If-None-Match": state.etag } : {};
      const res = await get(`${base}/${env}/manifest.json`, headers);
      if (res?.text !== undefined) {
        let m: Manifest | undefined;
        try {
          m = JSON.parse(res.text) as Manifest;
        } catch {
          // Not JSON: checkManifest reports it as a schema error.
        }
        activated = (await activate(m!, "network", res.etag)) || activated;
      }
    }
    // Nothing new this time: what's active was already in memory.
    if (state && !initial && !activated) state.source = "memory";
  };

  const refresh = () =>
    (inflight ??= run(() => cycle(false)).finally(() => (inflight = undefined)));

  if (o.bundled && !checkManifest(o.bundled.manifest, env)) {
    const { manifest, artifacts } = o.bundled;
    for (const [s, a] of Object.entries(artifacts)) keep(s, a, manifest.release.id);
    commit(manifest, "bundled");
  }
  const ready = run(() => cycle(true));

  const every = o.refreshInterval ?? 300_000;
  const timer =
    o.timer ??
    ((tick: () => void, ms: number) => {
      const h = setInterval(tick, ms) as unknown as { unref?: () => void };
      h.unref?.(); // don't keep a server process alive
      return () => clearInterval(h as unknown as number);
    });
  const stop = base && every > 0 ? timer(() => void refresh(), every) : undefined;
  const doc = (globalThis as { document?: Document }).document;
  const onVisible = () => doc!.visibilityState === "visible" && void refresh();
  if (base) doc?.addEventListener?.("visibilitychange", onVisible);

  /**
   * Resolve `id` along the active chain (SPEC §4.3) and format it. Missing,
   * failing and empty renders fall through to the inline default, then the ID.
   */
  const fmt = (
    m: Message,
    locale: string | string[],
    values?: Record<string, unknown>,
    onError?: FormatOptions["onError"],
  ) => formatToParts(m, locale, values, { bidiIsolation: o.bidiIsolation, functions: o.functions, onError });

  const lookup = (id: string) =>
    state?.catalogs.find(([l, messages]) => edits.has(l + " " + id) || Object.hasOwn(messages, id));

  const resolve = (id: string, values?: Record<string, unknown>, opts?: TranslateOptions) => {
    from = undefined;
    try {
      const st = state;
      if (st) {
        const releaseId = st.m.release.id;
        const found = lookup(id);
        if (found) {
          const [locale, messages] = found;
          const message = edits.get(locale + " " + id) ?? messages[id]!;
          const parts = fmt(message, locale, values, (e) =>
            emit({
              type: "format",
              detail: `${e.type} ${e.source}`,
              messageId: id,
              locale,
              releaseId,
            }),
          );
          if (partsToString(parts)) return (from = locale), parts;
        } else {
          const detail = `not in ${st.chain.join(", ")}`;
          emit({ type: "missing-message", detail, messageId: id, locale: st.locale, releaseId });
        }
      }
    } catch {
      // Fall through to the inline default.
    }
    const d = opts?.default;
    if (d && o.parseDefault) {
      try {
        const parts = fmt(o.parseDefault(d), state?.chain ?? requested, values);
        if (partsToString(parts)) return parts;
      } catch (e) {
        emit({ type: "format", detail: String(e), messageId: id });
      }
    }
    return [{ type: "text", value: d || id }] as Part[];
  };

  const explain = (id: string, locales?: string | readonly string[]): Explanation => {
    const st = state;
    const req = locales === undefined ? (st?.requested ?? requested) : list(locales);
    if (!st) {
      const release = null;
      return {
        id,
        requested: req,
        locale: null,
        chain: [],
        resolvedFrom: null,
        release,
        source: "inline",
        steps: [],
        rollout: release,
      };
    }
    const { m } = st;
    const [locale, chain] = plan(m, req);
    const steps: ExplainStep[] = [];
    let resolvedFrom: string | null = null;
    for (const l of chain) {
      const c = catalog(m, l);
      const found = !!c && (edits.has(l + " " + id) || Object.hasOwn(c, id));
      steps.push({ locale: l, outcome: !c ? "not-loaded" : found ? "found" : "missing" });
      if (found) {
        resolvedFrom = l;
        break;
      }
    }
    const release = { id: m.release.id, version: m.release.version };
    const source = resolvedFrom ? st.source : "inline";
    const rollout = st.r;
    return { id, requested: req, locale, chain, resolvedFrom, release, source, steps, rollout };
  };

  const add = <T>(set: Set<T>, f: T) => (set.add(f), () => void set.delete(f));

  const t: Runtime["t"] = (id, values, opts) => {
    let output = partsToString(resolve(id, values, opts));
    const locale = from;
    for (const f of hooks) {
      try {
        output = f({ id, locale, values, output }) ?? output;
      } catch {
        // A hook's bug must not break rendering.
      }
    }
    return output;
  };

  // The page's runtimes, where `glossa capture` and the overlay loader
  // (`@klarlabs-studio/glossa-runtime/dev`) find them (RFC 0004 §3.2, §5.1). Browsers only, so
  // a server rendering per request keeps nothing. Production runtimes are
  // listed too: a capture session has to tell a production page from a page
  // without Glossa, and the loader checks every runtime's environment.
  const registry: Runtime[] | undefined = doc
    ? ((globalThis as Record<symbol, Runtime[]>)[Symbol.for("glossa.runtimes")] ??= [])
    : undefined;
  const rt: Runtime = {
    ready,
    get locale() {
      return state?.locale;
    },
    get dir() {
      const st = state;
      return st?.m.locales.find((l) => l.code === st.locale)?.direction ?? "ltr";
    },
    get release() {
      return state && { id: state.m.release.id, version: state.m.release.version };
    },
    get environment() {
      return state?.m.environment;
    },
    get availableLocales() {
      return state?.m.locales ?? [];
    },
    t,
    parts: resolve,
    has: (id) => !!lookup(id),
    explain,
    setLocales(locales) {
      requested = list(locales);
      return run(async () => {
        if (state) await activate(state.raw, state.source, state.etag);
      });
    },
    refresh,
    subscribe: (f) => add(subscribers, f),
    onError: (f) => add(listeners, f),
    onRender(f) {
      hooks.add(f);
      call(subscribers);
      return () => void (hooks.delete(f), call(subscribers));
    },
    get hooked() {
      return hooks.size > 0;
    },
    override(id, locale, model) {
      if (env == "production") return false;
      const k = list(locale)[0] + " " + id;
      if (model) edits.set(k, model);
      else edits.delete(k);
      call(subscribers);
      return true;
    },
    dispose() {
      stop?.();
      doc?.removeEventListener?.("visibilitychange", onVisible);
      subscribers.clear();
      listeners.clear();
      // Not listed: -1 >>> 0 is past the end, so nothing is removed.
      registry?.splice(registry.indexOf(rt) >>> 0, 1);
    },
  };
  registry?.push(rt);
  return rt;
}
