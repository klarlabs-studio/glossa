<script setup lang="ts">
/** The price table budgets are charged with: the deployment's defaults and the workspace's overrides. */
import { computed, reactive, ref, watch } from "vue";
import type { AIPrice, AIPrices } from "../../api/intelligence-schemas";
import { strings } from "../../strings";

const props = defineProps<{ prices: AIPrices | undefined; canManage: boolean; busy: boolean }>();
const emit = defineEmits<{ save: [overrides: Record<string, AIPrice>] }>();
const s = strings.aiSettings;

const overrides = ref<Record<string, AIPrice>>({});
watch(
  () => props.prices,
  (p) => (overrides.value = structuredClone(p?.overrides ?? {})),
  { immediate: true },
);
const rows = computed(() =>
  Object.entries(props.prices?.effective ?? {})
    .concat(Object.entries(overrides.value).filter(([k]) => !(k in (props.prices?.effective ?? {}))))
    .sort((a, b) => a[0].localeCompare(b[0])),
);
const draft = reactive({ key: "", input: 0, output: 0 });

function add(): void {
  const key = draft.key.trim();
  if (!/^[^/\s]+\/\S+$/.test(key)) return;
  overrides.value = { ...overrides.value, [key]: { input_per_mtok: draft.input, output_per_mtok: draft.output } };
  emit("save", overrides.value);
  Object.assign(draft, { key: "", input: 0, output: 0 });
}
function remove(key: string): void {
  const next = { ...overrides.value };
  delete next[key];
  overrides.value = next;
  emit("save", next);
}
</script>

<template>
  <section class="card stack-sm" aria-labelledby="prices-h" data-testid="prices">
    <h2 id="prices-h">{{ s.prices }}</h2>
    <p class="hint">{{ s.pricesLead }}</p>
    <div class="table-wrap">
      <table class="table small">
        <thead>
          <tr>
            <th scope="col">{{ s.priceKey }}</th>
            <th scope="col">{{ s.priceIn }}</th>
            <th scope="col">{{ s.priceOut }}</th>
            <th scope="col"><span class="visually-hidden">{{ s.override }}</span></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="[k, p] in rows" :key="k">
            <th scope="row" class="mono">{{ k }}</th>
            <td>${{ p.input_per_mtok }}</td>
            <td>${{ p.output_per_mtok }}</td>
            <td>
              <template v-if="k in overrides">
                <span class="pill pill-accent">{{ s.override }}</span>
                <button v-if="canManage" type="button" class="btn btn-ghost btn-sm" :disabled="busy" :aria-label="s.removeOverride(k)" @click="remove(k)">×</button>
              </template>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <form v-if="canManage" class="row add" @submit.prevent="add">
      <div class="field">
        <label for="pr-key">{{ s.priceKey }}</label>
        <input id="pr-key" v-model="draft.key" class="mono" placeholder="mistral-eu/mistral-large" required pattern="[^/\s]+/\S+" />
      </div>
      <div class="field">
        <label for="pr-in">{{ s.priceIn }}</label>
        <input id="pr-in" v-model.number="draft.input" type="number" min="0" step="0.01" />
      </div>
      <div class="field">
        <label for="pr-out">{{ s.priceOut }}</label>
        <input id="pr-out" v-model.number="draft.output" type="number" min="0" step="0.01" />
      </div>
      <button type="submit" class="btn btn-sm" :disabled="busy">{{ s.addOverride }}</button>
    </form>
  </section>
</template>

<style scoped>
.table-wrap {
  overflow-x: auto;
}
.small {
  font-size: var(--kl-text-sm);
}
.add {
  align-items: flex-end;
}
.add input[type="number"] {
  inline-size: 7rem;
}
</style>
