<script setup lang="ts">
/**
 * Export (RFC 0003 §6): a catalog (XLIFF or JSON: locales, namespaces
 * chosen from the project's own — `GET …/namespaces`, with their message
 * counts —, review states, JSON layout and syntax), the project's
 * translation memory (TMX: source and target locales) or termbase (TBX).
 * The whole workspace's memory and termbase are exported from workspace
 * settings. The job is queued, followed until it's written, and the file
 * is downloaded with the session (several locales come as one zip). `po`
 * is import only.
 */
import { computed, onBeforeUnmount, ref, shallowRef, watch } from "vue";
import { RouterLink } from "vue-router";
import type { ExportRequest, IntegrationPort } from "../../api/integration";
import type { ExportJob, ExportOptions, IntegrationFormat, NamespaceSummary } from "../../api/integration-schemas";
import type { Project, ProjectLocale, ReviewState, Syntax } from "../../api/schemas";
import { bytes, canCancelExport, EXPORT_FORMATS, isCatalog, isTerminal, polling, pollUntil, saveBlob } from "../../lib/integration";
import { strings } from "../../strings";
import ErrorAlert from "../ErrorAlert.vue";
import ModalDialog from "../ModalDialog.vue";
import { failureText } from "./jobs";
import JobProgress from "./JobProgress.vue";

const props = defineProps<{
  open: boolean;
  port: IntegrationPort;
  tenant: string;
  projectId: string;
  project: Project | undefined;
  locales: ProjectLocale[];
  /** Start with this format (TMX from the memory, TBX from the termbase). */
  preset?: IntegrationFormat | undefined;
}>();
const emit = defineEmits<{ close: []; created: [job: ExportJob] }>();
const s = strings.integration;
const d = s.exportDialog;
const STATES: ReviewState[] = ["draft", "needs_review", "approved", "rejected"];

const format = ref<IntegrationFormat>("json");
const chosen = ref<string[]>([]);
const sourceLocale = ref("");
/** Chosen namespaces; none means every namespace. */
const namespaces = ref<string[]>([]);
const namespaceList = shallowRef<NamespaceSummary[]>([]);
const namespacesNext = ref<string>();
const namespacesLoading = ref(false);
const states = ref<ReviewState[]>(["approved"]);
const layout = ref<"flat" | "nested">("flat");
const syntax = ref<Syntax>("mf1");
const job = shallowRef<ExportJob>();
const error = ref<unknown>(null);
const busy = ref(false);
const saved = ref("");
let poller: AbortController | undefined;

const source = computed(() => props.project?.source_locale);
const sorted = computed(() => [...props.locales].sort((a, b) => Number(b.is_source) - Number(a.is_source) || a.code.localeCompare(b.code)));
/** Which locales the format writes: XLIFF targets only; JSON any; TMX targets of units. */
const choosable = computed(() => (format.value === "json" ? sorted.value : sorted.value.filter((l) => !l.is_source)));
const title = computed(() => (props.preset === "tmx" ? d.titleTmx : props.preset === "tbx" ? d.titleTbx : d.title));

function defaults(): void {
  chosen.value = format.value === "tmx" ? [] : choosable.value.map((l) => l.code);
  sourceLocale.value = "";
}

/** The project's namespaces, a page at a time. */
async function loadNamespaces(more = false): Promise<void> {
  namespacesLoading.value = true;
  try {
    const p = await props.port.namespaces(props.tenant, props.projectId, more ? namespacesNext.value : undefined);
    namespaceList.value = more ? [...namespaceList.value, ...p.items] : p.items;
    namespacesNext.value = p.next;
  } catch (e) {
    error.value = e;
  } finally {
    namespacesLoading.value = false;
  }
}

watch(
  () => props.open,
  (open) => {
    if (!open) {
      poller?.abort();
      return;
    }
    format.value = props.preset ?? "json";
    namespaces.value = [];
    namespaceList.value = [];
    namespacesNext.value = undefined;
    states.value = ["approved"];
    layout.value = "flat";
    syntax.value = props.project?.settings.default_syntax ?? "mf1";
    job.value = undefined;
    error.value = null;
    saved.value = "";
    defaults();
    if (isCatalog(format.value)) void loadNamespaces();
  },
  { immediate: true },
);
watch(format, (f) => {
  if (props.open && !job.value) defaults();
  if (props.open && isCatalog(f) && !namespaceList.value.length && !namespacesLoading.value) void loadNamespaces();
});
onBeforeUnmount(() => poller?.abort());

