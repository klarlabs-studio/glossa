<script setup lang="ts">
/** Terminology QA on the draft, live: forbidden or deprecated terms used, preferred terms missing. */
import type { TermFinding } from "../../api/knowledge-schemas";
import { strings } from "../../strings";

defineProps<{ id: string; findings: TermFinding[] }>();
const s = strings.terms;
</script>

<template>
  <div :id="id" class="alert" :class="findings.some((f) => f.severity === 'error') ? 'alert-error' : 'alert-warn'" role="status" data-testid="term-findings">
    <p class="alert-title">{{ s.findingsTitle }}</p>
    <ul class="findings">
      <li v-for="(f, i) in findings" :key="`${f.code}-${f.term_id}-${i}`">
        <span class="pill" :class="f.severity === 'error' ? 'pill-err' : 'pill-warn'">{{ f.severity === "error" ? s.severity.error : s.severity.warning }}</span>
        <span>{{ f.code === "term_forbidden" ? s.forbiddenUsed(f.text) : s.missingTerm(f.text) }}</span>
        <span v-if="f.suggestions.length" class="muted">{{ s.useInstead(f.suggestions) }}</span>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.findings {
  list-style: none;
  margin: var(--kl-space-2) 0 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-1);
}
.findings li {
  display: flex;
  flex-wrap: wrap;
  gap: var(--kl-space-2);
  align-items: baseline;
}
</style>
