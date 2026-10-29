<script setup lang="ts">
/**
 * The workflow editor (RFC 0006 §8, §14 decision 12): a document editor
 * with a rendered chart, not a visual builder. The text is the API's
 * document, exactly — what `glossa workflow push` sends — and the chart
 * beside it is drawn from that text as the author types.
 *
 * Three promises:
 *
 * 1. **The server decides validity.** A moment after the author stops
 *    typing, the document goes to the lint endpoint, which runs the same
 *    compile and lint a save runs. Its findings are placed on the lines
 *    they are about (a gutter mark, and a "go to line" in the list). A
 *    save that is refused anyway shows the save's own findings the same
 *    way; nothing is stored.
 * 2. **Versions are immutable.** A save is version n+1, sent with the
 *    version the author edited as `If-Match`. When another save overtook
 *    it, the author's text stays in the editor and they choose: load the
 *    newer version, or save theirs over it knowingly.
 * 3. **Old versions are readable, never editable.** Viewing one is
 *    read-only; "edit from" copies it into the editor, and saving it
 *    makes a new version.
 */
import { computed, nextTick, onBeforeUnmount, ref, shallowRef, useTemplateRef, watch } from "vue";
import { RouterLink, useRoute, useRouter } from "vue-router";
import { projects as projectsApi } from "../../api/endpoints";
import { isApiError } from "../../api/errors";
import type { Project } from "../../api/schemas";
import { useWorkflows, WorkflowRejected } from "../../api/workflows";
import type { WorkflowDefinition, WorkflowDefinitionVersion, WorkflowDocument, WorkflowFinding } from "../../api/workflows-schemas";
import ErrorAlert from "../../components/ErrorAlert.vue";
import StateChart from "../../components/workflow/StateChart.vue";
import { absoluteTime, relativeTime } from "../../lib/time";
import { formatDocument, keyLines, parseDocument, refuses, sortFindings, starterDocument } from "../../lib/workflow";
import { allows } from "../../session/permissions";
import { usePeople } from "../../session/people";
import { useGrant } from "../../session/session";
import { problemText, strings } from "../../strings";

const props = withDefaults(defineProps<{ lintDelay?: number; chartDelay?: number }>(), { lintDelay: 800, chartDelay: 400 });
const s = strings.workflows;
const route = useRoute();
const router = useRouter();
const port = useWorkflows();
const tenant = computed(() => String(route.params.tenant));
const definitionId = computed(() => (typeof route.params.definition === "string" ? route.params.definition : undefined));
const creating = computed(() => !definitionId.value);
const grant = useGrant(() => tenant.value);
const canManage = computed(() => allows(grant.value, "workflows.manage"));
const person = usePeople(() => tenant.value);
const area = useTemplateRef<HTMLTextAreaElement>("area");
const gutter = useTemplateRef<HTMLElement>("gutter");
const findingsHeading = useTemplateRef<HTMLHeadingElement>("findingsHeading");

// ── what the screen holds ──────────────────────────────────────────
const definition = shallowRef<WorkflowDefinition>();
const etag = ref<string>();
const versions = shallowRef<WorkflowDefinitionVersion[]>([]);
const versionsError = ref<unknown>(null);
const phase = ref<"loading" | "ready" | "failed">("loading");
const loadError = ref<unknown>(null);
const projects = shallowRef<Project[]>([]);
const scope = ref("");
const subject = ref<"translation" | "release_request">("translation");
/** The text in the editor, and the text it was loaded with (to know what changed). */
const text = ref("");
const baseline = ref("");
const status = ref("");
const saveError = ref<unknown>(null);
const overtaken = ref(false);
const busy = ref(false);

/** The version being read, from `?version=`; undefined is the latest (the editable one). */
const viewing = computed(() => {
  const v = Number(route.query.version);
  return Number.isInteger(v) && v > 0 && definition.value && v !== definition.value.version ? v : undefined;
});
const viewed = computed(() => (viewing.value ? versions.value.find((v) => v.version === viewing.value) : undefined));
const readOnly = computed(() => !canManage.value || viewing.value !== undefined);
const shown = computed(() => (viewed.value ? formatDocument(viewed.value.document) : text.value));

