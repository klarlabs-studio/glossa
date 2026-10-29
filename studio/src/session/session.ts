/**
 * The signed-in person: who they are, which tenants they're in, and the
 * CSRF token their session needs. One reactive store for the whole app.
 */
import { computed, readonly, shallowRef } from "vue";
import { setCsrfToken } from "../api/client";
import { auth, me as meApi } from "../api/endpoints";
import { isApiError } from "../api/errors";
import type { Me, Membership, Session } from "../api/schemas";
import { grantFor, type Grant } from "./permissions";

export type SessionStatus = "unknown" | "anonymous" | "authenticated";

const me = shallowRef<Me | null>(null);
const status = shallowRef<SessionStatus>("unknown");

const LAST_TENANT = "glossa:last-tenant";

function storage(): Storage | undefined {
  try {
    return globalThis.localStorage;
  } catch {
    return undefined;
  }
}

function setMe(value: Me | null): void {
  me.value = value;
  status.value = value ? "authenticated" : "anonymous";
  setCsrfToken(value?.csrf_token);
}

/** Load the person behind the session cookie; null when there's none. */
export async function refreshSession(): Promise<Me | null> {
  try {
    setMe(await meApi.get());
  } catch (e) {
    if (isApiError(e) && e.status === 401) setMe(null);
    else throw e;
  }
  return me.value;
}

/** After a sign-in operation: adopt its CSRF token and load memberships. */
export async function establishSession(session: Session): Promise<Me | null> {
  setCsrfToken(session.csrf_token);
  return refreshSession();
}

export async function signOut(everywhere = false): Promise<void> {
  try {
    await (everywhere ? auth.signOutEverywhere() : auth.signOut());
  } finally {
    setMe(null);
  }
}

/** The session ended server-side (a 401 on a request that needed it). */
export function sessionExpired(): void {
  setMe(null);
}

export function membershipFor(tenantId: string | undefined): Membership | undefined {
  return me.value?.memberships.find((m) => m.tenant.id === tenantId);
}

export function rememberTenant(tenantId: string): void {
  storage()?.setItem(LAST_TENANT, tenantId);
}

/** Where "home" is: the last tenant used, else the person's own. */
export function homeTenant(): string | undefined {
  const last = storage()?.getItem(LAST_TENANT) ?? undefined;
  if (last && membershipFor(last)) return last;
  return me.value?.person.individual_tenant_id ?? me.value?.memberships[0]?.tenant.id;
}

export function useSession() {
  return {
    me: readonly(me),
    status: readonly(status),
    person: computed(() => me.value?.person),
    memberships: computed(() => me.value?.memberships ?? []),
  };
}

/** The grant for a tenant, reactive to session changes. */
export function useGrant(tenantId: () => string | undefined) {
  return computed<Grant>(() => grantFor(membershipFor(tenantId())));
}
