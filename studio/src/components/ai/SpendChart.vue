<script setup lang="ts">
/**
 * Spend per day this month: one series, so one hue and no legend (the
 * title names it). Thin bars with rounded tops on a recessive baseline,
 * a hover tooltip per day, and the same numbers as a table.
 */
import { computed, ref } from "vue";
import { formatUSD } from "../../lib/money";
import type { DaySpend } from "../../lib/spend";
import { strings } from "../../strings";

const props = defineProps<{ days: DaySpend[] }>();
const s = strings.aiSettings;

const W = 560;
const H = 140;
const PAD = { top: 12, right: 8, bottom: 22, left: 8 };
const innerW = W - PAD.left - PAD.right;
const innerH = H - PAD.top - PAD.bottom;

const max = computed(() => Math.max(1, ...props.days.map((d) => d.micro)));
const slot = computed(() => innerW / Math.max(1, props.days.length));
const barW = computed(() => Math.max(2, Math.min(18, slot.value - 2)));
const total = computed(() => props.days.reduce((n, d) => n + d.micro, 0));

/** A bar path with 4px rounded top corners anchored to the baseline. */
function bar(i: number, micro: number): string {
  const h = micro <= 0 ? 0 : Math.max(2, (micro / max.value) * innerH);
  const x = PAD.left + i * slot.value + (slot.value - barW.value) / 2;
  const y = PAD.top + innerH - h;
  const r = Math.min(4, barW.value / 2, h);
  const w = barW.value;
  return `M${x},${PAD.top + innerH} V${y + r} Q${x},${y} ${x + r},${y} H${x + w - r} Q${x + w},${y} ${x + w},${y + r} V${PAD.top + innerH} Z`;
}
const hover = ref<number>();
const label = (d: DaySpend) => `${d.day}: ${formatUSD(d.micro)} · ${s.calls(d.calls)}`;
</script>

<template>
  <figure class="chart stack-sm" data-testid="spend-chart">
    <figcaption class="label">{{ s.spendChart }}</figcaption>
    <p v-if="!total" class="muted">{{ s.spendChartEmpty }}</p>
    <template v-else>
      <div class="plot">
        <svg :viewBox="`0 0 ${W} ${H}`" role="img" :aria-label="`${s.spendChart}: ${formatUSD(total)}`">
          <line class="base" :x1="PAD.left" :x2="W - PAD.right" :y1="PAD.top + innerH" :y2="PAD.top + innerH" />
          <text class="axis" :x="PAD.left" :y="PAD.top - 2">{{ formatUSD(max) }}</text>
          <g v-for="(d, i) in days" :key="d.day" @mouseenter="hover = i" @mouseleave="hover = undefined">
            <rect class="hit" :x="PAD.left + i * slot" :y="PAD.top" :width="slot" :height="innerH" />
            <path v-if="d.micro > 0" class="bar" :class="{ on: hover === i }" :d="bar(i, d.micro)" />
            <title>{{ label(d) }}</title>
          </g>
          <text class="axis" :x="PAD.left" :y="H - 6">{{ days[0]?.day.slice(5) }}</text>
          <text class="axis end" :x="W - PAD.right" :y="H - 6">{{ days.at(-1)?.day.slice(5) }}</text>
        </svg>
        <p v-if="hover !== undefined && days[hover]" class="tip" aria-hidden="true">{{ label(days[hover]!) }}</p>
      </div>
      <details>
        <summary class="hint">{{ s.spendTable }}</summary>
        <table class="table small">
          <thead>
            <tr>
              <th scope="col">{{ s.when }}</th>
              <th scope="col">{{ s.cost }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="d in days.filter((x) => x.micro > 0)" :key="d.day">
              <td>{{ d.day }}</td>
              <td>{{ formatUSD(d.micro) }} · {{ s.calls(d.calls) }}</td>
            </tr>
          </tbody>
        </table>
      </details>
    </template>
  </figure>
</template>

<style scoped>
.chart {
  margin: 0;
}
.plot {
  position: relative;
}
svg {
  inline-size: 100%;
  block-size: auto;
  display: block;
}
.base {
  stroke: var(--kl-border-strong);
  stroke-width: 1;
}
.bar {
  fill: var(--kl-accent);
}
.bar.on {
  fill: var(--kl-accent-dark);
}
.hit {
  fill: transparent;
}
.axis {
  fill: var(--kl-ink-secondary);
  font-size: 11px;
}
.axis.end {
  text-anchor: end;
}
.tip {
  position: absolute;
  inset-block-start: 0;
  inset-inline-end: 0;
  font-size: var(--kl-text-sm);
  background: var(--kl-surface);
  border: 1px solid var(--kl-border);
  border-radius: var(--kl-radius-sm);
  padding: 0.1em 0.5em;
}
.small {
  font-size: var(--kl-text-sm);
}
</style>
