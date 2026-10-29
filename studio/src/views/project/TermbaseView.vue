<script setup lang="ts">
/**
 * The termbase (intent §19.2): concepts that apply to this project —
 * its own and the workspace's — with their terms per locale and status,
 * definitions and history. Editing needs `knowledge.write`.
 */
import { computed, ref, shallowRef, watch } from "vue";
import type { Versioned } from "../../api/errors";
import type { TermConcept, TermConceptRevision } from "../../api/knowledge-schemas";
import { useIntegration } from "../../api/integration";
import { useKnowledge } from "../../api/knowledge";
import ErrorAlert from "../../components/ErrorAlert.vue";
import ExportDialog from "../../components/integration/ExportDialog.vue";
import ConceptDialog from "../../components/knowledge/ConceptDialog.vue";
import ModalDialog from "../../components/ModalDialog.vue";
import { byStatus, statusTone } from "../../lib/terms";
import { relativeTime } from "../../lib/time";
import { allows } from "../../session/permissions";
import { useSession } from "../../session/session";
import { principalLabel, strings } from "../../strings";
import { useProject } from "./context";

const { tenant, projectId, project, locales, grant } = useProject();
const { person } = useSession();
const integration = useIntegration();
const exportOpen = ref(false);
const s = strings.termbase;
const port = useKnowledge();
const canWrite = computed(() => allows(grant.value, "knowledge.write"));
const sortedLocales = computed(() => [...locales.value].sort((a, b) => Number(b.is_source) - Number(a.is_source) || a.code.localeCompare(b.code)));

const q = ref("");
const locale = ref("");
const concepts = shallowRef<TermConcept[]>([]);
const loading = ref(false);
const error = ref<unknown>(null);
const status = ref("");

async function load(): Promise<void> {
  loading.value = true;
  error.value = null;
  try {
    concepts.value = await port.concepts(tenant.value, {
      project: projectId.value,
      ...(q.value.trim() ? { q: q.value.trim() } : {}),
      ...(locale.value ? { locale: locale.value } : {}),
    });
  } catch (e) {
    error.value = e;
  } finally {
    loading.value = false;
  }
}
watch([projectId, locale], load, { immediate: true });

/** A concept's name in lists: its first preferred term in the source locale, else any term. */
function label(c: TermConcept): string {
  const src = sortedLocales.value[0]?.code;
  const t = byStatus(c.terms).find((x) => x.locale === src) ?? byStatus(c.terms)[0];
  return t?.text ?? c.definition.slice(0, 40);
}
function termsByLocale(c: TermConcept): Array<[string, TermConcept["terms"]]> {
  const m = new Map<string, TermConcept["terms"]>();
  for (const t of c.terms) m.set(t.locale, [...(m.get(t.locale) ?? []), t]);
  return [...m].map(([l, ts]) => [l, byStatus(ts)] as [string, TermConcept["terms"]]).sort((a, b) => a[0].localeCompare(b[0]));
}

// ── dialogs ─────────────────────────────────────────────────────────
const editing = shallowRef<Versioned<TermConcept>>();
const dialogOpen = ref(false);
function openNew(): void {
  editing.value = undefined;
  dialogOpen.value = true;
}
async function openEdit(c: TermConcept): Promise<void> {
  error.value = null;
  try {
    editing.value = await port.concept(tenant.value, c.id);
    dialogOpen.value = true;
  } catch (e) {
    error.value = e;
  }
}
async function onSaved(_c: TermConcept, created: boolean): Promise<void> {
  dialogOpen.value = false;
  status.value = created ? s.created : s.saved;
  await load();
}

const history = shallowRef<{ concept: TermConcept; revisions: TermConceptRevision[] }>();
async function openHistory(c: TermConcept): Promise<void> {
  try {
    history.value = { concept: c, revisions: await port.conceptRevisions(tenant.value, c.id) };
  } catch (e) {
    error.value = e;
  }
}

const deleting = shallowRef<TermConcept>();
const deleteBusy = ref(false);
async function confirmDelete(): Promise<void> {
  const c = deleting.value;
  if (!c) return;
  deleteBusy.value = true;
  try {
    await port.deleteConcept(tenant.value, c.id);
    deleting.value = undefined;
    status.value = s.deleted;
    await load();
  } catch (e) {
    error.value = e;
    deleting.value = undefined;
  } finally {
    deleteBusy.value = false;
  }
}
</script>