let loadAbort: AbortController | undefined;
async function load(): Promise<void> {
  loadAbort?.abort();
  const a = (loadAbort = new AbortController());
  phase.value = "loading";
  loadError.value = null;
  saveError.value = null;
  overtaken.value = false;
  findings.value = undefined;
  if (creating.value) {
    definition.value = undefined;
    etag.value = undefined;
    versions.value = [];
    text.value = baseline.value = formatDocument(starterDocument("my-workflow", subject.value));
    phase.value = "ready";
    void projectsApi.list(tenant.value).then((p) => (projects.value = p), () => undefined);
    return;
  }
  try {
    const id = definitionId.value!;
    const [d, vs] = await Promise.all([port.definition(tenant.value, id, a.signal), port.versions(tenant.value, id, a.signal)]);
    if (a.signal.aborted) return;
    definition.value = d.value;
    etag.value = d.etag;
    versions.value = vs;
    const latest = vs.find((v) => v.version === d.value.version) ?? (await port.version(tenant.value, id, d.value.version, a.signal));
    if (a.signal.aborted) return;
    text.value = baseline.value = formatDocument(latest.document);
    phase.value = "ready";
  } catch (e) {
    if (a.signal.aborted) return;
    loadError.value = e;
    phase.value = "failed";
  }
}

// ── the document as typed ──────────────────────────────────────────
const parsed = computed(() => parseDocument(text.value));
const lastGood = shallowRef<WorkflowDocument>();
watch(
  parsed,
  (p) => {
    if (p.ok) lastGood.value = p.doc;
  },
  { immediate: true },
);
const chartDoc = computed(() => (viewed.value ? viewed.value.document : lastGood.value));
const dirty = computed(() => !viewing.value && text.value !== baseline.value);
const lineCount = computed(() => shown.value.split("\n").length);
const renamed = computed(() => {
  const d = definition.value;
  const p = parsed.value;
  return d && p.ok && typeof p.doc.name === "string" && p.doc.name !== d.name ? { from: d.name, to: p.doc.name } : undefined;
});
const parseMessage = computed(() => {
  const p = parsed.value;
  if (p.ok) return "";
  return p.message === "not-an-object" ? s.notObject : s.notJson(p.line);
});

// ── lint, a moment after typing stops ──────────────────────────────
interface Checked {
  forText: string;
  valid: boolean;
  findings: WorkflowFinding[];
  /** From a refused save rather than a lint. */
  fromSave?: boolean;
}
const findings = shallowRef<Checked>();
const checking = ref(false);
const checkError = ref<unknown>(null);
let lintTimer: ReturnType<typeof setTimeout> | undefined;
let lintAbort: AbortController | undefined;

async function check(): Promise<void> {
  clearTimeout(lintTimer);
  const p = parsed.value;
  if (!p.ok || readOnly.value) return;
  lintAbort?.abort();
  const a = (lintAbort = new AbortController());
  const forText = text.value;
  checking.value = true;
  checkError.value = null;
  try {
    const r = await port.lint(tenant.value, p.doc, a.signal);
    if (a.signal.aborted) return;
    findings.value = { forText, valid: r.valid, findings: r.findings };
  } catch (e) {
    if (!a.signal.aborted) checkError.value = e;
  } finally {
    if (!a.signal.aborted) checking.value = false;
  }
}
watch(text, () => {
  clearTimeout(lintTimer);
  if (phase.value !== "ready" || readOnly.value || !parsed.value.ok) return;
  lintTimer = setTimeout(() => void check(), props.lintDelay);
});
onBeforeUnmount(() => {
  clearTimeout(lintTimer);
  lintAbort?.abort();
  loadAbort?.abort();
});

