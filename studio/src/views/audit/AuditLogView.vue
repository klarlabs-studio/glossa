<script setup lang="ts">
/**
 * The workspace's audit log (RFC 0006 §6): hash-chained, content-free
 * entries, newest first, with filters and paging. Reading it needs
 * `audit.read` (owners and admins); without it the screen says so and
 * asks the server nothing.
 */
import { computed, ref, shallowRef, watch } from "vue";
import { RouterLink, useRoute } from "vue-router";
import { useAudit, type AuditFilter } from "../../api/audit";
import type { AuditEntry, AuditSource } from "../../api/audit-schemas";
import { absoluteTime } from "../../lib/time";
import { allows } from "../../session/permissions";
import { useGrant } from "../../session/session";
import { problemText, strings } from "../../strings";

const a = strings.audit;
const s = a.log;
const route = useRoute();
const port = useAudit();
const tenant = computed(() => String(route.params.tenant));
const grant = useGrant(() => tenant.value);
const canRead = computed(() => allows(grant.value, "audit.read"));
const canExport = computed(() => allows(grant.value, "audit.export"));

type Phase = "loading" | "ready" | "failed";
const phase = ref<Phase>("loading");
const loadError = ref<unknown>(null);
const entries = shallowRef<AuditEntry[]>([]);
const next = ref<string | undefined>();
const moreBusy = ref(false);
const moreFailed = ref(false);

const draft = ref({ from: "", to: "", actor: "", action: "", source: "" as "" | AuditSource, project: "" });
const filter = shallowRef<AuditFilter>({});
const rangeError = ref("");
const filtered = computed(() => Object.keys(filter.value).length > 0);

/** `datetime-local` is the viewer's local time; the API takes instants. */
const instant = (local: string) => (local ? new Date(local).toISOString() : undefined);

function toFilter(): AuditFilter | undefined {
  const d = draft.value;
  const from = instant(d.from);
  const to = instant(d.to);
  if (from && to && Date.parse(to) <= Date.parse(from)) {
    rangeError.value = s.invalidRange;
    return undefined;
  }
  rangeError.value = "";
  const f: AuditFilter = {};
  if (from) f.from = from;
  if (to) f.to = to;
  if (d.actor.trim()) f.actor = d.actor.trim();
  if (d.action.trim()) f.action = d.action.trim();
  if (d.source) f.source = d.source;
  if (d.project.trim()) f.project = d.project.trim();
  return f;
}

async function load(): Promise<void> {
  if (!canRead.value) return;
  phase.value = "loading";
  loadError.value = null;
  moreFailed.value = false;
  try {
    const r = await port.entries(tenant.value, filter.value);
    entries.value = r.items;
    next.value = r.next;
    phase.value = "ready";
  } catch (e) {
    entries.value = [];
    next.value = undefined;
    loadError.value = e;
    phase.value = "failed";
  }
}

async function loadMore(): Promise<void> {
  if (!next.value || moreBusy.value) return;
  moreBusy.value = true;
  moreFailed.value = false;
  try {
    const r = await port.entries(tenant.value, filter.value, next.value);
    entries.value = [...entries.value, ...r.items];
    next.value = r.next;
  } catch {
    moreFailed.value = true;
  } finally {
    moreBusy.value = false;
  }
}

function apply(): void {
  const f = toFilter();
  if (!f) return;
  filter.value = f;
  void load();
}

function clear(): void {
  draft.value = { from: "", to: "", actor: "", action: "", source: "", project: "" };
  rangeError.value = "";
  filter.value = {};
  void load();
}

watch([tenant, canRead], () => void load(), { immediate: true });
</script>

