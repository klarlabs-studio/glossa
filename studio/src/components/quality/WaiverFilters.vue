<script setup lang="ts">
/**
 * The waiver list's filters (RFC 0005 §9): the layer and code a waiver
 * accepts, the fingerprint `glossa waive` printed, and whether it
 * stands now.
 *
 * "Standing and past" is the default rather than "standing only",
 * because a revoked waiver is history this project keeps on purpose and
 * hiding it by default would quietly answer "what has this project ever
 * accepted?" with a different question.
 */
import { computed } from "vue";
import { LAYERS } from "../../lib/quality";
import type { WaiverFilterState } from "../../lib/waivers";
import { strings } from "../../strings";

const props = defineProps<{ filter: WaiverFilterState; matched: number }>();
const emit = defineEmits<{ "update:filter": [value: WaiverFilterState] }>();
const s = strings.quality;

const active = computed(() => !!(props.filter.layer || props.filter.code || props.filter.fingerprint || props.filter.state));
const set = (patch: Partial<WaiverFilterState>) => emit("update:filter", { ...props.filter, ...patch });
const pick = (e: Event) => (e.target as HTMLSelectElement).value;
const typed = (e: Event) => (e.target as HTMLInputElement).value;
</script>

<template>
  <div class="filters" role="group" :aria-label="s.waiverFilters">
    <div class="field">
      <label for="w-state">{{ s.waiverState }}</label>
      <select id="w-state" :value="filter.state" data-testid="waiver-state" @change="set({ state: pick($event) })">
        <option value="">{{ s.waiverStateAny }}</option>
        <option value="active">{{ s.waiverStateActive }}</option>
        <option value="inactive">{{ s.waiverStateInactive }}</option>
      </select>
    </div>
    <div class="field">
      <label for="w-layer">{{ s.layer }}</label>
      <select id="w-layer" :value="filter.layer" data-testid="waiver-layer" @change="set({ layer: pick($event) })">
        <option value="">{{ s.anyLayer }}</option>
        <option v-for="l in LAYERS" :key="l" :value="l">{{ s.layerName[l] ?? l }}</option>
      </select>
    </div>
    <div class="field">
      <label for="w-code">{{ s.code }}</label>
      <input id="w-code" type="search" :value="filter.code" :placeholder="s.codePlaceholder" @change="set({ code: typed($event) })" />
    </div>
    <div class="field wide">
      <label for="w-fingerprint">{{ s.waiverFingerprint }}</label>
      <input
        id="w-fingerprint"
        type="search"
        :value="filter.fingerprint"
        :placeholder="s.waiverFingerprintPlaceholder"
        data-testid="waiver-fingerprint"
        @change="set({ fingerprint: typed($event) })"
      />
    </div>
    <div class="trailing">
      <button
        v-if="active"
        type="button"
        class="btn btn-sm"
        data-testid="waiver-clear"
        @click="set({ layer: '', code: '', fingerprint: '', state: '' })"
      >
        {{ s.clear }}
      </button>
    </div>
    <p class="matched" role="status" data-testid="waiver-matched">{{ s.waiverMatched(matched) }}</p>
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
.wide {
  min-inline-size: 16rem;
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
