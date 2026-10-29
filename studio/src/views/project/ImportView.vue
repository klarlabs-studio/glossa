<script setup lang="ts">
/**
 * The import wizard (RFC 0003 §6): choose or drop a file, check how it's
 * read (the format is recognized, and can be changed; options per
 * format), choose what to do — a dry run first by default; overwrite
 * only with `integration.manage`, after confirming — then upload with
 * progress and land on the job's results.
 *
 * Locale-scoped `integration.import` is mirrored: locales the member
 * can't import are disabled with the reason, and a file in one of them
 * can't be sent. The server decides again when the job runs.
 */
import { computed, ref, shallowRef, watch } from "vue";
import { RouterLink, useRouter } from "vue-router";
import type { ImportRequest } from "../../api/integration";
import { useIntegration } from "../../api/integration";
import type { ImportMode, ImportOptions, IntegrationFormat } from "../../api/integration-schemas";
import type { ReviewState, Syntax } from "../../api/schemas";
import ErrorAlert from "../../components/ErrorAlert.vue";
import JobProgress from "../../components/integration/JobProgress.vue";
import { useImportRun } from "../../components/integration/useImportRun";
import ModalDialog from "../../components/ModalDialog.vue";
import {
  bytes,
  canImportAny,
  canOverwrite,
  detectFormat,
  IMPORT_FORMATS,
  importableFormats,
  isCatalog,
  localeBlock,
  localeFromName,
  poLanguage,
  projectLocale,
  readHead,
  xliffHasTargets,
  xliffLanguages,
} from "../../lib/integration";
import { strings } from "../../strings";
import { useProject } from "./context";

const { tenant, projectId, project, locales, targets, grant } = useProject();
const router = useRouter();
const port = useIntegration();
const s = strings.integration;
const w = s.wizard;

const codes = computed(() => locales.value.map((l) => l.code));
const source = computed(() => project.value?.source_locale);
const allowed = computed(() => importableFormats(grant.value));
const MODES: ImportMode[] = ["dry_run", "merge", "overwrite"];
const STATES: ReviewState[] = ["draft", "needs_review", "approved"];

// ── the file ──────────────────────────────────────────────────────────
const file = shallowRef<File>();
const head = ref("");
const detected = ref<IntegrationFormat>();
const format = ref<IntegrationFormat | "">("");
const dragging = ref(false);

// ── options ───────────────────────────────────────────────────────────
const jsonLocale = ref("");
const poLocale = ref("");
const namespace = ref("");
const syntax = ref<Syntax>("mf1");
const state = ref<ReviewState>("needs_review");
const pluralVariable = ref("");
const xliffIcu = ref(false);
/** The locale an XLIFF file's translations are imported as (`options.locale`). */
const xliffLocale = ref("");
const mode = ref<ImportMode>("dry_run");

/** A target the member may import, for a sensible default. */
const firstImportable = () => locales.value.find((l) => !l.is_source && !localeBlock(grant.value, l.code, source.value))?.code ?? "";

async function choose(f: File | undefined): Promise<void> {
  if (!f) return;
  file.value = f;
  head.value = await readHead(f);
  detected.value = detectFormat(f.name, head.value);
  format.value = detected.value ?? "";
  const named = localeFromName(f.name, codes.value);
  jsonLocale.value = named ?? (localeBlock(grant.value, source.value ?? "", source.value) ? firstImportable() : (source.value ?? ""));
  poLocale.value = "";
  syntax.value = project.value?.settings.default_syntax ?? "mf1";
  namespace.value = "";
  pluralVariable.value = "";
  xliffIcu.value = false;
  xliffLocale.value = projectLocale(xliffLanguages(head.value).target, targets.value.map((l) => l.code)) ?? "";
}
function onPick(e: Event): void {
  void choose((e.target as HTMLInputElement).files?.[0]);
}
function onDrop(e: DragEvent): void {
  dragging.value = false;
  void choose(e.dataTransfer?.files?.[0]);
}

watch(format, (f) => {
  state.value = f === "po" ? "approved" : "needs_review";
});

