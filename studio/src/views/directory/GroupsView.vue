<script setup lang="ts">
/**
 * The organization's groups (RFC 0006 §4.3): named sets of members that
 * assignments and approvals name instead of people. Groups carry no
 * permissions — roles do — and this screen says so.
 *
 * Changing a group needs `members.manage`; anyone who may read members
 * sees them. A rename carries the group's ETag, so two people renaming
 * at once don't silently overwrite each other.
 */
import { computed, nextTick, ref, shallowRef, watch } from "vue";
import { RouterLink, useRoute } from "vue-router";
import { useDirectory } from "../../api/directory";
import { isApiError } from "../../api/errors";
import { newIdempotencyKey } from "../../api/releases";
import type { Member } from "../../api/schemas";
import type { Group } from "../../api/work-schemas";
import ModalDialog from "../../components/ModalDialog.vue";
import { allows } from "../../session/permissions";
import { useGrant } from "../../session/session";
import { problemText, strings } from "../../strings";

const d = strings.directory;
const s = d.groups;
const route = useRoute();
const port = useDirectory();
const tenant = computed(() => String(route.params.tenant));
const grant = useGrant(() => tenant.value);
const canManage = computed(() => allows(grant.value, "members.manage"));

type Phase = "loading" | "ready" | "failed";
const phase = ref<Phase>("loading");
const loadError = ref<unknown>(null);
const groups = shallowRef<Group[]>([]);
const members = shallowRef<Member[] | undefined>();
const status = ref("");
const errors = ref<Record<string, string>>({});
const busy = ref(false);

async function load(): Promise<void> {
  phase.value = "loading";
  loadError.value = null;
  const [gs, ms] = await Promise.allSettled([port.groups(tenant.value), port.members(tenant.value)]);
  members.value = ms.status === "fulfilled" ? ms.value : undefined;
  if (gs.status === "rejected") {
    groups.value = [];
    loadError.value = gs.reason;
    phase.value = "failed";
    return;
  }
  groups.value = gs.value;
  phase.value = "ready";
}
watch(tenant, () => void load(), { immediate: true });

const byId = computed(() => new Map((members.value ?? []).map((m) => [m.id, m])));
const nameOf = (id: string) => {
  const m = byId.value.get(id);
  return m ? m.display_name || m.email : d.unknownMember(id);
};
const candidates = (g: Group) => (members.value ?? []).filter((m) => !g.members.includes(m.id));

function setError(id: string, e: unknown): void {
  errors.value = { ...errors.value, [id]: problemText(e) };
}
function clearError(id: string): void {
  const { [id]: _gone, ...rest } = errors.value;
  errors.value = rest;
}
function replace(g: Group): void {
  groups.value = groups.value.map((x) => (x.id === g.id ? g : x));
}

// ── create ──────────────────────────────────────────────────────────
const newName = ref("");
const createError = ref("");
let createKey = newIdempotencyKey();
watch(newName, () => {
  createKey = newIdempotencyKey();
});
async function create(): Promise<void> {
  const name = newName.value.trim();
  if (!name || busy.value) return;
  busy.value = true;
  createError.value = "";
  status.value = "";
  try {
    const g = await port.createGroup(tenant.value, name, createKey);
    groups.value = [...groups.value, g];
    newName.value = "";
    status.value = s.created(g.name);
    await nextTick();
    document.getElementById(`grp-h-${g.id}`)?.focus();
  } catch (e) {
    createError.value = problemText(e);
  } finally {
    busy.value = false;
  }
}

// ── members ─────────────────────────────────────────────────────────
const picks = ref<Record<string, string>>({});
async function add(g: Group): Promise<void> {
  const memberId = picks.value[g.id];
  if (!memberId || busy.value) return;
  busy.value = true;
  status.value = "";
  clearError(g.id);
  try {
    replace(await port.addToGroup(tenant.value, g.id, memberId));
    picks.value = { ...picks.value, [g.id]: "" };
    status.value = s.added(nameOf(memberId), g.name);
  } catch (e) {
    setError(g.id, e);
  } finally {
    busy.value = false;
  }
}
async function removeMember(g: Group, memberId: string): Promise<void> {
  if (busy.value) return;
  busy.value = true;
  status.value = "";
  clearError(g.id);
  try {
    await port.removeFromGroup(tenant.value, g.id, memberId);
    replace({ ...g, members: g.members.filter((m) => m !== memberId) });
    status.value = s.removed(nameOf(memberId), g.name);
    await nextTick();
    document.getElementById(`grp-h-${g.id}`)?.focus();
  } catch (e) {
    if (isApiError(e, "not_in_group")) await load();
    setError(g.id, e);
  } finally {
    busy.value = false;
  }
}

