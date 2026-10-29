<script setup lang="ts">
/**
 * The review queue (intent §24, RFC 0003 §3.3): pending AI suggestions
 * across messages and the member's locales, riskiest first, triaged from
 * the keyboard — a accept, e edit, r reject, j/k move. Batch accept only
 * takes suggestions the routing policy recommends approving.
 *
 * Where a workflow is bound (RFC 0006 §3.1), an "assigned to me" filter
 * narrows the queue to the units of my live assignments. Intelligence's
 * queue stays the source; the assignments only say which of it is mine.
 */
import { computed, nextTick, ref, shallowRef, useTemplateRef, watch } from "vue";
import { RouterLink, useRoute, useRouter } from "vue-router";
import { isApiError } from "../../api/errors";
import type { AISuggestion } from "../../api/intelligence-schemas";
import { useIntelligence } from "../../api/intelligence";
import { useWork } from "../../api/work";
import SuggestionCard from "../../components/assist/SuggestionCard.vue";
import ErrorAlert from "../../components/ErrorAlert.vue";
import ModalDialog from "../../components/ModalDialog.vue";
import { actionTone, bandOf, bandTone, batchAcceptable, formatScore } from "../../lib/confidence";
import { ariaKeys, keyLabel, useShortcuts } from "../../lib/shortcuts";
import { liveUnits, unitKey } from "../../lib/work";
import { allowsFor } from "../../session/permissions";
import { problemText, strings } from "../../strings";
import { useProject } from "./context";

const route = useRoute();
const router = useRouter();
const { tenant, projectId, locales, targets, grant } = useProject();
const s = strings.review;
const ai = strings.ai;
const port = useIntelligence();
const ref_ = () => ({ tenant: tenant.value, project: projectId.value });

/** The target locales this member may decide suggestions in. */
const myLocales = computed(() => targets.value.filter((l) => allowsFor(grant.value, "intelligence.translate", l.code)));
const localeFilter = computed(() => {
  const want = typeof route.query.locale === "string" ? route.query.locale : "";
  return myLocales.value.some((l) => l.code === want) ? want : "";
});
/** Unscoped members see the whole queue; scoped ones, their locales (the server takes at most 20). */
const queryLocales = computed(() => {
  if (localeFilter.value) return [localeFilter.value];
  if (myLocales.value.length === targets.value.length) return [];
  return myLocales.value.slice(0, 20).map((l) => l.code);
});
const localeOf = (code: string) => locales.value.find((l) => l.code === code);

const items = shallowRef<AISuggestion[]>([]);
const next = ref<string>();
const loading = ref(false);
const error = ref<unknown>(null);
const status = ref("");
const active = ref(0);
const busy = ref(false);
const editing = ref(false);
const editText = ref("");
const editArea = useTemplateRef<HTMLTextAreaElement>("editArea");
const listEl = useTemplateRef<HTMLUListElement>("listEl");

async function load(more = false): Promise<void> {
  loading.value = true;
  error.value = null;
  try {
    const page = await port.reviewQueue(ref_(), queryLocales.value, more ? next.value : undefined);
    items.value = more ? [...items.value, ...page.items] : page.items;
    next.value = page.next;
    if (!more) active.value = 0;
  } catch (e) {
    error.value = e;
  } finally {
    loading.value = false;
  }
}
watch(queryLocales, () => void load(), { immediate: true });

// ── "assigned to me" (RFC 0006 §3.1) ──────────────────────────────────
const work = useWork();
/** Whether a workflow applies to translations in any of my locales; the filter exists only then. */
const workflow = ref<"checking" | "bound" | "unbound" | "failed">("checking");
watch(
  () => [projectId.value, myLocales.value.map((l) => l.code).join(" ")] as const,
  async () => {
    workflow.value = "checking";
    const codes = myLocales.value.map((l) => l.code);
    try {
      const answers = await Promise.all((codes.length ? codes : [undefined]).map((c) => work.resolveWorkflow(ref_(), c)));
      workflow.value = answers.some((r) => r.bound) ? "bound" : "unbound";
    } catch {
      workflow.value = "failed";
    }
  },
  { immediate: true },
);
const mineOn = computed(() => workflow.value === "bound" && route.query.mine === "1");
type Mine = { phase: "off" } | { phase: "loading" } | { phase: "ready"; units: Set<string> } | { phase: "failed"; error: unknown };
const mine = shallowRef<Mine>({ phase: "off" });
watch(
  mineOn,
  async (on) => {
    if (!on) {
      mine.value = { phase: "off" };
      return;
    }
    mine.value = { phase: "loading" };
    try {
      mine.value = { phase: "ready", units: liveUnits(await work.myAssignments(tenant.value, { project: projectId.value })) };
    } catch (error) {
      mine.value = { phase: "failed", error };
    }
    active.value = 0;
  },
  { immediate: true },
);
/** What the list shows: the queue, or — filtered — only my units of it. Unknown assignments show nothing, never everything. */
const shown = computed(() => {
  if (!mineOn.value) return items.value;
  const m = mine.value;
  if (m.phase !== "ready") return [];
  return items.value.filter((sg) => m.units.has(unitKey(sg.message_id, sg.locale)));
});
function setMine(on: boolean): void {
  void router.replace({ query: { ...route.query, mine: on ? "1" : undefined } });
}