const lines = computed(() => keyLines(shown.value));
const placed = computed(() => (findings.value ? sortFindings(findings.value.findings, lines.value) : []));
const staleCheck = computed(() => !!findings.value && findings.value.forText !== text.value);
const marks = computed(() => {
  const m = new Map<number, WorkflowFinding["severity"]>();
  if (staleCheck.value || viewing.value) return m;
  for (const f of placed.value) if (f.line !== undefined && !m.has(f.line)) m.set(f.line, f.severity);
  return m;
});
const notes = computed(() => placed.value.filter((f) => f.severity === "info").length);
const summary = computed(() => {
  const c = findings.value;
  if (!c) return s.findingsIdle;
  if (!c.valid || refuses(c.findings)) return s.findingsInvalid(c.findings.filter((f) => f.severity !== "info").length);
  return notes.value ? s.findingsValidNotes(notes.value) : s.findingsValid;
});

function goToLine(n: number): void {
  const el = area.value;
  if (!el) return;
  const all = el.value.split("\n");
  const start = all.slice(0, n - 1).reduce((sum, l) => sum + l.length + 1, 0);
  el.focus();
  el.setSelectionRange(start, start + (all[n - 1]?.length ?? 0));
  const lh = parseFloat(getComputedStyle(el).lineHeight) || 20;
  el.scrollTop = Math.max(0, (n - 3) * lh);
  syncGutter();
}
function syncGutter(): void {
  if (gutter.value && area.value) gutter.value.scrollTop = area.value.scrollTop;
}

// ── save ───────────────────────────────────────────────────────────
const canSave = computed(() => !readOnly.value && parsed.value.ok && !busy.value && (creating.value || dirty.value) && !renamed.value);
const saveLabel = computed(() => (busy.value ? s.saving : creating.value ? s.saveFirst : s.saveNext((definition.value?.version ?? 0) + 1)));

async function save(): Promise<void> {
  const p = parsed.value;
  if (!p.ok || !canSave.value) return;
  busy.value = true;
  saveError.value = null;
  overtaken.value = false;
  status.value = "";
  const forText = text.value;
  try {
    if (creating.value) {
      const r = await port.create(tenant.value, p.doc, scope.value || undefined);
      baseline.value = forText;
      findings.value = { forText, valid: true, findings: r.value.findings };
      status.value = s.createdStatus(r.value.name);
      await router.replace({ name: "workflow", params: { tenant: tenant.value, definition: r.value.id } });
      return;
    }
    const id = definitionId.value!;
    const r = await port.save(tenant.value, id, p.doc, etag.value ?? "");
    etag.value = r.etag;
    definition.value = { ...definition.value!, version: r.value.version };
    baseline.value = forText;
    findings.value = { forText, valid: true, findings: r.value.findings };
    status.value = s.savedStatus(r.value.name, r.value.version);
    try {
      versions.value = await port.versions(tenant.value, id);
      versionsError.value = null;
    } catch (e) {
      versionsError.value = e;
    }
  } catch (e) {
    if (e instanceof WorkflowRejected) {
      findings.value = { forText, valid: false, findings: e.workflowFindings, fromSave: true };
      status.value = s.rejected(e.workflowFindings.length);
      await nextTick();
      findingsHeading.value?.focus();
    } else if (isApiError(e, "precondition_failed")) {
      overtaken.value = true;
    } else {
      saveError.value = e;
    }
  } finally {
    busy.value = false;
  }
}

/** After an overtaken save: discard mine and read the latest. */
async function loadTheirs(): Promise<void> {
  await load();
  await nextTick();
  area.value?.focus();
}
/** After an overtaken save: take the newer version's tag and save my text over it, knowingly. */
async function keepMine(): Promise<void> {
  const id = definitionId.value;
  if (!id) return;
  try {
    const d = await port.definition(tenant.value, id);
    definition.value = d.value;
    etag.value = d.etag;
    overtaken.value = false;
    await save();
  } catch (e) {
    saveError.value = e;
  }
}

/** Copy an older version into the editor; saving it makes a new version. */
async function restore(): Promise<void> {
  const v = viewed.value;
  if (!v) return;
  text.value = formatDocument(v.document);
  await router.replace({ query: {} });
  status.value = s.restored(v.version);
  await nextTick();
  area.value?.focus();
}

// Registered last: loading resets state declared above.
watch([tenant, definitionId], () => void load(), { immediate: true });

