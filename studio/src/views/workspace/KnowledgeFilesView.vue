<script setup lang="ts">
/**
 * Workspace settings › Translation memory & termbase (RFC 0003 §6): the
 * workspace's own memory and termbase, shared by every project, in and
 * out as files — outside any project, on the tenant's own routes
 * (`tm-import-jobs`, `termbase-import-jobs`, `tm-export-jobs`,
 * `termbase-export-jobs`).
 *
 * Import: a TMX or TBX file, a dry run first by default, overwrite after
 * confirming; it needs `integration.manage` and `knowledge.write`
 * (mirrored; the server decides). Export: the whole memory (narrowed by
 * source and target locales) or termbase, downloaded with the session.
 * Both lists refresh while a job is on its way.
 */
import { computed, onBeforeUnmount, ref, shallowRef, watch } from "vue";
import { RouterLink, useRoute, useRouter } from "vue-router";
import { useIntegration } from "../../api/integration";
import type { ExportJob, ImportJob, ImportMode, IntegrationFormat, KnowledgeKind } from "../../api/integration-schemas";
import ErrorAlert from "../../components/ErrorAlert.vue";
import { failureText, stateTone } from "../../components/integration/jobs";
import JobProgress from "../../components/integration/JobProgress.vue";
import { useImportRun } from "../../components/integration/useImportRun";
import LocaleInput from "../../components/LocaleInput.vue";
import ModalDialog from "../../components/ModalDialog.vue";
import { checkLocale } from "../../lib/bcp47";
import { bytes, canCancelExport, canCancelImport, canOverwrite, detectFormat, isTerminal, polling, pollUntil, readHead, saveBlob } from "../../lib/integration";
import { absoluteTime, relativeTime } from "../../lib/time";
import { allows } from "../../session/permissions";
import { usePeople } from "../../session/people";
import { membershipFor, useGrant } from "../../session/session";
import { strings } from "../../strings";

const route = useRoute();
const router = useRouter();
const tenant = computed(() => String(route.params.tenant));
const grant = useGrant(() => tenant.value);
const port = useIntegration();
const people = usePeople(() => tenant.value);
const s = strings.integration;
const k = strings.tenantSettings.knowledge;
const w = s.wizard;
const MODES: ImportMode[] = ["dry_run", "merge", "overwrite"];
const KINDS: KnowledgeKind[] = ["tm", "termbase"];
const tenantName = computed(() => membershipFor(tenant.value)?.tenant.name ?? "");
const canImport = computed(() => allows(grant.value, "integration.manage") && allows(grant.value, "knowledge.write"));
const kindOfFormat = (f: IntegrationFormat): KnowledgeKind | undefined => (f === "tmx" ? "tm" : f === "tbx" ? "termbase" : undefined);

// ── the lists ─────────────────────────────────────────────────────────
const imports = shallowRef<ImportJob[]>([]);
const exports = shallowRef<ExportJob[]>([]);
const loading = ref(false);
const error = ref<unknown>(null);
const status = ref("");
let timer: ReturnType<typeof setTimeout> | undefined;

const newest = <T extends { created_at: string; id: string }>(lists: T[][]) =>
  lists.flat().sort((a, b) => b.created_at.localeCompare(a.created_at) || b.id.localeCompare(a.id));
const active = computed(() => imports.value.some((j) => j.state === "queued" || j.state === "running") || exports.value.some((j) => !isTerminal(j.state)));

async function load(): Promise<void> {
  loading.value = true;
  error.value = null;
  try {
    const [i, e] = await Promise.all([
      Promise.all(KINDS.map((kind) => port.knowledgeImports(tenant.value, kind))),
      Promise.all(KINDS.map((kind) => port.knowledgeExports(tenant.value, kind))),
    ]);
    imports.value = newest(i.map((p) => p.items));
    exports.value = newest(e.map((p) => p.items));
  } catch (e) {
    error.value = e;
  } finally {
    loading.value = false;
    schedule();
  }
}
function schedule(): void {
  clearTimeout(timer);
  if (active.value) timer = setTimeout(() => void load(), Math.max(polling.ms, 1) * 2);
}
watch(tenant, load, { immediate: true });
onBeforeUnmount(() => clearTimeout(timer));

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

