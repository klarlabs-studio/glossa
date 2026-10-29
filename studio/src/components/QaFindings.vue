<script setup lang="ts">
import type { QAFinding } from "../api/schemas";

defineProps<{ id: string; title: string; findings: QAFinding[]; tone: "error" | "warn" }>();
</script>

<template>
  <div :id="id" class="alert" :class="tone === 'error' ? 'alert-error' : 'alert-warn'" :role="tone === 'error' ? 'alert' : 'status'">
    <p class="alert-title">{{ title }}</p>
    <ul class="findings">
      <li v-for="(f, i) in findings" :key="`${f.code}-${i}`">
        <span class="pill" :class="f.severity === 'error' ? 'pill-err' : 'pill-warn'">{{ f.severity }}</span>
        <code class="code">{{ f.code }}</code>
        <span v-if="f.subject" class="subject">— <code>{{ f.subject }}</code></span>
        <span class="msg">{{ f.message }}</span>
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
  gap: var(--kl-space-2);
}
.findings li {
  display: flex;
  flex-wrap: wrap;
  gap: var(--kl-space-2);
  align-items: baseline;
}
.msg {
  flex-basis: 100%;
}
</style>