const noStates = computed(() => isCatalog(format.value) && states.value.length === 0);

function request(): ExportRequest {
  const f = format.value;
  const options: ExportOptions = {};
  if (isCatalog(f)) {
    if (chosen.value.length) options.locales = [...chosen.value];
    if (namespaces.value.length) options.namespaces = [...namespaces.value];
    options.states = [...states.value];
  }
  if (f === "json") {
    options.layout = layout.value;
    options.syntax = syntax.value;
  }
  if (f === "tmx") {
    if (chosen.value.length) options.locales = [...chosen.value];
    if (sourceLocale.value) options.source_locale = sourceLocale.value;
  }
  return {
    format: f,
    project_id: props.projectId,
    ...(Object.keys(options).length ? { options } : {}),
  };
}

async function start(): Promise<void> {
  if (busy.value || noStates.value) return;
  busy.value = true;
  error.value = null;
  poller?.abort();
  const ctrl = (poller = new AbortController());
  try {
    const created = await props.port.createExport(props.tenant, request(), globalThis.crypto.randomUUID());
    job.value = created;
    emit("created", created);
    await pollUntil(() => props.port.exportJob(props.tenant, created.id), (j) => isTerminal(j.state), (j) => (job.value = j), polling.ms, ctrl.signal);
  } catch (e) {
    if (!(e instanceof DOMException && e.name === "AbortError")) error.value = e;
  } finally {
    busy.value = false;
  }
}

async function cancel(): Promise<void> {
  const j = job.value;
  if (!j) return;
  try {
    poller?.abort();
    job.value = await props.port.cancelExport(props.tenant, j.id);
  } catch (e) {
    error.value = e;
  }
}

const downloading = ref(false);
async function download(): Promise<void> {
  const j = job.value;
  if (!j) return;
  downloading.value = true;
  error.value = null;
  try {
    const f = await props.port.download(props.tenant, j);
    saveBlob(f.blob, f.name);
    saved.value = s.downloaded(f.name);
  } catch (e) {
    error.value = e;
  } finally {
    downloading.value = false;
  }
}

const localesHint = computed(() => (format.value === "xliff" ? d.localesHintXliff : format.value === "json" ? d.localesHintJson : d.localesHintTmx));
</script>

