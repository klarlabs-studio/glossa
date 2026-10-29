<script setup lang="ts">
/**
 * Live preview of source and translation with editable sample values.
 * Plural arguments get one-click samples for every plural category of the
 * *target* language, so a translator sees each form their language needs.
 */
import type { Message } from "@klarlabs-studio/glossa/messageformat";
import { computed, onMounted, reactive, shallowRef, watch } from "vue";
import type { Argument } from "../api/schemas";
import { loadFormatter, renderPreview, type Preview } from "../lib/preview";
import { defaultSample, isTemporal, pluralExamples, toValues } from "../lib/samples";
import { strings } from "../strings";

const props = defineProps<{
  args: Argument[];
  source: { model: Message; locale: string; dir: string };
  target: { model: Message | undefined; locale: string; dir: string };
  notice?: string | undefined;
}>();
const s = strings.workspace;

const mf = shallowRef<Awaited<ReturnType<typeof loadFormatter>>>();
onMounted(async () => {
  mf.value = await loadFormatter();
});

const samples = reactive<Record<string, string>>({});
watch(
  () => props.args,
  (args) => {
    for (const k of Object.keys(samples)) delete samples[k];
    for (const a of args) samples[a.name] = defaultSample(a);
  },
  { immediate: true },
);

const values = computed(() => toValues(props.args, samples));
const render = (model: Message | undefined, locale: string): Preview | undefined =>
  mf.value && model ? renderPreview(mf.value, model, locale, values.value) : undefined;
const sourcePreview = computed(() => render(props.source.model, props.source.locale));
const targetPreview = computed(() => render(props.target.model, props.target.locale));

const pluralArgs = computed(() =>
  props.args
    .filter((a) => a.selector && (a.selector.kind === "plural" || a.selector.kind === "ordinal"))
    .map((a) => ({ arg: a, examples: pluralExamples(props.target.locale, a.selector?.kind === "ordinal" ? "ordinal" : "cardinal") })),
);
</script>

<template>
  <section class="preview stack-sm" aria-labelledby="preview-h">
    <h3 id="preview-h">{{ s.preview }}</h3>
    <dl class="outputs">
      <dt>{{ s.previewSource }} <span class="muted mono">{{ source.locale }}</span></dt>
      <dd class="out" :lang="source.locale" :dir="source.dir" data-testid="preview-source">
        <template v-if="sourcePreview">
          <component :is="seg.kind === 'text' ? 'span' : 'bdi'" v-for="(seg, i) in sourcePreview.segments" :key="i" :class="`seg-${seg.kind}`">{{ seg.text }}</component>
        </template>
      </dd>
      <dt>{{ s.previewTarget }} <span class="muted mono">{{ target.locale }}</span></dt>
      <dd class="out" :lang="target.locale" :dir="target.dir" aria-live="polite" data-testid="preview-target">
        <template v-if="targetPreview">
          <component :is="seg.kind === 'text' ? 'span' : 'bdi'" v-for="(seg, i) in targetPreview.segments" :key="i" :class="`seg-${seg.kind}`">{{ seg.text }}</component>
        </template>
        <span v-else class="muted">{{ s.previewEmpty }}</span>
      </dd>
    </dl>
    <p v-if="notice" class="hint">{{ notice }}</p>
    <div v-if="targetPreview?.errors.length" class="hint warn">
      {{ s.formatErrors }}: {{ targetPreview.errors.map((e) => e.type + (e.source ? ` ${e.source}` : "")).join(", ") }}
    </div>

    <fieldset v-if="args.length" class="samples">
      <legend>{{ s.samples }}</legend>
      <div v-for="a in args" :key="a.name" class="sample">
        <label :for="`sample-${a.name}`" class="mono">${{ a.name }} <span class="muted">{{ a.type }}</span></label>
        <input
          :id="`sample-${a.name}`"
          v-model="samples[a.name]"
          :placeholder="isTemporal(a) ? 'YYYY-MM-DDThh:mm:ssZ' : undefined"
          autocomplete="off"
          spellcheck="false"
        />
      </div>
      <div v-for="p in pluralArgs" :key="`plural-${p.arg.name}`" class="plurals" role="group" :aria-label="`${s.pluralForms(target.locale)}: $${p.arg.name}`">
        <span class="hint">{{ s.pluralForms(target.locale) }}</span>
        <button
          v-for="ex in p.examples"
          :key="ex.category"
          type="button"
          class="btn btn-sm"
          :aria-pressed="samples[p.arg.name] === String(ex.value)"
          @click="samples[p.arg.name] = String(ex.value)"
        >
          {{ ex.category }} <span class="muted">· {{ ex.value }}</span>
        </button>
      </div>
    </fieldset>
  </section>
</template>

<style scoped>
.outputs {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: var(--kl-space-2) var(--kl-space-3);
  margin: 0;
  align-items: baseline;
}
.outputs dt {
  font-size: var(--kl-text-sm);
  color: var(--kl-ink-secondary);
}
.out {
  margin: 0;
  padding: var(--kl-space-2) var(--kl-space-3);
  border-radius: var(--kl-radius-md);
  background: var(--kl-surface);
  border: 1px solid var(--kl-border);
  min-block-size: 2.4rem;
  white-space: pre-wrap;
  font-size: var(--kl-text-md);
  line-height: var(--kl-leading-snug);
}
.seg-value {
  background: var(--kl-accent-dim);
  border-radius: var(--kl-radius-sm);
  padding: 0 2px;
}
.seg-fallback {
  color: var(--gs-err);
  background: var(--gs-err-bg);
  font-family: var(--kl-font-mono);
  font-size: 0.85em;
}
.seg-markup {
  color: var(--kl-ink-secondary);
  font-family: var(--kl-font-mono);
  font-size: 0.8em;
}
.warn {
  color: var(--gs-warn);
}
.samples {
  border: 1px solid var(--kl-border);
  border-radius: var(--kl-radius-md);
  padding: var(--kl-space-3);
  margin: 0;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(12rem, 1fr));
  gap: var(--kl-space-3);
}
.samples legend {
  font-size: var(--kl-text-sm);
  font-weight: var(--kl-weight-medium);
  padding: 0 var(--kl-space-1);
}
.sample {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.sample label {
  font-size: var(--kl-text-sm);
}
.plurals {
  grid-column: 1 / -1;
  display: flex;
  flex-wrap: wrap;
  gap: var(--kl-space-2);
  align-items: center;
}
.plurals [aria-pressed="true"] {
  border-color: var(--kl-accent);
  background: var(--kl-accent-dim);
}
</style>
