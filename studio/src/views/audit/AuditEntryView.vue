<script setup lang="ts">
/**
 * One audit entry (RFC 0006 §6.1): every field as recorded, the summary
 * as the content-free object it is, and its place in the hash chain.
 * Needs `audit.read`. An entry outside a project-scoped reader's
 * projects is "not found", exactly like one that does not exist.
 */
import { computed, ref, shallowRef, watch } from "vue";
import { RouterLink, useRoute } from "vue-router";
import { useAudit } from "../../api/audit";
import type { AuditEntry } from "../../api/audit-schemas";
import { isApiError } from "../../api/errors";
import { absoluteTime } from "../../lib/time";
import { allows } from "../../session/permissions";
import { useGrant } from "../../session/session";
import { problemText, strings } from "../../strings";

const a = strings.audit;
const s = a.entry;
const route = useRoute();
const port = useAudit();
const tenant = computed(() => String(route.params.tenant));
const sequence = computed(() => Number(route.params.sequence));
const grant = useGrant(() => tenant.value);
const canRead = computed(() => allows(grant.value, "audit.read"));

type Phase = "loading" | "ready" | "missing" | "failed";
const phase = ref<Phase>("loading");
const loadError = ref<unknown>(null);
const entry = shallowRef<AuditEntry | undefined>();

async function load(): Promise<void> {
  if (!canRead.value) return;
  phase.value = "loading";
  try {
    entry.value = await port.entry(tenant.value, sequence.value);
    phase.value = "ready";
  } catch (e) {
    entry.value = undefined;
    loadError.value = e;
    phase.value = isApiError(e, "not_found") ? "missing" : "failed";
  }
}
watch([tenant, sequence, canRead], () => void load(), { immediate: true });

const summary = computed(() => (entry.value ? JSON.stringify(entry.value.summary, null, 2) : ""));
const hasSummary = computed(() => !!entry.value && Object.keys(entry.value.summary).length > 0);
</script>

<template>
  <div class="page stack audit-entry">
    <div class="page-header">
      <div class="stack-sm">
        <RouterLink :to="{ name: 'audit-log', params: { tenant } }">{{ s.back }}</RouterLink>
        <h1>{{ s.title(String(route.params.sequence)) }}</h1>
        <p class="muted lead">{{ s.lead }}</p>
      </div>
    </div>

    <p v-if="!canRead" class="alert" role="status" data-testid="audit-no-access">{{ a.log.noAccess }}</p>
    <p v-else-if="phase === 'loading'" class="muted" role="status" data-testid="entry-loading">{{ s.loading }}</p>
    <p v-else-if="phase === 'missing'" class="alert" role="status" data-testid="entry-missing">{{ s.notFound }}</p>
    <div v-else-if="phase === 'failed'" class="alert alert-error stack-sm" role="alert" data-testid="entry-failed">
      <p class="alert-title">{{ s.failed }}</p>
      <p>{{ problemText(loadError) }}</p>
      <div><button type="button" class="btn btn-sm" @click="load">{{ a.retry }}</button></div>
    </div>
    <template v-else-if="entry">
      <dl class="card fields" data-testid="entry-fields">
        <dt>{{ s.fields.when }}</dt>
        <dd>
          <time :datetime="entry.occurred_at">{{ absoluteTime(entry.occurred_at) }}</time> <span class="muted mono">{{ entry.occurred_at }}</span>
        </dd>
        <dt>{{ s.fields.actor }}</dt>
        <dd class="mono" data-testid="entry-actor">{{ entry.actor }}</dd>
        <dt>{{ s.fields.action }}</dt>
        <dd class="mono" data-testid="entry-action">{{ entry.action }}</dd>
        <dt>{{ s.fields.source }}</dt>
        <dd>{{ a.log.sources[entry.source] ?? entry.source }}</dd>
        <dt>{{ s.fields.about }}</dt>
        <dd class="mono">{{ entry.aggregate_type }}:{{ entry.aggregate_id }}</dd>
        <template v-if="entry.project_id">
          <dt>{{ s.fields.project }}</dt>
          <dd class="mono">{{ entry.project_id }}</dd>
        </template>
        <template v-if="entry.locale">
          <dt>{{ s.fields.locale }}</dt>
          <dd class="mono">{{ entry.locale }}</dd>
        </template>
        <dt>{{ s.fields.event }}</dt>
        <dd class="mono">{{ entry.event_id }}</dd>
        <template v-if="entry.request_id">
          <dt>{{ s.fields.request }}</dt>
          <dd class="mono">{{ entry.request_id }}</dd>
        </template>
        <template v-if="entry.trace_id">
          <dt>{{ s.fields.trace }}</dt>
          <dd class="mono">{{ entry.trace_id }}</dd>
        </template>
      </dl>

      <section class="card stack-sm" aria-labelledby="entry-summary-h">
        <h2 id="entry-summary-h">{{ s.fields.summary }}</h2>
        <pre v-if="hasSummary" class="mono summary" data-testid="entry-summary">{{ summary }}</pre>
        <p v-else class="muted">{{ s.noSummary }}</p>
      </section>

      <section class="card stack-sm" aria-labelledby="entry-chain-h">
        <h2 id="entry-chain-h">{{ s.fields.hash }}</h2>
        <p class="muted">{{ s.chain }}</p>
        <dl class="fields">
          <dt>{{ s.fields.prev }}</dt>
          <dd class="mono hash" data-testid="entry-prev-hash">{{ entry.prev_hash }}</dd>
          <dt>{{ s.fields.hash }}</dt>
          <dd class="mono hash" data-testid="entry-hash">{{ entry.hash }}</dd>
        </dl>
      </section>

      <nav class="row" aria-label="Neighbouring entries">
        <RouterLink v-if="entry.sequence > 1" class="btn" :to="{ name: 'audit-entry', params: { tenant, sequence: entry.sequence - 1 } }" data-testid="entry-previous">
          {{ s.previous }}
        </RouterLink>
        <RouterLink class="btn" :to="{ name: 'audit-entry', params: { tenant, sequence: entry.sequence + 1 } }" data-testid="entry-next">{{ s.next }}</RouterLink>
      </nav>
    </template>
  </div>
</template>

<style scoped>
.audit-entry {
  max-inline-size: var(--kl-content-md, 56rem);
}
.lead {
  max-inline-size: 60ch;
}
.fields {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 0.5rem 1.25rem;
  margin: 0;
}
.fields dt {
  color: var(--kl-color-text-muted, inherit);
}
.fields dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.summary {
  margin: 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.hash {
  word-break: break-all;
}
</style>