// ── what the file says, and whether the member may import it ────────────
const xliffTarget = computed(() => xliffLanguages(head.value).target);
/** An XLIFF file with translations, or naming a target locale, is imported as a locale the member chooses (default: its trgLang). */
const xliffNeedsLocale = computed(() => !!xliffTarget.value || xliffHasTargets(head.value));
const xliffHint = computed(() => {
  const t = xliffTarget.value;
  if (!t) return w.xliffNoTrgLang;
  return projectLocale(t, targets.value.map((l) => l.code)) ? w.xliffTargetFrom(t) : w.xliffTargetUnknown(t);
});
const poHeader = computed(() => poLanguage(head.value));
/** The catalog locale this import writes, as the project names it (or as the file does when the project lacks it). */
const targetLocale = computed<string | undefined>(() => {
  switch (format.value) {
    case "xliff":
      return xliffLocale.value || undefined;
    case "json":
      return jsonLocale.value || source.value;
    case "po": {
      const l = poLocale.value || poHeader.value;
      return l && (projectLocale(l, codes.value) ?? l);
    }
    default:
      return undefined;
  }
});
const notInProject = computed(() => !!targetLocale.value && !projectLocale(targetLocale.value, codes.value));

/** Why this can't be sent, in words; undefined when it can. */
const problem = computed<string | undefined>(() => {
  const f = format.value;
  if (!file.value || !f) return undefined;
  if (!allowed.value.includes(f)) return `${s.format[f]}${w.needsManage}`;
  if (mode.value === "overwrite" && !canOverwrite(grant.value)) return w.modes.overwrite;
  if (!isCatalog(f)) return undefined;
  if (f === "xliff" && !xliffNeedsLocale.value) return canOverwrite(grant.value) ? undefined : w.cannotImportSource;
  if (f === "xliff" && !xliffLocale.value) return w.xliffChoose;
  const l = targetLocale.value;
  if (!l) return undefined;
  if (notInProject.value) return w.notInProject(l);
  const block = localeBlock(grant.value, l, source.value);
  return block === "source" ? w.cannotImportSource : block === "scope" ? w.cannotImportLocale(l) : undefined;
});

const localeNote = (code: string) => {
  const block = localeBlock(grant.value, code, source.value);
  return block === "source" ? w.blockedSource : block === "scope" ? w.blockedScope : "";
};

function request(): ImportRequest {
  const f = format.value as IntegrationFormat;
  const options: ImportOptions = {};
  if (f === "xliff" && xliffIcu.value) options.syntax = "mf1";
  if (f === "xliff" && xliffNeedsLocale.value && xliffLocale.value) options.locale = xliffLocale.value;
  if (f === "json") {
    options.locale = jsonLocale.value || source.value;
    options.syntax = syntax.value;
    if (options.locale !== source.value) options.state = state.value;
  }
  if (f === "po") {
    if (poLocale.value) options.locale = poLocale.value;
    options.state = state.value;
    if (pluralVariable.value.trim()) options.plural_variable = pluralVariable.value.trim();
  }
  if ((f === "json" || f === "po") && namespace.value.trim()) options.namespace = namespace.value.trim();
  return {
    format: f,
    mode: mode.value,
    file_name: file.value!.name,
    project_id: projectId.value,
    ...(Object.keys(options).length ? { options } : {}),
  };
}

// ── run ───────────────────────────────────────────────────────────────
const runner = useImportRun(port, tenant);
const confirming = ref(false);
const canStart = computed(() => !!file.value && !!format.value && !problem.value && runner.phase.value === "idle");

async function start(confirmed = false): Promise<void> {
  if (!canStart.value || !file.value) return;
  if (mode.value === "overwrite" && !confirmed) {
    confirming.value = true;
    return;
  }
  confirming.value = false;
  const done = await runner.run(request(), file.value);
  if (done) await router.push({ name: "import-job", params: { tenant: tenant.value, project: projectId.value, job: done.id } });
}
</script>

