<script setup lang="ts">
/**
 * A project's workflow instances (RFC 0006 §2.5): the units — and
 * release requests — with work in flight under a binding, or finished.
 *
 * The filters live in the route's query (`status`, `locale`, `message`,
 * `subject_id`), so a link from a translation lands here already narrowed
 * to that unit, and the back button undoes a filter.
 *
 * An instance names its message by id only. When the list is filtered by
 * a key, every row is that key; otherwise the row shows a short id.
 */
import { computed, ref, shallowRef, watch } from "vue";
import { RouterLink, useRoute, useRouter } from "vue-router";
import { useWorkflows, type InstanceQuery } from "../../api/workflows";
import type { WorkflowDefinition, WorkflowInstance, WorkflowInstanceStatus } from "../../api/workflows-schemas";
import { absoluteTime, relativeTime } from "../../lib/time";
import { problemText, strings } from "../../strings";
import { useProject } from "./context";

const s = strings.instances;
const route = useRoute();
const router = useRouter();
const port = useWorkflows();
const { tenant, projectId } = useProject();
const project = () => ({ tenant: tenant.value, project: projectId.value });

const q = (k: string) => (typeof route.query[k] === "string" && route.query[k] ? String(route.query[k]) : undefined);
const query = computed<InstanceQuery>(() => {
  const status = q("status");
  return {
    ...(status === "active" || status === "finished" ? { status: status as WorkflowInstanceStatus } : {}),
    ...(q("locale") ? { locale: q("locale")! } : {}),
    ...(q("message") ? { message: q("message")! } : {}),
    ...(q("subject_id") ? { subject_id: q("subject_id")! } : {}),
  };
});

// The form edits a copy; submitting puts it in the query.
const form = ref({ status: "", locale: "", message: "" });
watch(
  query,
  (v) => {
    form.value = { status: v.status ?? "", locale: v.locale ?? "", message: v.message ?? "" };
  },
  { immediate: true },
);
const filtered = computed(() => Object.keys(query.value).length > 0);

async function apply(): Promise<void> {
  const f = form.value;
  await router.push({
    query: {
      ...(f.status ? { status: f.status } : {}),
      ...(f.locale.trim() ? { locale: f.locale.trim() } : {}),
      ...(f.message.trim() ? { message: f.message.trim() } : {}),
      ...(q("subject_id") ? { subject_id: q("subject_id") } : {}),
    },
  });
}
async function clear(): Promise<void> {
  await router.push({ query: {} });
}

type Phase = "loading" | "ready" | "failed";
const phase = ref<Phase>("loading");
const loadError = ref<unknown>(null);
const items = shallowRef<WorkflowInstance[]>([]);
const next = ref<string>();
const more = ref(false);
const moreError = ref<unknown>(null);
const definitions = shallowRef(new Map<string, WorkflowDefinition>());
let seq = 0;

async function load(): Promise<void> {
  const mine = ++seq;
  phase.value = "loading";
  loadError.value = null;
  moreError.value = null;
  try {
    const page = await port.instances(project(), query.value);
    if (mine !== seq) return;
    items.value = page.items;
    next.value = page.next;
    phase.value = "ready";
  } catch (e) {
    if (mine !== seq) return;
    items.value = [];
    loadError.value = e;
    phase.value = "failed";
  }
}
async function loadMore(): Promise<void> {
  if (!next.value || more.value) return;
  more.value = true;
  moreError.value = null;
  try {
    const page = await port.instances(project(), query.value, next.value);
    items.value = [...items.value, ...page.items];
    next.value = page.next;
  } catch (e) {
    moreError.value = e;
  } finally {
    more.value = false;
  }
}
/** Names for the definitions, best effort: a failure only shortens labels. */
async function loadDefinitions(): Promise<void> {
  try {
    const list = await port.definitions(tenant.value, projectId.value);
    definitions.value = new Map(list.map((d) => [d.id, d]));
  } catch {
    definitions.value = new Map();
  }
}
watch([projectId, query], () => void load(), { immediate: true });
watch(projectId, () => void loadDefinitions(), { immediate: true });

const subjectOf = (i: WorkflowInstance): string => {
  if (i.subject === "release_request") return s.releaseRequest(i.subject_id);
  return query.value.message ?? s.message_(i.subject_id);
};
const workflowOf = (i: WorkflowInstance) => s.workflowVersion(definitions.value.get(i.definition_id)?.name ?? s.unknownDefinition(i.definition_id), i.definition_version);
const instanceRoute = (i: WorkflowInstance) => ({
  name: "workflow-instance",
  params: { tenant: tenant.value, project: projectId.value, instance: i.id },
  query: query.value.message ? { key: query.value.message } : {},
});
</script>

