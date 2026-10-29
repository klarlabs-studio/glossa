/**
 * An in-memory ContextPort for component tests, with Context's reads in
 * miniature: a message's usages and captures come from the current
 * builds the fake holds, filters combine, unused messages are the active
 * ones no usage names, and the coverage counts say whether anything was
 * ever uploaded.
 * Test-only: nothing in the app imports it.
 */
import type { ContextCoverage, ContextPort } from "../api/context";
import type { CaptureRegion, ContextUsage, MessageCapture } from "../api/context-schemas";
import { ApiError } from "../api/errors";

const NOW = "2026-09-19T08:00:00Z";

export interface FakeCapture {
  /** The messages this capture shows; every one gets the capture's regions. */
  keys: string[];
  capture: MessageCapture;
}

export interface FakeContextState {
  usages: ContextUsage[];
  captures: FakeCapture[];
  /** The project's active messages, to count coverage and find the unused ones. */
  active: Array<{ id: string; key: string }>;
  /** Builds considered; 0 means nothing was uploaded yet. */
  builds: number;
}

export interface FakeContext extends ContextPort {
  readonly calls: Array<[string, ...unknown[]]>;
  readonly state: FakeContextState;
}

/** A usage, with the shape the API sends. */
export function usage(over: Partial<ContextUsage> & { key: string }): ContextUsage {
  return {
    message_id: over.key.replace(/\W/g, "_"),
    file: "src/App.vue",
    line: 12,
    column: 3,
    kind: "t",
    build_id: "b1",
    application_id: "a1",
    commit: "9f2c1e7a9f2c1e7a9f2c1e7a9f2c1e7a9f2c1e7a",
    branch: "main",
    on_default_branch: true,
    source: "plugin",
    ...over,
  };
}

export function region(over: Partial<CaptureRegion> = {}): CaptureRegion {
  return { kind: "element", box: { x: 40, y: 120, width: 90, height: 24 }, visible: true, ...over };
}

/** A capture that shows `keys`, with the shape the API sends. */
export function capture(id: string, keys: string[], over: Partial<MessageCapture> = {}): FakeCapture {
  return {
    keys,
    capture: {
      id,
      build_id: "b2",
      application_id: "a1",
      commit: "9f2c1e7a9f2c1e7a9f2c1e7a9f2c1e7a9f2c1e7a",
      branch: "main",
      on_default_branch: true,
      route: "/checkout",
      viewport: { width: 1280, height: 800 },
      locale: "en",
      image: { digest: "d".repeat(64), width: 1280, height: 2400, url: `/v1/tenants/t/projects/p/captures/${id}/image` },
      regions: [region()],
      created_at: NOW,
      ...over,
    },
  };
}

export function createFakeContext(state: Partial<FakeContextState> = {}): FakeContext {
  const s: FakeContextState = { usages: [], captures: [], active: [], builds: 1, ...state };
  const calls: Array<[string, ...unknown[]]> = [];
  const idOf = (key: string) => s.active.find((m) => m.key === key)?.id ?? s.usages.find((u) => u.key === key)?.message_id ?? key;
  const known = (key: string) => s.active.some((m) => m.key === key) || s.usages.some((u) => u.key === key) || s.captures.some((c) => c.keys.includes(key));
  const need = (key: string) => {
    if (!known(key)) throw new ApiError(404, "not_found", "No such message.");
  };
  const coverage = (): ContextCoverage => ({
    current_builds: s.builds,
    active_messages: s.active.length,
    unused_messages: unused().length,
  });
  const unused = () => s.active.filter((m) => !s.usages.some((u) => u.message_id === m.id));

  return {
    calls,
    state: s,
    async messageUsages(_tenant, _project, key, q = {}) {
      calls.push(["messageUsages", key, q]);
      need(key);
      const limit = q.limit ?? 100;
      const all = s.usages.filter((u) => u.key === key);
      return { message_id: idOf(key), key, usages: all.slice(0, limit), truncated: all.length > limit };
    },
    async messageCaptures(_tenant, _project, key, q = {}) {
      calls.push(["messageCaptures", key, q]);
      need(key);
      const limit = q.limit ?? 100;
      const all = s.captures.filter((c) => c.keys.includes(key)).map((c) => c.capture);
      return { message_id: idOf(key), key, captures: all.slice(0, limit), truncated: all.length > limit };
    },
    async usages(_tenant, _project, f = {}) {
      calls.push(["usages", f]);
      return s.usages.filter(
        (u) => (!f.route || u.route === f.route) && (!f.component || u.component === f.component) && (!f.file || u.file === f.file),
      );
    },
    async unusedMessages() {
      calls.push(["unusedMessages"]);
      return { items: unused(), ...coverage() };
    },
    async coverage() {
      calls.push(["coverage"]);
      return coverage();
    },
  };
}
