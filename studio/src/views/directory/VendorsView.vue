<script setup lang="ts">
/**
 * The organization's vendors (RFC 0006 §3.3): agencies and freelancer
 * teams that work inside this organization as named sets of members, each
 * of whom sees only the work assigned to them.
 *
 * Adding a vendor needs `vendors.manage`; member counts come from the
 * member list, and say "not known" rather than zero when it can't be read.
 */
import { computed, ref, shallowRef, watch } from "vue";
import { RouterLink, useRoute, useRouter } from "vue-router";
import { useDirectory } from "../../api/directory";
import type { Vendor } from "../../api/directory-schemas";
import { newIdempotencyKey } from "../../api/releases";
import type { Member } from "../../api/schemas";
import { allows } from "../../session/permissions";
import { useGrant } from "../../session/session";
import { problemText, strings } from "../../strings";

const d = strings.directory;
const s = d.vendors;
const route = useRoute();
const router = useRouter();
const port = useDirectory();
const tenant = computed(() => String(route.params.tenant));
const grant = useGrant(() => tenant.value);
const canManage = computed(() => allows(grant.value, "vendors.manage"));

type Phase = "loading" | "ready" | "failed";
const phase = ref<Phase>("loading");
const loadError = ref<unknown>(null);
const vendors = shallowRef<Vendor[]>([]);
const members = shallowRef<Member[] | undefined>();

async function load(): Promise<void> {
  phase.value = "loading";
  loadError.value = null;
  const [vs, ms] = await Promise.allSettled([port.vendors(tenant.value), port.members(tenant.value)]);
  members.value = ms.status === "fulfilled" ? ms.value : undefined;
  if (vs.status === "rejected") {
    vendors.value = [];
    loadError.value = vs.reason;
    phase.value = "failed";
    return;
  }
  vendors.value = vs.value;
  phase.value = "ready";
}
watch(tenant, () => void load(), { immediate: true });

const countOf = (v: Vendor) => members.value?.filter((m) => m.vendor_id === v.id).length;

const form = ref({ name: "", contact: "", locales: "" });
const busy = ref(false);
const createError = ref("");
let key = newIdempotencyKey();
watch(form, () => (key = newIdempotencyKey()), { deep: true });
const splitLocales = (text: string) => text.split(/[\s,]+/).map((l) => l.trim()).filter(Boolean);

async function create(): Promise<void> {
  const name = form.value.name.trim();
  if (!name || busy.value) return;
  busy.value = true;
  createError.value = "";
  try {
    const contact = form.value.contact.trim();
    const locales = splitLocales(form.value.locales);
    const v = await port.createVendor(tenant.value, { name, ...(contact ? { contact } : {}), ...(locales.length ? { locales } : {}) }, key);
    await router.push({ name: "vendor", params: { tenant: tenant.value, vendor: v.id } });
  } catch (e) {
    createError.value = problemText(e);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="page stack vendors">
    <div class="page-header">
      <div class="stack-sm">
        <RouterLink :to="{ name: 'projects', params: { tenant } }">{{ d.back }}</RouterLink>
        <h1>{{ s.title }}</h1>
        <p class="muted lead">{{ s.lead }}</p>
      </div>
    </div>
    <p v-if="!canManage" class="alert" data-testid="vendors-read-only">{{ s.readOnly }}</p>

    <form v-if="canManage" class="card stack-sm" aria-labelledby="vnd-create-h" data-testid="vendor-create" @submit.prevent="create">
      <h2 id="vnd-create-h">{{ s.create }}</h2>
      <div class="grid">
        <div class="field">
          <label for="vnd-name">{{ s.name }}</label>
          <input id="vnd-name" v-model="form.name" maxlength="100" required />
        </div>
        <div class="field">
          <label for="vnd-contact">{{ s.contact }}</label>
          <input id="vnd-contact" v-model="form.contact" maxlength="200" aria-describedby="vnd-contact-hint" />
          <span id="vnd-contact-hint" class="hint">{{ s.contactHint }}</span>
        </div>
        <div class="field">
          <label for="vnd-locales">{{ s.locales }}</label>
          <input id="vnd-locales" v-model="form.locales" class="mono" aria-describedby="vnd-locales-hint" />
          <span id="vnd-locales-hint" class="hint">{{ s.localesHint }}</span>
        </div>
      </div>
      <p v-if="createError" class="alert alert-error" role="alert" data-testid="vendor-create-error">{{ createError }}</p>
      <div><button type="submit" class="btn btn-primary" :disabled="busy || !form.name.trim()">{{ s.create }}</button></div>
    </form>

    <p v-if="phase === 'loading'" class="muted" role="status" data-testid="vendors-loading">{{ s.loading }}</p>
    <div v-else-if="phase === 'failed'" class="alert alert-error stack-sm" role="alert" data-testid="vendors-failed">
      <p class="alert-title">{{ s.failed }}</p>
      <p>{{ problemText(loadError) }}</p>
      <div><button type="button" class="btn btn-sm" @click="load">{{ d.retry }}</button></div>
    </div>
    <p v-else-if="!vendors.length" class="card muted" data-testid="vendors-empty">{{ s.empty }}</p>
    <template v-else>
      <p v-if="members === undefined" class="alert alert-warn" data-testid="vendors-members-failed">{{ s.membersFailed }}</p>
      <div class="scroll">
        <table class="table" data-testid="vendor-list">
          <thead>
            <tr>
              <th scope="col">{{ s.columns.name }}</th>
              <th scope="col">{{ s.columns.contact }}</th>
              <th scope="col">{{ s.columns.locales }}</th>
              <th scope="col" class="num">{{ s.columns.members }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="v in vendors" :key="v.id" data-testid="vendor-row" :data-vendor="v.id">
              <th scope="row">
                <RouterLink :to="{ name: 'vendor', params: { tenant, vendor: v.id } }">{{ v.name }}</RouterLink>
              </th>
              <td>{{ v.contact }}</td>
              <td class="mono">{{ v.locales.length ? v.locales.join(", ") : s.noLocales }}</td>
              <td class="num" data-testid="vendor-members">{{ s.members(countOf(v)) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
  </div>
</template>

<style scoped>
.vendors {
  max-inline-size: var(--kl-content-lg, 64rem);
}
.lead {
  max-inline-size: 48rem;
}
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(14rem, 1fr));
  gap: var(--kl-space-3);
}
.scroll {
  overflow-x: auto;
}
.num {
  text-align: end;
  font-variant-numeric: tabular-nums;
}
</style>
