<script setup lang="ts">
/**
 * "Fill with AI" for the workspace's current filter. It first asks the
 * server what a fill would do (`ai-fill-previews`, which writes nothing):
 * the messages per locale, the jobs it would reuse, the exact
 * translation-memory matches that need no provider call, what would be
 * refused and why, and the estimated and maximum cost. Confirming queues
 * the fill, then follows its jobs (`ai-jobs?fill=`) until they settle.
 * Messages are selected by their translation's state (`select`), not by
 * listing keys; a search still narrows the fill to the keys it shows.
 */
import { computed, onBeforeUnmount, ref, shallowRef, watch } from "vue";
import { RouterLink } from "vue-router";
import type { AIFill, AIFillPreview, AIFillSelect, AIJob } from "../../api/intelligence-schemas";
import { useIntelligence, type FillInput } from "../../api/intelligence";
import { newIdempotencyKey } from "../../api/releases";
import { fillPlan, fillProgress, FILL_POLL_MS } from "../../lib/fill";
import { formatUSD } from "../../lib/money";
import { strings } from "../../strings";
import ErrorAlert from "../ErrorAlert.vue";
import ModalDialog from "../ModalDialog.vue";

const props = defineProps<{
  open: boolean;
  tenant: string;
  projectId: string;
  locale: string;
  namespace: string | undefined;
  /** The selection to start with (the workspace's coverage filter). */
  select: AIFillSelect;
  /** The keys the search narrows the list to, when it does. */
  keys: string[] | undefined;
}>();
const emit = defineEmits<{ close: []; settled: [fill: AIFill] }>();
const s = strings.fill;
const port = useIntelligence();
const SELECTS: AIFillSelect[] = ["missing", "outdated", "missing_or_outdated"];

const select = ref<AIFillSelect>(props.select);
const preview = shallowRef<AIFillPreview>();
const previewing = ref(false);
const fill = shallowRef<AIFill>();
const jobs = shallowRef<AIJob[]>([]);
const error = ref<unknown>(null);
const busy = ref(false);
let key = newIdempotencyKey();
let timer: ReturnType<typeof setTimeout> | undefined;
let previewSeq = 0;

const ref_ = () => ({ tenant: props.tenant, project: props.projectId });
const input = (): FillInput => ({
  locales: [props.locale],
  select: select.value,
  ...(props.namespace ? { namespace: props.namespace } : {}),
  ...(props.keys ? { keys: props.keys } : {}),
});

/** Ask what a fill would do; only the latest answer counts. */
async function loadPreview(): Promise<void> {
  const seq = ++previewSeq;
  previewing.value = true;
  preview.value = undefined;
  error.value = null;
  try {
    const p = await port.previewFill(ref_(), input());
    if (seq === previewSeq) preview.value = p;
  } catch (e) {
    if (seq === previewSeq) error.value = e;
  } finally {
    if (seq === previewSeq) previewing.value = false;
  }
}

watch(
  () => props.open,
  (open) => {
    if (!open) return stop();
    fill.value = undefined;
    jobs.value = [];
    error.value = null;
    key = newIdempotencyKey();
    if (select.value === props.select) void loadPreview();
    else select.value = props.select; // the watch below previews
  },
  { immediate: true },
);
watch(select, () => {
  if (props.open && !fill.value) void loadPreview();
});

const plan = computed(() => (preview.value ? fillPlan(preview.value) : undefined));
const canStart = computed(() => !busy.value && !previewing.value && !!plan.value && plan.value.messages > 0);
const progress = computed(() => fillProgress(jobs.value));

function stop(): void {
  clearTimeout(timer);
  timer = undefined;
  previewSeq++;
}

async function poll(): Promise<void> {
  const f = fill.value;
  if (!f) return;
  try {
    jobs.value = await port.jobs(props.tenant, { fill: f.id });
    error.value = null;
  } catch (e) {
    error.value = e;
  }
  if (progress.value.active > 0 && props.open) timer = setTimeout(() => void poll(), FILL_POLL_MS);
  else if (progress.value.active === 0) emit("settled", f);
}

async function start(): Promise<void> {
  if (!canStart.value) return;
  busy.value = true;
  error.value = null;
  try {
    fill.value = await port.createFill(ref_(), input(), key);
    await poll();
  } catch (e) {
    error.value = e;
  } finally {
    busy.value = false;
  }
}

async function cancel(): Promise<void> {
  const f = fill.value;
  if (!f) return;
  busy.value = true;
  try {
    fill.value = await port.cancelFill(props.tenant, f.id);
    stop();
    await poll();
  } catch (e) {
    error.value = e;
  } finally {
    busy.value = false;
  }
}
onBeforeUnmount(stop);

const skipped = computed(() => Object.entries(fill.value?.skipped ?? {}).filter(([, n]) => n > 0));
</script>

