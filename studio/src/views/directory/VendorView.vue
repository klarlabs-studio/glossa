<script setup lang="ts">
/**
 * One vendor (RFC 0006 §3.3): its details, its people, and — said plainly
 * before anything else — what those people can and can't see. Every
 * vendor member is a translator with `assigned` visibility, enforced by
 * the server on every read; Studio explains it and never offers another.
 *
 * Changing the vendor needs `vendors.manage`; a member's project scope
 * needs `members.manage`; inviting the vendor's people, or taking someone
 * off it, needs both. Every change carries the resource's ETag.
 */
import { computed, nextTick, ref, shallowRef, watch } from "vue";
import { RouterLink, useRoute, useRouter } from "vue-router";
import { useDirectory } from "../../api/directory";
import type { Vendor } from "../../api/directory-schemas";
import { projects as projectsApi } from "../../api/endpoints";
import { newIdempotencyKey } from "../../api/releases";
import type { Member, Project } from "../../api/schemas";
import ModalDialog from "../../components/ModalDialog.vue";
import { allows } from "../../session/permissions";
import { useGrant } from "../../session/session";
import { problemText, strings } from "../../strings";

const d = strings.directory;
const s = d.vendor;
const route = useRoute();
const router = useRouter();
const port = useDirectory();
const tenant = computed(() => String(route.params.tenant));
const vendorId = computed(() => String(route.params.vendor));
const grant = useGrant(() => tenant.value);
const canEdit = computed(() => allows(grant.value, "vendors.manage"));
const canScope = computed(() => allows(grant.value, "members.manage"));
const canInvite = computed(() => canEdit.value && canScope.value);

type Phase = "loading" | "ready" | "failed";
const phase = ref<Phase>("loading");
const loadError = ref<unknown>(null);
const vendor = shallowRef<Vendor>();
const etag = ref<string>();
const members = shallowRef<Member[] | undefined>();
const projects = shallowRef<Project[] | undefined>();
const status = ref("");
const busy = ref(false);
const form = ref({ name: "", contact: "", locales: "" });
const saveError = ref("");

const splitLocales = (text: string) => text.split(/[\s,]+/).map((l) => l.trim()).filter(Boolean);
function fill(v: Vendor): void {
  form.value = { name: v.name, contact: v.contact ?? "", locales: v.locales.join(" ") };
}

async function load(): Promise<void> {
  phase.value = "loading";
  loadError.value = null;
  const [v, ms, ps] = await Promise.allSettled([port.vendor(tenant.value, vendorId.value), port.members(tenant.value), projectsApi.list(tenant.value)]);
  members.value = ms.status === "fulfilled" ? ms.value : undefined;
  projects.value = ps.status === "fulfilled" ? ps.value : undefined;
  if (v.status === "rejected") {
    loadError.value = v.reason;
    phase.value = "failed";
    return;
  }
  vendor.value = v.value.value;
  etag.value = v.value.etag;
  fill(v.value.value);
  phase.value = "ready";
}
watch([tenant, vendorId], () => void load(), { immediate: true });

const people = computed(() => (members.value ?? []).filter((m) => m.vendor_id === vendorId.value));
const nameOf = (m: Member) => m.display_name || m.email;
const projectName = (id: string) => projects.value?.find((p) => p.id === id)?.name ?? d.unknownProject(id);
const scopeText = (m: Member) => (m.projects.length ? m.projects.map(projectName).join(", ") : d.everyProject);

// ── details ─────────────────────────────────────────────────────────
async function save(): Promise<void> {
  const v = vendor.value;
  const name = form.value.name.trim();
  if (!v || !name || busy.value) return;
  busy.value = true;
  saveError.value = "";
  status.value = "";
  try {
    const r = await port.updateVendor(tenant.value, v.id, { name, contact: form.value.contact.trim(), locales: splitLocales(form.value.locales) }, etag.value ?? "");
    vendor.value = r.value;
    etag.value = r.etag;
    fill(r.value);
    status.value = s.saved(r.value.name);
  } catch (e) {
    saveError.value = problemText(e);
  } finally {
    busy.value = false;
  }
}

// ── delete ──────────────────────────────────────────────────────────
const deleting = ref(false);
const deleteError = ref<unknown>(null);
async function remove(): Promise<void> {
  const v = vendor.value;
  if (!v || busy.value) return;
  busy.value = true;
  deleteError.value = null;
  try {
    await port.deleteVendor(tenant.value, v.id);
    deleting.value = false;
    await router.push({ name: "vendors", params: { tenant: tenant.value } });
  } catch (e) {
    deleteError.value = e;
  } finally {
    busy.value = false;
  }
}