// ── import ────────────────────────────────────────────────────────────
const file = shallowRef<File>();
const format = ref<IntegrationFormat>();
const mode = ref<ImportMode>("dry_run");
const runner = useImportRun(port, tenant);
const confirming = ref(false);
const importKind = computed(() => (format.value ? kindOfFormat(format.value) : undefined));
const importProblem = computed(() => (file.value && !importKind.value ? k.notKnowledge : undefined));
const canStart = computed(() => !!file.value && !!importKind.value && runner.phase.value === "idle");

async function choose(e: Event): Promise<void> {
  const f = (e.target as HTMLInputElement).files?.[0];
  if (!f) return;
  file.value = f;
  format.value = detectFormat(f.name, await readHead(f));
}
async function startImport(confirmed = false): Promise<void> {
  if (!canStart.value || !file.value || !format.value) return;
  if (mode.value === "overwrite" && !confirmed) {
    confirming.value = true;
    return;
  }
  confirming.value = false;
  const done = await runner.run({ format: format.value, mode: mode.value, file_name: file.value.name }, file.value);
  if (done) await router.push({ name: "workspace-import-job", params: { tenant: tenant.value, job: done.id } });
}

// ── export ────────────────────────────────────────────────────────────
const exportKind = ref<KnowledgeKind>("tm");
const sourceLocale = ref("");
const targetInput = ref("");
const targets = ref<string[]>([]);
const exportJob = shallowRef<ExportJob>();
const exportError = ref<unknown>(null);
const exporting = ref(false);
const saved = ref("");
let poller: AbortController | undefined;
onBeforeUnmount(() => poller?.abort());

const sourceCheck = computed(() => checkLocale(sourceLocale.value));
const sourceBad = computed(() => exportKind.value === "tm" && !!sourceLocale.value.trim() && !sourceCheck.value.ok);
function addTarget(): void {
  const c = checkLocale(targetInput.value);
  if (!c.ok) return;
  if (!targets.value.includes(c.tag)) targets.value = [...targets.value, c.tag];
  targetInput.value = "";
}
const removeTarget = (l: string) => (targets.value = targets.value.filter((x) => x !== l));

async function startExport(): Promise<void> {
  if (exporting.value || sourceBad.value) return;
  exporting.value = true;
  exportError.value = null;
  saved.value = "";
  poller?.abort();
  const ctrl = (poller = new AbortController());
  try {
    const options: { locales?: string[]; source_locale?: string } = {};
    if (exportKind.value === "tm") {
      if (targets.value.length) options.locales = [...targets.value];
      if (sourceCheck.value.ok) options.source_locale = sourceCheck.value.tag;
    }
    const created = await port.createKnowledgeExport(tenant.value, exportKind.value, Object.keys(options).length ? { options } : {}, globalThis.crypto.randomUUID());
    exportJob.value = created;
    exports.value = [created, ...exports.value.filter((x) => x.id !== created.id)];
    const done = await pollUntil(() => port.exportJob(tenant.value, created.id), (j) => isTerminal(j.state), (j) => (exportJob.value = j), polling.ms, ctrl.signal);
    exports.value = exports.value.map((x) => (x.id === done.id ? done : x));
  } catch (e) {
    if (!(e instanceof DOMException && e.name === "AbortError")) exportError.value = e;
  } finally {
    exporting.value = false;
  }
}
async function downloadExport(): Promise<void> {
  const j = exportJob.value;
  if (!j) return;
  try {
    const f = await port.download(tenant.value, j);
    saveBlob(f.blob, f.name);
    saved.value = s.downloaded(f.name);
  } catch (e) {
    exportError.value = e;
  }
}

const name = (j: { file_name: string }) => j.file_name || s.unnamed;
</script>

