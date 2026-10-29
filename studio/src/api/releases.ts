/**
 * The Releases port: everything Studio does with environments, releases
 * and delivery keys goes through it. `apiReleases` implements it over the
 * generated /v1 client with every response checked by zod; component
 * tests provide an in-memory fake (src/test/fake-releases.ts).
 */
import { inject, type InjectionKey } from "vue";
import type { z } from "zod";
import { client } from "./client";
import { all } from "./endpoints";
import { done, failure, read, send, type RawResult, type Versioned } from "./errors";
import { ReleaseHeld } from "./release-ops-schemas";
import * as S from "./schemas";

export interface ProjectRef {
  tenant: string;
  project: string;
}

export interface Page<T> {
  items: T[];
  next: string | undefined;
}

export interface PublishInput {
  environment: string;
  note?: string | undefined;
}

export interface ReleasesPort {
  environments(p: ProjectRef): Promise<S.Environment[]>;
  /** One environment with its ETag, for a policy change. */
  environment(p: ProjectRef, name: string): Promise<Versioned<S.Environment>>;
  updatePolicy(p: ProjectRef, name: string, policy: S.EnvironmentPolicy, etag: string): Promise<S.Environment>;
  /** Newest first. */
  deployments(p: ProjectRef, environment: string, pageSize?: number): Promise<S.Deployment[]>;
  /** Newest first. */
  releases(p: ProjectRef, pageToken?: string): Promise<Page<S.Release>>;
  release(p: ProjectRef, id: string): Promise<S.Release>;
  /** What changed in `id` compared with `base` (default: its parent). */
  diff(p: ProjectRef, id: string, base?: string): Promise<S.ReleaseDiff>;
  /** What publishing to `environment` would ship now, against what it serves; nothing is stored. */
  previewPublish(p: ProjectRef, environment: string): Promise<S.ReleasePreview>;
  /**
   * `idempotencyKey` makes a retry of the same confirmation safe. Into an
   * environment that requires approval the release is recorded but held:
   * a `ReleaseHeld` names the release request, and no pointer moved.
   */
  publish(p: ProjectRef, input: PublishInput, idempotencyKey: string): Promise<S.Release | ReleaseHeld>;
  /** Held like a publish where the environment requires approval. */
  promote(p: ProjectRef, environment: string, releaseId: string): Promise<S.Environment | ReleaseHeld>;
  rollback(p: ProjectRef, environment: string, releaseId: string): Promise<S.Environment>;
  signingKeys(p: ProjectRef): Promise<S.SigningKey[]>;
  deliveryKeys(p: ProjectRef): Promise<S.DeliveryKey[]>;
  /** Without a scope the key reads `production` only. */
  createDeliveryKey(p: ProjectRef, name: string, scope: S.DeliveryKeyScope | undefined, idempotencyKey: string): Promise<S.DeliveryKey>;
  /** Replaces what a key reads; the key itself doesn't change. */
  setDeliveryKeyScope(p: ProjectRef, id: string, scope: S.DeliveryKeyScope): Promise<S.DeliveryKey>;
  revokeDeliveryKey(p: ProjectRef, id: string): Promise<void>;
}

const value = async <T>(p: Promise<Versioned<T>>): Promise<T> => (await p).value;

/** Whether a publish or promote was held for approval (RFC 0006 §5.1): nothing deployed. */
export const isHeld = (r: object): r is ReleaseHeld => "release_request_id" in r;

/** A `202` is a held move (`ReleaseHeld`); any other success is `ok`. */
async function readMove<T extends z.ZodType>(request: Promise<RawResult>, ok: T): Promise<z.infer<T> | ReleaseHeld> {
  const r = await send(request);
  if (!r.response.ok) throw failure(r);
  const held = r.response.status === 202;
  return held ? value(read(Promise.resolve(r), ReleaseHeld)) : value(read(Promise.resolve(r), ok));
}

const PAGE = 100;
const ENV = "/v1/tenants/{tenant}/projects/{project}/environments/{environment}" as const;

