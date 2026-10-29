/**
 * The GitHub port (RFC 0004 §6): connecting the Glossa GitHub App to a
 * workspace and tying its repositories to projects.
 *
 * The install flow is a round trip. `startInstall` issues a single-use
 * `state` and the GitHub URL to send the person to; GitHub sends them
 * back to Studio with that `state`, an `installation_id`, a `code` and
 * a `setup_action`, and `completeInstall` hands those to the server,
 * which verifies through the person's own GitHub token that they can
 * actually see the installation before mapping it to the workspace.
 * Nothing of that token ever reaches Studio.
 *
 * `apiGitHub` implements the port over the generated /v1 client with
 * every response checked by zod; component tests provide an in-memory
 * fake (src/test/fake-github.ts). Like the other ports it loads with
 * the screen that uses it, not with the app shell.
 */
import { inject, type InjectionKey } from "vue";
import { client } from "./client";
import { done, read, type Versioned } from "./errors";
import * as G from "./github-schemas";
import type { components } from "./schema";
import { page } from "./schemas";

type Body<N extends keyof components["schemas"]> = components["schemas"][N];
export type GitConnectionRequest = Body<"GitConnectionRequest">;
export type GitConnectionChange = Body<"GitConnectionChange">;

/** What GitHub hands back on the callback. */
export interface InstallCallback {
  state: string;
  installation_id: number;
  code: string;
  setup_action?: string;
}

export interface GitHubPort {
  /** A single-use state and where to send the person. */
  startInstall(tenant: string): Promise<G.GitHubInstallIntent>;
  /** Finish the round trip; the server verifies the person owns the installation. */
  completeInstall(tenant: string, cb: InstallCallback): Promise<G.GitHubInstallation>;
  /** The workspace's installations, each with the repositories the App can see. */
  installations(tenant: string): Promise<G.GitHubInstallation[]>;
  /** Forget an installation locally. It stays installed on GitHub. */
  forgetInstallation(tenant: string, id: string): Promise<void>;

  /** The workspace's Git connections. */
  connections(tenant: string, project?: string): Promise<G.GitConnection[]>;
  connect(tenant: string, body: GitConnectionRequest, idempotencyKey: string): Promise<G.GitConnection>;
  changeConnection(tenant: string, id: string, body: GitConnectionChange, version: number): Promise<G.GitConnection>;
  disconnect(tenant: string, id: string): Promise<void>;
}

const value = async <T>(p: Promise<Versioned<T>>): Promise<T> => (await p).value;
const PAGE = 100;

/** Reads every page: both lists are short, and a half-shown list of repositories would be misleading. */
async function all<T>(fetchPage: (token?: string) => Promise<{ items: T[]; next_page_token?: string | undefined }>): Promise<T[]> {
  const out: T[] = [];
  let token: string | undefined;
  // Bounded, so a server that always returns a token cannot spin here.
  for (let i = 0; i < 50; i++) {
    const p = await fetchPage(token);
    out.push(...p.items);
    token = p.next_page_token;
    if (!token) break;
  }
  return out;
}

export const apiGitHub: GitHubPort = {
  startInstall: (tenant) => value(read(client.POST("/v1/tenants/{tenant}/github/install-intents", { params: { path: { tenant } } }), G.GitHubInstallIntent)),
  completeInstall: (tenant, cb) =>
    value(
      read(
        client.POST("/v1/tenants/{tenant}/github/installations", {
          params: { path: { tenant } },
          body: { state: cb.state, installation_id: cb.installation_id, code: cb.code, setup_action: cb.setup_action },
        }),
        G.GitHubInstallation,
      ),
    ),
  installations: (tenant) =>
    all(async (page_token) =>
      value(
        read(
          client.GET("/v1/tenants/{tenant}/github/installations", { params: { path: { tenant }, query: { page_size: PAGE, page_token } } }),
          page(G.GitHubInstallation),
        ),
      ),
    ),
  forgetInstallation: (tenant, installation) => done(client.DELETE("/v1/tenants/{tenant}/github/installations/{installation}", { params: { path: { tenant, installation } } })),

  connections: (tenant, project) =>
    all(async (page_token) =>
      value(
        read(
          client.GET("/v1/tenants/{tenant}/github/connections", { params: { path: { tenant }, query: { project, page_size: PAGE, page_token } } }),
          page(G.GitConnection),
        ),
      ),
    ),
  connect: (tenant, body, key) =>
    value(read(client.POST("/v1/tenants/{tenant}/github/connections", { params: { path: { tenant }, header: { "Idempotency-Key": key } }, body }), G.GitConnection)),
  changeConnection: (tenant, connection, body, version) =>
    value(
      read(
        client.PATCH("/v1/tenants/{tenant}/github/connections/{connection}", {
          params: { path: { tenant, connection }, header: { "If-Match": `"${version}"` } },
          body,
        }),
        G.GitConnection,
      ),
    ),
  disconnect: (tenant, connection) => done(client.DELETE("/v1/tenants/{tenant}/github/connections/{connection}", { params: { path: { tenant, connection } } })),
};

export const GITHUB: InjectionKey<GitHubPort> = Symbol("github");

/** The provided port, else the API adapter. */
export function useGitHub(): GitHubPort {
  return inject(GITHUB, apiGitHub);
}
