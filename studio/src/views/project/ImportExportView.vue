<script setup lang="ts">
/**
 * Import & export (RFC 0003 §6): start an import (`i`) or an export
 * (`x`, and shortcuts for the translation memory and the termbase), and
 * the history of both — state, who asked, counts — for this project or
 * the whole workspace (tenant-wide TMX and TBX jobs have no project).
 * The lists refresh while any job is on its way; running imports and
 * queued exports can be cancelled; finished exports downloaded.
 */
import { computed, onBeforeUnmount, ref, shallowRef, watch } from "vue";
import { RouterLink, useRouter } from "vue-router";
import { useIntegration } from "../../api/integration";
import type { ExportJob, ImportJob, IntegrationFormat } from "../../api/integration-schemas";
import ErrorAlert from "../../components/ErrorAlert.vue";
import ExportDialog from "../../components/integration/ExportDialog.vue";
import { stateTone } from "../../components/integration/jobs";
import { canCancelExport, canCancelImport, canImportAny, isTerminal, polling, saveBlob } from "../../lib/integration";
import { ariaKeys, keyLabel, useShortcuts } from "../../lib/shortcuts";
import { absoluteTime, relativeTime } from "../../lib/time";
import { usePeople } from "../../session/people";
import { strings } from "../../strings";
import { useProject } from "./context";

const router = useRouter();
const { tenant, projectId, project, locales, grant } = useProject();
const port = useIntegration();
const people = usePeople(() => tenant.value);
const s = strings.integration;
const canImport = computed(() => canImportAny(grant.value));

const scope = ref<"project" | "tenant">("project");
const imports = shallowRef<ImportJob[]>([]);
const exports = shallowRef<ExportJob[]>([]);
const nextImports = ref<string>();
const nextExports = ref<string>();
const loading = ref(false);
const error = ref<unknown>(null);
const status = ref("");
let timer: ReturnType<typeof setTimeout> | undefined;
let seq = 0;

const query = () => (scope.value === "project" ? { project: projectId.value } : {});
const active = computed(() => imports.value.some((j) => j.state === "queued" || j.state === "running") || exports.value.some((j) => !isTerminal(j.state)));

async function load(): Promise<void> {
  const mine = ++seq;
  loading.value = true;
  error.value = null;
  try {
    const [i, e] = await Promise.all([port.importJobs(tenant.value, query()), port.exportJobs(tenant.value, query())]);
    if (mine !== seq) return;
    imports.value = i.items;
    nextImports.value = i.next;
    exports.value = e.items;
    nextExports.value = e.next;
  } catch (e) {
    if (mine === seq) error.value = e;
  } finally {
    if (mine === seq) loading.value = false;
    schedule();
  }
}
/** Refresh while something is on its way (the first page is where running jobs are: newest first). */
function schedule(): void {
  clearTimeout(timer);
  if (active.value) timer = setTimeout(() => void refresh(), Math.max(polling.ms, 1) * 2);
}
async function refresh(): Promise<void> {
  try {
    const [i, e] = await Promise.all([port.importJobs(tenant.value, query()), port.exportJobs(tenant.value, query())]);
    const merge = <T extends { id: string }>(fresh: T[], old: T[]) => [...fresh, ...old.filter((o) => !fresh.some((f) => f.id === o.id))];
    imports.value = merge(i.items, imports.value);
    exports.value = merge(e.items, exports.value);
  } catch {
    // the next refresh tries again
  }
  schedule();
}
watch([tenant, projectId, scope], load, { immediate: true });
onBeforeUnmount(() => clearTimeout(timer));

async function more(which: "imports" | "exports"): Promise<void> {
  try {
    if (which === "imports") {
      const p = await port.importJobs(tenant.value, query(), nextImports.value);
      imports.value = [...imports.value, ...p.items];
      nextImports.value = p.next;
    } else {
      const p = await port.exportJobs(tenant.value, query(), nextExports.value);
      exports.value = [...exports.value, ...p.items];
      nextExports.value = p.next;
    }
  } catch (e) {
    error.value = e;
  }
}

async function cancelImport(j: ImportJob): Promise<void> {
  try {
    const next = await port.cancelImport(tenant.value, j.id);
    imports.value = imports.value.map((x) => (x.id === next.id ? next : x));
    status.value = next.state === "cancelled" ? s.cancelled : s.cancelRequested;
    schedule();
  } catch (e) {
    error.value = e;
  }
}
async function cancelExport(j: ExportJob): Promise<void> {
  try {
    const next = await port.cancelExport(tenant.value, j.id);
    exports.value = exports.value.map((x) => (x.id === next.id ? next : x));
    status.value = s.cancelled;
  } catch (e) {
    error.value = e;
  }
}