const current = computed(() => shown.value[active.value]);
const canDecide = (sg: AISuggestion | undefined) =>
  !!sg && allowsFor(grant.value, "intelligence.translate", sg.locale) && allowsFor(grant.value, "translations.write", sg.locale);

// Moving on leaves an edit behind. Each item carries its message's
// current source (read by the server in one query per page), so focusing
// one needs no request.
watch(
  () => current.value?.id,
  () => (editing.value = false),
);
const source = computed(() => locales.value.find((l) => l.is_source));
/** The message's key now: a rename since the suggestion shows here. */
const keyOf = (sg: AISuggestion) => sg.source?.message_key ?? sg.message_key;
/** The source moved on since the suggestion was made against it. */
const sourceChanged = (sg: AISuggestion) => !!sg.source && sg.source.source_revision > sg.source_revision;

function move(by: number): void {
  if (!shown.value.length) return;
  active.value = Math.max(0, Math.min(shown.value.length - 1, active.value + by));
  void nextTick(() => listEl.value?.querySelector<HTMLElement>(`[data-index="${active.value}"]`)?.scrollIntoView?.({ block: "nearest" }));
}

function removeDecided(id: string): void {
  const i = shown.value.findIndex((x) => x.id === id);
  if (i < 0) return;
  items.value = items.value.filter((x) => x.id !== id);
  active.value = Math.min(i, Math.max(0, shown.value.length - 1));
}

async function decide(sg: AISuggestion, run: () => Promise<AISuggestion>, done: string): Promise<void> {
  if (busy.value || !canDecide(sg)) return;
  busy.value = true;
  error.value = null;
  try {
    await run();
    removeDecided(sg.id);
    editing.value = false;
    status.value = done;
    listEl.value?.focus();
  } catch (e) {
    if (isApiError(e, "suggestion_decided") || isApiError(e, "suggestion_outdated")) {
      removeDecided(sg.id);
      status.value = problemText(e);
    } else error.value = e;
  } finally {
    busy.value = false;
  }
}

function accept(): void {
  const sg = current.value;
  if (sg) void decide(sg, () => port.accept(tenant.value, sg.id), s.accepted(sg.message_key, sg.locale));
}
function acceptEdit(): void {
  const sg = current.value;
  if (sg && editText.value.trim()) void decide(sg, () => port.accept(tenant.value, sg.id, { text: editText.value, syntax: "mf2" }), s.acceptedEdit(sg.message_key, sg.locale));
}
function reject(): void {
  const sg = current.value;
  if (sg) void decide(sg, () => port.reject(tenant.value, sg.id), s.rejected(sg.message_key, sg.locale));
}
async function startEdit(): Promise<void> {
  const sg = current.value;
  if (!sg || !canDecide(sg)) return;
  editText.value = sg.message;
  editing.value = true;
  await nextTick();
  editArea.value?.focus();
}
function cancelEdit(): void {
  editing.value = false;
  listEl.value?.focus();
}

useShortcuts({
  queueNext: () => move(1),
  queuePrev: () => move(-1),
  queueAccept: () => accept(),
  queueEdit: () => void startEdit(),
  queueReject: () => reject(),
  queueAcceptEdit: () => {
    if (!editing.value) return false;
    acceptEdit();
  },
  queueCancelEdit: () => {
    if (!editing.value) return false;
    cancelEdit();
  },
});

function onListKey(e: KeyboardEvent): void {
  if (e.key === "ArrowDown") move(1);
  else if (e.key === "ArrowUp") move(-1);
  else if (e.key === "Home") active.value = 0;
  else if (e.key === "End") active.value = Math.max(0, shown.value.length - 1);
  else return;
  e.preventDefault();
}

// ── batch accept ────────────────────────────────────────────────────
const recommended = computed(() => shown.value.filter((x) => batchAcceptable(x) && canDecide(x)));
const batchOpen = ref(false);
async function acceptRecommended(): Promise<void> {
  busy.value = true;
  let ok = 0;
  let failed = 0;
  for (const sg of recommended.value) {
    try {
      await port.accept(tenant.value, sg.id);
      removeDecided(sg.id);
      ok++;
    } catch {
      failed++;
    }
  }
  busy.value = false;
  batchOpen.value = false;
  status.value = s.batchDone(ok, failed);
}

