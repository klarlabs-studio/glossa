<script setup lang="ts">
/**
 * A modal on the native <dialog>: the browser traps focus, makes the page
 * inert, closes on Escape and returns focus to the opener. Content renders
 * only while open, so each opening starts fresh.
 */
import { nextTick, useId, useTemplateRef, watch } from "vue";

const props = defineProps<{ open: boolean; title: string; wide?: boolean }>();
const emit = defineEmits<{ close: [] }>();
const id = useId();
const dialog = useTemplateRef<HTMLDialogElement>("dialog");

watch(
  () => props.open,
  async (isOpen) => {
    await nextTick();
    const d = dialog.value;
    if (!d) return;
    if (isOpen && !d.open) d.showModal?.();
    if (!isOpen && d.open) d.close?.();
  },
  { immediate: true },
);
</script>

<template>
  <dialog ref="dialog" class="dialog modal" :class="{ wide }" :aria-labelledby="id" @close="emit('close')" @cancel.prevent="emit('close')">
    <div v-if="open" class="stack">
      <h2 :id="id">{{ title }}</h2>
      <slot />
      <div v-if="$slots.actions" class="row actions">
        <slot name="actions" />
      </div>
    </div>
  </dialog>
</template>

<style scoped>
.modal {
  inline-size: 36rem;
}
.modal.wide {
  inline-size: 48rem;
}
.actions {
  justify-content: flex-end;
  padding-block-start: var(--kl-space-2);
}
</style>
