/**
 * Where the persisted last-good release lives (runtimes/SPEC.md §3, step 2).
 * Values are plain JSON. Adapters may be sync or async and may throw; the
 * runtime treats any failure as "nothing persisted".
 */
export interface RuntimeStorage {
  get(key: string): unknown;
  set(key: string, value: unknown): void | Promise<void>;
}

/** In-process storage: tests, SSR, or several runtimes sharing one process. */
export function memoryStorage(): RuntimeStorage {
  const items = new Map<string, unknown>();
  return { get: (k) => items.get(k), set: (k, v) => void items.set(k, v) };
}

/**
 * `localStorage` (or `sessionStorage`) as JSON: synchronous and small, so it
 * suits small catalogs. Larger ones should use `indexedDbStorage` from
 * `@klarlabs-studio/glossa-runtime/idb`.
 */
export function webStorage(
  store: Pick<Storage, "getItem" | "setItem"> = localStorage,
): RuntimeStorage {
  return {
    get(k) {
      try {
        return JSON.parse(store.getItem(k) ?? "null") ?? undefined;
      } catch {
        return undefined;
      }
    },
    set: (k, v) => store.setItem(k, JSON.stringify(v)),
  };
}