<template>
  <div class="page stack">
    <div class="page-header">
      <div class="stack-sm">
        <h1>{{ s.title }}</h1>
        <p class="muted lead">{{ s.lead }}</p>
      </div>
      <div class="row">
        <button type="button" class="btn" @click="exportOpen = true">{{ strings.integration.exportTbx }}</button>
        <button v-if="canWrite" type="button" class="btn btn-primary" @click="openNew">{{ s.newConcept }}</button>
      </div>
    </div>
    <p v-if="!canWrite" class="alert">{{ s.readOnly }}</p>
    <form class="row filters" role="search" :aria-label="s.search" @submit.prevent="load">
      <div class="field grow">
        <label for="tb-q">{{ s.search }}</label>
        <input id="tb-q" v-model="q" type="search" maxlength="200" autocomplete="off" />
      </div>
      <div class="field">
        <label for="tb-locale">{{ s.locale }}</label>
        <select id="tb-locale" v-model="locale">
          <option value="">{{ s.allLocales }}</option>
          <option v-for="l in sortedLocales" :key="l.code" :value="l.code">{{ l.code }}</option>
        </select>
      </div>
      <button type="submit" class="btn">{{ strings.concordance.search }}</button>
    </form>
    <ErrorAlert :error="error" />
    <p class="muted" role="status" data-testid="termbase-status">{{ status }}</p>

    <p v-if="loading && !concepts.length" class="muted">{{ strings.app.loading }}</p>
    <p v-else-if="!concepts.length" class="card muted">{{ q || locale ? s.noMatch : s.empty }}</p>
    <ul v-else class="concepts" data-testid="concepts">
      <li v-for="c in concepts" :key="c.id" class="card stack-sm concept">
        <div class="row">
          <h2 class="name">{{ label(c) }}</h2>
          <span class="pill pill-neutral">{{ c.project_id ? s.scopeProject : s.scopeTenant }}</span>
          <span v-if="c.domain" class="pill pill-neutral">{{ c.domain }}</span>
          <span class="hint">{{ s.version(c.version) }} · {{ principalLabel(c.updated_by, person?.id) }}, {{ relativeTime(c.updated_at) }}</span>
          <span class="spacer" />
          <button type="button" class="btn btn-sm" :aria-label="s.history(label(c))" @click="openHistory(c)">{{ strings.workspace.tabs.history }}</button>
          <template v-if="canWrite">
            <button type="button" class="btn btn-sm" :aria-label="s.edit(label(c))" @click="openEdit(c)">{{ strings.app.edit }}</button>
            <button type="button" class="btn btn-sm btn-danger" :aria-label="s.remove(label(c))" @click="deleting = c">{{ strings.app.delete }}</button>
          </template>
        </div>
        <p v-if="c.definition">{{ c.definition }}</p>
        <p v-if="c.note" class="hint">{{ c.note }}</p>
        <dl class="terms">
          <template v-for="[l, ts] in termsByLocale(c)" :key="l">
            <dt class="mono">{{ l }}</dt>
            <dd>
              <span v-for="t in ts" :key="t.id" class="term">
                <span :lang="l">{{ t.text }}</span>
                <span class="pill" :class="`pill-${statusTone(t.status)}`">{{ strings.terms.status[t.status] }}</span>
              </span>
            </dd>
          </template>
        </dl>
      </li>
    </ul>

    <ExportDialog
      :open="exportOpen"
      :port="integration"
      :tenant="tenant"
      :project-id="projectId"
      :project="project"
      :locales="locales"
      preset="tbx"
      @close="exportOpen = false"
    />

    <ConceptDialog :open="dialogOpen" :tenant="tenant" :project-id="projectId" :locales="sortedLocales" :concept="editing" @close="dialogOpen = false" @saved="onSaved" />

    <ModalDialog :open="!!history" :title="history ? s.revisions(label(history.concept)) : ''" wide @close="history = undefined">
      <ol v-if="history" class="revisions">
        <li v-for="r in history.revisions" :key="r.version" class="stack-sm">
          <p>
            <strong>{{ s.version(r.version) }}</strong> · {{ s.revisionAction[r.action] }} · {{ principalLabel(r.author, person?.id) }}, {{ relativeTime(r.created_at) }}
          </p>
          <p class="hint">{{ r.concept.terms.map((t) => `${t.locale}: ${t.text} (${strings.terms.status[t.status]})`).join(" · ") }}</p>
        </li>
      </ol>
      <template #actions>
        <button type="button" class="btn" @click="history = undefined">{{ strings.app.close }}</button>
      </template>
    </ModalDialog>

    <ModalDialog :open="!!deleting" :title="deleting ? s.confirmDelete(label(deleting)) : ''" @close="deleting = undefined">
      <p>{{ s.confirmDeleteLead }}</p>
      <template #actions>
        <button type="button" class="btn" @click="deleting = undefined">{{ strings.app.cancel }}</button>
        <button type="button" class="btn btn-danger" :disabled="deleteBusy" @click="confirmDelete">{{ strings.app.delete }}</button>
      </template>
    </ModalDialog>
  </div>
</template>

<style scoped>
.lead {
  max-inline-size: 48rem;
}
.filters {
  align-items: flex-end;
}
.grow {
  flex: 1;
  min-inline-size: 12rem;
}
.concepts {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-3);
}
.concept {
  padding: var(--kl-space-4);
}
.name {
  font-size: var(--kl-text-base);
}
.terms {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: var(--kl-space-1) var(--kl-space-3);
  margin: 0;
}
.terms dt {
  color: var(--kl-ink-secondary);
}
.terms dd {
  margin: 0;
  display: flex;
  flex-wrap: wrap;
  gap: var(--kl-space-3);
}
.term {
  display: inline-flex;
  gap: var(--kl-space-1);
  align-items: baseline;
}
.revisions {
  margin: 0;
  padding-inline-start: var(--kl-space-5);
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-3);
}
</style>
