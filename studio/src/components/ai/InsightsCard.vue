<script setup lang="ts">
/**
 * How AI is doing here: people's decisions per locale (acceptance rate,
 * mean edit distance), the disclosures log (which provider saw what,
 * exactly), and the eval baseline the server was committed with.
 */
import { onMounted, ref, shallowRef } from "vue";
import type { AIDisclosure, AIEvalBaseline, AIMetrics } from "../../api/intelligence-schemas";
import { useIntelligence } from "../../api/intelligence";
import { absoluteTime } from "../../lib/time";
import { strings } from "../../strings";
import ErrorAlert from "../ErrorAlert.vue";

const props = defineProps<{ tenant: string; projectId: string }>();
const s = strings.aiSettings;
const port = useIntelligence();

const metrics = shallowRef<AIMetrics>();
const disclosures = shallowRef<AIDisclosure[]>([]);
const next = ref<string>();
const baseline = shallowRef<AIEvalBaseline>();
const error = ref<unknown>(null);

async function more(): Promise<void> {
  try {
    const page = await port.disclosures(props.tenant, { project: props.projectId }, next.value);
    disclosures.value = [...disclosures.value, ...page.items];
    next.value = page.next;
  } catch (e) {
    error.value = e;
  }
}

onMounted(async () => {
  const [m, b] = await Promise.allSettled([port.metrics({ tenant: props.tenant, project: props.projectId }), port.evalBaseline(props.tenant)]);
  if (m.status === "fulfilled") metrics.value = m.value;
  else error.value = m.reason;
  if (b.status === "fulfilled") baseline.value = b.value;
  await more();
});

const pct = s.percent;
</script>

<template>
  <section class="card stack" aria-labelledby="ins-h" data-testid="insights">
    <h2 id="ins-h">{{ s.insights }}</h2>
    <ErrorAlert :error="error" />

    <div class="stack-sm">
      <h3>{{ s.metrics }}</h3>
      <p v-if="metrics && !metrics.locales.length" class="muted">{{ s.metricsEmpty }}</p>
      <div v-else-if="metrics" class="table-wrap">
        <table class="table small" data-testid="ai-metrics">
          <thead>
            <tr>
              <th scope="col">{{ s.locale }}</th>
              <th scope="col">{{ s.acceptanceRate }}</th>
              <th scope="col">{{ s.acceptance }}</th>
              <th scope="col">{{ s.edited }}</th>
              <th scope="col">{{ s.rejected }}</th>
              <th scope="col">{{ s.editDistance }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="m in metrics.locales" :key="m.locale" :data-locale="m.locale">
              <th scope="row" class="mono">{{ m.locale }}</th>
              <td>
                <span class="rate">
                  <span class="track" aria-hidden="true"><span class="fill" :style="{ inlineSize: pct(m.acceptance_rate) }" /></span>
                  {{ pct(m.acceptance_rate) }}
                </span>
              </td>
              <td>{{ m.accepted }}</td>
              <td>{{ m.edited }}</td>
              <td>{{ m.rejected }}</td>
              <td>{{ m.mean_edit_distance.toFixed(1) }} ({{ pct(m.mean_edit_ratio) }})</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <div class="stack-sm">
      <h3>{{ s.disclosures }}</h3>
      <p class="hint">{{ s.disclosuresLead }}</p>
      <p v-if="!disclosures.length" class="muted">{{ s.disclosuresEmpty }}</p>
      <ul v-else class="disclosures" data-testid="disclosures">
        <li v-for="d in disclosures" :key="d.id">
          <details>
            <summary>
              {{ absoluteTime(d.occurred_at) }} · <span class="mono">{{ d.provider }}/{{ d.model }}</span> · {{ s.tasks[d.task] ?? d.task }} · {{ d.locale }}
            </summary>
            <div class="stack-sm sent">
              <p class="hint">{{ s.whatWasSent }} ({{ s.message }} <code>{{ d.message_id }}</code>)</p>
              <pre v-for="(m, i) in d.sent" :key="i" class="mono" tabindex="0">{{ m.role }}: {{ m.text }}</pre>
            </div>
          </details>
        </li>
      </ul>
      <div v-if="next">
        <button type="button" class="btn btn-sm" @click="more">{{ strings.review.loadMore }}</button>
      </div>
    </div>

    <div v-if="baseline" class="stack-sm">
      <h3>{{ s.baseline }}</h3>
      <p class="hint">{{ s.baselineLead }}</p>
      <div class="table-wrap">
        <table class="table small">
          <thead>
            <tr>
              <th scope="col">{{ s.pair }}</th>
              <th scope="col">{{ s.cases }}</th>
              <th scope="col">{{ s.structural }}</th>
              <th scope="col">{{ s.terminology }}</th>
              <th scope="col">{{ s.formality }}</th>
              <th scope="col">{{ s.editRatio }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="[pair, m] in Object.entries(baseline.pairs).sort((a, b) => a[0].localeCompare(b[0]))" :key="pair">
              <th scope="row" class="mono">{{ pair }}</th>
              <td>{{ m.cases }}</td>
              <td>{{ pct(m.structural_pass_rate) }}</td>
              <td>{{ pct(m.terminology_compliance) }}</td>
              <td>{{ pct(m.formality_compliance) }}</td>
              <td>{{ pct(m.mean_edit_ratio) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </section>
</template>

<style scoped>
.table-wrap {
  overflow-x: auto;
}
.small {
  font-size: var(--kl-text-sm);
}
.rate {
  display: inline-flex;
  align-items: center;
  gap: var(--kl-space-2);
}
.track {
  inline-size: 5rem;
  block-size: 0.5rem;
  border-radius: 4px;
  background: var(--kl-surface-muted);
  overflow: hidden;
}
.fill {
  display: block;
  block-size: 100%;
  background: var(--kl-accent);
  border-radius: 4px;
}
.disclosures {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-2);
}
.disclosures summary {
  cursor: pointer;
}
.sent pre {
  white-space: pre-wrap;
  margin: 0;
  padding: var(--kl-space-2);
  background: var(--kl-surface-muted);
  border-radius: var(--kl-radius-sm);
  max-block-size: 16rem;
  overflow: auto;
  font-size: var(--kl-text-sm);
}
</style>
