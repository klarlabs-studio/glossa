import "fake-indexeddb/auto";
import { describe, expect, it } from "vitest";
import { indexedDbStorage } from "./idb.js";
import { memoryStorage, webStorage } from "./storage.js";
import type { RuntimeStorage } from "./storage.js";

const record = { etag: '"m1"', manifest: { release: { id: "rel_1" } }, artifacts: { ab: "{}" } };

/** A `localStorage` stand-in: strings only, like the real one. */
function fakeWebStorage() {
  const items = new Map<string, string>();
  return {
    getItem: (k: string) => items.get(k) ?? null,
    setItem: (k: string, v: string) => void items.set(k, String(v)),
    items,
  };
}

const adapters: Array<[string, () => RuntimeStorage]> = [
  ["memoryStorage", () => memoryStorage()],
  ["webStorage", () => webStorage(fakeWebStorage())],
  ["indexedDbStorage", () => indexedDbStorage(`glossa-test-${Math.random()}`)],
];

describe.each(adapters)("%s", (_name, create) => {
  it("returns undefined for a missing key", async () => {
    expect(await create().get("glossa:pk:production")).toBeUndefined();
  });

  it("round-trips a persisted release and overwrites it", async () => {
    const s = create();
    await s.set("glossa:pk:production", record);
    expect(await s.get("glossa:pk:production")).toEqual(record);
    await s.set("glossa:pk:production", { ...record, etag: '"m2"' });
    expect(await s.get("glossa:pk:production")).toEqual({ ...record, etag: '"m2"' });
  });
});

describe("webStorage", () => {
  it("stores JSON strings and treats unreadable entries as missing", async () => {
    const ls = fakeWebStorage();
    const s = webStorage(ls);
    await s.set("k", record);
    expect(ls.items.get("k")).toBe(JSON.stringify(record));
    ls.items.set("k", "{not json");
    expect(await s.get("k")).toBeUndefined();
  });
});

describe("indexedDbStorage", () => {
  it("shares data between instances on the same database", async () => {
    const name = `glossa-test-${Math.random()}`;
    await indexedDbStorage(name).set("k", record);
    expect(await indexedDbStorage(name).get("k")).toEqual(record);
  });
});