const downloading = ref<string>();
async function download(j: ExportJob): Promise<void> {
  downloading.value = j.id;
  error.value = null;
  try {
    const f = await port.download(tenant.value, j);
    saveBlob(f.blob, f.name);
    status.value = s.downloaded(f.name);
  } catch (e) {
    error.value = e;
  } finally {
    downloading.value = undefined;
  }
}

// ── export dialog ─────────────────────────────────────────────────────
const exportOpen = ref(false);
const preset = ref<IntegrationFormat>();
function openExport(f?: IntegrationFormat): void {
  preset.value = f;
  exportOpen.value = true;
}
function onExportCreated(j: ExportJob): void {
  exports.value = [j, ...exports.value.filter((x) => x.id !== j.id)];
}
function onExportClosed(): void {
  exportOpen.value = false;
  void refresh();
}

useShortcuts({
  importFile: () => {
    if (!canImport.value) return false;
    void router.push({ name: "import", params: { tenant: tenant.value, project: projectId.value } });
  },
  exportFile: () => openExport(),
});

const name = (j: { file_name: string }) => j.file_name || s.unnamed;
const importsProgress = (j: ImportJob) => (j.total_items ? `${j.processed_items}/${j.total_items}` : "");
</script>

<template>
  <div class="page stack">
    <div class="page-header">
      <div class="stack-sm">
        <h1>{{ s.title }}</h1>
        <p class="muted lead">{{ s.lead }}</p>
      </div>
      <div class="row">
        <RouterLink
          v-if="canImport"
          class="btn btn-primary"
          :to="{ name: 'import', params: { tenant, project: projectId } }"
          :aria-keyshortcuts="ariaKeys(['i'])"
        >
          {{ s.importFile }} <kbd class="kbd-hint" aria-hidden="true">{{ keyLabel("i") }}</kbd>
        </RouterLink>
        <button type="button" class="btn" :aria-keyshortcuts="ariaKeys(['x'])" @click="openExport()">
          {{ s.export }} <kbd class="kbd-hint" aria-hidden="true">{{ keyLabel("x") }}</kbd>
        </button>
      </div>
    </div>
    <p v-if="!canImport" class="alert">{{ s.noImportRights }}</p>
    <div class="row">
      <button type="button" class="btn btn-sm" @click="openExport('tmx')">{{ s.exportTmx }}</button>
      <button type="button" class="btn btn-sm" @click="openExport('tbx')">{{ s.exportTbx }}</button>
      <RouterLink class="btn btn-sm btn-ghost" :to="{ name: 'workspace-knowledge', params: { tenant } }">
        {{ strings.nav.workspaceSettings }}: {{ strings.tenantSettings.knowledgeLink }}
      </RouterLink>
      <span class="spacer" />
      <div class="field inline">
        <label for="jobs-scope">{{ s.show }}</label>
        <select id="jobs-scope" v-model="scope">
          <option value="project">{{ s.scopeProject }}</option>
          <option value="tenant">{{ s.scopeTenant }}</option>
        </select>
      </div>
    </div>
    <ErrorAlert :error="error" />
    <p class="muted" role="status" data-testid="files-status">{{ status }}</p>

    <section class="stack-sm" aria-labelledby="imports-h">
      <h2 id="imports-h">{{ s.imports }}</h2>
      <p v-if="loading && !imports.length" class="muted">{{ strings.app.loading }}</p>
      <p v-else-if="!imports.length" class="card muted">{{ s.noImports }}</p>
      <div v-else class="scroll">
        <table class="table" data-testid="import-jobs">
          <thead>
            <tr>
              <th scope="col">{{ s.columns.file }}</th>
              <th scope="col">{{ s.columns.format }}</th>
              <th scope="col">{{ s.columns.mode }}</th>
              <th scope="col">{{ s.columns.state }}</th>
              <th scope="col">{{ s.columns.results }}</th>
              <th scope="col">{{ s.columns.requestedBy }}</th>
              <th scope="col">{{ s.columns.when }}</th>
              <th scope="col"><span class="visually-hidden">{{ s.columns.actions }}</span></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="j in imports" :key="j.id" :data-job="j.id">
              <td class="mono file">{{ name(j) }}</td>
              <td>{{ s.format[j.format] }}<span v-if="!j.project_id" class="hint"> · {{ s.scopeTenant }}</span></td>
              <td>{{ s.mode[j.mode] }}</td>
              <td>
                <span class="pill" :class="stateTone(j.state)">{{ s.state[j.state] }}</span>
                <span v-if="j.state === 'running'" class="hint"> {{ importsProgress(j) }}</span>
              </td>
              <td>{{ j.state === "succeeded" ? s.counts(j.summary.created, j.summary.updated, j.summary.conflict, j.summary.invalid) : "—" }}</td>
              <td>{{ people(j.created_by) }}</td>
              <td :title="absoluteTime(j.created_at)">{{ relativeTime(j.created_at) }}</td>
              <td class="actions">
                <RouterLink class="btn btn-sm" :to="{ name: 'import-job', params: { tenant, project: projectId, job: j.id } }">
                  {{ s.open }}<span class="visually-hidden">{{ s.ofJob(name(j)) }}</span>
                </RouterLink>
                <button v-if="canCancelImport(j)" type="button" class="btn btn-sm" @click="cancelImport(j)">
                  {{ s.cancel }}<span class="visually-hidden">{{ s.ofJob(name(j)) }}</span>
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <button v-if="nextImports" type="button" class="btn btn-sm more" @click="more('imports')">{{ s.loadMore }}</button>
    </section>

    <section class="stack-sm" aria-labelledby="exports-h">
      <h2 id="exports-h">{{ s.exports }}</h2>
      <p v-if="loading && !exports.length" class="muted">{{ strings.app.loading }}</p>
      <p v-else-if="!exports.length" class="card muted">{{ s.noExports }}</p>
      <div v-else class="scroll">
        <table class="table" data-testid="export-jobs">
          <thead>
            <tr>
              <th scope="col">{{ s.columns.file }}</th>
              <th scope="col">{{ s.columns.format }}</th>
              <th scope="col">{{ s.columns.state }}</th>
              <th scope="col">{{ s.columns.written }}</th>
              <th scope="col">{{ s.columns.requestedBy }}</th>
              <th scope="col">{{ s.columns.when }}</th>
              <th scope="col"><span class="visually-hidden">{{ s.columns.actions }}</span></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="j in exports" :key="j.id" :data-job="j.id">
              <td class="mono file">{{ j.file_name || "—" }}</td>
              <td>{{ s.format[j.format] }}<span v-if="!j.project_id" class="hint"> · {{ s.scopeTenant }}</span></td>
              <td><span class="pill" :class="stateTone(j.state)">{{ s.state[j.state] }}</span></td>
              <td>{{ j.state === "succeeded" ? s.wrote(j.written) : "—" }}</td>
              <td>{{ people(j.created_by) }}</td>
              <td :title="absoluteTime(j.created_at)">{{ relativeTime(j.created_at) }}</td>
              <td class="actions">
                <template v-if="j.state === 'succeeded'">
                  <span v-if="j.files_deleted_at || !j.download_url" class="hint">{{ s.expired }}</span>
                  <button v-else type="button" class="btn btn-sm" :disabled="downloading === j.id" @click="download(j)">
                    {{ s.download }}<span class="visually-hidden">{{ s.ofJob(j.file_name) }}</span>
                  </button>
                </template>
                <button v-if="canCancelExport(j)" type="button" class="btn btn-sm" @click="cancelExport(j)">
                  {{ s.cancel }}<span class="visually-hidden">{{ s.ofJob(s.format[j.format] ?? j.format) }}</span>
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <button v-if="nextExports" type="button" class="btn btn-sm more" @click="more('exports')">{{ s.loadMore }}</button>
    </section>

    <ExportDialog
      :open="exportOpen"
      :port="port"
      :tenant="tenant"
      :project-id="projectId"
      :project="project"
      :locales="locales"
      :preset="preset"
      @close="onExportClosed"
      @created="onExportCreated"
    />
  </div>
</template>

<style scoped>
.lead {
  max-inline-size: 48rem;
}
.inline {
  flex-direction: row;
  align-items: center;
  gap: var(--kl-space-2);
}
.scroll {
  overflow-x: auto;
}
.file {
  word-break: break-all;
}
.actions {
  white-space: nowrap;
  text-align: end;
}
.actions > * + * {
  margin-inline-start: var(--kl-space-2);
}
.more {
  align-self: flex-start;
}
</style>
