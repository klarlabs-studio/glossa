<script setup lang="ts">
/**
 * The quality view's filters (RFC 0005 §2.1): the layer that found it,
 * the severity it stands at now — `waived` selects the accepted
 * findings rather than hiding them — the locale and the rule.
 *
 * Every control is a labelled native control, so it is reachable and
 * announced without any script of ours.
 */
import { computed } from "vue";
import type { ProjectLocale } from "../../api/schemas";
import { LAYERS, SEVERITIES, isFiltered, type QualityFilterState } from "../../lib/quality";
import { strings } from "../../strings";

const props = defineProps<{
  filter: QualityFilterState;
  locales: ProjectLocale[];
  /** How many of the loaded findings the filters leave. */
  matched: number;
}>();
const emit = defineEmits<{ "update:filter": [value: QualityFilterState] }>();
const s = strings.quality;

const active = computed(() => isFiltered(props.filter));
const set = (patch: Partial<QualityFilterState>) => emit("update:filter", { ...props.filter, ...patch });
const pick = (e: Event) => (e.target as HTMLSelectElement).value;
</script>

<template>
  <div class="filters" role="group" :aria-label="s.filters">
    <div class="field">
      <label for="q-layer">{{ s.layer }}</label>
      <select id="q-layer" :value="filter.layer" @change="set({ layer: pick($event) })">
        <option value="">{{ s.anyLayer }}</option>
        <option v-for="l in LAYERS" :key="l" :value="l">{{ s.layerName[l] ?? l }}</option>
      </select>
    </div>
    <div class="field">
      <label for="q-severity">{{ s.severity }}</label>
      <select id="q-severity" :value="filter.severity" @change="set({ severity: pick($event) })">
        <option value="">{{ s.anySeverity }}</option>
        <option v-for="sev in SEVERITIES" :key="sev" :value="sev">{{ s.severityName[sev] ?? sev }}</option>
      </select>
    </div>
    <div class="field">
      <label for="q-locale">{{ s.locale }}</label>
      <select id="q-locale" :value="filter.locale" @change="set({ locale: pick($event) })">
        <option value="">{{ s.anyLocale }}</option>
        <option v-for="l in locales" :key="l.code" :value="l.code">{{ l.code }}</option>
      </select>
    </div>
    <div class="field">
      <label for="q-code">{{ s.code }}</label>
      <input
        id="q-code"
        type="search"
        :value="filter.code"
        :placeholder="s.codePlaceholder"
        @change="set({ code: ($event.target as HTMLInputElement).value })"
      />
    </div>
    <div class="trailing">
      <button v-if="active" type="button" class="btn btn-sm" data-testid="quality-clear" @click="set({ layer: '', severity: '', locale: '', code: '' })">
        {{ s.clear }}
      </button>
    </div>
    <p class="matched" role="status" data-testid="quality-matched">{{ s.matched(matched) }}</p>
  </div>
</template>

<style scoped>
.filters {
  display: flex;
  flex-wrap: wrap;
  gap: var(--kl-space-3);
  align-items: flex-end;
}
.field {
  min-inline-size: 10rem;
}
.trailing {
  display: flex;
  align-items: center;
  min-block-size: 2.25rem;
}
.matched {
  flex-basis: 100%;
  color: var(--kl-ink-secondary);
  font-size: var(--kl-text-sm);
}
</style>
