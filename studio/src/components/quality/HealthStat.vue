<script setup lang="ts">
/**
 * One of the seven numbers (RFC 0005 §8): its name, what it stands at,
 * and one line of detail.
 *
 * The whole reason this is a component and not a `<span>`: a `value` of
 * `undefined` is rendered as the words "Not measured", in the neutral
 * tone, with `detail` carrying why. It is never rendered as `0`. A
 * health screen that shows a confident zero for a thing nobody measured
 * is worse than no health screen.
 *
 * Tone is never the only signal — the value itself is always a word or
 * a number a person can read, so the meaning survives a monochrome
 * screen, a colour-blind reader and a screenshot in a bug report.
 */
import { computed } from "vue";
import { strings } from "../../strings";

const props = withDefaults(
  defineProps<{
    label: string;
    /** What it stands at. `undefined` means nobody measured it. */
    value: string | undefined;
    detail?: string | undefined;
    tone?: "ok" | "warn" | "err" | "neutral";
    testid?: string;
  }>(),
  { detail: undefined, tone: "neutral", testid: undefined },
);
const s = strings.health;

const measured = computed(() => props.value !== undefined);
const tone = computed(() => (measured.value ? props.tone : "neutral"));
const pill = computed(() => `pill-${tone.value}`);
</script>

<template>
  <div class="stat" :data-testid="testid" :data-measured="String(measured)">
    <dt class="label">{{ label }}</dt>
    <dd class="value">
      <span class="pill" :class="pill" data-testid="stat-value">{{ value ?? s.notMeasured }}</span>
      <span v-if="detail" class="hint detail" data-testid="stat-detail">{{ detail }}</span>
    </dd>
  </div>
</template>

<style scoped>
.stat {
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-1);
  min-inline-size: 0;
}
.label {
  font-size: var(--kl-text-sm);
  color: var(--kl-ink-secondary);
}
.value {
  margin: 0;
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-1);
  align-items: flex-start;
}
.detail {
  text-wrap: pretty;
}
</style>