<template>
  <div class="page stack instances">
    <div class="page-header">
      <div class="stack-sm">
        <RouterLink :to="{ name: 'project-workflow', params: { tenant, project: projectId } }">{{ s.back }}</RouterLink>
        <h1>{{ s.title }}</h1>
        <p class="muted lead">{{ s.lead }}</p>
      </div>
    </div>

    <form class="row filters" :aria-label="s.filters" data-testid="instances-filters" @submit.prevent="apply">
      <div class="field">
        <label for="inst-status">{{ s.status }}</label>
        <select id="inst-status" v-model="form.status">
          <option value="">{{ s.anyStatus }}</option>
          <option value="active">{{ s.statusName.active }}</option>
          <option value="finished">{{ s.statusName.finished }}</option>
        </select>
      </div>
      <div class="field">
        <label for="inst-locale">{{ s.locale }}</label>
        <input id="inst-locale" v-model="form.locale" class="mono" maxlength="35" autocomplete="off" />
      </div>
      <div class="field">
        <label for="inst-message">{{ s.message }}</label>
        <input id="inst-message" v-model="form.message" class="mono" maxlength="512" aria-describedby="inst-message-hint" autocomplete="off" />
        <span id="inst-message-hint" class="hint">{{ s.messageHint }}</span>
      </div>
      <div class="row">
        <button type="submit" class="btn">{{ s.apply }}</button>
        <button v-if="filtered" type="button" class="btn btn-ghost" data-testid="instances-clear" @click="clear">{{ s.clear }}</button>
      </div>
    </form>

    <p v-if="phase === 'loading'" class="muted" role="status" data-testid="instances-loading">{{ s.loading }}</p>
    <div v-else-if="phase === 'failed'" class="alert alert-error stack-sm" role="alert" data-testid="instances-failed">
      <p class="alert-title">{{ s.failed }}</p>
      <p>{{ problemText(loadError) }}</p>
      <div><button type="button" class="btn btn-sm" @click="load">{{ s.retry }}</button></div>
    </div>
    <p v-else-if="!items.length" class="card muted" data-testid="instances-empty">{{ s.empty }}</p>
    <template v-else>
      <p class="hint" role="status">{{ s.count(items.length, !!next) }}</p>
      <p v-if="!query.message && items.some((i) => i.subject === 'translation')" class="hint">{{ s.unknownKeyHint }}</p>
      <div class="scroll">
        <table class="table" data-testid="instances-table">
          <thead>
            <tr>
              <th scope="col">{{ s.columns.subject }}</th>
              <th scope="col">{{ s.columns.locale }}</th>
              <th scope="col">{{ s.columns.workflow }}</th>
              <th scope="col">{{ s.columns.state }}</th>
              <th scope="col">{{ s.columns.status }}</th>
              <th scope="col">{{ s.columns.updated }}</th>
              <th scope="col"><span class="visually-hidden">{{ s.columns.open }}</span></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="i in items" :key="i.id" data-testid="instance-row" :data-instance="i.id">
              <th scope="row" class="mono">{{ subjectOf(i) }}</th>
              <td>{{ i.locale ?? "—" }}</td>
              <td>{{ workflowOf(i) }}</td>
              <td class="mono">{{ i.state }}</td>
              <td>
                <span class="pill" :class="`pill-${s.statusTone[i.status] ?? 'neutral'}`">{{ s.statusName[i.status] ?? i.status }}</span>
              </td>
              <td>
                <time :datetime="i.updated_at" :title="absoluteTime(i.updated_at)">{{ relativeTime(i.updated_at) }}</time>
              </td>
              <td>
                <RouterLink :to="instanceRoute(i)" data-testid="instance-open">
                  {{ s.open }}<span class="visually-hidden">{{ s.ofInstance(subjectOf(i)) }}</span>
                </RouterLink>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-if="moreError" class="alert alert-error" role="alert">{{ problemText(moreError) }}</p>
      <div v-if="next">
        <button type="button" class="btn" :disabled="more" data-testid="instances-more" @click="loadMore">{{ s.loadMore }}</button>
      </div>
    </template>
  </div>
</template>

<style scoped>
.instances {
  max-inline-size: var(--kl-content-lg, 64rem);
}
.lead {
  max-inline-size: 48rem;
}
.filters {
  align-items: flex-end;
  flex-wrap: wrap;
}
.scroll {
  overflow-x: auto;
}
</style>
