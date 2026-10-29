/**
 * An in-memory GitHubPort for component tests, with the server's rules
 * in miniature (RFC 0004 §6): an install intent's state is single-use
 * and the callback must hand it back; a repository the installation
 * cannot see is refused; the same repository and path twice is refused,
 * a second path is not. Test-only: nothing in the app imports it.
 */
import { ApiError } from "../api/errors";
import type { GitConnectionChange, GitConnectionRequest, GitHubPort, InstallCallback } from "../api/github";
import type { GitConnection, GitHubInstallation, GitHubInstallIntent } from "../api/github-schemas";

export interface FakeGitHub extends GitHubPort {
  readonly calls: Array<[string, ...unknown[]]>;
  installationList: GitHubInstallation[];
  connectionList: GitConnection[];
  /** Where startInstall says to send the person. */
  installUrl: string;
  /** The installation a redeemed state resolves to. */
  installs: GitHubInstallation;
  /** Set to make every call fail with this code (e.g. github_not_configured). */
  failWith: string | undefined;
}

const NOW = "2026-09-20T10:00:00Z";

export function installation(over: Partial<GitHubInstallation> = {}): GitHubInstallation {
  return {
    id: "inst-1",
    installation_id: 4242,
    account_login: "acme",
    account_type: "Organization",
    state: "active",
    connected_by: "person:one",
    connected_at: NOW,
    repositories: [{ repository_id: 9001, name: "shop", full_name: "acme/shop", private: false, default_branch: "main" }],
    repositories_unavailable: false,
    ...over,
  };
}

export function connection(over: Partial<GitConnection> = {}): GitConnection {
  return {
    id: "conn-1",
    installation_id: "inst-1",
    repository_id: 9001,
    repository_name: "acme/shop",
    project_id: "p1",
    application_id: "a1",
    default_branch: "main",
    path: "",
    created_by: "person:one",
    created_at: NOW,
    updated_at: NOW,
    version: 0,
    ...over,
  };
}

export function createFakeGitHub(over: Partial<FakeGitHub> = {}): FakeGitHub {
  const calls: Array<[string, ...unknown[]]> = [];
  const states = new Set<string>();
  let nextId = 2;

  const fail = (): void => {
    if (!fake.failWith) return;
    const status = fake.failWith === "github_not_configured" ? 503 : 400;
    throw new ApiError(status, fake.failWith, `the fake refuses with ${fake.failWith}`);
  };

  const fake: FakeGitHub = {
    calls,
    installationList: [],
    connectionList: [],
    installUrl: "https://github.test/apps/glossa/installations/new",
    installs: installation(),
    failWith: undefined,

    async startInstall(tenant): Promise<GitHubInstallIntent> {
      calls.push(["startInstall", tenant]);
      fail();
      const state = `state-${states.size + 1}`;
      states.add(state);
      return { state, install_url: `${fake.installUrl}?state=${state}`, expires_at: NOW };
    },

    async completeInstall(tenant, cb: InstallCallback): Promise<GitHubInstallation> {
      calls.push(["completeInstall", tenant, cb]);
      fail();
      if (!states.delete(cb.state)) throw new ApiError(400, "invalid_install_state", "the install state is unknown, expired or already used");
      if (fake.installationList.some((i) => i.installation_id === cb.installation_id)) {
        throw new ApiError(409, "installation_already_claimed", "this GitHub installation is already connected to a workspace");
      }
      const made = { ...fake.installs, installation_id: cb.installation_id };
      fake.installationList = [...fake.installationList, made];
      return made;
    },

    async installations(tenant): Promise<GitHubInstallation[]> {
      calls.push(["installations", tenant]);
      fail();
      return fake.installationList;
    },

    async forgetInstallation(tenant, id): Promise<void> {
      calls.push(["forgetInstallation", tenant, id]);
      fail();
      if (!fake.installationList.some((i) => i.id === id)) throw new ApiError(404, "not_found", "no such installation");
      fake.installationList = fake.installationList.filter((i) => i.id !== id);
      fake.connectionList = fake.connectionList.filter((c) => c.installation_id !== id);
    },

    async connections(tenant, project): Promise<GitConnection[]> {
      calls.push(["connections", tenant, project]);
      fail();
      return project ? fake.connectionList.filter((c) => c.project_id === project) : fake.connectionList;
    },

    async connect(tenant, body: GitConnectionRequest, key): Promise<GitConnection> {
      calls.push(["connect", tenant, body, key]);
      fail();
      const inst = fake.installationList.find((i) => i.id === body.installation_id);
      const repo = (inst?.repositories ?? []).find((r) => r.repository_id === body.repository_id);
      if (!repo) throw new ApiError(404, "repository_not_visible", "the installation cannot see that repository");
      const path = (body.path ?? "").replace(/^\/+|\/+$/g, "");
      if (fake.connectionList.some((c) => c.repository_id === body.repository_id && c.path === path)) {
        throw new ApiError(409, "connection_exists", "this repository and path are already connected");
      }
      const made = connection({
        id: `conn-${nextId++}`,
        installation_id: body.installation_id,
        repository_id: body.repository_id,
        repository_name: repo.full_name,
        project_id: body.project_id,
        application_id: body.application_id,
        default_branch: body.default_branch || repo.default_branch,
        path,
      });
      fake.connectionList = [...fake.connectionList, made];
      return made;
    },

    async changeConnection(tenant, id, body: GitConnectionChange, version): Promise<GitConnection> {
      calls.push(["changeConnection", tenant, id, body, version]);
      fail();
      const have = fake.connectionList.find((c) => c.id === id);
      if (!have) throw new ApiError(404, "not_found", "no such connection");
      const next = { ...have, ...body, path: (body.path ?? "").replace(/^\/+|\/+$/g, "") };
      fake.connectionList = fake.connectionList.map((c) => (c.id === id ? next : c));
      return next;
    },

    async disconnect(tenant, id): Promise<void> {
      calls.push(["disconnect", tenant, id]);
      fail();
      if (!fake.connectionList.some((c) => c.id === id)) throw new ApiError(404, "not_found", "no such connection");
      fake.connectionList = fake.connectionList.filter((c) => c.id !== id);
    },
  };
  return Object.assign(fake, over);
}
