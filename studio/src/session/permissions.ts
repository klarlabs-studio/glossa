/**
 * What the signed-in member may do in a tenant, mirrored from Identity's
 * role matrix (platform/internal/identity/domain/access.go) so Studio can
 * disable what would be refused and say why. The server remains the
 * authority: every write is checked there again.
 */
import type { Membership, Role } from "../api/schemas";
import { ancestry } from "../lib/bcp47";

export type Permission =
  | "tenant.read"
  | "tenant.manage"
  | "members.read"
  | "members.manage"
  | "owners.manage"
  | "tokens.read"
  | "tokens.manage"
  | "catalog.read"
  | "catalog.write"
  | "translations.read"
  | "translations.write"
  | "translations.review"
  | "releases.read"
  | "releases.publish"
  | "knowledge.read"
  | "knowledge.write"
  | "intelligence.read"
  | "intelligence.manage"
  | "intelligence.translate"
  | "integration.read"
  | "integration.import"
  | "integration.manage"
  // The operations permissions (RFC 0006 §4.2).
  | "workflows.read"
  | "workflows.manage"
  | "assignments.read"
  | "assignments.manage"
  | "approvals.decide"
  | "vendors.manage"
  | "audit.read"
  | "audit.export";

const ALL: Permission[] = [
  "approvals.decide", "assignments.manage", "assignments.read", "audit.export", "audit.read", "vendors.manage", "workflows.manage", "workflows.read",
  "catalog.read", "catalog.write", "integration.import", "integration.manage", "integration.read", "intelligence.manage", "intelligence.read", "intelligence.translate",
  "knowledge.read", "knowledge.write", "members.manage", "members.read", "owners.manage", "releases.publish",
  "releases.read", "tenant.manage", "tenant.read", "tokens.manage", "tokens.read", "translations.read",
  "translations.review", "translations.write",
];
const READ_ALL: Permission[] = [
  "tenant.read", "members.read", "catalog.read", "translations.read", "releases.read", "knowledge.read",
  "intelligence.read", "integration.read", "workflows.read", "assignments.read",
];

export const ROLE_PERMISSIONS: Record<Role, readonly Permission[]> = {
  owner: ALL,
  // Only an owner manages owners and, by default, exports the audit log.
  admin: ALL.filter((p) => p !== "owners.manage" && p !== "audit.export"),
  developer: [
    ...READ_ALL, "tokens.read", "tokens.manage", "catalog.write", "translations.write", "releases.publish",
    "knowledge.write", "intelligence.translate", "integration.import", "integration.manage",
  ],
  translator: [...READ_ALL, "translations.write", "intelligence.translate", "integration.import"],
  reviewer: [...READ_ALL, "translations.write", "translations.review", "intelligence.translate", "integration.import", "approvals.decide"],
};

const LOCALE_SCOPED_PERMISSIONS = new Set<Permission>([
  "translations.write", "translations.review", "intelligence.translate", "integration.import", "approvals.decide",
]);
const LOCALE_SCOPED_ROLES = new Set<Role>(["translator", "reviewer"]);

/** Permission → the locales it's limited to; `null` means every locale. */
export type Grant = ReadonlyMap<Permission, readonly string[] | null>;

export function grantFor(membership: Pick<Membership, "roles" | "locales"> | undefined): Grant {
  const grant = new Map<Permission, string[] | null>();
  if (!membership) return grant;
  for (const role of membership.roles) {
    for (const p of ROLE_PERMISSIONS[role]) {
      const scoped = LOCALE_SCOPED_PERMISSIONS.has(p) && LOCALE_SCOPED_ROLES.has(role) && membership.locales.length > 0;
      const scope = scoped ? [...membership.locales] : null;
      const cur = grant.get(p);
      if (cur === undefined) grant.set(p, scope);
      else if (cur === null || scope === null) grant.set(p, null);
      else grant.set(p, [...new Set([...cur, ...scope])]);
    }
  }
  return grant;
}

/** Granted for every locale. */
export function allows(grant: Grant, p: Permission): boolean {
  return grant.get(p) === null;
}

/**
 * Granted for `locale`: the scope names it or one of its ancestors
 * (`de` covers `de-AT`). Advisory — the server applies the CLDR data.
 */
export function allowsFor(grant: Grant, p: Permission, locale: string): boolean {
  const scope = grant.get(p);
  if (scope === undefined) return false;
  if (scope === null) return true;
  return ancestry(locale).some((l) => scope.includes(l));
}
