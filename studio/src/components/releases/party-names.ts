/**
 * Names for the parties an approval requirement names (RFC 0006 §5.1): a
 * group by id or name, a member by id. Best effort and read once per
 * screen: a failure only leaves the ids as they are.
 */
import { shallowRef, watch, type ShallowRef } from "vue";
import { members as membersApi } from "../../api/endpoints";
import { useWork } from "../../api/work";
import type { PartyNames } from "../../lib/release-ops";

export function usePartyNames(tenant: () => string): ShallowRef<PartyNames> {
  const work = useWork();
  const names = shallowRef<PartyNames>({});
  watch(
    tenant,
    async (t) => {
      const [gs, ms] = await Promise.allSettled([work.groups(t), membersApi.list(t)]);
      const groups = gs.status === "fulfilled" ? gs.value : [];
      const members = ms.status === "fulfilled" ? ms.value : [];
      names.value = {
        group: (g) => groups.find((x) => x.id === g || x.name.toLowerCase() === g.toLowerCase())?.name,
        member: (id) => {
          const m = members.find((x) => x.id === id);
          return m ? m.display_name || m.email : undefined;
        },
      };
    },
    { immediate: true },
  );
  return names;
}
