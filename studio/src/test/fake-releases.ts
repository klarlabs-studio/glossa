/**
 * An in-memory ReleasesPort for component tests, with Release's rules:
 * publish snapshots the catalog under the environment's policy and points
 * the environment at it; promote requires the policy to cover the
 * release's; rollback only reaches releases the environment served;
 * policy changes need the current ETag; revoked keys stay listed. Into an
 * environment with an `approval` requirement a publish records its release
 * and a promote names one, and both are held: a pending release request is
 * made (withdrawing the environment's earlier pending one) and no pointer
 * moves (RFC 0006 §5.1).
 * Test-only: nothing in the app imports it.
 */
import { ApiError, type Versioned } from "../api/errors";
import type { PublishInput, ProjectRef, ReleasesPort } from "../api/releases";
import type { ReleaseHeld, ReleaseRequest } from "../api/release-ops-schemas";
import type { Deployment, DeliveryKey, DeliveryKeyScope, Environment, EnvironmentPolicy, Release, ReleaseDiff, ReleasePreview, ReleaseProblem, SigningKey } from "../api/schemas";
import { covers, DEFAULT_ENVIRONMENTS } from "../lib/releases";

/** Locale → message ID → text: what a publish would ship. */
export type FakeCatalog = Record<string, Record<string, string>>;

export interface FakeReleases extends ReleasesPort {
  /** What the next publish ships. */
  catalog: FakeCatalog;
  /** Set to make the catalog unreleasable: previews list these, publishes fail with not_releasable. */
  problems: ReleaseProblem[];
  readonly calls: Array<[string, ...unknown[]]>;
  readonly state: {
    envs: Map<string, { env: Environment; etag: number }>;
    releases: Release[];
    deployments: Map<string, Deployment[]>;
    keys: DeliveryKey[];
    /** Release requests, newest first (shared with the release-ops fake). */
    requests: ReleaseRequest[];
  };
  /** Point an environment at a release as a deploy does (the release-ops fake deploys with it). */
  point(name: string, releaseId: string, action: Deployment["action"]): Environment;
}

const policyFor = (name: string): EnvironmentPolicy =>
  name === "production" || name === "staging"
    ? { states: ["approved"], include_outdated: true }
    : { states: ["draft", "needs_review", "approved"], include_outdated: true };

