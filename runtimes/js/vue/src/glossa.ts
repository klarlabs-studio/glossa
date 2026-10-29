/**
 * The Vue plugin and composables. One runtime (runtimes/SPEC.md) per app, or
 * one shared by several apps (Astro islands); reactivity comes from a version
 * counter the runtime bumps on every activation, so `t()` in a render
 * function re-renders when a new release or locale activates.
 *
 * SSR: nothing touches browser globals at import or render time, and on the
 * server nothing subscribes to the runtime (no reactivity is needed there, and
 * a runtime shared across requests must not collect a listener per request).
 * Hydration matches as long as the client's runtime renders the same release
 * and locale on its first render: pass the same `bundled` release and
 * `locales`. A newer persisted or network release activates after hydration
 * and re-renders.
 */
import { computed, inject, shallowRef } from "vue";
import type { App, ComputedRef, InjectionKey, ShallowRef } from "vue";
import { createRuntime } from "@klarlabs-studio/glossa-runtime";
import type {
  Explanation,
  ManifestLocale,
  Part,
  Runtime,
  RuntimeOptions,
  TranslateOptions,
} from "@klarlabs-studio/glossa-runtime";

import { GlossaText } from "./glossa-text.js";

export interface GlossaOptions extends RuntimeOptions {
  /** Use this runtime instead of creating one (e.g. one runtime shared by all islands of a page). */
  runtime?: Runtime;
}

/** What `app.use()` takes. */
export interface Glossa {
  readonly runtime: Runtime;
  install(app: App): void;
}

/** The values a message takes; `{}` for none. */
export type MessageValues = Record<string, unknown>;

/**
 * Message ID → its values: the shape `glossa generate` emits. Register it once
 * to type every `useGlossa().t` call (see README):
 * `declare module "@klarlabs-studio/glossa-vue" { interface GlossaRegister { messages: Messages } }`.
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
  /** Render a message; reactive, never throws, never empty. */
  t<K extends keyof M & string>(id: K, ...args: Args<M[K]>): string;
  /** The same as parts, for custom rendering. */
  parts<K extends keyof M & string>(id: K, ...args: Args<M[K]>): Part[];
  /** Why `id` renders the way it does (SPEC §6). */
  explain(id: string, locales?: string | readonly string[]): Explanation;
  /** Switch locales; resolves once the new locale is active. */
  setLocale(locales: string | readonly string[]): Promise<void>;
  /** The active locale, once a release is active. */
  locale: ComputedRef<string | undefined>;
  dir: ComputedRef<"ltr" | "rtl">;
  release: ComputedRef<{ id: string; version: number } | undefined>;
  /** The active release's locales, for a locale picker. */
  availableLocales: ComputedRef<readonly ManifestLocale[]>;
  runtime: Runtime;
}

interface State {
  runtime: Runtime;
  version: ShallowRef<number>;
}

export const glossaKey: InjectionKey<State> = Symbol("glossa");

const browser = () => typeof document !== "undefined";

/** Create the plugin: `app.use(createGlossa({ edge, deliveryKey, locales, bundled }))`. */
export function createGlossa(options: GlossaOptions = {}): Glossa {
  const { runtime: given, ...rest } = options;
  const runtime = given ?? createRuntime(rest);
  return {
    runtime,
    install(app) {
      const state: State = { runtime, version: shallowRef(0) };
      app.provide(glossaKey, state);
      app.component("GlossaText", GlossaText);
      app.config.globalProperties.$t = bind(state).t;
      if (!browser()) return;
      const off = runtime.subscribe(() => state.version.value++);
      app.onUnmount(() => {
        off();
        if (!given) runtime.dispose();
      });
    },
  };
}

function bind<M>(state: State): UseGlossa<M> {
  const { runtime, version } = state;
  const track = <T>(value: () => T) => (version.value, value());
  return {
    t: (id: string, values?: MessageValues, opts?: TranslateOptions) =>
      track(() => runtime.t(id, values, opts)),
    parts: (id: string, values?: MessageValues, opts?: TranslateOptions) =>
      track(() => runtime.parts(id, values, opts)),
    explain: (id, locales) => track(() => runtime.explain(id, locales)),
    setLocale: (locales) => runtime.setLocales(locales),
    locale: computed(() => track(() => runtime.locale)),
    dir: computed(() => track(() => runtime.dir)),
    release: computed(() => track(() => runtime.release)),
    availableLocales: computed(() => track(() => runtime.availableLocales)),
    runtime,
  } as UseGlossa<M>;
}

let fallback: State | undefined;

/** The app's state; without the plugin, an inline-defaults-only runtime and a warning. */
export function useState(): State {
  const state = inject(glossaKey, undefined);
  if (state) return state;
  if (!fallback) {
    console.warn(
      "[glossa] useGlossa() without app.use(createGlossa(…)): rendering inline defaults.",
    );
    fallback = { runtime: createRuntime({ storage: null, locales: [] }), version: shallowRef(0) };
  }
  return fallback;
}

/** Reactive `t`, `locale`, `dir`, `setLocale`, `explain` for the current app. Call in `setup()`. */
export function useGlossa(): UseGlossa {
  return bind(useState());
}

/**
 * `useGlossa()` typed by a message map: `useMessages<Messages>()` checks IDs
 * and values at compile time. `Messages` is what `glossa generate` will emit
 * (message ID → values); until then it can be written by hand.
 */
export function useMessages<M = RegisteredMessages>(): UseGlossa<M> {
  return bind<M>(useState());
}

declare module "vue" {
  interface ComponentCustomProperties {
    /** Reactive `t` in templates: `{{ $t("cart.checkout") }}`. */
    $t: UseGlossa["t"];
  }
}
