/**
 * Pure release logic Studio needs before asking the server: the order
 * environments show in, the eligibility check mirrored from Release's
 * domain (the server stays the authority), rollback targets from an
 * environment's history, and diff totals.
 */
import type { Deployment, Environment, EnvironmentPolicy, LocaleDiff, Release, ReleaseDiff, ShippableState } from "../api/schemas";

/** The defaults every project has, in the order text travels. */
export const DEFAULT_ENVIRONMENTS = ["development", "preview", "staging", "production"] as const;

export const SHIPPABLE_STATES: readonly ShippableState[] = ["draft", "needs_review", "approved"];

/** Defaults in pipeline order, then custom environments by name. */
export function sortEnvironments(envs: readonly Environment[]): Environment[] {
  const rank = (name: string) => {
    const i = (DEFAULT_ENVIRONMENTS as readonly string[]).indexOf(name);
    return i < 0 ? DEFAULT_ENVIRONMENTS.length : i;
  };
  return [...envs].sort((a, b) => rank(a.name) - rank(b.name) || a.name.localeCompare(b.name));
}

/**
 * Whether everything a release built under `built` may ship is allowed
 * under `target` (Release's `Policy.Covers`): a release with drafts can't
 * be promoted where only approved text ships.
 */
export function covers(target: EnvironmentPolicy, built: EnvironmentPolicy): boolean {
  return built.states.every((s) => target.states.includes(s)) && (target.include_outdated || !built.include_outdated);
}

/** The review states `built` ships that `target` doesn't allow. */
export function uncovered(target: EnvironmentPolicy, built: EnvironmentPolicy): { states: ShippableState[]; outdated: boolean } {
  return {
    states: built.states.filter((s) => !target.states.includes(s)),
    outdated: built.include_outdated && !target.include_outdated,
  };
}

/** Release id → the environments serving it. */
export function servingMap(envs: readonly Environment[]): Map<string, string[]> {
  const out = new Map<string, string[]>();
  for (const e of sortEnvironments(envs)) {
    if (!e.current_release_id) continue;
    out.set(e.current_release_id, [...(out.get(e.current_release_id) ?? []), e.name]);
  }
  return out;
}

/** Releases an environment served before, excluding the current one, newest version first. */
export function rollbackCandidates(history: readonly Deployment[], releases: ReadonlyMap<string, Release>, current: string | undefined): Release[] {
  const ids = new Set(history.map((d) => d.release_id));
  if (current) ids.delete(current);
  return [...ids]
    .map((id) => releases.get(id))
    .filter((r): r is Release => r !== undefined)
    .sort((a, b) => b.version - a.version);
}

/**
 * What a rollback without an explicit target picks on the server: the
 * newest release served that is older than the current one.
 */
export function defaultRollbackTarget(candidates: readonly Release[], current: Release | undefined): Release | undefined {
  if (!current) return undefined;
  return candidates.find((r) => r.version < current.version);
}

export interface DiffTotals {
  added: number;
  changed: number;
  removed: number;
}

export function localeTotals(d: LocaleDiff): DiffTotals {
  return { added: d.added.length, changed: d.changed.length, removed: d.removed.length };
}

export function diffTotals(diff: ReleaseDiff): DiffTotals {
  return diff.locales.reduce(
    (t, d) => ({ added: t.added + d.added.length, changed: t.changed + d.changed.length, removed: t.removed + d.removed.length }),
    { added: 0, changed: 0, removed: 0 },
  );
}

export const isEmptyDiff = (diff: ReleaseDiff): boolean => {
  const t = diffTotals(diff);
  return t.added + t.changed + t.removed === 0;
};
