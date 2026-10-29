/**
 * The rules the work screens apply to what the API returns (RFC 0006
 * §3.1–§3.2), kept out of the components so they can be tested alone.
 */
import type { Role } from "../api/schemas";
import { isLive, type Approval, type Assignment, type Group } from "../api/work-schemas";

/** The locales an assignment's units are in, sorted, each once. */
export function unitLocales(a: Pick<Assignment, "units">): string[] {
  return [...new Set(a.units.map((u) => u.locale))].sort();
}

export type Due = "none" | "due" | "overdue";

/** Whether a live assignment is past its due date. A finished one is never overdue. */
export function dueState(a: Pick<Assignment, "due_at" | "state">, now: number = Date.now()): Due {
  if (!a.due_at) return "none";
  return isLive(a) && Date.parse(a.due_at) < now ? "overdue" : "due";
}

/** Live work first, then finished, each oldest due (then oldest made) first. */
export function byUrgency(a: Assignment, b: Assignment): number {
  const live = Number(isLive(b)) - Number(isLive(a));
  if (live) return live;
  const due = (x: Assignment) => (x.due_at ? Date.parse(x.due_at) : Number.POSITIVE_INFINITY);
  return due(a) - due(b) || Date.parse(a.created_at) - Date.parse(b.created_at);
}

/** A unit as one string, for set membership. */
export const unitKey = (message: string, locale: string): string => `${message}\u0000${locale}`;

/** Every unit of the live assignments, for "assigned to me". */
export function liveUnits(list: readonly Assignment[]): Set<string> {
  const out = new Set<string>();
  for (const a of list) if (isLive(a)) for (const u of a.units) out.add(unitKey(u.message_id, u.locale));
  return out;
}

/**
 * Whether the signed-in member is in an approval's eligible party.
 * `maybe` is a group whose members could not be read: the approval is
 * listed and the server decides, rather than it silently disappearing.
 */
export type Eligibility = "yes" | "maybe" | "no";

export interface Self {
  memberId: string;
  roles: readonly Role[];
  /** The tenant's groups; `undefined` when they could not be read. */
  groups: readonly Group[] | undefined;
}

export function eligibility(ap: Pick<Approval, "eligible">, self: Self): Eligibility {
  const e = ap.eligible;
  switch (e.kind) {
    case "member":
      return e.id === self.memberId ? "yes" : "no";
    case "role":
      return e.role && self.roles.includes(e.role) ? "yes" : "no";
    case "group": {
      if (!self.groups) return "maybe";
      const g = self.groups.find((x) => x.id === e.id);
      return g?.members.includes(self.memberId) ? "yes" : "no";
    }
    default:
      // An approval is never asked of a vendor (RFC 0006 §3.2).
      return "no";
  }
}

/** Whether `principal` has decided on this approval already; one person counts once. */
export const hasDecided = (ap: Pick<Approval, "decisions">, principal: string): boolean => ap.decisions.some((d) => d.principal === principal);

/** Distinct people who granted it so far. */
export const grantCount = (ap: Pick<Approval, "decisions">): number =>
  new Set(ap.decisions.filter((d) => d.decision === "granted").map((d) => d.principal)).size;
