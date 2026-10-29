<script setup lang="ts">
/**
 * The workspace's workflow definitions (RFC 0006 §2.3): each a document
 * at its latest version, bindable by every project or by one. Opening
 * one is the editor; deleting one removes its bindings and keeps its
 * versions, so whatever ran on them stays explainable.
 */
import { computed, nextTick, ref, shallowRef, useTemplateRef, watch } from "vue";
import { RouterLink, useRoute } from "vue-router";
import { projects as projectsApi } from "../../api/endpoints";
import { useWorkflows } from "../../api/workflows";
import type { WorkflowDefinition } from "../../api/workflows-schemas";
import ErrorAlert from "../../components/ErrorAlert.vue";
import ModalDialog from "../../components/ModalDialog.vue";
import { absoluteTime, relativeTime } from "../../lib/time";
import { allows } from "../../session/permissions";
import { usePeople } from "../../session/people";
import { useGrant } from "../../session/session";
import { problemText, strings } from "../../strings";

const s = strings.workflows;
const route = useRoute();
const port = useWorkflows();
const tenant = computed(() => String(route.params.tenant));
const grant = useGrant(() => tenant.value);
const canManage = computed(() => allows(grant.value, "workflows.manage"));
const person = usePeople(() => tenant.value);
const heading = useTemplateRef<HTMLHeadingElement>("heading");

type Phase = "loading" | "ready" | "failed";
const phase = ref<Phase>("loading");
const loadError = ref<unknown>(null);
const items = shallowRef<WorkflowDefinition[]>([]);
const projectNames = shallowRef(new Map<string, string>());
const status = ref("");

async function load(): Promise<void> {
  phase.value = "loading";
  loadError.value = null;
  const [defs, projects] = await Promise.allSettled([port.definitions(tenant.value), projectsApi.list(tenant.value)]);
  if (projects.status === "fulfilled") projectNames.value = new Map(projects.value.map((p) => [p.id, p.name]));
  if (defs.status === "rejected") {
    items.value = [];
    loadError.value = defs.reason;
    phase.value = "failed";
    return;
  }
  items.value = [...defs.value].sort((a, b) => a.name.localeCompare(b.name));
  phase.value = "ready";
}
watch(tenant, () => void load(), { immediate: true });

const scopeOf = (d: WorkflowDefinition) => (d.project_id ? s.onlyProject(projectNames.value.get(d.project_id) ?? d.project_id.slice(0, 8)) : s.everyProject);

// ── delete ─────────────────────────────────────────────────────────
const removing = ref<WorkflowDefinition>();
const busy = ref(false);
const removeError = ref<unknown>(null);
function askRemove(d: WorkflowDefinition): void {
  status.value = "";
  removeError.value = null;
  removing.value = d;
}
async function confirmRemove(): Promise<void> {
  const d = removing.value;
  if (!d || busy.value) return;
  busy.value = true;
  try {
    await port.remove(tenant.value, d.id);
    removing.value = undefined;
    items.value = items.value.filter((x) => x.id !== d.id);
    status.value = s.removed(d.name);
    await nextTick();
    heading.value?.focus();
  } catch (e) {
    removeError.value = e;
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="page stack workflows">
    <nav :aria-label="strings.nav.breadcrumb" class="crumbs">
      <RouterLink :to="{ name: 'projects', params: { tenant } }">{{ s.back }}</RouterLink>
    </nav>
    <div class="page-header">
      <div class="stack-sm">
        <h1 ref="heading" tabindex="-1">{{ s.title }}</h1>
        <p class="muted lead">{{ s.lead }}</p>
      </div>
      <RouterLink v-if="canManage" class="btn btn-primary" :to="{ name: 'workflow-new', params: { tenant } }" data-testid="workflow-create">{{ s.create }}</RouterLink>
    </div>
    <p v-if="!canManage" class="alert" data-testid="workflows-read-only">{{ s.readOnly }}</p>
    <p role="status" aria-live="polite" data-testid="workflows-status">{{ status }}</p>

    <p v-if="phase === 'loading'" class="muted" data-testid="workflows-loading">{{ s.loading }}</p>
    <div v-else-if="phase === 'failed'" class="alert alert-error stack-sm" role="alert" data-testid="workflows-failed">
      <p class="alert-title">{{ s.failed }}</p>
      <p>{{ problemText(loadError) }}</p>
      <div><button type="button" class="btn btn-sm" @click="load">{{ s.retry }}</button></div>
    </div>
    <p v-else-if="!items.length" class="card muted" data-testid="workflows-empty">{{ s.empty }}</p>
    <div v-else class="scroll">
      <table class="table" data-testid="workflow-list">
        <thead>
          <tr>
            <th scope="col">{{ s.columns.name }}</th>
            <th scope="col">{{ s.columns.subject }}</th>
            <th scope="col">{{ s.columns.version }}</th>
            <th scope="col">{{ s.columns.scope }}</th>
            <th scope="col">{{ s.columns.saved }}</th>
            <th v-if="canManage" scope="col"><span class="visually-hidden">{{ strings.releases.columns.actions }}</span></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="d in items" :key="d.id" :data-definition="d.id">
            <th scope="row">
              <RouterLink class="mono" :to="{ name: 'workflow', params: { tenant, definition: d.id } }">{{ d.name }}</RouterLink>
            </th>
            <td>{{ s.subject[d.subject] ?? d.subject }}</td>
            <td>{{ s.version(d.version) }}</td>
            <td>{{ scopeOf(d) }}</td>
            <td>
              <time :datetime="d.created_at" :title="absoluteTime(d.created_at)">{{ relativeTime(d.created_at) }}</time>
              <span class="muted"> · {{ person(d.created_by) }}</span>
            </td>
            <td v-if="canManage" class="actions">
              <button type="button" class="btn btn-sm btn-ghost" data-testid="workflow-remove" @click="askRemove(d)">
                {{ s.remove }}<span class="visually-hidden">{{ s.removeOf(d.name) }}</span>
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <ModalDialog :open="!!removing" :title="removing ? s.removeTitle(removing.name) : ''" @close="removing = undefined">
      <p>{{ s.removeLead }}</p>
      <p v-if="removing?.name === 'review' || removing?.name === 'release-approval'" class="alert alert-warn" data-testid="workflow-remove-default">{{ s.removeDefault }}</p>
      <ErrorAlert :error="removeError" />
      <template #actions>
        <button type="button" class="btn" :disabled="busy" @click="removing = undefined">{{ strings.app.cancel }}</button>
        <button type="button" class="btn btn-danger" :disabled="busy" data-testid="workflow-remove-confirm" @click="confirmRemove">{{ s.removeConfirm }}</button>
      </template>
    </ModalDialog>
  </div>
</template>

<style scoped>
.workflows {
  max-inline-size: var(--kl-content-lg, 64rem);
}
.lead {
  max-inline-size: 48rem;
}
.crumbs a {
  color: var(--kl-ink-secondary);
}
.scroll {
  overflow-x: auto;
}
.actions {
  text-align: end;
}
</style>
