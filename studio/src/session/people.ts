/**
 * Names for the principals the API records (`person:<id>`, `token:<id>`):
 * the member's display name or email where the tenant's member list has
 * them, "you" for the signed-in person, a short ID otherwise. Loaded once
 * per tenant and best effort; a failure only makes labels shorter.
 */
import { computed, shallowRef, watch, type ComputedRef } from "vue";
import { members } from "../api/endpoints";
import { principalLabel } from "../strings";
import { useSession } from "./session";

const cache = new Map<string, Promise<Map<string, string>>>();

async function namesFor(tenant: string): Promise<Map<string, string>> {
  let p = cache.get(tenant);
  if (!p) {
    p = members
      .list(tenant)
      .then((list) => new Map(list.filter((m) => m.person_id).map((m) => [m.person_id!, m.display_name || m.email])))
      .catch(() => {
        cache.delete(tenant);
        return new Map<string, string>();
      });
    cache.set(tenant, p);
  }
  return p;
}

export type PersonLabel = (principal: string) => string;

export function usePeople(tenant: () => string): ComputedRef<PersonLabel> {
  const { person } = useSession();
  const names = shallowRef(new Map<string, string>());
  watch(
    tenant,
    async (t) => {
      names.value = await namesFor(t);
    },
    { immediate: true },
  );
  return computed(() => {
    const self = person.value?.id;
    const known = names.value;
    return (principal: string) => {
      const [kind, id] = principal.split(":");
      if (kind === "person" && id && id !== self && known.has(id)) return known.get(id)!;
      return principalLabel(principal, self);
    };
  });
}
