/**
 * `@klarlabs-studio/glossa-runtime/idb`: IndexedDB storage for the persisted last-good
 * release, for catalogs too large for `localStorage`. A separate entry, so
 * apps that don't use it don't ship it.
 *
 * ```ts
 * import { createRuntime } from "@klarlabs-studio/glossa-runtime";
 * import { indexedDbStorage } from "@klarlabs-studio/glossa-runtime/idb";
 *
 * createRuntime({ …, storage: indexedDbStorage() });
 * ```
 */
import type { RuntimeStorage } from "./storage.js";

const STORE = "releases";

const result = <T>(req: IDBRequest<T>) =>
  new Promise<T>((resolve, reject) => {
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });

/** Key-value storage in the IndexedDB database `name` (object store `releases`). */
export function indexedDbStorage(name = "glossa"): RuntimeStorage {
  let db: Promise<IDBDatabase> | undefined;
  const store = async (mode: IDBTransactionMode) => {
    db ??= (() => {
      const req = indexedDB.open(name, 1);
      req.onupgradeneeded = () => req.result.createObjectStore(STORE);
      return result(req);
    })();
    return (await db).transaction(STORE, mode);
  };
  return {
    get: async (key) => result((await store("readonly")).objectStore(STORE).get(key)),
    async set(key, value) {
      const tx = await store("readwrite");
      tx.objectStore(STORE).put(value, key);
      await new Promise<void>((resolve, reject) => {
        tx.oncomplete = () => resolve();
        tx.onerror = tx.onabort = () => reject(tx.error);
      });
    },
  };
}
