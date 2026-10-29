<script setup lang="ts">
import type { TranslationRevision } from "../api/schemas";
import { principalLabel, strings } from "../strings";
import { stateTone } from "../lib/review";

defineProps<{ revisions: TranslationRevision[]; lang: string; dir: string; selfId?: string | undefined }>();
const s = strings.workspace;

function detail(d: Record<string, unknown>): string {
  return Object.entries(d)
    .map(([k, v]) => `${k}: ${typeof v === "string" ? v : JSON.stringify(v)}`)
    .join(" · ");
}
</script>

<template>
  <p v-if="revisions.length === 0" class="muted">{{ s.historyEmpty }}</p>
  <ol v-else class="history" :aria-label="s.history">
    <li v-for="r in revisions" :key="r.revision" class="rev">
      <div class="head">
        <strong>{{ s.revision(r.revision) }}</strong>
        <span class="pill pill-neutral">{{ s.kind[r.kind] }}</span>
        <span class="pill" :class="`pill-${stateTone(r.state)}`">{{ s.stateLabel[r.state] }}</span>
        <span v-if="r.self_review" class="pill pill-warn" data-testid="self-review">{{ s.selfApproved }}</span>
        <span class="muted">{{ r.origin }}</span>
      </div>
      <p v-if="r.kind === 'content'" class="text" :lang="lang" :dir="dir">{{ r.text }}</p>
      <p class="meta muted">
        {{ s.by(principalLabel(r.author, selfId)) }} ·
        <time :datetime="r.created_at">{{ new Date(r.created_at).toLocaleString() }}</time> ·
        {{ s.madeAgainst(r.source_revision) }}
        <template v-if="Object.keys(r.origin_detail).length"> · {{ detail(r.origin_detail) }}</template>
      </p>
      <p v-if="r.findings.length" class="meta">
        <span v-for="f in r.findings" :key="f.code" class="pill" :class="f.severity === 'error' ? 'pill-err' : 'pill-warn'">{{ f.code }}</span>
      </p>
    </li>
  </ol>
</template>

<style scoped>
.history {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
}
.rev {
  padding: var(--kl-space-3) 0;
  border-block-end: 1px solid var(--kl-border);
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-1);
}
.head {
  display: flex;
  flex-wrap: wrap;
  gap: var(--kl-space-2);
  align-items: center;
}
.text {
  white-space: pre-wrap;
}
.meta {
  font-size: var(--kl-text-sm);
  display: flex;
  flex-wrap: wrap;
  gap: var(--kl-space-1);
}
</style>