<template>
  <div class="page stack audit-log">
    <div class="page-header">
      <div class="stack-sm">
        <RouterLink :to="{ name: 'projects', params: { tenant } }">{{ a.back }}</RouterLink>
        <h1>{{ s.title }}</h1>
        <p class="muted lead">{{ s.lead }}</p>
      </div>
      <div v-if="canExport" class="row">
        <RouterLink class="btn" :to="{ name: 'audit-exports', params: { tenant } }" data-testid="audit-exports-link">{{ s.exportsLink }}</RouterLink>
      </div>
    </div>

    <p v-if="!canRead" class="alert" role="status" data-testid="audit-no-access">{{ s.noAccess }}</p>
    <template v-else>
      <form class="card stack-sm" aria-labelledby="audit-filters-h" data-testid="audit-filters" @submit.prevent="apply">
        <h2 id="audit-filters-h">{{ s.filters }}</h2>
        <div class="grid">
          <div class="field">
            <label for="af-from">{{ s.from }}</label>
            <input id="af-from" v-model="draft.from" type="datetime-local" />
          </div>
          <div class="field">
            <label for="af-to">{{ s.to }}</label>
            <input id="af-to" v-model="draft.to" type="datetime-local" />
          </div>
          <div class="field">
            <label for="af-actor">{{ s.actor }}</label>
            <input id="af-actor" v-model="draft.actor" class="mono" maxlength="200" aria-describedby="af-actor-hint" />
            <span id="af-actor-hint" class="hint">{{ s.actorHint }}</span>
          </div>
          <div class="field">
            <label for="af-action">{{ s.action }}</label>
            <input id="af-action" v-model="draft.action" class="mono" maxlength="200" aria-describedby="af-action-hint" />
            <span id="af-action-hint" class="hint">{{ s.actionHint }}</span>
          </div>
          <div class="field">
            <label for="af-source">{{ s.source }}</label>
            <select id="af-source" v-model="draft.source">
              <option value="">{{ s.anySource }}</option>
              <option v-for="(label, value) in s.sources" :key="value" :value="value">{{ label }}</option>
            </select>
          </div>
          <div class="field">
            <label for="af-project">{{ s.project }}</label>
            <input id="af-project" v-model="draft.project" class="mono" maxlength="200" />
          </div>
        </div>
        <p v-if="rangeError" class="alert alert-error" role="alert" data-testid="audit-range-error">{{ rangeError }}</p>
        <div class="row">
          <button type="submit" class="btn btn-primary">{{ s.apply }}</button>
          <button type="button" class="btn" :disabled="!filtered && Object.values(draft).every((v) => !v)" @click="clear">{{ s.clear }}</button>
        </div>
      </form>

      <p v-if="phase === 'loading'" class="muted" role="status" data-testid="audit-loading">{{ s.loading }}</p>
      <div v-else-if="phase === 'failed'" class="alert alert-error stack-sm" role="alert" data-testid="audit-failed">
        <p class="alert-title">{{ s.failed }}</p>
        <p>{{ problemText(loadError) }}</p>
        <div><button type="button" class="btn btn-sm" @click="load">{{ a.retry }}</button></div>
      </div>
      <p v-else-if="!entries.length" class="card muted" data-testid="audit-empty">{{ filtered ? s.emptyFiltered : s.empty }}</p>
      <template v-else>
        <p class="muted" data-testid="audit-count">{{ s.shown(entries.length) }}</p>
        <div class="scroll">
          <table class="table" data-testid="audit-list">
            <thead>
              <tr>
                <th scope="col" class="num">{{ s.columns.sequence }}</th>
                <th scope="col">{{ s.columns.when }}</th>
                <th scope="col">{{ s.columns.actor }}</th>
                <th scope="col">{{ s.columns.action }}</th>
                <th scope="col">{{ s.columns.about }}</th>
                <th scope="col">{{ s.columns.source }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="e in entries" :key="e.sequence" data-testid="audit-row" :data-sequence="e.sequence">
                <th scope="row" class="num">
                  <RouterLink :to="{ name: 'audit-entry', params: { tenant, sequence: e.sequence } }">{{ e.sequence }}</RouterLink>
                </th>
                <td>
                  <time :datetime="e.occurred_at">{{ absoluteTime(e.occurred_at) }}</time>
                </td>
                <td class="mono">{{ e.actor }}</td>
                <td class="mono">{{ e.action }}</td>
                <td class="mono">{{ e.aggregate_type }}:{{ e.aggregate_id }}</td>
                <td>{{ s.sources[e.source] ?? e.source }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-if="moreFailed" class="alert alert-error" role="alert" data-testid="audit-more-failed">{{ s.moreFailed }}</p>
        <div v-if="next">
          <button type="button" class="btn" :disabled="moreBusy" data-testid="audit-more" @click="loadMore">{{ moreBusy ? s.loadingMore : s.more }}</button>
        </div>
      </template>
    </template>
  </div>
</template>

<style scoped>
.audit-log {
  max-inline-size: var(--kl-content-lg, 72rem);
}
.lead {
  max-inline-size: 60ch;
}
</style>
