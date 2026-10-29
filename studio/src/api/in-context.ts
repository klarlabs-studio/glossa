/**
 * The in-context editing port (RFC 0004 §5.2): where a project's
 * in-product editor may run, and the credential Studio mints for it.
 *
 * The overlay runs on the product's own preview deployment, so it can
 * never use Studio's session cookie. It opens a popup on Studio
 * instead — `/in-context/authorize?project=…&origin=…` — and that page,
 * which does have the session, mints a grant and posts it back to the
 * one origin that asked. The grant lives fifteen minutes, is bound to
 * that project and that origin, and is never stored anywhere but the
 * editor's memory.
 *
 * Registering an origin is the decision that a page served from there
 * may borrow a person's permissions, so the server asks for
 * `tokens.manage`, not `catalog.write`. Reading the list needs only
 * `catalog.read`: the popup has to say where the editor may run.
 *
 * `apiInContext` implements the port over the generated /v1 client with
 * every response checked by zod; component tests provide an in-memory
 * fake (src/test/fake-in-context.ts).
 */
import { inject, type InjectionKey } from "vue";
import { client } from "./client";
import { done, read, type Versioned } from "./errors";
import * as I from "./in-context-schemas";
import { z } from "zod";

export interface InContextPort {
  /** The project's registered preview origins, in origin order. */
  origins(tenant: string, project: string): Promise<I.PreviewOrigin[]>;
  /** Allow the editor on an origin. `label` is what people call the deployment. */
  register(
    tenant: string,
    project: string,
    body: { origin: string; label?: string },
    idempotencyKey: string,
  ): Promise<I.PreviewOrigin>;
  /** Stop allowing it. Grants minted for the origin end with it. */
  unregister(tenant: string, project: string, id: string): Promise<void>;
  /** Mint the editor's credential for an origin. The token is shown once. */
  mint(tenant: string, project: string, origin: string): Promise<I.InContextGrant>;
}

const value = async <T>(p: Promise<Versioned<T>>): Promise<T> => (await p).value;

const OriginList = z.object({ items: z.array(I.PreviewOrigin) });

export const apiInContext: InContextPort = {
  origins: async (tenant, project) =>
    (
      await value(
        read(
          client.GET("/v1/tenants/{tenant}/projects/{project}/preview-origins", {
            params: { path: { tenant, project } },
          }),
          OriginList,
        ),
      )
    ).items,
  register: (tenant, project, body, key) =>
    value(
      read(
        client.POST("/v1/tenants/{tenant}/projects/{project}/preview-origins", {
          params: { path: { tenant, project }, header: { "Idempotency-Key": key } },
          body,
        }),
        I.PreviewOrigin,
      ),
    ),
  unregister: (tenant, project, preview_origin) =>
    done(
      client.DELETE("/v1/tenants/{tenant}/projects/{project}/preview-origins/{preview_origin}", {
        params: { path: { tenant, project, preview_origin } },
      }),
    ),
  mint: (tenant, project, origin) =>
    value(
      read(
        client.POST("/v1/tenants/{tenant}/projects/{project}/in-context-grants", {
          params: { path: { tenant, project } },
          body: { origin },
        }),
        I.InContextGrant,
      ),
    ),
};

export const IN_CONTEXT: InjectionKey<InContextPort> = Symbol("in-context");

/** The provided port, else the API adapter. */
export function useInContext(): InContextPort {
  return inject(IN_CONTEXT, apiInContext);
}