// ── a member's project scope ────────────────────────────────────────
const scoping = ref<Member>();
const scope = ref<string[]>([]);
const scopeError = ref("");
function openScope(m: Member): void {
  scoping.value = m;
  scope.value = [...m.projects];
  scopeError.value = "";
}
function replaceMember(m: Member): void {
  members.value = (members.value ?? []).map((x) => (x.id === m.id ? m : x));
}
async function saveScope(): Promise<void> {
  const m = scoping.value;
  if (!m || busy.value) return;
  busy.value = true;
  scopeError.value = "";
  try {
    const current = await port.member(tenant.value, m.id);
    const r = await port.restrict(tenant.value, m.id, { projects: [...scope.value] }, current.etag ?? "");
    replaceMember(r.value);
    scoping.value = undefined;
    status.value = s.scopeSaved(nameOf(r.value));
  } catch (e) {
    scopeError.value = problemText(e);
  } finally {
    busy.value = false;
  }
}

// ── taking a member off the vendor ──────────────────────────────────
const offboarding = ref<Member>();
const offError = ref("");
async function offVendor(): Promise<void> {
  const m = offboarding.value;
  if (!m || busy.value) return;
  busy.value = true;
  offError.value = "";
  try {
    const current = await port.member(tenant.value, m.id);
    const r = await port.restrict(tenant.value, m.id, { vendor_id: "" }, current.etag ?? "");
    replaceMember(r.value);
    offboarding.value = undefined;
    status.value = s.offVendored(nameOf(r.value));
    await nextTick();
    document.getElementById("vnd-members-h")?.focus();
  } catch (e) {
    // The server decides what visibility the member keeps; its refusal is the sentence.
    offError.value = problemText(e);
  } finally {
    busy.value = false;
  }
}

