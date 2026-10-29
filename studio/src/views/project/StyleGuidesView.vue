<script setup lang="ts">
/**
 * Style guides (intent §19.3, RFC 0003 §2.3): the workspace's guide and
 * this project's (project, locale and namespace scopes), each with its
 * version history, and a preview of the effective style for a locale and
 * namespace. Editing needs `knowledge.write`.
 */
import { computed, ref, shallowRef, watch } from "vue";
import type { Versioned } from "../../api/errors";
import type { StyleGuide, StyleGuideVersion } from "../../api/knowledge-schemas";
import { useKnowledge } from "../../api/knowledge";
import StylePane from "../../components/assist/StylePane.vue";
import ErrorAlert from "../../components/ErrorAlert.vue";
import StyleGuideDialog from "../../components/knowledge/StyleGuideDialog.vue";
import ModalDialog from "../../components/ModalDialog.vue";
import { scopeLabel, styleSummary } from "../../lib/style";
import { relativeTime } from "../../lib/time";
import { allows } from "../../session/permissions";
import { useSession } from "../../session/session";
import { principalLabel, strings } from "../../strings";
import { useProject } from "./context";

const { tenant, projectId, project, locales, targets, grant } = useProject();
const { person } = useSession();
const s = strings.style;
const port = useKnowledge();
const canWrite = computed(() => allows(grant.value, "knowledge.write"));
const sortedLocales = computed(() => [...locales.value].sort((a, b) => Number(b.is_source) - Number(a.is_source) || a.code.localeCompare(b.code)));

const guides = shallowRef<StyleGuide[]>([]);
const loading = ref(false);
const error = ref<unknown>(null);
const status = ref("");

async function load(): Promise<void> {
  loading.value = true;
  error.value = null;
  try {
    const [workspace, own] = await Promise.all([
      port.styleGuides(tenant.value, { tenant_only: true }),
      port.styleGuides(tenant.value, { project: projectId.value }),
    ]);
    const depth = (g: StyleGuide) => (g.project_id ? 1 : 0) + (g.locale ? 1 : 0) + (g.namespace ? 1 : 0);
    guides.value = [...workspace, ...own.filter((g) => !workspace.some((w) => w.id === g.id))].sort(
      (a, b) => depth(a) - depth(b) || (a.locale ?? "").localeCompare(b.locale ?? "") || (a.namespace ?? "").localeCompare(b.namespace ?? ""),
    );
  } catch (e) {
    error.value = e;
  } finally {
    loading.value = false;
  }
}
watch(projectId, load, { immediate: true });

const names = computed(() => ({ tenant: s.tenantScope, project: project.value?.name ?? "" }));
const title = (g: StyleGuide) => g.name || scopeLabel(g, names.value);

// ── dialogs ─────────────────────────────────────────────────────────
const editing = shallowRef<Versioned<StyleGuide>>();
const dialogOpen = ref(false);
function openNew(): void {
  editing.value = undefined;
  dialogOpen.value = true;
}
async function openEdit(g: StyleGuide): Promise<void> {
  try {
    editing.value = await port.styleGuide(tenant.value, g.id);
    dialogOpen.value = true;
  } catch (e) {
    error.value = e;
  }
}
async function onSaved(_g: StyleGuide, created: boolean): Promise<void> {
  dialogOpen.value = false;
  status.value = created ? s.created : s.saved;
  await load();
}

const versions = shallowRef<{ guide: StyleGuide; items: StyleGuideVersion[] }>();
async function openVersions(g: StyleGuide): Promise<void> {
  try {
    versions.value = { guide: g, items: await port.styleGuideVersions(tenant.value, g.id) };
  } catch (e) {
    error.value = e;
  }
}

const deleting = shallowRef<StyleGuide>();
async function confirmDelete(): Promise<void> {
  const g = deleting.value;
  if (!g) return;
  try {
    await port.deleteStyleGuide(tenant.value, g.id);
    status.value = s.deleted;
    await load();
  } catch (e) {
    error.value = e;
  } finally {
    deleting.value = undefined;
  }
}

// ── effective style preview ─────────────────────────────────────────
const previewLocale = ref("");
const previewNs = ref("default");
watch(targets, (t) => (previewLocale.value ||= t[0]?.code ?? ""), { immediate: true });
</script>

