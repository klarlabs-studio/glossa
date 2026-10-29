<script setup lang="ts">
/**
 * The termbase terms in this source: each concept found, its definition,
 * and the target locale's terms — the ones to use first, then the ones
 * to avoid — plus the live check of the draft.
 */
import { computed } from "vue";
import type { TermFinding, TermHit, TermRecognition } from "../../api/knowledge-schemas";
import type { ProjectLocale } from "../../api/schemas";
import { allowed, byStatus, statusTone } from "../../lib/terms";
import { strings } from "../../strings";
import ErrorAlert from "../ErrorAlert.vue";

const props = defineProps<{
  recognition: TermRecognition | undefined;
  error: unknown;
  findings: TermFinding[];
  checkState: "idle" | "checking" | "stale";
  /** Whether there is a draft to check. */
  hasDraft: boolean;
  source: ProjectLocale;
  target: ProjectLocale;
}>();
const s = strings.terms;

/** One entry per concept, in text order. */
const concepts = computed(() => {
  const seen = new Map<string, TermHit>();
  for (const h of props.recognition?.hits ?? []) if (!seen.has(h.concept_id)) seen.set(h.concept_id, h);
  return [...seen.values()];
});
const flagged = (conceptId: string) => props.findings.some((f) => f.concept_id === conceptId);
</script>

<template>
  <section class="pane stack-sm" aria-labelledby="terms-h" data-testid="terms-pane">
    <h3 id="terms-h">{{ s.title }}</h3>
    <ErrorAlert :error="error" />
    <p v-if="!recognition && !error" class="muted" role="status">{{ strings.app.loading }}</p>
    <p v-else-if="recognition && !concepts.length" class="muted">{{ s.none }}</p>
    <ul v-else-if="recognition" class="concepts">
      <li v-for="h in concepts" :key="h.concept_id" class="concept stack-sm" :class="{ flagged: flagged(h.concept_id) }" data-testid="term-concept">
        <p>
          <strong :lang="source.code" :dir="source.direction">{{ h.text }}</strong>
          <span class="pill" :class="`pill-${statusTone(h.term.status)}`">{{ s.status[h.term.status] }}</span>
        </p>
        <p v-if="h.definition" class="hint">{{ h.definition }}</p>
        <ul v-if="h.targets?.length" class="targets" :aria-label="s.targetTerms(target.code)">
          <li v-for="t in byStatus(h.targets)" :key="t.id" :class="{ avoid: !allowed(t.status) }">
            <span :lang="target.code" :dir="target.direction">{{ t.text }}</span>
            <span class="pill" :class="`pill-${statusTone(t.status)}`">{{ allowed(t.status) ? s.status[t.status] : s.avoid(s.status[t.status] ?? t.status) }}</span>
            <span v-if="t.note" class="hint">{{ t.note }}</span>
          </li>
        </ul>
        <p v-else class="hint">{{ s.noTarget(target.code) }}</p>
      </li>
    </ul>
    <p v-if="checkState === 'stale'" class="hint" role="status">{{ s.checkStale }}</p>
    <p v-else-if="hasDraft && recognition && concepts.length && !findings.length && checkState === 'idle'" class="hint ok" role="status">{{ s.checkClean }}</p>
  </section>
</template>

<style scoped>
.concepts,
.targets {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-2);
}
.concept {
  padding: var(--kl-space-3);
  border: 1px solid var(--kl-border);
  border-radius: var(--kl-radius-md);
  background: var(--kl-surface);
}
.concept.flagged {
  border-color: var(--gs-warn);
}
.concept p {
  display: flex;
  gap: var(--kl-space-2);
  align-items: baseline;
  flex-wrap: wrap;
}
.targets li {
  display: flex;
  gap: var(--kl-space-2);
  align-items: baseline;
  flex-wrap: wrap;
}
.avoid > span:first-child {
  text-decoration: line-through;
  text-decoration-color: var(--gs-err);
}
.ok {
  color: var(--gs-ok);
}
</style>