<template>
  <ModalDialog :open="open" :title="s.title(locale)" wide @close="emit('close')">
    <template v-if="!fill">
      <p>{{ s.scope(locale, namespace, keys?.length) }}</p>
      <fieldset class="stack-sm choices">
        <legend class="label">{{ s.select(locale) }}</legend>
        <label v-for="v in SELECTS" :key="v" class="check">
          <input v-model="select" type="radio" name="fill-select" :value="v" :disabled="busy" />
          <span>{{ s.selects[v] }}</span>
        </label>
      </fieldset>

      <section class="stack-sm plan" aria-labelledby="fill-plan-h" aria-live="polite" :aria-busy="previewing" data-testid="fill-preview">
        <h3 id="fill-plan-h">{{ s.previewTitle }}</h3>
        <p v-if="previewing" class="muted" role="status">{{ s.previewing }}</p>
        <template v-else-if="preview && plan">
          <ul v-if="preview.warnings.length" class="stack-sm warnings" data-testid="fill-warnings">
            <li v-for="w in preview.warnings" :key="w" class="alert alert-warn">{{ s.warning(w) }}</li>
          </ul>
          <p v-if="!plan.messages" data-testid="fill-plan-empty">{{ s.nothingPlanned }}</p>
          <template v-else>
            <p class="count" data-testid="fill-plan-messages">{{ s.planMessages(plan.messages) }}</p>
            <ul class="breakdown" data-testid="fill-plan">
              <li v-if="plan.provider">{{ s.planProvider(plan.provider) }}</li>
              <li v-if="plan.tmExact">{{ s.planTmExact(plan.tmExact) }}</li>
              <li v-if="plan.existing">{{ s.planExisting(plan.existing) }}</li>
              <li v-for="[why, n] in plan.refused" :key="why">{{ s.refused(why, n) }}</li>
            </ul>
            <details v-for="l in preview.locales" :key="l.locale" class="keys">
              <summary>{{ s.showKeys(l.locale, l.keys.length) }}</summary>
              <ul class="mono">
                <li v-for="k in l.keys" :key="k">{{ k }}</li>
              </ul>
            </details>
            <p data-testid="fill-cost">{{ s.cost(formatUSD(preview.cost.estimated_micro_usd), formatUSD(preview.cost.max_micro_usd)) }}</p>
            <p v-if="preview.cost.unpriced" class="hint">{{ s.unpriced }}</p>
          </template>
          <p v-if="plan.skipped.length" class="hint">{{ s.skipped(plan.skipped) }}</p>
        </template>
      </section>
      <p class="hint">{{ s.lead }}</p>
    </template>
    <div v-else class="stack-sm" data-testid="fill-progress">
      <ul v-if="fill.warnings.length" class="stack-sm warnings" data-testid="fill-warnings">
        <li v-for="w in fill.warnings" :key="w" class="alert alert-warn">{{ s.warning(w) }}</li>
      </ul>
      <p>{{ s.queued(fill.jobs_created, fill.jobs_existing) }}</p>
      <p v-if="skipped.length" class="hint">{{ s.skipped(skipped) }}</p>
      <template v-if="progress.total">
        <label for="fill-bar" class="label">{{ s.progressLabel }}</label>
        <progress id="fill-bar" :max="progress.total" :value="progress.total - progress.active" />
        <p role="status" data-testid="fill-status">{{ s.progress(progress) }}</p>
        <ul v-if="progress.failures.length" class="hint failures">
          <li v-for="[code, n] in progress.failures" :key="code">{{ s.failure(code, n) }}</li>
        </ul>
      </template>
      <p v-else role="status" data-testid="fill-status">{{ s.nothing }}</p>
    </div>
    <ErrorAlert :error="error" />
    <template #actions>
      <template v-if="!fill">
        <button type="button" class="btn" @click="emit('close')">{{ strings.app.cancel }}</button>
        <button type="button" class="btn btn-primary" :disabled="!canStart" data-testid="fill-start" @click="start">{{ s.start(plan?.messages ?? 0) }}</button>
      </template>
      <template v-else>
        <button v-if="progress.queued > 0" type="button" class="btn btn-danger" :disabled="busy" @click="cancel">{{ s.cancelQueued }}</button>
        <RouterLink v-if="progress.active === 0 && progress.succeeded > 0" class="btn" :to="{ name: 'review', params: { tenant, project: projectId }, query: { locale } }">{{ s.openQueue }}</RouterLink>
        <button type="button" class="btn btn-primary" @click="emit('close')">{{ progress.active ? s.keepRunning : strings.app.close }}</button>
      </template>
    </template>
  </ModalDialog>
</template>

<style scoped>
.choices {
  border: none;
  margin: 0;
  padding: 0;
}
.plan {
  padding: var(--kl-space-3);
  border: 1px solid var(--kl-border);
  border-radius: var(--kl-radius-md);
  background: var(--kl-surface);
}
.plan h3 {
  font-size: var(--kl-text-sm);
}
.count {
  font-weight: var(--kl-weight-medium);
}
.breakdown,
.failures {
  margin: 0;
  padding-inline-start: var(--kl-space-5);
}
.keys ul {
  margin: var(--kl-space-2) 0 0;
  padding-inline-start: var(--kl-space-5);
  max-block-size: 12rem;
  overflow-y: auto;
  font-size: var(--kl-text-sm);
}
.warnings {
  list-style: none;
  margin: 0;
  padding: 0;
}
progress {
  inline-size: 100%;
  accent-color: var(--kl-accent);
}
</style>
