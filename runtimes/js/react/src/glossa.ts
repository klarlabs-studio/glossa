/**
 * The React provider and hooks. One runtime (runtimes/SPEC.md) per app, or
 * one shared by several roots; components re-render through
 * `useSyncExternalStore` whenever a new release or locale activates.
 *
 * The store's snapshot is a view: `t`, `parts`, `explain`, `locale`, `dir` …
 * bound to a runtime. It is rebuilt when the active release or locale
 * changes, so its identity says "something to re-render" and components that
 * memoize on `t` recompute.
 *
 * SSR: nothing touches browser globals at import or render time, and React
 * never subscribes on the server, so a runtime shared across requests
 * collects no listeners. Hydration renders the server snapshot. On the client
 * that is a second, bundled-only runtime: exactly what the server rendered
 * from the same `bundled` release and `locales`, even if a newer persisted or
 * network release activated before React got to hydrate. React then renders
 * the live view and patches the difference.
 */
import { createContext, createElement, useContext, useSyncExternalStore } from "react";
import type { ReactNode } from "react";
import { createRuntime } from "@klarlabs-studio/glossa-runtime";
import type {
  Explanation,
  ManifestLocale,
  Part,
  Runtime,
  RuntimeOptions,
  TranslateOptions,
} from "@klarlabs-studio/glossa-runtime";

export interface GlossaOptions extends RuntimeOptions {
  /** Use this runtime instead of creating one (e.g. one runtime shared by several React roots). */
  runtime?: Runtime;
}

/** What `<GlossaProvider glossa>` takes. */
export interface Glossa {
  readonly runtime: Runtime;
  /** @internal The external store `useSyncExternalStore` reads. */
  readonly store: Store;
}

/** The values a message takes; `{}` for none. */
export type MessageValues = Record<string, unknown>;

/**
 * Message ID → its values: the shape `glossa generate` emits. Register it once
 * to type every `useGlossa().t` call and `<T id>` (see README):
 * `declare module "@klarlabs-studio/glossa-react" { interface GlossaRegister { messages: Messages } }`.
 */
export interface GlossaRegister {}

/** The registered message map, or any string ID with any values. */
export type RegisteredMessages = GlossaRegister extends { messages: infer M }
  ? M
  : Record<string, MessageValues>;

/** Values are optional for messages that take none, required otherwise. */
type Args<V> = {} extends V
  ? [values?: V, opts?: TranslateOptions]
  : [values: V, opts?: TranslateOptions];

export interface UseGlossa<M = RegisteredMessages> {
  /** Render a message; never throws, never empty. */
  t<K extends keyof M & string>(id: K, ...args: Args<M[K]>): string;
  /** The same as parts, for custom rendering. */
  parts<K extends keyof M & string>(id: K, ...args: Args<M[K]>): Part[];
  /** Why `id` renders the way it does (SPEC §6). */
  explain(id: string, locales?: string | readonly string[]): Explanation;
  /** Switch locales; resolves once the new locale is active. */
  setLocales(locales: string | readonly string[]): Promise<void>;
  /** The active locale, once a release is active. */
  locale: string | undefined;
  dir: "ltr" | "rtl";
  release: { id: string; version: number } | undefined;
  /** The active release's locales, for a locale picker. */
  availableLocales: readonly ManifestLocale[];
  runtime: Runtime;
}

/** @internal The external store behind the hooks. */
export interface Store {
  subscribe(onChange: () => void): () => void;
  snapshot(): View;
  serverSnapshot(): View;
}

const browser = () => typeof document !== "undefined";

/**
 * @internal What the store hands out: the public view plus whether `rt` has
 * an `onRender` hook, which `<T>` reads (RFC 0004 §3.1). During hydration it's
 * the hydration runtime's, which never has one, so hydration matches.
 */
export type View = UseGlossa<unknown> & { hooked: boolean };

/** A view of `rt` as of now; actions go to the app's runtime `live`. */
const view = (rt: Runtime, live: Runtime): View =>
  ({
    t: (id: string, values?: MessageValues, opts?: TranslateOptions) => rt.t(id, values, opts),
    parts: (id: string, values?: MessageValues, opts?: TranslateOptions) =>
      rt.parts(id, values, opts),
    explain: (id, locales) => rt.explain(id, locales),
    setLocales: (locales) => live.setLocales(locales),
    locale: rt.locale,
    dir: rt.dir,
    release: rt.release,
    availableLocales: rt.availableLocales,
    runtime: live,
    hooked: rt.hooked,
  }) as View;

/** Create the instance for `<GlossaProvider glossa={createGlossa({ edge, deliveryKey, locales, bundled })}>`. */
export function createGlossa(options: GlossaOptions = {}): Glossa {
  const { runtime: given, ...rest } = options;
  const runtime = given ?? createRuntime(rest);
  let key: string | undefined;
  let current: View;
  let hydration: View | undefined;
  const snapshot = () => {
    // Rendering is a function of the active release and locale, and of
    // whether a capture session's hook is installed; rebuild the view only
    // when they change, so the snapshot is stable in between.
    const k = `${runtime.release?.id} ${runtime.locale} ${runtime.hooked}`;
    if (k !== key) {
      key = k;
      current = view(runtime, runtime);
    }
    return current;
  };
  return {
    runtime,
    store: {
      subscribe: (onChange) => runtime.subscribe(onChange),
      snapshot,
      serverSnapshot: () =>
        !browser() || given
          ? snapshot()
          : (hydration ??= view(
              createRuntime({
                bundled: rest.bundled,
                locales: rest.locales,
                environment: rest.environment,
                bidiIsolation: rest.bidiIsolation,
                functions: rest.functions,
                storage: null,
              }),
              runtime,
            )),
    },
  };
}

const Context = createContext<Glossa | undefined>(undefined);

/** Makes `glossa` available to `useGlossa()`, `useMessages()` and `<T>` below it. */
export function GlossaProvider(props: { glossa: Glossa; children?: ReactNode }): ReactNode {
  return createElement(Context.Provider, { value: props.glossa }, props.children);
}

let fallback: Glossa | undefined;

function useStore(): Store {
  const glossa = useContext(Context);
  if (glossa) return glossa.store;
  if (!fallback) {
    console.warn(
      "[glossa] useGlossa() outside <GlossaProvider glossa={createGlossa(…)}>: rendering inline defaults.",
    );
    fallback = createGlossa({ storage: null, locales: [] });
  }
  return fallback.store;
}

/** `t`, `parts`, `locale`, `dir`, `setLocales`, `explain` for the current provider; re-renders on every activation. */
export function useGlossa(): UseGlossa {
  return useMessages();
}

/**
 * `useGlossa()` typed by a message map: `useMessages<Messages>()` checks IDs
 * and values at compile time. `Messages` is what `glossa generate` emits
 * (message ID → values).
 */
export function useMessages<M = RegisteredMessages>(): UseGlossa<M> {
  const store = useStore();
  return useSyncExternalStore(
    store.subscribe,
    store.snapshot,
    store.serverSnapshot,
  ) as UseGlossa<M>;
}