function setLocale(code: string): void {
  void router.replace({ query: { ...route.query, locale: code || undefined } });
}
</script>

<template>
  <div class="page stack review">
    <div class="page-header">
      <div class="stack-sm">
        <h1>{{ s.title }}</h1>
        <p class="muted lead">{{ s.lead }}</p>
      </div>
      <div class="row">
        <div class="field">
          <label for="rq-locale">{{ s.locale }}</label>
          <select id="rq-locale" :value="localeFilter" @change="setLocale(($event.target as HTMLSelectElement).value)">
            <option value="">{{ s.allLocales }}</option>
            <option v-for="l in myLocales" :key="l.code" :value="l.code">{{ l.code }}</option>
          </select>
        </div>
        <div v-if="workflow === 'bound'" class="field mine">
          <label class="check" for="rq-mine">
            <input id="rq-mine" type="checkbox" :checked="mineOn" aria-describedby="rq-mine-hint" data-testid="review-mine" @change="setMine(($event.target as HTMLInputElement).checked)" />
            {{ strings.work.assignedToMe }}
          </label>
          <span id="rq-mine-hint" class="hint">{{ strings.work.assignedHint }}</span>
        </div>
        <button type="button" class="btn" :disabled="!recommended.length || busy" data-testid="batch-accept" @click="batchOpen = true">{{ s.batch(recommended.length) }}</button>
      </div>
    </div>
    <ErrorAlert :error="error" />
    <p v-if="workflow === 'failed'" class="hint" data-testid="review-workflow-unknown">{{ strings.work.resolutionFailed }}</p>
    <p class="muted" role="status" data-testid="review-status">{{ status }}</p>

    <p v-if="loading && !items.length" class="muted">{{ strings.app.loading }}</p>
    <p v-else-if="mine.phase === 'loading'" class="muted" data-testid="review-mine-loading">{{ strings.work.assignedLoading }}</p>
    <div v-else-if="mine.phase === 'failed'" class="alert alert-error" role="alert" data-testid="review-mine-failed">
      <p>{{ strings.work.assignedFailed }}</p>
      <p>{{ problemText(mine.error) }}</p>
    </div>
    <p v-else-if="mineOn && !shown.length" class="card muted" data-testid="review-mine-empty">{{ strings.work.assignedEmpty }}</p>
    <p v-else-if="!items.length" class="card muted" data-testid="review-empty">{{ s.empty }}</p>
    <div v-else class="queue">
      <div class="stack-sm">
        <p class="hint">{{ s.count(shown.length, !!next) }} · <span aria-hidden="true">{{ s.keys }}</span></p>
        <ul
          ref="listEl"
          class="items"
          role="listbox"
          tabindex="0"
          :aria-label="s.list"
          :aria-activedescendant="current ? `rq-${current.id}` : undefined"
          data-testid="review-list"
          @keydown="onListKey"
        >
          <li
            v-for="(sg, i) in shown"
            :id="`rq-${sg.id}`"
            :key="sg.id"
            role="option"
            :aria-selected="i === active"
            :data-index="i"
            class="item"
            @click="active = i"
          >
            <div class="row">
              <span class="mono key">{{ sg.message_key }}</span>
              <span class="pill pill-neutral">{{ sg.locale }}</span>
            </div>
            <div class="row">
              <span class="pill" :class="`pill-${bandTone(bandOf(sg.score))}`">{{ ai.band[bandOf(sg.score)] }} · {{ formatScore(sg.score) }}</span>
              <span class="pill" :class="`pill-${actionTone(sg.action)}`">{{ ai.action[sg.action] }}</span>
              <span v-for="t in sg.risk_tags" :key="t" class="pill pill-warn">{{ ai.riskTag(t) }}</span>
            </div>
            <p class="preview" :lang="sg.locale">{{ sg.message }}</p>
          </li>
        </ul>
        <button v-if="next" type="button" class="btn btn-sm" :disabled="loading" @click="load(true)">{{ s.loadMore }}</button>
      </div>

      <section v-if="current" class="detail card stack" :aria-labelledby="`rq-h-${current.id}`" data-testid="review-detail">
        <div class="row">
          <h2 :id="`rq-h-${current.id}`" class="mono">{{ current.message_key }}</h2>
          <span class="pill pill-neutral">{{ current.locale }}</span>
          <span class="spacer" />
          <RouterLink :to="{ name: 'translate', params: { tenant, project: projectId }, query: { key: keyOf(current), locale: current.locale } }">{{ s.openInEditor }}</RouterLink>
        </div>
        <div v-if="current.source" class="stack-sm">
          <h3>{{ s.source }} <span class="muted mono">{{ source?.code }}</span></h3>
          <p v-if="current.source.message_key !== current.message_key" class="hint">{{ s.renamed(current.source.message_key) }}</p>
          <p class="source" :lang="source?.code" :dir="source?.direction" data-testid="review-source">{{ current.source.text }}</p>
          <p v-if="sourceChanged(current)" class="alert alert-warn" data-testid="review-source-changed">{{ s.sourceChanged(current.source_revision, current.source.source_revision) }}</p>
          <p v-if="current.source.state === 'obsolete'" class="hint">{{ s.sourceObsolete }}</p>
        </div>
        <p v-else class="hint">{{ s.sourceGone }}</p>
        <SuggestionCard :suggestion="current" :lang="current.locale" :dir="localeOf(current.locale)?.direction ?? 'auto'" />
        <template v-if="canDecide(current)">
          <div v-if="editing" class="stack-sm">
            <label for="rq-edit" class="label">{{ s.editLabel }}</label>
            <textarea id="rq-edit" ref="editArea" v-model="editText" rows="3" class="mono" :lang="current.locale" data-testid="review-edit" />
            <div class="row">
              <button type="button" class="btn btn-primary" :disabled="busy || !editText.trim()" :aria-keyshortcuts="ariaKeys(['Mod', 'Enter'])" @click="acceptEdit">
                {{ s.acceptEdit }} <span class="kbd-hint" aria-hidden="true">{{ keyLabel("Mod") }}{{ keyLabel("Enter").split(" ")[0] }}</span>
              </button>
              <button type="button" class="btn" :disabled="busy" aria-keyshortcuts="Escape" @click="cancelEdit">{{ strings.app.cancel }} <kbd aria-hidden="true">Esc</kbd></button>
            </div>
          </div>
          <div v-else class="row">
            <button type="button" class="btn btn-primary" :disabled="busy" aria-keyshortcuts="a" @click="accept">{{ s.accept }} <kbd aria-hidden="true">a</kbd></button>
            <button type="button" class="btn" :disabled="busy" aria-keyshortcuts="e" @click="startEdit">{{ s.edit }} <kbd aria-hidden="true">e</kbd></button>
            <button type="button" class="btn btn-danger" :disabled="busy" aria-keyshortcuts="r" @click="reject">{{ s.reject }} <kbd aria-hidden="true">r</kbd></button>
          </div>
        </template>
        <p v-else class="hint">{{ s.noDecide(current.locale) }}</p>
      </section>
    </div>

    <ModalDialog :open="batchOpen" :title="s.batchTitle" @close="batchOpen = false">
      <p>{{ s.batchLead(recommended.length) }}</p>
      <template #actions>
        <button type="button" class="btn" :disabled="busy" @click="batchOpen = false">{{ strings.app.cancel }}</button>
        <button type="button" class="btn btn-primary" :disabled="busy || !recommended.length" @click="acceptRecommended">{{ s.batchConfirm(recommended.length) }}</button>
      </template>
    </ModalDialog>
  </div>
