<script setup lang="ts">
/** Concordance: how a phrase was translated before, searched in the source or target side of the memory. */
import { ref, shallowRef } from "vue";
import type { TMConcordance } from "../../api/knowledge-schemas";
import { useKnowledge } from "../../api/knowledge";
import type { ProjectLocale } from "../../api/schemas";
import { strings } from "../../strings";
import ErrorAlert from "../ErrorAlert.vue";

const props = defineProps<{ tenant: string; projectId: string; source: ProjectLocale; target: ProjectLocale }>();
const s = strings.concordance;
const port = useKnowledge();

const q = ref("");
const side = ref<"source" | "target">("source");
const result = shallowRef<TMConcordance>();
const error = ref<unknown>(null);
const busy = ref(false);

async function search(): Promise<void> {
  const phrase = q.value.trim();
  if (!phrase) return;
  busy.value = true;
  error.value = null;
  try {
    result.value = await port.concordance(props.tenant, {
      q: phrase,
      side: side.value,
      source_locale: props.source.code,
      target_locale: props.target.code,
      project: props.projectId,
      limit: 10,
    });
  } catch (e) {
    error.value = e;
  } finally {
    busy.value = false;
  }
}

/** Search for a phrase from elsewhere (a selection in the source, say). */
function searchFor(phrase: string): void {
  q.value = phrase;
  void search();
}
defineExpose({ searchFor });
</script>

<template>
  <section class="pane stack-sm" aria-labelledby="conc-h" data-testid="concordance">
    <h3 id="conc-h">{{ s.title }}</h3>
    <form class="row search" role="search" :aria-label="s.title" @submit.prevent="search">
      <label for="conc-q" class="visually-hidden">{{ s.phrase }}</label>
      <input id="conc-q" v-model="q" type="search" maxlength="200" :placeholder="s.placeholder" autocomplete="off" />
      <label for="conc-side" class="visually-hidden">{{ s.side }}</label>
      <select id="conc-side" v-model="side">
        <option value="source">{{ s.inSource(source.code) }}</option>
        <option value="target">{{ s.inTarget(target.code) }}</option>
      </select>
      <button type="submit" class="btn btn-sm" :disabled="busy || !q.trim()">{{ s.search }}</button>
    </form>
    <ErrorAlert :error="error" />
    <p v-if="result && !result.matches.length" class="muted" role="status">{{ s.none }}</p>
    <ul v-else-if="result" class="results" :aria-label="s.results">
      <li v-for="m in result.matches" :key="m.unit.id" class="stack-sm">
        <p :lang="source.code" :dir="source.direction">{{ m.unit.source }}</p>
        <p :lang="target.code" :dir="target.direction" class="target">{{ m.unit.target }}</p>
        <p class="hint">{{ s.similarity(m.similarity) }}<template v-if="m.unit.message_key"> · {{ m.unit.message_key }}</template></p>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.search input {
  flex: 1;
  min-inline-size: 8rem;
}
.results {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-3);
}
.results li {
  padding-block-end: var(--kl-space-2);
  border-block-end: 1px solid var(--kl-border);
}
.target {
  font-weight: var(--kl-weight-medium);
}
</style>
