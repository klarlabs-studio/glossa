<script setup lang="ts">
import { computed } from "vue";
import { diffWords } from "../lib/diff";

const props = defineProps<{ before: string; after: string; lang: string; dir: string }>();
const segments = computed(() => diffWords(props.before, props.after));
</script>

<template>
  <p class="diff" :lang="lang" :dir="dir">
    <template v-for="(s, i) in segments" :key="i">
      <del v-if="s.op === 'delete'">{{ s.text }}</del>
      <ins v-else-if="s.op === 'insert'">{{ s.text }}</ins>
      <span v-else>{{ s.text }}</span>
    </template>
  </p>
</template>

<style scoped>
.diff {
  white-space: pre-wrap;
  line-height: var(--kl-leading-normal);
}
del {
  color: var(--gs-err);
  background: var(--gs-err-bg);
  text-decoration: line-through;
}
ins {
  color: var(--gs-ok);
  background: var(--gs-ok-bg);
  text-decoration: underline;
}
</style>