<template>
  <div class="page stack">
    <div class="page-header">
      <div class="stack-sm">
        <RouterLink :to="{ name: 'files', params: { tenant, project: projectId } }">← {{ w.back }}</RouterLink>
        <h1>{{ w.title }}</h1>
        <p class="muted lead">{{ w.lead }}</p>
      </div>
    </div>
    <p v-if="!canImportAny(grant)" class="alert">{{ s.noImportRights }}</p>

    <form v-else class="stack" :aria-label="w.title" @submit.prevent="start()">
      <fieldset class="card stack step" :disabled="runner.phase.value !== 'idle'">
        <legend><span class="num" aria-hidden="true">1</span> {{ w.stepFile }}</legend>
        <div
          class="drop"
          :class="{ over: dragging }"
          data-testid="drop-zone"
          @dragover.prevent="dragging = true"
          @dragleave="dragging = false"
          @drop.prevent="onDrop"
        >
          <p class="muted">{{ w.drop }}</p>
          <div class="field">
            <label for="imp-file">{{ w.choose }}</label>
            <input id="imp-file" type="file" accept=".xlf,.xliff,.json,.po,.pot,.tmx,.tbx" aria-describedby="imp-file-hint" @change="onPick" />
            <p id="imp-file-hint" class="hint">{{ w.chooseHint }}</p>
          </div>
          <p v-if="file" class="mono" data-testid="chosen-file">{{ w.chosen(file.name, bytes(file.size)) }}</p>
        </div>
        <div v-if="file" class="field narrow">
          <label for="imp-format">{{ w.format }}</label>
          <select id="imp-format" v-model="format" aria-describedby="imp-format-hint">
            <option value="" disabled>—</option>
            <option v-for="f in IMPORT_FORMATS" :key="f" :value="f" :disabled="!allowed.includes(f)">
              {{ s.format[f] }}{{ allowed.includes(f) ? "" : w.needsManage }}
            </option>
          </select>
          <p id="imp-format-hint" class="hint">{{ detected ? w.detected(s.format[detected]!) : w.undetected }}</p>
        </div>
      </fieldset>

      <fieldset v-if="file && format" class="card stack step" :disabled="runner.phase.value !== 'idle'" data-testid="import-options">
        <legend><span class="num" aria-hidden="true">2</span> {{ w.stepOptions }}</legend>

        <template v-if="format === 'xliff'">
          <div v-if="xliffNeedsLocale" class="field narrow">
            <label for="imp-xliff-locale">{{ w.xliffTarget }}</label>
            <select id="imp-xliff-locale" v-model="xliffLocale" aria-describedby="imp-xliff-locale-hint">
              <option value="" disabled>{{ w.chooseLocale }}</option>
              <option v-for="l in targets" :key="l.code" :value="l.code" :disabled="!!localeNote(l.code)">{{ l.code }}{{ localeNote(l.code) }}</option>
            </select>
            <p id="imp-xliff-locale-hint" class="hint" data-testid="xliff-target">{{ xliffHint }}</p>
          </div>
          <p v-else data-testid="xliff-target">{{ w.xliffNoTarget }}</p>
          <label class="check"><input v-model="xliffIcu" type="checkbox" /> {{ w.xliffIcu }}</label>
        </template>

        <template v-if="format === 'json'">
          <div class="field narrow">
            <label for="imp-json-locale">{{ w.jsonLocale }}</label>
            <select id="imp-json-locale" v-model="jsonLocale">
              <option v-for="l in locales" :key="l.code" :value="l.code" :disabled="!!localeNote(l.code)">
                {{ l.code }}{{ l.is_source ? w.sourceCatalog : "" }}{{ localeNote(l.code) }}
              </option>
            </select>
            <p class="hint">{{ w.layout }}</p>
          </div>
          <fieldset class="stack-sm plain">
            <legend class="label">{{ w.syntax }}</legend>
            <label class="check"><input v-model="syntax" type="radio" name="imp-syntax" value="mf1" /> {{ w.syntaxMf1 }}</label>
            <label class="check"><input v-model="syntax" type="radio" name="imp-syntax" value="mf2" /> {{ w.syntaxMf2 }}</label>
          </fieldset>
        </template>

        <template v-if="format === 'po'">
          <div class="field narrow">
            <label for="imp-po-locale">{{ w.poLocale }}</label>
            <select id="imp-po-locale" v-model="poLocale">
              <option value="">{{ w.poFromHeader(poHeader) }}</option>
              <option v-for="l in locales" :key="l.code" :value="l.code" :disabled="l.is_source || !!localeNote(l.code)">
                {{ l.code }}{{ l.is_source ? "" : localeNote(l.code) }}
              </option>
            </select>
          </div>
          <div class="field narrow">
            <label for="imp-plural">{{ w.pluralVariable }}</label>
            <input id="imp-plural" v-model="pluralVariable" placeholder="count" maxlength="64" autocomplete="off" />
          </div>
        </template>

        <template v-if="format === 'json' || format === 'po'">
          <div class="field narrow">
            <label for="imp-ns">{{ w.namespace }}</label>
            <input id="imp-ns" v-model="namespace" placeholder="default" maxlength="64" autocomplete="off" aria-describedby="imp-ns-hint" />
            <p id="imp-ns-hint" class="hint">{{ w.namespaceHint }}</p>
          </div>
          <div v-if="format === 'po' || targetLocale !== source" class="field narrow">
            <label for="imp-state">{{ w.state }}</label>
            <select id="imp-state" v-model="state" aria-describedby="imp-state-hint">
              <option v-for="st in STATES" :key="st" :value="st">{{ s.exportDialog.stateNames[st] }}</option>
            </select>
            <p id="imp-state-hint" class="hint">{{ format === "po" ? w.stateHintPo : w.stateHintJson }}</p>
          </div>
        </template>

        <p v-if="format === 'tmx' || format === 'tbx'" class="hint" data-testid="knowledge-scope">
          {{ w.projectKnowledge(s.kind[format === "tmx" ? "tm" : "termbase"]!) }}
          <RouterLink :to="{ name: 'workspace-knowledge', params: { tenant } }">{{ strings.tenantSettings.knowledgeLink }}</RouterLink>
        </p>
      </fieldset>

      <fieldset v-if="file && format" class="card stack step" :disabled="runner.phase.value !== 'idle'">
        <legend><span class="num" aria-hidden="true">3</span> {{ w.stepMode }}</legend>
        <div class="modes">
          <label v-for="m in MODES" :key="m" class="mode" :class="{ chosen: mode === m, disabled: m === 'overwrite' && !canOverwrite(grant) }">
            <input v-model="mode" type="radio" name="imp-mode" :value="m" :disabled="m === 'overwrite' && !canOverwrite(grant)" :aria-describedby="`imp-mode-${m}`" />
            <span class="stack-sm">
              <strong>{{ s.mode[m] }}</strong>
              <span :id="`imp-mode-${m}`" class="hint">{{ w.modes[m] }}</span>
            </span>
          </label>
        </div>
      </fieldset>

      <p v-if="problem" class="alert alert-warn" role="alert" data-testid="import-problem">{{ problem }}</p>
      <ErrorAlert :error="runner.error.value" />

      <JobProgress
        v-if="runner.phase.value !== 'idle'"
        :text="runner.progressText.value"
        :value="runner.progressValue.value"
        :label="w.progress"
        cancellable
        @cancel="runner.cancel()"
      />
      <div v-else-if="file && format" class="row">
        <button type="submit" class="btn" :class="mode === 'overwrite' ? 'btn-danger' : 'btn-primary'" :disabled="!canStart">{{ w.start[mode] }}</button>
      </div>
    </form>

    <ModalDialog :open="confirming" :title="w.confirmOverwrite" @close="confirming = false">
      <p>{{ w.confirmOverwriteLead(file?.name ?? "") }}</p>
      <template #actions>
        <button type="button" class="btn" @click="confirming = false">{{ strings.app.cancel }}</button>
        <button type="button" class="btn btn-danger" @click="start(true)">{{ w.overwrite }}</button>
      </template>
    </ModalDialog>
  </div>
</template>

<style scoped>
.lead {
  max-inline-size: 48rem;
}
.step {
  margin: 0;
  min-inline-size: 0;
}
.step > legend {
  font-weight: var(--kl-weight-semibold);
  padding-inline: var(--kl-space-1);
  display: inline-flex;
  gap: var(--kl-space-2);
  align-items: center;
}
.num {
  display: inline-grid;
  place-items: center;
  inline-size: 1.5rem;
  block-size: 1.5rem;
  border-radius: var(--kl-radius-full);
  background: var(--kl-accent-dim);
  color: var(--gs-accent-ink);
  font-size: var(--kl-text-sm);
}
.plain {
  border: 0;
  margin: 0;
  padding: 0;
}
.narrow {
  max-inline-size: 28rem;
}
.drop {
  border: 2px dashed var(--gs-control-border);
  border-radius: var(--kl-radius-lg);
  padding: var(--kl-space-5);
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-3);
}
.drop.over {
  border-color: var(--kl-accent);
  background: var(--kl-accent-dim);
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
</style>
