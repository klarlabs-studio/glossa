<script setup lang="ts">
/**
 * The message list's filters by where a message appears (RFC 0004 §3.4):
 * route, component, file, and the coverage filters `unused` and `not
 * captured`. The choices come from the project's current builds, so the
 * group loads its index the first time it is opened — or right away when
 * a bookmarked view arrives with a filter already set.
 */
import { computed, onMounted, ref, watch } from "vue";
import { strings } from "../../strings";
import ErrorAlert from "../ErrorAlert.vue";
import { isContextFiltered, type ContextFilterState, type UsageIndex } from "./useContextFilter";

const props = defineProps<{
  filter: ContextFilterState;
  index: UsageIndex | undefined;
  loading: boolean;
  indexError: unknown;
  error: unknown;
  probe: { done: number; total: number; capped: boolean } | undefined;
  /** How many messages the filters leave, once they are resolved. */
  matched: number | undefined;
}>();
const emit = defineEmits<{ "update:filter": [value: ContextFilterState]; open: [] }>();
const s = strings.workspace.context;

const open = ref(isContextFiltered(props.filter));
onMounted(() => open.value && emit("open"));
watch(open, (isOpen) => isOpen && emit("open"));

const active = computed(() => isContextFiltered(props.filter));
const set = (patch: Partial<ContextFilterState>) => emit("update:filter", { ...props.filter, ...patch });
const pick = (e: Event) => (e.target as HTMLSelectElement).value;
</script>

<template>
  <div class="ctx stack-sm">
    <button type="button" class="btn btn-sm" :aria-expanded="open" aria-controls="ctx-filters" data-testid="ctx-toggle" @click="open = !open">
      {{ open ? s.hide : s.show }}
    </button>
    <div v-show="open" id="ctx-filters" class="stack-sm" role="group" :aria-label="s.legend">
      <ErrorAlert :error="indexError" />
      <ErrorAlert :error="error" />
      <p v-if="loading" class="hint" role="status">{{ s.loading }}</p>
      <p v-else-if="index && !index.hasData" class="hint" data-testid="ctx-no-data">{{ s.noData }}</p>
      <template v-else-if="index">
        <div class="field">
          <label for="ws-route">{{ s.route }}</label>
          <select id="ws-route" :value="filter.route" @change="set({ route: pick($event) })">
            <option value="">{{ s.any }}</option>
            <option v-for="r in index.routes" :key="r" :value="r">{{ r }}</option>
          </select>
        </div>
        <div class="field">
          <label for="ws-component">{{ s.component }}</label>
          <select id="ws-component" :value="filter.component" @change="set({ component: pick($event) })">
            <option value="">{{ s.any }}</option>
            <option v-for="c in index.components" :key="c" :value="c">{{ c }}</option>
          </select>
        </div>
        <div class="field">
          <label for="ws-file">{{ s.file }}</label>
          <select id="ws-file" :value="filter.file" @change="set({ file: pick($event) })">
            <option value="">{{ s.any }}</option>
            <option v-for="f in index.files" :key="f" :value="f">{{ f }}</option>
          </select>
        </div>
        <div class="field">
          <label for="ws-ctx-coverage">{{ s.coverage }}</label>
          <select id="ws-ctx-coverage" :value="filter.only" data-testid="ctx-coverage" @change="set({ only: pick($event) as ContextFilterState['only'] })">
            <option value="">{{ s.coverageAll }}</option>
            <option value="unused">{{ s.coverageUnused }}</option>
            <option value="uncaptured">{{ s.coverageUncaptured }}</option>
          </select>
        </div>
        <p v-if="probe && probe.done < probe.total" class="hint" role="status">{{ s.checking(probe.done, probe.total) }}</p>
        <p v-else-if="probe?.capped" class="hint" role="status">{{ s.capped(probe.total) }}</p>
        <p v-if="active && matched !== undefined" class="hint" role="status" data-testid="ctx-matched">{{ s.matched(matched) }}</p>
        <div v-if="active">
          <button type="button" class="btn btn-sm" data-testid="ctx-clear" @click="set({ route: '', component: '', file: '', only: '' })">{{ s.clear }}</button>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.ctx select {
  inline-size: 100%;
  min-inline-size: 0;
}
</style>