export function createFakeReleases(options: { sourceLocale?: string; catalog?: FakeCatalog; author?: string; signingKeys?: SigningKey[] } = {}): FakeReleases {
  const source = options.sourceLocale ?? "en";
  const author = options.author ?? "person:me";
  let clock = Date.parse("2026-09-19T08:00:00Z");
  const now = () => new Date((clock += 60_000)).toISOString();
  let seq = 0;
  const nextId = (prefix: string) => `${prefix}_${++seq}`;

  const envs = new Map<string, { env: Environment; etag: number }>();
  for (const name of DEFAULT_ENVIRONMENTS) {
    const t = now();
    envs.set(name, { env: { name, kind: "standard", policy: policyFor(name), created_at: t, updated_at: t }, etag: 1 });
  }
  const releases: Release[] = [];
  const snapshots = new Map<string, FakeCatalog>();
  const deployments = new Map<string, Deployment[]>();
  const keys: DeliveryKey[] = [];
  const replays = new Map<string, Release | ReleaseHeld>();
  const requests: ReleaseRequest[] = [];
  const calls: Array<[string, ...unknown[]]> = [];

  const envOf = (name: string) => {
    const e = envs.get(name);
    if (!e) throw new ApiError(404, "not_found", "No such environment.");
    return e;
  };
  const releaseOf = (id: string) => {
    const r = releases.find((x) => x.id === id);
    if (!r) throw new ApiError(404, "release_not_found", "No such release.");
    return r;
  };
  const move = (name: string, release: Release, action: Deployment["action"]) => {
    const e = envOf(name);
    const history = deployments.get(name) ?? [];
    // The fake never overrides the completeness requirement, so every
    // deployment it makes is an ordinary one.
    const d: Deployment = {
      number: history.length + 1,
      release_id: release.id,
      action,
      author,
      created_at: now(),
      forced: false,
    };
    if (e.env.current_release_id) d.previous_release_id = e.env.current_release_id;
    deployments.set(name, [d, ...history]);
    e.env = { ...e.env, current_release_id: release.id, updated_at: d.created_at };
    e.etag++;
    return structuredClone(e.env);
  };

  /** Hold a move into an environment that requires approval. */
  const hold = (name: string, release: Release, action: "publish" | "promote"): ReleaseHeld | undefined => {
    const e = envOf(name);
    if (!e.env.approval) return undefined;
    for (const r of requests) if (r.environment === name && r.state === "pending") Object.assign(r, { state: "withdrawn", reason: "A newer publish or promote replaced it." });
    const req: ReleaseRequest = {
      id: nextId("rr"),
      environment: name,
      release_id: release.id,
      action,
      requester: author,
      approval: structuredClone(e.env.approval),
      gate: { met: true },
      forced: false,
      state: "pending",
      created_at: now(),
    };
    requests.unshift(req);
    return { id: release.id, release_request_id: req.id, release_request: structuredClone(req) };
  };

  const localesOf = (snapshot: FakeCatalog) => [source, ...Object.keys(snapshot).filter((l) => l !== source).sort()];
  const countsOf = (snapshot: FakeCatalog) => {
    const codes = localesOf(snapshot);
    return {
      messages: Object.keys(snapshot[source] ?? {}).length,
      artifacts: codes.length,
      bytes: JSON.stringify(snapshot).length,
      new_artifacts: codes.length,
      locales: Object.fromEntries(codes.map((c) => [c, { messages: Object.keys(snapshot[c] ?? {}).length, outdated: 0 }])),
    };
  };
  const diffOf = (a: FakeCatalog, b: FakeCatalog) =>
    [...new Set([...Object.keys(a), ...Object.keys(b)])].map((locale) => {
      const now = a[locale] ?? {};
      const before = b[locale] ?? {};
      return {
        locale,
        added: Object.keys(now).filter((k) => !(k in before)),
        changed: Object.keys(now).filter((k) => k in before && before[k] !== now[k]),
        removed: Object.keys(before).filter((k) => !(k in now)),
      };
    });

  const fake: FakeReleases = {
    catalog: structuredClone(options.catalog ?? { [source]: { "app.title": "Demo" } }),
    problems: [],
    calls,
    state: { envs, releases, deployments, keys, requests },
    point: (name, id, action) => move(name, releaseOf(id), action),

    async environments(p) {
      calls.push(["environments", p]);
      return [...envs.values()].map((e) => structuredClone(e.env));
    },
    async environment(p, name): Promise<Versioned<Environment>> {
      calls.push(["environment", p, name]);
      const e = envOf(name);
      return { value: structuredClone(e.env), etag: String(e.etag) };
    },
    async updatePolicy(p, name, policy, etag) {
      calls.push(["updatePolicy", p, name, policy, etag]);
      const e = envOf(name);
      if (String(e.etag) !== etag) throw new ApiError(412, "precondition_failed", "Changed meanwhile.");
      e.env = { ...e.env, policy: structuredClone(policy), updated_at: now() };
      e.etag++;
      return structuredClone(e.env);
    },
    async deployments(p, name, pageSize = 50) {
      calls.push(["deployments", p, name, pageSize]);
      envOf(name);
      return structuredClone((deployments.get(name) ?? []).slice(0, pageSize));
    },
    async releases(p, pageToken) {
      calls.push(["releases", p, pageToken]);
      return { items: structuredClone(releases), next: undefined };
    },
    async release(p, id) {
      calls.push(["release", p, id]);
      return structuredClone(releaseOf(id));
    },
    async diff(p, id, base): Promise<ReleaseDiff> {
      calls.push(["diff", p, id, base]);
      const head = releaseOf(id);
      const baseId = base ?? head.parent_id;
      const a = snapshots.get(head.id) ?? {};
      const b = baseId ? (snapshots.get(releaseOf(baseId).id) ?? {}) : {};
      const locales = diffOf(a, b);
      return baseId ? { release_id: id, base_release_id: baseId, locales } : { release_id: id, locales };
    },
    async previewPublish(p, name): Promise<ReleasePreview> {
      calls.push(["previewPublish", p, name]);
      const e = envOf(name);
      const out: ReleasePreview = { environment: name, policy: structuredClone(e.env.policy), releasable: fake.problems.length === 0, problems: structuredClone(fake.problems) };
      if (e.env.current_release_id) out.base_release_id = e.env.current_release_id;
      if (!out.releasable) return out;
      const snapshot = structuredClone(fake.catalog);
      const base = e.env.current_release_id ? (snapshots.get(e.env.current_release_id) ?? {}) : {};
      out.source_locale = source;
      out.locales = localesOf(snapshot).map((code) => ({ code, direction: code === "ar" || code === "he" ? "rtl" : "ltr" }));
      out.counts = countsOf(snapshot);
      out.changes = diffOf(snapshot, base);
      out.manifest_digest = "f".repeat(64);
      return out;
    },
    async publish(p: ProjectRef, input: PublishInput, key: string) {
      calls.push(["publish", p, input, key]);
      const replay = replays.get(key);
      if (replay) return structuredClone(replay);
      const e = envOf(input.environment);
      if (fake.problems.length) throw new ApiError(422, "not_releasable", fake.problems.map((x) => x.detail).join("; "));
      const snapshot = structuredClone(fake.catalog);
      const codes = localesOf(snapshot);
      const r: Release = {
        id: nextId("rel"),
        version: releases.length + 1,
        environment: e.env.name,
        policy: structuredClone(e.env.policy),
        manifest_digest: (releases.length + 1).toString(16).padStart(64, "0"),
        source_locale: source,
        locales: codes.map((code) => ({ code, direction: code === "ar" || code === "he" ? "rtl" : "ltr" })),
        counts: countsOf(snapshot),
        author,
        created_at: now(),
      };
      if (e.env.current_release_id) r.parent_id = e.env.current_release_id;
      if (input.note) r.note = input.note;
      releases.unshift(r);
      snapshots.set(r.id, snapshot);
      const held = hold(e.env.name, r, "publish");
      replays.set(key, held ?? r);
      if (held) return structuredClone(held);
      move(e.env.name, r, "publish");
      return structuredClone(r);
    },
    async promote(p, name, id) {
      calls.push(["promote", p, name, id]);
      const e = envOf(name);
      const r = releaseOf(id);
      if (!covers(e.env.policy, r.policy)) throw new ApiError(409, "release_ineligible", "Not eligible.");
      if (e.env.current_release_id === id) return structuredClone(e.env);
      return hold(name, r, "promote") ?? move(name, r, "promote");
    },
    async rollback(p, name, id) {
      calls.push(["rollback", p, name, id]);
      const e = envOf(name);
      if (!(deployments.get(name) ?? []).some((d) => d.release_id === id)) throw new ApiError(409, "not_in_history", "Never served.");
      if (e.env.current_release_id === id) return structuredClone(e.env);
      return move(name, releaseOf(id), "rollback");
    },
    async signingKeys(p) {
      calls.push(["signingKeys", p]);
      return structuredClone(options.signingKeys ?? []);
    },
    async deliveryKeys(p) {
      calls.push(["deliveryKeys", p]);
      return structuredClone(keys);
    },
    async createDeliveryKey(p, name, scope, key) {
      calls.push(["createDeliveryKey", p, name, scope, key]);
      const n = keys.length + 1;
      const k: DeliveryKey = {
        id: nextId("dk"),
        name,
        key: `glossa_pk_${String(n).padStart(32, "K")}`,
        scope: scope ? structuredClone(scope) : { environments: ["production"], branches: false },
        created_by: author,
        created_at: now(),
      };
      if (k.scope.environments.length === 0 && !k.scope.branches) {
        throw new ApiError(400, "invalid_key_scope", "A scope allows 1-50 environments, or branch previews.");
      }
      keys.push(k);
      return structuredClone(k);
    },
    async setDeliveryKeyScope(p, id, scope: DeliveryKeyScope) {
      calls.push(["setDeliveryKeyScope", p, id, scope]);
      const k = keys.find((x) => x.id === id);
      if (!k) throw new ApiError(404, "not_found", "No such key.");
      if (k.revoked_at) throw new ApiError(409, "key_revoked", "The key is revoked.");
      if (scope.environments.length === 0 && !scope.branches) {
        throw new ApiError(400, "invalid_key_scope", "A scope allows 1-50 environments, or branch previews.");
      }
      k.scope = structuredClone(scope);
      return structuredClone(k);
    },
    async revokeDeliveryKey(p, id) {
      calls.push(["revokeDeliveryKey", p, id]);
      const k = keys.find((x) => x.id === id);
      if (!k) throw new ApiError(404, "not_found", "No such key.");
      if (k.revoked_at) throw new ApiError(409, "key_revoked", "Already revoked.");
      k.revoked_at = now();
    },
  };
  return fake;
}
