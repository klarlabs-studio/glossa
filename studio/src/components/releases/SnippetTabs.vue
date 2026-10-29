<script setup lang="ts">
/** Integration snippets as WAI-ARIA tabs: arrows, Home and End move between runtimes. */
import { nextTick, ref, useId } from "vue";
import type { Snippet } from "../../lib/snippets";
import { strings } from "../../strings";
import CopyButton from "../CopyButton.vue";

const props = defineProps<{ snippets: Snippet[]; label: string }>();
const s = strings.deliveryKeys;
const uid = useId();
const active = ref(0);
const tabId = (i: number) => `${uid}-tab-${i}`;
const panelId = (i: number) => `${uid}-panel-${i}`;

async function onKey(e: KeyboardEvent): Promise<void> {
  const n = props.snippets.length;
  const moves: Record<string, number> = { ArrowRight: active.value + 1, ArrowLeft: active.value - 1 + n, Home: 0, End: n - 1 };
  const to = moves[e.key];
  if (to === undefined) return;
  e.preventDefault();
  active.value = to % n;
  await nextTick();
  document.getElementById(tabId(active.value))?.focus();
}
</script>

<template>
  <div class="snippets">
    <div class="tabs" role="tablist" :aria-label="label" @keydown="onKey">
      <button
        v-for="(sn, i) in snippets"
        :id="tabId(i)"
        :key="sn.id"
        type="button"
        role="tab"
        class="tab"
        :aria-selected="i === active"
        :aria-controls="panelId(i)"
        :tabindex="i === active ? 0 : -1"
        @click="active = i"
      >
        {{ sn.label }}
      </button>
    </div>
    <div
      v-for="(sn, i) in snippets"
      v-show="i === active"
      :id="panelId(i)"
      :key="sn.id"
      role="tabpanel"
      :aria-labelledby="tabId(i)"
      class="panel stack-sm"
      :data-testid="`snippet-${sn.id}`"
    >
      <div class="row">
        <span class="label">{{ s.install }}</span>
        <code class="install">{{ sn.install }}</code>
      </div>
      <pre tabindex="0" role="region" :aria-label="s.snippetOf(sn.label)"><code>{{ sn.code }}</code></pre>
      <div><CopyButton :text="sn.code" :label="sn.label" /></div>
    </div>
  </div>
</template>

<style scoped>
.tabs {
  display: flex;
  flex-wrap: wrap;
  border-block-end: 1px solid var(--kl-border);
}
.tab {
  font: inherit;
  background: none;
  border: none;
  border-block-end: 2px solid transparent;
  margin-block-end: -1px;
  padding: var(--kl-space-2) var(--kl-space-3);
  color: var(--kl-ink-secondary);
  cursor: pointer;
}
.tab[aria-selected="true"] {
  color: var(--kl-ink);
  border-block-end-color: var(--kl-accent);
}
.panel {
  padding-block-start: var(--kl-space-3);
}
.install {
  overflow-wrap: anywhere;
}
pre {
  margin: 0;
  padding: var(--kl-space-3) var(--kl-space-4);
  background: var(--kl-surface-muted);
  color: var(--kl-ink);
  border: 1px solid var(--kl-border);
  border-radius: var(--kl-radius-md);
  overflow-x: auto;
  font-family: var(--kl-font-mono);
  font-size: var(--kl-text-sm);
  line-height: var(--kl-leading-normal);
  tab-size: 4;
}
pre code {
  font-size: inherit;
}
</style>
