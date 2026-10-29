<script setup lang="ts">
/** The style that applies here: every guide from tenant to namespace merged, the narrowest winning (a summary). */
import { computed, onBeforeUnmount, ref, shallowRef, watch } from "vue";
import type { EffectiveStyleGuide } from "../../api/knowledge-schemas";
import { useKnowledge } from "../../api/knowledge";
import { styleSummary } from "../../lib/style";
import { strings } from "../../strings";
import ErrorAlert from "../ErrorAlert.vue";

const props = defineProps<{ tenant: string; projectId: string; locale: string; namespace: string }>();
const s = strings.style;
const port = useKnowledge();

const effective = shallowRef<EffectiveStyleGuide>();
const error = ref<unknown>(null);
let abort: AbortController | undefined;

watch(
  () => [props.locale, props.namespace, props.projectId],
  async () => {
    abort?.abort();
    const a = (abort = new AbortController());
    error.value = null;
    effective.value = undefined;
    try {
      const r = await port.effectiveStyle(props.tenant, { project: props.projectId, locale: props.locale, namespace: props.namespace }, a.signal);
      if (!a.signal.aborted) effective.value = r;
    } catch (e) {
      if (!a.signal.aborted) error.value = e;
    }
  },
  { immediate: true },
);
onBeforeUnmount(() => abort?.abort());

const lines = computed(() => (effective.value ? styleSummary(effective.value.fields, s.summary) : []));
const rules = computed(() => (effective.value?.rules ?? []).filter((r) => !r.disabled));
</script>

<template>
  <section class="pane stack-sm" aria-labelledby="style-h" data-testid="style-pane">
    <h3 id="style-h">{{ s.paneTitle(locale, namespace) }}</h3>
    <ErrorAlert :error="error" />
    <p v-if="!effective && !error" class="muted" role="status">{{ strings.app.loading }}</p>
    <p v-else-if="effective && !lines.length && !rules.length" class="muted">{{ s.none }}</p>
    <template v-else-if="effective">
      <dl class="summary">
        <template v-for="l in lines" :key="l.label">
          <dt>{{ l.label }}</dt>
          <dd>{{ l.value }}</dd>
        </template>
      </dl>
      <ul v-if="rules.length" class="rules" :aria-label="s.rules">
        <li v-for="r in rules" :key="r.id">
          <details>
            <summary>{{ r.title || r.id }}</summary>
            <div class="stack-sm rule">
              <p v-if="r.rationale" class="hint">{{ r.rationale }}</p>
              <p v-for="(g, i) in r.good ?? []" :key="`g${i}`" class="ex ex-good" :lang="locale"><span class="visually-hidden">{{ s.good }}: </span>{{ g }}</p>
              <p v-for="(b, i) in r.bad ?? []" :key="`b${i}`" class="ex ex-bad" :lang="locale"><span class="visually-hidden">{{ s.bad }}: </span>{{ b }}</p>
            </div>
          </details>
        </li>
      </ul>
      <p class="hint">{{ s.sources(effective.sources.length) }}</p>
    </template>
  </section>
</template>

<style scoped>
.summary {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: var(--kl-space-1) var(--kl-space-3);
  margin: 0;
}
.summary dt {
  color: var(--kl-ink-secondary);
  font-size: var(--kl-text-sm);
}
.summary dd {
  margin: 0;
}
.rules {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-1);
}
.rules summary {
  cursor: pointer;
}
.rule {
  padding: var(--kl-space-2) 0 var(--kl-space-2) var(--kl-space-4);
}
.ex {
  padding-inline-start: var(--kl-space-2);
  border-inline-start: 3px solid;
}
.ex-good {
  border-color: var(--gs-ok);
}
.ex-bad {
  border-color: var(--gs-err);
  text-decoration: line-through;
  text-decoration-color: var(--gs-err);
}
</style>
