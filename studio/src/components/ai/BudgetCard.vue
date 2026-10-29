<script setup lang="ts">
/** The monthly budget (integer micro-USD), this month's spend against it, per day and per model. */
import { computed, ref, watch } from "vue";
import type { AIBudget, AISettings, AISpendEntry } from "../../api/intelligence-schemas";
import { formatUSD, parseUSD, spentShare, toUSDText } from "../../lib/money";
import { dailySpend } from "../../lib/spend";
import { strings } from "../../strings";
import SpendChart from "./SpendChart.vue";

const props = defineProps<{
  settings: AISettings | undefined;
  budget: AIBudget | undefined;
  spend: AISpendEntry[];
  canManage: boolean;
  busy: boolean;
}>();
const emit = defineEmits<{ save: [update: { monthly_budget_micro_usd: number; max_concurrent_jobs: number }] }>();
const s = strings.aiSettings;

const budgetText = ref("");
const concurrency = ref(4);
const invalid = ref(false);
watch(
  () => props.settings,
  (st) => {
    if (!st) return;
    budgetText.value = toUSDText(st.monthly_budget_micro_usd);
    concurrency.value = st.max_concurrent_jobs;
  },
  { immediate: true },
);

function save(): void {
  const micro = parseUSD(budgetText.value);
  invalid.value = micro === undefined;
  if (micro === undefined) return;
  emit("save", { monthly_budget_micro_usd: micro, max_concurrent_jobs: concurrency.value });
}

const share = computed(() => (props.budget ? spentShare(props.budget.spent_micro_usd, props.budget.monthly_budget_micro_usd) : 0));
const days = computed(() => (props.budget ? dailySpend(props.spend, props.budget.month_start) : []));
</script>

<template>
  <section class="card stack" aria-labelledby="budget-h" data-testid="budget">
    <h2 id="budget-h">{{ s.budgetTitle }}</h2>
    <div v-if="budget" class="stack-sm">
      <p class="row">
        <strong data-testid="budget-spent">{{ s.spent(formatUSD(budget.spent_micro_usd), formatUSD(budget.monthly_budget_micro_usd)) }}</strong>
        <span class="hint">{{ s.remaining(formatUSD(budget.remaining_micro_usd)) }} · {{ s.calls(budget.calls) }}</span>
      </p>
      <meter class="meter" min="0" max="1" low="0.75" high="0.9" optimum="0" :value="share" :aria-label="s.spent(formatUSD(budget.spent_micro_usd), formatUSD(budget.monthly_budget_micro_usd))" />
      <SpendChart :days="days" />
      <div v-if="budget.by_provider.length" class="table-wrap">
        <table class="table small">
          <caption class="label cap">{{ s.byModel }}</caption>
          <thead>
            <tr>
              <th scope="col">{{ s.provider }}</th>
              <th scope="col">{{ s.model }}</th>
              <th scope="col">{{ s.cost }}</th>
              <th scope="col">{{ s.tokens }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="p in budget.by_provider" :key="`${p.provider}/${p.model}`">
              <td class="mono">{{ p.provider }}</td>
              <td class="mono">{{ p.model }}</td>
              <td>{{ formatUSD(p.cost_micro_usd) }} · {{ s.calls(p.calls) }}</td>
              <td>{{ p.input_tokens.toLocaleString() }} / {{ p.output_tokens.toLocaleString() }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
    <form v-if="canManage && settings" class="row form" @submit.prevent="save">
      <div class="field">
        <label for="ai-budget">{{ s.budget }}</label>
        <input id="ai-budget" v-model="budgetText" inputmode="decimal" :aria-invalid="invalid ? 'true' : undefined" aria-describedby="ai-budget-hint" />
      </div>
      <div class="field">
        <label for="ai-conc">{{ s.concurrency }}</label>
        <input id="ai-conc" v-model.number="concurrency" type="number" min="1" max="64" />
      </div>
      <button type="submit" class="btn" :disabled="busy">{{ s.saveBudget }}</button>
      <p id="ai-budget-hint" class="hint full" :class="{ err: invalid }">{{ invalid ? s.invalidBudget : s.budgetHint }}</p>
    </form>
  </section>
</template>

<style scoped>
.meter {
  inline-size: 100%;
  block-size: 0.75rem;
}
.table-wrap {
  overflow-x: auto;
}
.small {
  font-size: var(--kl-text-sm);
}
.cap {
  text-align: start;
  padding-block-end: var(--kl-space-2);
}
.form {
  align-items: flex-end;
}
.full {
  flex-basis: 100%;
}
.err {
  color: var(--gs-err);
}
</style>
