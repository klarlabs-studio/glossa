<script setup lang="ts">
/**
 * The workflow document's chart, drawn (RFC 0006 §8: "the JSON document
 * with … Mermaid rendering beside it"), with the same chart as a table
 * underneath. The table is not a fallback: it is what a screen reader
 * reads, and what anyone reads when the drawing is too dense.
 *
 * The drawing follows the editor as the author types, a moment after
 * they stop. A document that doesn't parse keeps the last drawing and
 * says it is out of date rather than blanking it.
 */
import { computed, onBeforeUnmount, ref, watch } from "vue";
import type { WorkflowDocument } from "../../api/workflows-schemas";
import { useChartRenderer } from "../../lib/mermaid";
import { chartOf, toMermaid } from "../../lib/workflow";
import { problemText, strings } from "../../strings";

const props = defineProps<{
  /** The document as last parsed; `undefined` before anything parsed. */
  doc: WorkflowDocument | undefined;
  /** The editor's text no longer parses, so `doc` is an older one. */
  stale?: boolean;
  /** Milliseconds to wait after a change before drawing (0 in tests). */
  delay?: number;
}>();
const s = strings.workflows.chart;
const render = useChartRenderer();

const chart = computed(() => (props.doc ? chartOf(props.doc) : undefined));
const source = computed(() => (chart.value ? toMermaid(chart.value) : ""));
const svg = ref("");
const phase = ref<"idle" | "drawing" | "ready" | "failed">("idle");
const error = ref<unknown>(null);
let timer: ReturnType<typeof setTimeout> | undefined;
let seq = 0;

async function draw(text: string): Promise<void> {
  const mine = ++seq;
  if (!text || !chart.value?.states.length) {
    svg.value = "";
    phase.value = "idle";
    return;
  }
  phase.value = "drawing";
  try {
    const out = await render(`wf-chart-${mine}`, text);
    if (mine !== seq) return;
    svg.value = out;
    phase.value = "ready";
  } catch (e) {
    if (mine !== seq) return;
    error.value = e;
    phase.value = "failed";
  }
}

watch(
  source,
  (text) => {
    clearTimeout(timer);
    const wait = props.delay ?? 400;
    if (wait <= 0) void draw(text);
    else timer = setTimeout(() => void draw(text), wait);
  },
  { immediate: true },
);
onBeforeUnmount(() => {
  clearTimeout(timer);
  seq++;
});

const initial = computed(() => chart.value?.initial);
</script>

<template>
  <figure class="chart stack-sm" aria-labelledby="wf-chart-caption" data-testid="workflow-chart">
    <figcaption id="wf-chart-caption" class="row">
      <span class="label">{{ s.title }}</span>
      <span v-if="stale" class="pill pill-warn" data-testid="chart-stale">{{ s.stale }}</span>
    </figcaption>
    <p v-if="!chart || !chart.states.length" class="muted" data-testid="chart-empty">{{ s.empty }}</p>
    <template v-else>
      <p v-if="phase === 'drawing' && !svg" class="muted" role="status">{{ s.drawing }}</p>
      <div v-if="phase === 'failed'" class="alert alert-warn" data-testid="chart-failed">
        <p>{{ s.failed }}</p>
        <p class="hint">{{ problemText(error) }}</p>
      </div>
      <!-- The drawing is for the eye; the table below says the same to everyone. Mermaid's strict mode sanitizes the SVG. -->
      <div v-if="svg" class="drawing" aria-hidden="true" data-testid="chart-svg" v-html="svg" />

      <table class="table transitions" data-testid="chart-table">
        <caption class="visually-hidden">{{ s.tableCaption }}</caption>
        <thead>
          <tr>
            <th scope="col">{{ s.from }}</th>
            <th scope="col">{{ s.event }}</th>
            <th scope="col">{{ s.guard }}</th>
            <th scope="col">{{ s.actions }}</th>
            <th scope="col">{{ s.to }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(t, i) in chart.transitions" :key="i">
            <th scope="row" class="mono">
              {{ t.from }}<span v-if="t.from === initial" class="hint"> {{ s.initial }}</span>
            </th>
            <td class="mono">{{ t.event }}</td>
            <td class="mono">{{ t.guard ?? "—" }}</td>
            <td class="mono">{{ t.actions.join(", ") || "—" }}</td>
            <td class="mono">{{ t.target }}</td>
          </tr>
        </tbody>
      </table>
      <p class="hint" data-testid="chart-finals">{{ s.finals(chart.states.filter((x) => x.final).map((x) => x.id)) }}</p>
    </template>
  </figure>
</template>

<style scoped>
.chart {
  margin: 0;
}
.drawing {
  overflow-x: auto;
  padding: var(--kl-space-3);
  background: var(--kl-surface);
  border: 1px solid var(--kl-border);
  border-radius: var(--kl-radius-md);
}
.drawing :deep(svg) {
  max-inline-size: 100%;
  block-size: auto;
}
.transitions {
  font-size: var(--kl-text-sm);
}
</style>
