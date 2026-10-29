/**
 * zod schemas for the GitHub integration (RFC 0004 §6): installations,
 * the repositories they cover, and the Git connections that tie a
 * repository to a project and application. Kept out of ./schemas so
 * only the workspace's GitHub screen loads them; the compile-time
 * assertions at the bottom tie each to the contract, so a change to
 * `platform/api/openapi.yaml` that breaks one fails `pnpm lint`.
 */
import { z } from "zod";
import type { components } from "./schema";

const timestamp = z.string().min(1);
const id = z.string().min(1);

export const GitHubInstallationState = z.enum(["active", "suspended", "revoked"]);

export const GitHubRepository = z.object({
  repository_id: z.number().int(),
  name: z.string(),
  full_name: z.string(),
  private: z.boolean(),
  default_branch: z.string(),
});

export const GitHubInstallation = z.object({
  id,
  installation_id: z.number().int(),
  account_login: z.string(),
  account_type: z.enum(["User", "Organization"]),
  state: GitHubInstallationState,
  connected_by: z.string().optional(),
  connected_at: timestamp,
  repositories: z.array(GitHubRepository).optional(),
  repositories_unavailable: z.boolean(),
});

export const GitHubInstallIntent = z.object({
  state: z.string(),
  install_url: z.string(),
  expires_at: timestamp,
});

export const GitConnection = z.object({
  id,
  installation_id: id,
  repository_id: z.number().int(),
  repository_name: z.string(),
  project_id: id,
  application_id: id,
  default_branch: z.string(),
  path: z.string(),
  created_by: z.string().optional(),
  created_at: timestamp,
  updated_at: timestamp.optional(),
  version: z.number().int(),
});

export type GitHubInstallationState = z.infer<typeof GitHubInstallationState>;
export type GitHubRepository = z.infer<typeof GitHubRepository>;
export type GitHubInstallation = z.infer<typeof GitHubInstallation>;
export type GitHubInstallIntent = z.infer<typeof GitHubInstallIntent>;
export type GitConnection = z.infer<typeof GitConnection>;

// ── contract alignment (compile time only) ─────────────────────────────
type C = components["schemas"];
type Fits<A, B> = [A] extends [B] ? true : false;
type Assert<T extends true> = T;
export type GitHubContractAlignment = [
  Assert<Fits<GitHubInstallation, C["GitHubInstallation"]>>,
  Assert<Fits<GitHubRepository, C["GitHubRepository"]>>,
  Assert<Fits<GitHubInstallIntent, C["GitHubInstallIntent"]>>,
  Assert<Fits<GitConnection, C["GitConnection"]>>,
  Assert<Fits<C["GitHubInstallationState"], GitHubInstallationState>>,
];