/** A different starter for a release-request workflow, while the author hasn't started writing. */
watch(subject, (sub) => {
  if (creating.value && text.value === baseline.value) text.value = baseline.value = formatDocument(starterDocument("my-workflow", sub));
});

const title = computed(() => (creating.value ? s.newTitle : s.editTitle(definition.value?.name ?? "…")));
</script>

<template>
  <div class="page stack editor-page">
    <nav :aria-label="strings.nav.breadcrumb" class="crumbs">
      <RouterLink :to="{ name: 'workflows', params: { tenant } }">{{ s.allWorkflows }}</RouterLink>
    </nav>
    <div class="page-header">
      <div class="stack-sm">
        <h1>{{ title }}</h1>
        <p class="muted lead">{{ s.editorLead }}</p>
      </div>
    </div>
    <p role="status" aria-live="polite" data-testid="editor-status">{{ status }}</p>

    <p v-if="phase === 'loading'" class="muted" data-testid="editor-loading">{{ strings.app.loading }}</p>
    <div v-else-if="phase === 'failed'" class="alert alert-error stack-sm" role="alert" data-testid="editor-failed">
      <p class="alert-title">{{ s.loadFailed }}</p>
      <p>{{ problemText(loadError) }}</p>
      <div><button type="button" class="btn btn-sm" @click="load">{{ s.retry }}</button></div>
    </div>
    <template v-else>
      <p v-if="!canManage" class="alert" data-testid="editor-read-only">{{ s.readOnlyEditor }}</p>
      <div v-if="viewing && definition" class="alert stack-sm" data-testid="editor-viewing">
        <p>{{ s.viewingVersion(viewing, definition.version) }}</p>
        <div class="row">
          <RouterLink class="btn btn-sm" :to="{ query: {} }">{{ s.backToLatest }}</RouterLink>
          <button v-if="canManage && viewed" type="button" class="btn btn-sm" :aria-describedby="'restore-hint'" data-testid="editor-restore" @click="restore">{{ s.restore(viewing) }}</button>
          <span id="restore-hint" class="hint">{{ s.restoreHint }}</span>
        </div>
      </div>

      <div v-if="creating" class="row fields">
        <div class="field">
          <label for="wf-subject">{{ s.subjectLabel }}</label>
          <select id="wf-subject" v-model="subject" aria-describedby="wf-subject-hint" :disabled="busy">
            <option value="translation">{{ s.subject.translation }}</option>
            <option value="release_request">{{ s.subject.release_request }}</option>
          </select>
          <span id="wf-subject-hint" class="hint">{{ s.subjectHint }}</span>
        </div>
        <div class="field">
          <label for="wf-scope">{{ s.scope }}</label>
          <select id="wf-scope" v-model="scope" aria-describedby="wf-scope-hint" :disabled="busy" data-testid="editor-scope">
            <option value="">{{ s.everyProject }}</option>
            <option v-for="p in projects" :key="p.id" :value="p.id">{{ s.onlyProject(p.name) }}</option>
          </select>
          <span id="wf-scope-hint" class="hint">{{ s.scopeHint }}</span>
        </div>
      </div>

      <div class="workbench">
        <section class="stack-sm doc" aria-labelledby="wf-doc-label">
          <div class="row">
            <label id="wf-doc-label" for="wf-doc" class="label">{{ s.documentLabel }}</label>
            <span class="spacer" />
            <span class="hint">{{ s.lines(lineCount) }}</span>
            <span v-if="dirty" class="pill pill-warn" data-testid="editor-dirty">{{ s.unsaved }}</span>
          </div>
          <div class="code" :class="{ readonly: readOnly }">
            <div ref="gutter" class="gutter" aria-hidden="true">
              <span v-for="n in lineCount" :key="n" class="ln" :class="marks.get(n) ? `mark-${marks.get(n)}` : ''" :data-line="n" :title="marks.get(n) ? s.gutterMark(n, s.severity[marks.get(n)!] ?? '') : undefined">{{ n }}</span>
            </div>
            <textarea
              id="wf-doc"
              ref="area"
              :value="shown"
              class="mono"
              wrap="off"
              spellcheck="false"
              autocapitalize="off"
              autocomplete="off"
              :readonly="readOnly"
              :aria-invalid="!parsed.ok || (!!findings && !staleCheck && refuses(findings.findings)) ? 'true' : undefined"
              aria-describedby="wf-doc-hint wf-doc-parse wf-findings-summary"
              data-testid="editor-text"
              @input="text = ($event.target as HTMLTextAreaElement).value"
              @scroll="syncGutter"
            />
          </div>
          <span id="wf-doc-hint" class="hint">{{ s.documentHint }}</span>
          <p id="wf-doc-parse" class="field-error" :role="parseMessage ? 'alert' : undefined" data-testid="editor-parse">{{ viewing ? "" : parseMessage }}</p>
          <p v-if="renamed" class="alert alert-warn" data-testid="editor-renamed">{{ s.renamed(renamed.from, renamed.to) }}</p>

          <section v-if="!viewing" class="stack-sm findings" aria-labelledby="wf-findings-h" data-testid="editor-findings">
            <div class="row">
              <h2 id="wf-findings-h" ref="findingsHeading" tabindex="-1" class="label">{{ s.findingsTitle }}</h2>
              <span class="spacer" />
              <button v-if="!readOnly" type="button" class="btn btn-sm" :disabled="checking || !parsed.ok" data-testid="editor-check" @click="check">{{ checking ? s.checking : s.check }}</button>
            </div>
            <p id="wf-findings-summary" :class="findings && (!findings.valid || refuses(findings.findings)) ? 'field-error' : 'hint'" data-testid="editor-findings-summary">
              {{ summary }}<template v-if="staleCheck"> {{ s.findingsStale }}</template>
            </p>
            <p v-if="checkError" class="alert alert-warn" data-testid="editor-check-failed">{{ s.checkFailed }} {{ problemText(checkError) }}</p>
            <ol v-if="placed.length" class="finding-list" :class="{ stale: staleCheck }">
              <li v-for="(f, i) in placed" :key="i" class="finding" :data-severity="f.severity" data-testid="editor-finding">
                <span class="pill" :class="f.severity === 'error' ? 'pill-err' : f.severity === 'warning' ? 'pill-warn' : 'pill-neutral'">{{ s.severity[f.severity] }}</span>
                <span class="message">{{ f.message }}</span>
                <span class="hint mono">{{ f.rule }}<template v-if="f.path"> · {{ f.path }}</template></span>
                <button v-if="f.line !== undefined" type="button" class="btn btn-sm btn-ghost" data-testid="editor-goto" @click="goToLine(f.line)">{{ s.goToLine(f.line) }}</button>
                <span v-else class="hint">{{ s.wholeDocument }}</span>
              </li>
            </ol>
          </section>

          <div v-if="overtaken" class="alert alert-warn stack-sm" role="alert" data-testid="editor-overtaken">
            <p>{{ s.overtaken }}</p>
            <div class="row">
              <button type="button" class="btn btn-sm" @click="loadTheirs">{{ s.loadTheirs }}</button>
              <button type="button" class="btn btn-sm" data-testid="editor-keep-mine" @click="keepMine">{{ s.keepMine }}</button>
            </div>
          </div>
          <ErrorAlert :error="saveError" />
          <div v-if="!readOnly" class="row">
            <button type="button" class="btn btn-primary" :disabled="!canSave" data-testid="editor-save" @click="save">{{ saveLabel }}</button>
            <span v-if="!creating && !dirty" class="hint">{{ s.saved }}</span>
          </div>
        </section>

        <aside class="stack side">
          <StateChart :doc="chartDoc" :stale="!viewing && !parsed.ok" :delay="chartDelay" />

          <section v-if="!creating" class="stack-sm" aria-labelledby="wf-versions-h" data-testid="editor-versions">
            <h2 id="wf-versions-h" class="label">{{ s.versionsTitle }}</h2>
            <p class="hint">{{ s.versionsLead }}</p>
            <p v-if="versionsError" class="alert alert-warn">{{ s.versionsFailed }} {{ problemText(versionsError) }}</p>
            <ol class="versions">
              <li v-for="v in versions" :key="v.version" :data-version="v.version" :aria-current="(viewing ?? definition?.version) === v.version ? 'true' : undefined">
                <span class="v">{{ s.versionRow(v.version) }}</span>
                <span v-if="v.version === definition?.version" class="pill pill-accent">{{ s.latest }}</span>
                <span class="hint">
                  {{ s.savedBy(person(v.created_by), relativeTime(v.created_at)) }}
                  <time class="visually-hidden" :datetime="v.created_at">{{ absoluteTime(v.created_at) }}</time>
                </span>
                <RouterLink
                  v-if="(viewing ?? definition?.version) !== v.version"
                  class="btn btn-sm btn-ghost"
                  :to="{ query: v.version === definition?.version ? {} : { version: String(v.version) } }"
                  data-testid="editor-view-version"
                >
                  {{ s.view }}<span class="visually-hidden">{{ s.viewOf(v.version) }}</span>
                </RouterLink>
              </li>
            </ol>
          </section>
        </aside>
      </div>
    </template>
  </div>
