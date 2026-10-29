/**
 * `@klarlabs-studio/glossa/astro/translate`: `t(id, fallback)` for shared helpers and libraries.
 *
 * It imports nothing from Astro or from a virtual module, so code that uses it loads anywhere
 * (unit tests, scripts, a package that is also used outside Astro) with no stubbing. It renders
 * with the runtime of the page (browser) or of the request being rendered (server) when there is
 * one, and returns the fallback as written when there isn't.
 *
 * The page's runtime exists once `getRuntime()` from `/astro/client` has run, which every island,
 * `<glossa-provider>` and elements-enabled page does on load; a library that needs a runtime of its
 * own choosing takes one as a parameter instead.
 */
import type { Runtime } from "@klarlabs-studio/glossa-runtime";

/** Where `/astro/client` publishes the page's runtime. */
export const PAGE_RUNTIME = Symbol.for("glossa.astro.page");
const SERVER_RUNTIME = Symbol.for("glossa.astro.server");

type Slots = Record<symbol, (() => Runtime) | Runtime | undefined>;

/** The runtime to render with now: the request's (server), else the page's, else `undefined`. */
export function currentRuntime(): Runtime | undefined {
  const slots = globalThis as Slots;
  return (slots[SERVER_RUNTIME] as (() => Runtime) | undefined)?.() ?? (slots[PAGE_RUNTIME] as Runtime | undefined);
}

/**
 * Use `runtime` as the page's runtime (or none, with `undefined`): for tests and for hosts that
 * create their own. `/astro/client` does this itself for the runtime it creates.
 */
export function provideRuntime(runtime: Runtime | undefined): void {
  (globalThis as Slots)[PAGE_RUNTIME] = runtime;
}

/**
 * The message `id`, rendered with `values`, or `fallback` when there is no runtime or the release
 * lacks it. Without a runtime the fallback is returned literally (nothing to interpolate with).
 */
export function t(id: string, fallback: string, values?: Record<string, unknown>): string {
  return currentRuntime()?.t(id, values, { default: fallback }) ?? fallback;
}

/** Whether a message `id` exists in the current runtime's release; `false` without a runtime. */
export function has(id: string): boolean {
  return currentRuntime()?.has(id) ?? false;
}