</template>

<style scoped>
.review {
  max-inline-size: var(--kl-content-xl, 80rem);
}
.lead {
  max-inline-size: 48rem;
}
.mine {
  max-inline-size: 18rem;
}
.check {
  display: inline-flex;
  align-items: center;
  gap: var(--kl-space-2);
}
.queue {
  display: grid;
  grid-template-columns: minmax(18rem, 26rem) minmax(0, 1fr);
  gap: var(--kl-space-6);
  align-items: start;
}
.items {
  list-style: none;
  margin: 0;
  padding: 0;
  border: 1px solid var(--kl-border);
  border-radius: var(--kl-radius-md);
  max-block-size: 70vh;
  overflow-y: auto;
}
.item {
  padding: var(--kl-space-3);
  border-block-end: 1px solid var(--kl-border);
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-1);
  cursor: pointer;
}
.item[aria-selected="true"] {
  background: var(--kl-accent-dim);
  box-shadow: inset 3px 0 0 var(--kl-accent);
}
.key {
  font-weight: var(--kl-weight-medium);
  word-break: break-all;
}
.preview {
  font-size: var(--kl-text-sm);
  color: var(--kl-ink-secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.source {
  white-space: pre-wrap;
  padding: var(--kl-space-3);
  background: var(--kl-surface);
  border: 1px solid var(--kl-border);
  border-radius: var(--kl-radius-md);
}
textarea {
  inline-size: 100%;
}
@media (max-width: 60rem) {
  .queue {
    grid-template-columns: 1fr;
  }
}
</style>