</template>

<style scoped>
.lead {
  max-inline-size: 52rem;
}
.crumbs a {
  color: var(--kl-ink-secondary);
}
.fields {
  align-items: flex-start;
}
.fields .field {
  flex: 1 1 16rem;
  max-inline-size: 24rem;
}
.workbench {
  display: grid;
  grid-template-columns: minmax(0, 3fr) minmax(0, 2fr);
  gap: var(--kl-space-5);
  align-items: start;
}
@media (max-width: 64rem) {
  .workbench {
    grid-template-columns: minmax(0, 1fr);
  }
}
.code {
  --line: 1.5;
  display: flex;
  border: 1px solid var(--kl-border);
  border-radius: var(--kl-radius-md);
  background: var(--kl-surface-raised);
  overflow: hidden;
}
.code:focus-within {
  outline: 2px solid var(--kl-accent);
  outline-offset: 1px;
}
.code.readonly {
  background: var(--kl-surface);
}
.gutter {
  display: flex;
  flex-direction: column;
  padding-block: var(--kl-space-2);
  padding-inline: var(--kl-space-2);
  overflow: hidden;
  block-size: 36rem;
  font-family: var(--kl-font-mono);
  font-size: var(--kl-text-sm);
  line-height: var(--line);
  color: var(--kl-ink-tertiary, var(--kl-ink-secondary));
  text-align: end;
  user-select: none;
  border-inline-end: 1px solid var(--kl-border);
  min-inline-size: 3.5rem;
}
.ln {
  display: block;
  padding-inline-end: var(--kl-space-1);
  border-inline-start: 3px solid transparent;
}
.mark-error {
  color: var(--kl-err, #b42318);
  border-inline-start-color: var(--kl-err, #b42318);
  font-weight: var(--kl-weight-semibold);
}
.mark-warning {
  color: var(--kl-warn, #b54708);
  border-inline-start-color: var(--kl-warn, #b54708);
}
.mark-info {
  border-inline-start-color: var(--kl-border-strong, var(--kl-border));
}
textarea {
  flex: 1;
  block-size: 36rem;
  margin: 0;
  padding: var(--kl-space-2) var(--kl-space-3);
  border: none;
  border-radius: 0;
  resize: vertical;
  font-size: var(--kl-text-sm);
  line-height: var(--line);
  white-space: pre;
  overflow: auto;
  background: transparent;
  tab-size: 2;
}
textarea:focus {
  outline: none;
}
.finding-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-2);
}
.finding-list.stale {
  opacity: 0.6;
}
.finding {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--kl-space-2);
}
.message {
  flex: 1 1 16rem;
}
.versions {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-1);
}
.versions li {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--kl-space-2);
  padding: var(--kl-space-1) var(--kl-space-2);
  border-radius: var(--kl-radius-md);
}
.versions li[aria-current="true"] {
  background: var(--kl-accent-dim);
}
.v {
  font-weight: var(--kl-weight-medium);
}
</style>