// ── inviting the vendor's people ────────────────────────────────────
const invite = ref({ email: "", locales: "", projects: [] as string[] });
const inviteError = ref("");
let inviteKey = newIdempotencyKey();
watch(invite, () => (inviteKey = newIdempotencyKey()), { deep: true });
async function sendInvite(): Promise<void> {
  const email = invite.value.email.trim();
  if (!email || busy.value) return;
  busy.value = true;
  inviteError.value = "";
  status.value = "";
  try {
    const m = await port.inviteVendorMember(
      tenant.value,
      { email, vendor_id: vendorId.value, roles: ["translator"], locales: splitLocales(invite.value.locales), projects: [...invite.value.projects] },
      inviteKey,
    );
    members.value = [...(members.value ?? []), m];
    invite.value = { email: "", locales: "", projects: [] };
    status.value = s.invited(email);
  } catch (e) {
    inviteError.value = problemText(e);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="page stack vendor">
    <p v-if="phase === 'loading'" class="muted" role="status" data-testid="vendor-loading">{{ s.loading }}</p>
    <div v-else-if="phase === 'failed'" class="alert alert-error stack-sm" role="alert" data-testid="vendor-failed">
      <p class="alert-title">{{ s.failed }}</p>
      <p>{{ problemText(loadError) }}</p>
      <div class="row">
        <button type="button" class="btn btn-sm" @click="load">{{ d.retry }}</button>
        <RouterLink :to="{ name: 'vendors', params: { tenant } }">{{ s.allVendors }}</RouterLink>
      </div>
    </div>
    <template v-else-if="vendor">
      <div class="page-header">
        <div class="stack-sm">
          <RouterLink :to="{ name: 'vendors', params: { tenant } }">{{ s.allVendors }}</RouterLink>
          <h1>{{ vendor.name }}</h1>
          <p v-if="vendor.contact" class="muted">{{ vendor.contact }}</p>
        </div>
        <button v-if="canEdit" type="button" class="btn btn-danger" data-testid="vendor-delete" @click="(deleting = true), (deleteError = null)">{{ s.delete }}</button>
      </div>
      <p role="status" aria-live="polite" data-testid="vendor-status">{{ status }}</p>

      <section class="card stack-sm visibility" aria-labelledby="vnd-vis-h" data-testid="vendor-visibility">
        <h2 id="vnd-vis-h">{{ s.visibilityTitle }}</h2>
        <p>{{ s.visibilityLead }}</p>
        <div class="cols">
          <div>
            <h3 class="label">{{ s.canDo }}</h3>
            <ul>
              <li v-for="line in s.can" :key="line">{{ line }}</li>
            </ul>
          </div>
          <div>
            <h3 class="label">{{ s.cannotDo }}</h3>
            <ul>
              <li v-for="line in s.cannot" :key="line">{{ line }}</li>
            </ul>
          </div>
        </div>
      </section>

      <section class="stack-sm" aria-labelledby="vnd-details-h">
        <h2 id="vnd-details-h">{{ s.details }}</h2>
        <p v-if="!canEdit" class="alert" data-testid="vendor-read-only">{{ s.readOnly }}</p>
        <form class="card stack-sm" data-testid="vendor-details" @submit.prevent="save">
          <div class="grid">
            <div class="field">
              <label for="vnd-name">{{ strings.directory.vendors.name }}</label>
              <input id="vnd-name" v-model="form.name" maxlength="100" required :disabled="!canEdit || busy" />
            </div>
            <div class="field">
              <label for="vnd-contact">{{ strings.directory.vendors.contact }}</label>
              <input id="vnd-contact" v-model="form.contact" maxlength="200" aria-describedby="vnd-contact-hint" :disabled="!canEdit || busy" />
              <span id="vnd-contact-hint" class="hint">{{ strings.directory.vendors.contactHint }}</span>
            </div>
            <div class="field">
              <label for="vnd-locales">{{ strings.directory.vendors.locales }}</label>
              <input id="vnd-locales" v-model="form.locales" class="mono" aria-describedby="vnd-locales-hint" :disabled="!canEdit || busy" />
              <span id="vnd-locales-hint" class="hint">{{ strings.directory.vendors.localesHint }}</span>
            </div>
          </div>
          <p v-if="saveError" class="alert alert-error" role="alert" data-testid="vendor-save-error">{{ saveError }}</p>
          <div v-if="canEdit"><button type="submit" class="btn btn-primary" :disabled="busy || !form.name.trim()">{{ s.save }}</button></div>
        </form>
      </section>

      <section class="stack-sm" aria-labelledby="vnd-members-h">
        <h2 id="vnd-members-h" tabindex="-1">{{ s.members }}</h2>
        <p class="muted">{{ s.membersLead }}</p>
        <p v-if="members === undefined" class="alert alert-error" role="alert" data-testid="vendor-members-failed">{{ s.membersFailed }}</p>
        <template v-else>
          <p v-if="projects === undefined" class="alert alert-warn">{{ s.projectsFailed }}</p>
          <p v-if="!people.length" class="card muted" data-testid="vendor-no-members">{{ s.noMembers }}</p>
          <div v-else class="scroll">
            <table class="table" data-testid="vendor-members">
              <thead>
                <tr>
                  <th scope="col">{{ s.columns.who }}</th>
                  <th scope="col">{{ s.columns.status }}</th>
                  <th scope="col">{{ s.columns.roles }}</th>
                  <th scope="col">{{ s.columns.locales }}</th>
                  <th scope="col">{{ s.columns.projects }}</th>
                  <th scope="col">{{ s.columns.visibility }}</th>
                  <th v-if="canScope" scope="col"><span class="visually-hidden">{{ s.columns.actions }}</span></th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="m in people" :key="m.id" data-testid="vendor-member" :data-member="m.id">
                  <th scope="row">
                    {{ nameOf(m) }}
                    <span v-if="m.display_name" class="hint"><br />{{ m.email }}</span>
                  </th>
                  <td>{{ d.status[m.status] ?? m.status }}</td>
                  <td>{{ m.roles.join(", ") }}</td>
                  <td class="mono">{{ m.locales.length ? m.locales.join(", ") : s.allLocales }}</td>
                  <td data-testid="member-projects">{{ scopeText(m) }}</td>
                  <td>
                    <span class="pill" :class="m.visibility === 'assigned' ? 'pill-warn' : 'pill-neutral'" data-testid="member-visibility">{{ d.visibility[m.visibility] }}</span>
                  </td>
                  <td v-if="canScope" class="actions">
                    <button type="button" class="btn btn-sm btn-ghost" data-testid="member-scope" @click="openScope(m)">
                      {{ s.editScope }}<span class="visually-hidden">{{ s.ofMember(nameOf(m)) }}</span>
                    </button>
                    <button v-if="canEdit" type="button" class="btn btn-sm btn-ghost" data-testid="member-off" @click="(offboarding = m), (offError = '')">
                      {{ s.offVendor }}<span class="visually-hidden">{{ s.ofMember(nameOf(m)) }}</span>
                    </button>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </template>
      </section>

      <section class="card stack-sm" aria-labelledby="vnd-invite-h">
        <h2 id="vnd-invite-h">{{ s.invite }}</h2>
        <p class="muted">{{ s.inviteLead }}</p>
        <p v-if="!canInvite" class="alert" data-testid="vendor-invite-needs">{{ s.inviteNeeds }}</p>
        <form v-else class="stack-sm" data-testid="vendor-invite" @submit.prevent="sendInvite">
          <div class="grid">
            <div class="field">
              <label for="vnd-inv-email">{{ s.email }}</label>
              <input id="vnd-inv-email" v-model="invite.email" type="email" required autocomplete="off" />
            </div>
            <div class="field">
              <label for="vnd-inv-locales">{{ s.inviteLocales }}</label>
              <input id="vnd-inv-locales" v-model="invite.locales" class="mono" aria-describedby="vnd-inv-locales-hint" />
              <span id="vnd-inv-locales-hint" class="hint">{{ s.inviteLocalesHint }}</span>
            </div>
          </div>
          <fieldset v-if="projects?.length" class="stack-sm" aria-describedby="vnd-inv-projects-hint">
            <legend class="label">{{ s.inviteProjects }}</legend>
            <label v-for="p in projects" :key="p.id" class="check">
              <input v-model="invite.projects" type="checkbox" :value="p.id" />
              <span>{{ p.name }}</span>
            </label>
            <span id="vnd-inv-projects-hint" class="hint">{{ s.inviteProjectsHint }}</span>
          </fieldset>
          <p v-if="inviteError" class="alert alert-error" role="alert" data-testid="vendor-invite-error">{{ inviteError }}</p>
          <div><button type="submit" class="btn btn-primary" :disabled="busy || !invite.email.trim()">{{ s.inviteSend }}</button></div>
        </form>
      </section>

      <ModalDialog :open="!!scoping" :title="scoping ? s.scopeTitle(nameOf(scoping)) : ''" @close="scoping = undefined">
        <p>{{ s.scopeLead }}</p>
        <form id="vnd-scope-form" class="stack-sm" data-testid="scope-form" @submit.prevent="saveScope">
          <fieldset class="stack-sm">
            <legend class="visually-hidden">{{ s.editScope }}</legend>
            <label v-for="p in projects ?? []" :key="p.id" class="check">
              <input v-model="scope" type="checkbox" :value="p.id" :disabled="busy" />
              <span>{{ p.name }}</span>
            </label>
          </fieldset>
        </form>
        <p v-if="scopeError" class="alert alert-error" role="alert" data-testid="scope-error">{{ scopeError }}</p>
        <template #actions>
          <button type="button" class="btn" :disabled="busy" @click="scoping = undefined">{{ strings.app.cancel }}</button>
          <button type="submit" form="vnd-scope-form" class="btn btn-primary" :disabled="busy" data-testid="scope-save">{{ s.scopeSave }}</button>
        </template>
      </ModalDialog>

      <ModalDialog :open="!!offboarding" :title="offboarding ? s.offVendorTitle(nameOf(offboarding), vendor.name) : ''" @close="offboarding = undefined">
        <p>{{ s.offVendorLead }}</p>
        <p v-if="offError" class="alert alert-error" role="alert" data-testid="off-error">{{ offError }}</p>
        <template #actions>
          <button type="button" class="btn" :disabled="busy" @click="offboarding = undefined">{{ strings.app.cancel }}</button>
          <button type="button" class="btn btn-danger" :disabled="busy" data-testid="off-confirm" @click="offVendor">{{ s.offVendorConfirm }}</button>
        </template>
      </ModalDialog>

      <ModalDialog :open="deleting" :title="s.deleteTitle(vendor.name)" @close="deleting = false">
        <p>{{ s.deleteLead }}</p>
        <p v-if="deleteError" class="alert alert-error" role="alert" data-testid="vendor-delete-error">{{ problemText(deleteError) }}</p>
        <template #actions>
          <button type="button" class="btn" :disabled="busy" @click="deleting = false">{{ strings.app.cancel }}</button>
          <button type="button" class="btn btn-danger" :disabled="busy" data-testid="vendor-delete-confirm" @click="remove">{{ s.deleteConfirm }}</button>
        </template>
      </ModalDialog>
    </template>
  </div>
</template>

<style scoped>
.vendor {
  max-inline-size: var(--kl-content-lg, 64rem);
}
.visibility {
  border-inline-start: 4px solid var(--kl-accent);
}
.cols,
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(16rem, 1fr));
  gap: var(--kl-space-3);
}
.cols ul {
  margin: var(--kl-space-1) 0 0;
  padding-inline-start: var(--kl-space-4);
}
fieldset {
  border: none;
  margin: 0;
  padding: 0;
}
.scroll {
  overflow-x: auto;
}
.actions {
  text-align: end;
  white-space: nowrap;
}
</style>
