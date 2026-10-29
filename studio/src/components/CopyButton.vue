<script setup lang="ts">
import { ref } from "vue";
import { strings } from "../strings";

const props = defineProps<{ text: string; label: string }>();
const s = strings.deliveryKeys;
const status = ref("");

async function copy(): Promise<void> {
  try {
    await navigator.clipboard.writeText(props.text);
    status.value = s.copied;
  } catch {
    status.value = s.copyFailed;
  }
}
</script>

<template>
  <span class="copy">
    <button type="button" class="btn btn-sm" @click="copy">
      {{ s.copy }}<span class="visually-hidden">{{ " " + label }}</span>
    </button>
    <span class="muted status" role="status">{{ status }}</span>
  </span>
</template>

<style scoped>
.copy {
  display: inline-flex;
  align-items: center;
  gap: var(--kl-space-2);
}
.status {
  font-size: var(--kl-text-sm);
}
</style>