<template>
  <ModalDialog :open="open" :title="title" wide @close="emit('close')">
    <form v-if="!job" id="export-form" class="stack" @submit.prevent="start">
      <div class="field narrow">
        <label for="exp-format">{{ d.format }}</label>
        <select id="exp-format" v-model="format" aria-describedby="exp-format-hint">
          <option v-for="f in EXPORT_FORMATS" :key="f" :value="f">{{ s.format[f] }} — {{ s.kind[f === "tmx" ? "tm" : f === "tbx" ? "termbase" : "catalog"] }}</option>
        </select>
        <p id="exp-format-hint" class="hint">{{ d.poImportOnly }}</p>
      </div>

      <p v-if="!isCatalog(format)" class="hint" data-testid="knowledge-scope">
        {{ d.projectKnowledge }}
        <RouterLink :to="{ name: 'workspace-knowledge', params: { tenant } }">{{ strings.tenantSettings.knowledgeLink }}</RouterLink>
      </p>

      <div v-if="format === 'tmx'" class="field narrow">
        <label for="exp-source">{{ d.sourceLocale }}</label>
        <select id="exp-source" v-model="sourceLocale">
          <option value="">{{ d.anySource }}</option>
          <option v-for="l in sorted" :key="l.code" :value="l.code">{{ l.code }}</option>
        </select>
      </div>

      <fieldset v-if="format !== 'tbx'" class="stack-sm plain" aria-describedby="exp-locales-hint">
        <legend class="label">{{ d.locales }}</legend>
        <p id="exp-locales-hint" class="hint">{{ localesHint }}</p>
        <div class="row">
          <label v-for="l in choosable" :key="l.code" class="check">
            <input v-model="chosen" type="checkbox" :value="l.code" />
            <span class="mono">{{ l.code }}</span><span v-if="l.code === source" class="hint">{{ d.source }}</span>
          </label>
        </div>
      </fieldset>

      <template v-if="isCatalog(format)">
        <fieldset class="stack-sm plain" aria-describedby="exp-ns-hint" data-testid="export-namespaces">
          <legend class="label">{{ d.namespaces }}</legend>
          <p id="exp-ns-hint" class="hint">{{ d.namespacesHint }}</p>
          <p v-if="namespacesLoading && !namespaceList.length" class="muted">{{ strings.app.loading }}</p>
          <p v-else-if="!namespaceList.length" class="muted">{{ d.noNamespaces }}</p>
          <div v-else class="row">
            <label v-for="n in namespaceList" :key="n.name" class="check">
              <input v-model="namespaces" type="checkbox" :value="n.name" />
              <span class="mono">{{ n.name }}</span><span class="hint">{{ d.namespaceCount(n.active_messages) }}</span>
            </label>
          </div>
          <button v-if="namespacesNext" type="button" class="btn btn-sm more" :disabled="namespacesLoading" @click="loadNamespaces(true)">{{ s.loadMore }}</button>
        </fieldset>
        <fieldset class="stack-sm plain" aria-describedby="exp-states-hint">
          <legend class="label">{{ d.states }}</legend>
          <p id="exp-states-hint" class="hint">{{ d.statesHint }}</p>
          <div class="row">
            <label v-for="st in STATES" :key="st" class="check"><input v-model="states" type="checkbox" :value="st" /> {{ d.stateNames[st] }}</label>
          </div>
          <p v-if="noStates" class="field-error" role="alert">{{ d.noStates }}</p>
        </fieldset>
      </template>

      <div v-if="format === 'json'" class="row options">
        <fieldset class="stack-sm plain">
          <legend class="label">{{ d.layout }}</legend>
          <label class="check"><input v-model="layout" type="radio" name="exp-layout" value="flat" /> {{ d.flat }}</label>
          <label class="check"><input v-model="layout" type="radio" name="exp-layout" value="nested" /> {{ d.nested }}</label>
        </fieldset>
        <fieldset class="stack-sm plain">
          <legend class="label">{{ d.syntax }}</legend>
          <label class="check"><input v-model="syntax" type="radio" name="exp-syntax" value="mf1" /> {{ s.wizard.syntaxMf1 }}</label>
          <label class="check"><input v-model="syntax" type="radio" name="exp-syntax" value="mf2" /> {{ s.wizard.syntaxMf2 }}</label>
        </fieldset>
      </div>
    </form>

    <div v-else class="stack" data-testid="export-job">
      <JobProgress
        v-if="!isTerminal(job.state)"
        :text="job.state === 'running' ? d.running : d.queued"
        :label="d.title"
        :cancellable="canCancelExport(job)"
        @cancel="cancel"
      />
      <div v-else-if="job.state === 'succeeded'" class="alert alert-ok stack-sm">
        <p role="status" data-testid="export-ready">{{ d.ready(job.file_name, bytes(job.file?.size ?? 0), job.written) }}</p>
      </div>
      <p v-else-if="job.state === 'failed'" class="alert alert-error" role="alert">{{ d.failed(failureText(job.failure_code, job.failure_message)) }}</p>
      <p v-else class="alert">{{ d.cancelledNote }}</p>
      <p v-if="saved" class="muted" role="status" data-testid="export-saved">{{ saved }}</p>
    </div>
    <ErrorAlert :error="error" />

    <template #actions>
      <button type="button" class="btn" @click="emit('close')">{{ strings.app.close }}</button>
      <button v-if="!job" type="submit" form="export-form" class="btn btn-primary" :disabled="busy || noStates">{{ d.start }}</button>
      <button v-else-if="job.state === 'succeeded'" type="button" class="btn btn-primary" :disabled="downloading" @click="download">
        {{ downloading ? s.downloading : s.download }}
      </button>
    </template>
  </ModalDialog>
</template>

<style scoped>
.plain {
  border: 0;
  margin: 0;
  padding: 0;
}
.narrow {
  max-inline-size: 28rem;
}
.more {
  align-self: flex-start;
}
.options {
  align-items: flex-start;
  gap: var(--kl-space-8);
}
</style>
