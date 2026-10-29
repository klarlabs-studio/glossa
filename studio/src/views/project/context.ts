/**
 * What every project screen shares: the project (with its ETag), its
 * locales and the member's grant. ProjectLayout loads it once and
 * provides it; screens inject it.
 */
import { inject, type ComputedRef, type InjectionKey, type Ref } from "vue";
import type { QualitySummary } from "../../api/quality-summary-schemas";
import type { Project, ProjectLocale } from "../../api/schemas";
import type { Grant } from "../../session/permissions";

export interface ProjectContext {
  tenant: ComputedRef<string>;
  projectId: ComputedRef<string>;
  project: Ref<Project | undefined>;
  etag: Ref<string | undefined>;
  locales: Ref<ProjectLocale[]>;
  /** Target locales: every locale but the source. */
  targets: ComputedRef<ProjectLocale[]>;
  grant: ComputedRef<Grant>;
  /**
   * The quality summary (RFC 0005 §8), loaded once here and read by the
   * navigation's one number and by the health header. `undefined` is
   * "not measured", never "all zero": `healthState` says which of the
   * three it is.
   */
  health: Ref<QualitySummary | undefined>;
  healthState: Ref<HealthState>;
  reloadProject(): Promise<void>;
  reloadLocales(): Promise<void>;
  reloadHealth(): Promise<void>;
}

/**
 * Anything but `ready` means nothing about this project's health is
 * known. The two failures are kept apart because they mean different
 * things to the person reading: `unreported` is a server that does not
 * compute the summary (which is every server until the summary slice
 * ships), and `failed` is one that tried and could not.
 */
export type HealthState = "loading" | "ready" | "unreported" | "failed";

export const PROJECT: InjectionKey<ProjectContext> = Symbol("project");

export function useProject(): ProjectContext {
  const ctx = inject(PROJECT);
  if (!ctx) throw new Error("useProject() outside ProjectLayout");
  return ctx;
}