<template>
  <div class="page stack">
    <div class="page-header">
      <div class="stack-sm">
        <h1>{{ s.title }}</h1>
        <p class="muted lead">{{ s.lead }}</p>
      </div>
      <button v-if="canWrite" type="button" class="btn btn-primary" @click="openNew">{{ s.newGuide }}</button>
    </div>
    <p v-if="!canWrite" class="alert">{{ s.readOnly }}</p>
    <ErrorAlert :error="error" />
    <p class="muted" role="status" data-testid="style-status">{{ status }}</p>

    <p v-if="loading && !guides.length" class="muted">{{ strings.app.loading }}</p>
    <p v-else-if="!guides.length" class="card muted">{{ s.empty }}</p>
    <ul v-else class="guides" data-testid="style-guides">
      <li v-for="g in guides" :key="g.id" class="card stack-sm">
        <div class="row">
          <h2 class="name">{{ title(g) }}</h2>
          <span class="pill pill-neutral">{{ scopeLabel(g, names) }}</span>
          <span class="hint">{{ s.version(g.version) }} · {{ s.updated(principalLabel(g.updated_by, person?.id), relativeTime(g.updated_at)) }}</span>
          <span class="spacer" />
          <button type="button" class="btn btn-sm" :aria-label="s.history(title(g))" @click="openVersions(g)">{{ strings.workspace.tabs.history }}</button>
          <template v-if="canWrite">
            <button type="button" class="btn btn-sm" :aria-label="s.edit(title(g))" @click="openEdit(g)">{{ strings.app.edit }}</button>
            <button type="button" class="btn btn-sm btn-danger" :aria-label="s.remove(title(g))" @click="deleting = g">{{ strings.app.delete }}</button>
          </template>
        </div>
        <dl class="summary">
          <template v-for="l in styleSummary(g.fields, s.summary)" :key="l.label">
            <dt>{{ l.label }}</dt>
            <dd>{{ l.value }}</dd>
          </template>
        </dl>
        <p v-if="g.rules.length" class="hint">{{ s.rules }}: {{ g.rules.map((r) => (r.disabled ? `${r.id} (off)` : r.title || r.id)).join(" · ") }}</p>
      </li>
    </ul>

    <section class="card stack-sm" aria-labelledby="preview-h">
      <h2 id="preview-h">{{ s.preview }}</h2>
      <p class="hint">{{ s.previewLead }}</p>
      <div class="row">
        <div class="field">
          <label for="sp-locale">{{ s.locale }}</label>
          <select id="sp-locale" v-model="previewLocale">
            <option v-for="l in sortedLocales" :key="l.code" :value="l.code">{{ l.code }}</option>
          </select>
        </div>
        <div class="field">
          <label for="sp-ns">{{ s.previewNamespace }}</label>
          <input id="sp-ns" v-model.lazy="previewNs" class="mono" />
        </div>
      </div>
      <StylePane v-if="previewLocale" :key="`${previewLocale}:${previewNs}:${guides.map((g) => g.version).join()}`" :tenant="tenant" :project-id="projectId" :locale="previewLocale" :namespace="previewNs || 'default'" />
    </section>

    <StyleGuideDialog :open="dialogOpen" :tenant="tenant" :project-id="projectId" :locales="sortedLocales" :guide="editing" @close="dialogOpen = false" @saved="onSaved" />

    <ModalDialog :open="!!versions" :title="versions ? s.versions(title(versions.guide)) : ''" wide @close="versions = undefined">
      <ol v-if="versions" class="versions">
        <li v-for="v in versions.items" :key="v.version" class="stack-sm">
          <p>
            <strong>{{ s.version(v.version) }}</strong> · {{ s.versionAction[v.action] }} · {{ principalLabel(v.author, person?.id) }}, {{ relativeTime(v.created_at) }}
          </p>
          <p class="hint">
            {{ styleSummary(v.style_guide.fields, s.summary).map((l) => `${l.label}: ${l.value}`).join(" · ") }}
            <template v-if="v.style_guide.rules.length"> · {{ s.rules }}: {{ v.style_guide.rules.length }}</template>
          </p>
        </li>
      </ol>
      <template #actions>
        <button type="button" class="btn" @click="versions = undefined">{{ strings.app.close }}</button>
      </template>
    </ModalDialog>

    <ModalDialog :open="!!deleting" :title="deleting ? s.confirmDelete(title(deleting)) : ''" @close="deleting = undefined">
      <p>{{ s.confirmDeleteLead }}</p>
      <template #actions>
        <button type="button" class="btn" @click="deleting = undefined">{{ strings.app.cancel }}</button>
        <button type="button" class="btn btn-danger" @click="confirmDelete">{{ strings.app.delete }}</button>
      </template>
    </ModalDialog>
  </div>
</template>

<style scoped>
.lead {
  max-inline-size: 48rem;
}
.guides {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-3);
}
.name {
  font-size: var(--kl-text-base);
}
.summary {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: var(--kl-space-1) var(--kl-space-3);
  margin: 0;
}
.summary dt {
  color: var(--kl-ink-secondary);
  font-size: var(--kl-text-sm);
}
.summary dd {
  margin: 0;
}
.versions {
  margin: 0;
  padding-inline-start: var(--kl-space-5);
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-3);
}
</style>
