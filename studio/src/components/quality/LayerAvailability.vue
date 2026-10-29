<script setup lang="ts">
/**
 * Which checks can run for one locale (RFC 0005 §8, intent §41).
 *
 * This is the subtle one, and the reason it is its own component: a
 * layer that *cannot run* for a language must never read like a layer
 * that ran and found nothing. Telling someone their Japanese is clean
 * when it was never checked is worse than telling them nothing.
 *
 * So every chip carries its state as a **word** — "Clean", "Not
 * available", "Not checked", "Not reported", or the count it found —
 * before it carries a colour, and the three non-clean states each get a
 * different border as well as a different tone. The reasons a layer
 * cannot run here are then spelled out in prose below the chips, for
 * everyone, not hidden in a `title`.
 */
import { computed } from "vue";
import type { LocaleLayer } from "../../lib/health";
import { availableLayers } from "../../lib/health";
import { strings } from "../../strings";

const props = defineProps<{ locale: string; layers: LocaleLayer[] }>();
const s = strings.health;
const layerNames = strings.quality.layerName;

const counted = computed(() => availableLayers(props.layers));

const TONE: Record<string, string> = {
  errors: "pill-err",
  warnings: "pill-warn",
  clean: "pill-ok",
  "not-checked": "pill-neutral",
  unavailable: "pill-neutral",
  unknown: "pill-neutral",
};

/** The word a chip shows. A graded layer shows what it found; the rest show what they are. */
function stateWord(l: LocaleLayer): string {
  if (l.state === "errors") return s.layerErrors(l.reported?.findings?.errors ?? 0);
  if (l.state === "warnings") return s.layerWarnings(l.reported?.findings?.warnings ?? 0);
  return s.layerState[l.state] ?? l.state;
}

const name = (l: LocaleLayer) => layerNames[l.layer] ?? l.layer;

/** The layers that cannot run here, with why — visible prose, not a tooltip. */
const unavailable = computed(() =>
  props.layers
    .filter((l) => l.state === "unavailable")
    .map((l) => ({ layer: l.layer, name: name(l), why: s.layerUnavailable[l.reported?.unavailable ?? ""] ?? "" })),
);
</script>

<template>
  <div class="stack-sm layers" :data-testid="`layers-${locale}`">
    <p class="hint" data-testid="layers-summary">{{ s.layersSummary(counted.available, counted.total) }}</p>
    <ul class="chips" :aria-label="`${s.layersTitle} — ${locale}`">
      <li
        v-for="l in layers"
        :key="l.layer"
        class="pill chip"
        :class="[TONE[l.state], `chip-${l.state}`]"
        :data-chip-layer="l.layer"
        :data-state="l.state"
        data-testid="layer-chip"
      >
        <span class="chip-name">{{ name(l) }}</span>
        <span class="chip-state">{{ stateWord(l) }}</span>
      </li>
    </ul>
    <ul v-if="unavailable.length" class="reasons" data-testid="layers-unavailable">
      <li v-for="u in unavailable" :key="u.layer">
        <strong>{{ s.layerName(u.name, s.layerState.unavailable ?? "") }}</strong>
        <span v-if="u.why"> — {{ u.why }}</span>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.chips,
.reasons {
  list-style: none;
  margin: 0;
  padding: 0;
}
.chips {
  display: flex;
  flex-wrap: wrap;
  gap: var(--kl-space-2);
}
.chip {
  gap: 0.45em;
  white-space: normal;
}
.chip-state {
  font-weight: var(--kl-weight-normal, normal);
  opacity: 0.95;
}
/* Shape as well as colour: a layer that cannot run here looks different
   from one that ran, on a monochrome screen as much as on a colour one. */
.chip-unavailable,
.chip-unknown {
  border-style: dashed;
}
.chip-not-checked {
  border-style: dotted;
}
.reasons {
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-1);
  font-size: var(--kl-text-sm);
  color: var(--kl-ink-secondary);
}
</style>