export const apiReleases: ReleasesPort = {
  environments: (p) =>
    all((page_token) =>
      value(
        read(
          client.GET("/v1/tenants/{tenant}/projects/{project}/environments", { params: { path: p, query: { page_size: PAGE, page_token } } }),
          S.page(S.Environment),
        ),
      ),
    ),
  environment: (p, environment) => read(client.GET(ENV, { params: { path: { ...p, environment } } }), S.Environment),
  updatePolicy: (p, environment, policy, etag) =>
    value(read(client.PATCH(ENV, { params: { path: { ...p, environment }, header: { "If-Match": etag } }, body: { policy } }), S.Environment)),
  deployments: async (p, environment, pageSize = 50) =>
    (
      await value(
        read(
          client.GET("/v1/tenants/{tenant}/projects/{project}/environments/{environment}/deployments", { params: { path: { ...p, environment }, query: { page_size: pageSize } } }),
          S.page(S.Deployment),
        ),
      )
    ).items,
  releases: async (p, page_token) => {
    const page = await value(
      read(
        client.GET("/v1/tenants/{tenant}/projects/{project}/releases", { params: { path: p, query: { page_size: 50, page_token } } }),
        S.page(S.Release),
      ),
    );
    return { items: page.items, next: page.next_page_token };
  },
  release: (p, release) =>
    value(read(client.GET("/v1/tenants/{tenant}/projects/{project}/releases/{release}", { params: { path: { ...p, release } } }), S.Release)),
  diff: (p, release, base) =>
    value(
      read(
        client.GET("/v1/tenants/{tenant}/projects/{project}/releases/{release}/diff", {
          params: { path: { ...p, release }, query: base ? { base } : {} },
        }),
        S.ReleaseDiff,
      ),
    ),
  previewPublish: (p, environment) =>
    value(read(client.POST(`${ENV}/release-previews`, { params: { path: { ...p, environment } } }), S.ReleasePreview)),
  publish: (p, input, key) =>
    readMove(
        client.POST("/v1/tenants/{tenant}/projects/{project}/releases", {
          params: { path: p, header: { "Idempotency-Key": key } },
          body: input.note ? { environment: input.environment, note: input.note } : { environment: input.environment },
        }),
        S.Release,
    ),
  promote: (p, environment, release_id) =>
    readMove(client.POST("/v1/tenants/{tenant}/projects/{project}/environments/{environment}/promotions", { params: { path: { ...p, environment } }, body: { release_id } }), S.Environment),
  rollback: (p, environment, release_id) =>
    value(read(client.POST("/v1/tenants/{tenant}/projects/{project}/environments/{environment}/rollbacks", { params: { path: { ...p, environment } }, body: { release_id } }), S.Environment)),
  signingKeys: async (p) =>
    (await value(read(client.GET("/v1/tenants/{tenant}/projects/{project}/release-signing-keys", { params: { path: p } }), S.SigningKeys)))
      .keys,
  deliveryKeys: (p) =>
    all((page_token) =>
      value(
        read(
          client.GET("/v1/tenants/{tenant}/projects/{project}/delivery-keys", { params: { path: p, query: { page_size: PAGE, page_token } } }),
          S.page(S.DeliveryKey),
        ),
      ),
    ),
  createDeliveryKey: (p, name, scope, key) =>
    value(
      read(
        client.POST("/v1/tenants/{tenant}/projects/{project}/delivery-keys", {
          params: { path: p, header: { "Idempotency-Key": key } },
          body: scope ? { name, scope } : { name },
        }),
        S.DeliveryKey,
      ),
    ),
  setDeliveryKeyScope: (p, delivery_key, scope) =>
    value(
      read(
        client.PUT("/v1/tenants/{tenant}/projects/{project}/delivery-keys/{delivery_key}/scope", {
          params: { path: { ...p, delivery_key } },
          body: scope,
        }),
        S.DeliveryKey,
      ),
    ),
  revokeDeliveryKey: (p, delivery_key) =>
    done(client.DELETE("/v1/tenants/{tenant}/projects/{project}/delivery-keys/{delivery_key}", { params: { path: { ...p, delivery_key } } })),
};

export const RELEASES: InjectionKey<ReleasesPort> = Symbol("releases");

/** The provided port (main.ts provides `apiReleases`). */
export function useReleases(): ReleasesPort {
  const port = inject(RELEASES);
  if (!port) throw new Error("useReleases() without a provided ReleasesPort");
  return port;
}

/** A fresh Idempotency-Key for one confirmation. */
export const newIdempotencyKey = (): string => globalThis.crypto.randomUUID();
