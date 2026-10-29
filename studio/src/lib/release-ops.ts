/**
 * The rules the release-policy screens apply to what the API returns
 * (RFC 0006 §5), kept out of the components so they can be tested alone.
 */
import type { EnvironmentApproval, EnvironmentApprovalParty } from "../api/schemas";
import type { Rollout } from "../api/release-ops-schemas";
import type { Approval } from "../api/work-schemas";
import { releaseOpsStrings as s } from "../strings-release-ops";

/** Names for a party's group or member; an id it doesn't know stays as it is. */
export interface PartyNames {
  group?: (idOrName: string) => string | undefined;
  member?: (id: string) => string | undefined;
}

/** "reviewers", "the group legal", "Vera". */
export function partyText(from: EnvironmentApprovalParty, names: PartyNames = {}): string {
  if (from.role) return s.party.role(from.role);
  if (from.group) return s.party.group(names.group?.(from.group) ?? from.group);
  if (from.member) return s.party.member(names.member?.(from.member) ?? from.member);
  return "";
}

/** "Needs 2 approvals from reviewers". */
export const requirementText = (a: EnvironmentApproval, names?: PartyNames): string => s.needs(a.n, partyText(a.from, names));

/** Two requirements ask the same of the same party (a restatement needs no `workflows.manage`). */
export function sameApproval(a: EnvironmentApproval | undefined, b: EnvironmentApproval | undefined): boolean {
  if (!a || !b) return !a && !b;
  return a.n === b.n && (a.from.role ?? "") === (b.from.role ?? "") && (a.from.group ?? "") === (b.from.group ?? "") && (a.from.member ?? "") === (b.from.member ?? "");
}

/** Distinct people who granted, as Release counts them. */
export const grants = (a: Pick<Approval, "decisions">): number => new Set(a.decisions.filter((d) => d.decision === "granted").map((d) => d.principal)).size;

/** The active rollout of an environment's list, if any. */
export const activeRollout = (list: readonly Rollout[]): Rollout | undefined => list.find((r) => r.status === "active");

export const DAY_SECONDS = 86_400;
export const MIN_DURATION_SECONDS = 3_600;
export const MAX_DURATION_SECONDS = 90 * DAY_SECONDS;
export const DEFAULT_DURATION_DAYS = 14;

/** Days (fractional allowed) as the API's seconds, or undefined when outside one hour–90 days. */
export function durationSeconds(days: number): number | undefined {
  if (!Number.isFinite(days)) return undefined;
  const sec = Math.round(days * DAY_SECONDS);
  return sec >= MIN_DURATION_SECONDS && sec <= MAX_DURATION_SECONDS ? sec : undefined;
}

/** A whole number from 0 to 100. */
export const validPercent = (n: number): boolean => Number.isInteger(n) && n >= 0 && n <= 100;
