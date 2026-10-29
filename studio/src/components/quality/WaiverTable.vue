<script setup lang="ts">
/**
 * The waiver list (RFC 0005 §2.3, §9): what this project accepts and
 * why, newest first, revocable. A waiver whose `accepts` is empty is an
 * unexamined one — no stored finding carries its fingerprint any more —
 * which is exactly what a dashboard should show rather than quietly
 * drop.
 */
import type { Waiver } from "../../api/quality-schemas";
import { absoluteTime, relativeTime } from "../../lib/time";
import type { PersonLabel } from "../../session/people";
import { strings } from "../../strings";

defineProps<{ waivers: Waiver[]; canRevoke: boolean; busy: boolean; person: PersonLabel }>();
const emit = defineEmits<{ revoke: [waiver: Waiver] }>();
const s = strings.quality;
const c = s.waiverColumns;

const state = (w: Waiver) => (w.active ? s.waiverActive : w.revoked_at ? s.waiverRevoked : s.waiverExpired);
const accepts = (w: Waiver) => (w.accepts?.code ? s.waiverAccepts(s.layerName[w.accepts.layer ?? ""] ?? w.accepts.layer ?? "", w.accepts.code) : "");
</script>

<template>
  <div v-if="waivers.length" class="scroll">
    <table class="table" data-testid="waiver-list">
      <thead>
        <tr>
          <th scope="col">{{ c.accepts }}</th>
          <th scope="col">{{ c.reason }}</th>
          <th scope="col">{{ c.reach }}</th>
          <th scope="col">{{ c.made }}</th>
          <th scope="col">{{ c.state }}</th>
          <th v-if="canRevoke" scope="col"><span class="visually-hidden">{{ c.actions }}</span></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="w in waivers" :key="w.id" :data-fingerprint="w.fingerprint" data-testid="waiver">
          <th scope="row">
            <template v-if="accepts(w)">
              <span class="accepts">{{ accepts(w) }}</span>
              <span v-if="w.accepts?.key" class="muted"><code>{{ w.accepts.key }}</code></span>
              <span v-if="w.accepts?.locale" class="muted">{{ s.inLocale(w.accepts.locale) }}</span>
            </template>
            <span v-else class="muted" data-testid="waiver-unexamined">{{ s.waiverUnexamined }}</span>
          </th>
          <td class="reason">{{ w.reason }}</td>
          <td>
            <template v-if="w.scope === 'branch' && w.ref">{{ s.waivedScopeBranch(w.ref) }}</template>
            <template v-else>{{ s.scopeProject }}</template>
            <span v-if="w.expires_at" class="muted"> · {{ s.waivedExpires(absoluteTime(w.expires_at)) }}</span>
          </td>
          <td>
            <time :datetime="w.created_at" :title="absoluteTime(w.created_at)">{{ relativeTime(w.created_at) }}</time>
            <span class="muted"> · {{ person(w.created_by) }}</span>
          </td>
          <td><span class="pill" :class="w.active ? 'pill-accent' : 'pill-neutral'">{{ state(w) }}</span></td>
          <td v-if="canRevoke" class="actions">
            <button v-if="w.active" type="button" class="btn btn-sm" :disabled="busy" @click="emit('revoke', w)">
              {{ s.revoke }}<span class="visually-hidden">{{ s.revokeFor(w.accepts?.code ?? w.fingerprint) }}</span>
            </button>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
  <p v-else class="muted" data-testid="no-waivers">{{ s.noWaivers }}</p>
</template>

<style scoped>
.scroll {
  overflow-x: auto;
}
.accepts {
  font-weight: var(--kl-weight-medium);
}
.reason {
  max-inline-size: 22rem;
  overflow-wrap: anywhere;
}
th code {
  overflow-wrap: anywhere;
}
.actions {
  text-align: end;
}
</style>