// ── rename ──────────────────────────────────────────────────────────
const renaming = ref<string>();
const renameTo = ref("");
async function startRename(g: Group): Promise<void> {
  renaming.value = g.id;
  renameTo.value = g.name;
  clearError(g.id);
  await nextTick();
  document.getElementById(`grp-rename-${g.id}`)?.focus();
}
async function rename(g: Group): Promise<void> {
  const name = renameTo.value.trim();
  if (!name || busy.value) return;
  busy.value = true;
  status.value = "";
  clearError(g.id);
  try {
    // The ETag of the group as it is now: a rename that raced another one is refused, not merged.
    const current = await port.group(tenant.value, g.id);
    const r = await port.renameGroup(tenant.value, g.id, name, current.etag ?? "");
    replace(r.value);
    renaming.value = undefined;
    status.value = s.renamed(r.value.name);
    await nextTick();
    document.getElementById(`grp-h-${g.id}`)?.focus();
  } catch (e) {
    setError(g.id, e);
  } finally {
    busy.value = false;
  }
}

// ── delete ──────────────────────────────────────────────────────────
const deleting = ref<Group>();
const deleteError = ref<unknown>(null);
async function remove(): Promise<void> {
  const g = deleting.value;
  if (!g || busy.value) return;
  busy.value = true;
  deleteError.value = null;
  try {
    await port.deleteGroup(tenant.value, g.id);
    groups.value = groups.value.filter((x) => x.id !== g.id);
    deleting.value = undefined;
    status.value = s.deleted(g.name);
  } catch (e) {
    deleteError.value = e;
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="page stack groups">
    <div class="page-header">
      <div class="stack-sm">
        <RouterLink :to="{ name: 'projects', params: { tenant } }">{{ d.back }}</RouterLink>
        <h1>{{ s.title }}</h1>
        <p class="muted lead">{{ s.lead }}</p>
      </div>
    </div>
    <p role="status" aria-live="polite" data-testid="groups-status">{{ status }}</p>
    <p v-if="!canManage" class="alert" data-testid="groups-read-only">{{ s.readOnly }}</p>

    <form v-if="canManage" class="card row create" data-testid="group-create" @submit.prevent="create">
      <div class="field">
        <label for="grp-new">{{ s.name }}</label>
        <input id="grp-new" v-model="newName" maxlength="100" required aria-describedby="grp-new-hint" />
        <span id="grp-new-hint" class="hint">{{ s.nameHint }}</span>
      </div>
      <button type="submit" class="btn btn-primary" :disabled="busy || !newName.trim()">{{ s.create }}</button>
      <p v-if="createError" class="alert alert-error" role="alert" data-testid="group-create-error">{{ createError }}</p>
    </form>

    <p v-if="phase === 'loading'" class="muted" role="status" data-testid="groups-loading">{{ s.loading }}</p>
    <div v-else-if="phase === 'failed'" class="alert alert-error stack-sm" role="alert" data-testid="groups-failed">
      <p class="alert-title">{{ s.failed }}</p>
      <p>{{ problemText(loadError) }}</p>
      <div><button type="button" class="btn btn-sm" @click="load">{{ d.retry }}</button></div>
    </div>
    <p v-else-if="!groups.length" class="card muted" data-testid="groups-empty">{{ s.empty }}</p>
    <template v-else>
      <p v-if="members === undefined" class="alert alert-warn" data-testid="groups-members-failed">{{ s.membersFailed }}</p>
      <ul class="cards">
        <li v-for="g in groups" :key="g.id">
          <article class="card stack-sm" :aria-labelledby="`grp-h-${g.id}`" data-testid="group" :data-group="g.id">
            <div class="row">
              <h2 :id="`grp-h-${g.id}`" tabindex="-1">{{ g.name }}</h2>
              <span class="pill pill-neutral">{{ s.members(g.members.length) }}</span>
              <span class="spacer" />
              <template v-if="canManage">
                <button type="button" class="btn btn-sm btn-ghost" data-testid="group-rename" @click="startRename(g)">
                  {{ s.rename }}<span class="visually-hidden">{{ s.ofGroup(g.name) }}</span>
                </button>
                <button type="button" class="btn btn-sm btn-ghost" data-testid="group-delete" @click="(deleting = g), (deleteError = null)">
                  {{ s.delete }}<span class="visually-hidden">{{ s.ofGroup(g.name) }}</span>
                </button>
              </template>
            </div>
            <form v-if="renaming === g.id" class="row" data-testid="group-rename-form" @submit.prevent="rename(g)">
              <div class="field">
                <label :for="`grp-rename-${g.id}`">{{ s.renameLabel(g.name) }}</label>
                <input :id="`grp-rename-${g.id}`" v-model="renameTo" maxlength="100" required />
              </div>
              <button type="submit" class="btn btn-primary btn-sm" :disabled="busy || !renameTo.trim()">{{ s.save }}</button>
              <button type="button" class="btn btn-sm" @click="renaming = undefined">{{ strings.app.cancel }}</button>
            </form>
            <p v-if="errors[g.id]" class="alert alert-error" role="alert" data-testid="group-error">{{ errors[g.id] }}</p>
            <p v-if="!g.members.length" class="muted">{{ s.noMembers }}</p>
            <ul v-else class="members" data-testid="group-members">
              <li v-for="id in g.members" :key="id" class="row">
                <span>{{ nameOf(id) }}</span>
                <button v-if="canManage" type="button" class="btn btn-sm btn-ghost" :disabled="busy" data-testid="group-remove" @click="removeMember(g, id)">
                  {{ s.remove }}<span class="visually-hidden">{{ s.fromGroup(nameOf(id)) }}</span>
                </button>
              </li>
            </ul>
            <form v-if="canManage && members !== undefined" class="row" data-testid="group-add" @submit.prevent="add(g)">
              <div class="field">
                <label :for="`grp-add-${g.id}`" class="visually-hidden">{{ s.addLabel(g.name) }}</label>
                <select :id="`grp-add-${g.id}`" v-model="picks[g.id]">
                  <option value="">{{ s.choose }}</option>
                  <option v-for="m in candidates(g)" :key="m.id" :value="m.id">{{ m.display_name || m.email }}</option>
                </select>
              </div>
              <button type="submit" class="btn btn-sm" :disabled="busy || !picks[g.id]">
                {{ s.add }}<span class="visually-hidden">{{ s.ofGroup(g.name) }}</span>
              </button>
            </form>
          </article>
        </li>
      </ul>
    </template>

    <ModalDialog :open="!!deleting" :title="deleting ? s.deleteTitle(deleting.name) : ''" @close="deleting = undefined">
      <p>{{ s.deleteLead }}</p>
      <p v-if="deleteError" class="alert alert-error" role="alert" data-testid="group-delete-error">{{ problemText(deleteError) }}</p>
      <template #actions>
        <button type="button" class="btn" :disabled="busy" @click="deleting = undefined">{{ strings.app.cancel }}</button>
        <button type="button" class="btn btn-danger" :disabled="busy" data-testid="group-delete-confirm" @click="remove">{{ s.deleteConfirm }}</button>
      </template>
    </ModalDialog>
  </div>
</template>

<style scoped>
.groups {
  max-inline-size: var(--kl-content-lg, 64rem);
}
.lead {
  max-inline-size: 48rem;
}
.create {
  align-items: flex-end;
  flex-wrap: wrap;
}
.cards {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-3);
}
h2 {
  margin: 0;
}
.members {
  margin: 0;
  padding-inline-start: var(--kl-space-4);
}
</style>