<template>
  <div class="page stack">
    <div class="page-header">
      <div class="stack-sm">
        <nav :aria-label="strings.nav.breadcrumb" class="crumbs">
          <RouterLink :to="{ name: 'projects', params: { tenant } }">{{ k.back }}</RouterLink>
          <span aria-hidden="true">/</span>
          <span>{{ strings.nav.workspaceSettings }}</span>
        </nav>
        <p v-if="tenantName" class="muted">{{ tenantName }}</p>
        <h1>{{ k.title }}</h1>
        <p class="muted lead">{{ k.lead }}</p>
      </div>
    </div>

    <section class="card stack" aria-labelledby="kn-import-h">
      <h2 id="kn-import-h">{{ k.importTitle }}</h2>
      <p class="hint">{{ k.importLead }}</p>
      <p v-if="!canImport" class="alert" data-testid="knowledge-no-import">{{ k.noImportRights }}</p>
      <form v-else class="stack" :aria-label="k.importTitle" @submit.prevent="startImport()">
        <fieldset class="stack plain" :disabled="runner.phase.value !== 'idle'">
          <div class="field">
            <label for="kn-file">{{ k.file }}</label>
            <input id="kn-file" type="file" accept=".tmx,.tbx" aria-describedby="kn-file-hint" @change="choose" />
            <p id="kn-file-hint" class="hint">{{ k.fileHint }}</p>
          </div>
          <p v-if="file && importKind && format" class="mono" data-testid="knowledge-file">
            {{ w.chosen(file.name, bytes(file.size)) }} — {{ k.detected(s.format[format]!, s.kind[importKind]!) }}
          </p>
          <div v-if="importKind" class="modes">
            <label v-for="m in MODES" :key="m" class="mode" :class="{ chosen: mode === m, disabled: m === 'overwrite' && !canOverwrite(grant) }">
              <input v-model="mode" type="radio" name="kn-mode" :value="m" :disabled="m === 'overwrite' && !canOverwrite(grant)" :aria-describedby="`kn-mode-${m}`" />
              <span class="stack-sm">
                <strong>{{ s.mode[m] }}</strong>
                <span :id="`kn-mode-${m}`" class="hint">{{ w.modes[m] }}</span>
              </span>
            </label>
          </div>
        </fieldset>
        <p v-if="importProblem" class="alert alert-warn" role="alert">{{ importProblem }}</p>
        <ErrorAlert :error="runner.error.value" />
        <JobProgress
          v-if="runner.phase.value !== 'idle'"
          :text="runner.progressText.value"
          :value="runner.progressValue.value"
          :label="w.progress"
          cancellable
          @cancel="runner.cancel()"
        />
        <div v-else-if="importKind" class="row">
          <button type="submit" class="btn" :class="mode === 'overwrite' ? 'btn-danger' : 'btn-primary'" :disabled="!canStart">{{ w.start[mode] }}</button>
        </div>
      </form>
    </section>

    <section class="card stack" aria-labelledby="kn-export-h">
      <h2 id="kn-export-h">{{ k.exportTitle }}</h2>
      <p class="hint">{{ k.exportLead }}</p>
      <form class="stack" :aria-label="k.exportTitle" @submit.prevent="startExport">
        <fieldset class="stack-sm plain" :disabled="exporting">
          <legend class="label">{{ k.what }}</legend>
          <label class="check"><input v-model="exportKind" type="radio" name="kn-export-kind" value="tm" /> {{ k.tm }}</label>
          <label class="check"><input v-model="exportKind" type="radio" name="kn-export-kind" value="termbase" /> {{ k.termbase }}</label>
        </fieldset>
        <template v-if="exportKind === 'tm'">
          <div class="narrow">
            <LocaleInput id="kn-source" v-model="sourceLocale" :label="k.sourceLocale" :hint="k.sourceHint" />
          </div>
          <div class="row targets">
            <div class="narrow">
              <LocaleInput id="kn-target" v-model="targetInput" :label="k.targetLocale" :hint="k.targetHint" @keydown.enter.prevent="addTarget" />
            </div>
            <button type="button" class="btn btn-sm" :disabled="!checkLocale(targetInput).ok" @click="addTarget">{{ k.addLocale }}</button>
          </div>
          <ul v-if="targets.length" class="row chips" :aria-label="k.chosenTargets" data-testid="knowledge-targets">
            <li v-for="l in targets" :key="l" class="pill pill-neutral">
              <span class="mono">{{ l }}</span>
              <button type="button" class="btn btn-ghost btn-sm" :aria-label="k.removeLocale(l)" @click="removeTarget(l)">×</button>
            </li>
          </ul>
        </template>
        <div class="row">
          <button type="submit" class="btn btn-primary" :disabled="exporting || sourceBad">{{ k.start }}</button>
        </div>
      </form>
      <div v-if="exportJob" class="stack-sm" data-testid="knowledge-export">
        <JobProgress v-if="!isTerminal(exportJob.state)" :text="exportJob.state === 'running' ? s.exportDialog.running : s.exportDialog.queued" :label="k.exportTitle" />
        <div v-else-if="exportJob.state === 'succeeded'" class="alert alert-ok row">
          <p role="status" data-testid="knowledge-export-ready">{{ s.exportDialog.ready(exportJob.file_name, bytes(exportJob.file?.size ?? 0), exportJob.written) }}</p>
          <button type="button" class="btn btn-sm btn-primary" @click="downloadExport">{{ s.download }}</button>
        </div>
        <p v-else-if="exportJob.state === 'failed'" class="alert alert-error" role="alert">
          {{ s.exportDialog.failed(failureText(exportJob.failure_code, exportJob.failure_message)) }}
        </p>
        <p v-if="saved" class="muted" role="status" data-testid="knowledge-saved">{{ saved }}</p>
      </div>
      <ErrorAlert :error="exportError" />
    </section>

    <ErrorAlert :error="error" />
    <p class="muted" role="status">{{ status }}</p>

    <section class="stack-sm" aria-labelledby="kn-imports-h">
      <h2 id="kn-imports-h">{{ k.imports }}</h2>
      <p v-if="loading && !imports.length" class="muted">{{ strings.app.loading }}</p>
      <p v-else-if="!imports.length" class="card muted">{{ s.noImports }}</p>
      <div v-else class="scroll">
        <table class="table" data-testid="knowledge-imports">
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
              <td>{{ s.format[j.format] }}</td>
              <td>{{ s.mode[j.mode] }}</td>
              <td><span class="pill" :class="stateTone(j.state)">{{ s.state[j.state] }}</span></td>
              <td>{{ j.state === "succeeded" ? s.counts(j.summary.created, j.summary.updated, j.summary.conflict, j.summary.invalid) : "—" }}</td>
              <td>{{ people(j.created_by) }}</td>
              <td :title="absoluteTime(j.created_at)">{{ relativeTime(j.created_at) }}</td>
              <td class="actions">
                <RouterLink class="btn btn-sm" :to="{ name: 'workspace-import-job', params: { tenant, job: j.id } }">
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
    </section>

    <section class="stack-sm" aria-labelledby="kn-exports-h">
      <h2 id="kn-exports-h">{{ k.exports }}</h2>
      <p v-if="loading && !exports.length" class="muted">{{ strings.app.loading }}</p>
      <p v-else-if="!exports.length" class="card muted">{{ s.noExports }}</p>
      <div v-else class="scroll">
        <table class="table" data-testid="knowledge-exports">
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
              <td>{{ s.format[j.format] }}</td>
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
    </section>

    <ModalDialog :open="confirming" :title="w.confirmOverwrite" @close="confirming = false">
      <p>{{ w.confirmOverwriteLead(file?.name ?? "") }}</p>
      <template #actions>
        <button type="button" class="btn" @click="confirming = false">{{ strings.app.cancel }}</button>
        <button type="button" class="btn btn-danger" @click="startImport(true)">{{ w.overwrite }}</button>
      </template>
    </ModalDialog>
  </div>
</template>

<style scoped>
.lead {
  max-inline-size: 48rem;
}
.crumbs {
  display: flex;
  gap: var(--kl-space-2);
  align-items: center;
}
.plain {
  border: 0;
  margin: 0;
  padding: 0;
  min-inline-size: 0;
}
.narrow {
  max-inline-size: 28rem;
}
.targets {
  align-items: flex-end;
}
.chips {
  list-style: none;
  margin: 0;
  padding: 0;
  gap: var(--kl-space-2);
}
.chips li {
  display: inline-flex;
  align-items: center;
  gap: var(--kl-space-1);
}
.modes {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(14rem, 1fr));
  gap: var(--kl-space-3);
}
.mode {
  display: flex;
  gap: var(--kl-space-3);
  align-items: flex-start;
  border: 1px solid var(--kl-border);
  border-radius: var(--kl-radius-md);
  padding: var(--kl-space-3);
  cursor: pointer;
}
.mode.chosen {
  border-color: var(--kl-accent);
  box-shadow: 0 0 0 1px var(--kl-accent);
}
.mode.disabled {
  cursor: not-allowed;
  border-style: dashed;
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
</style>
