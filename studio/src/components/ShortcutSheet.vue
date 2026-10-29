<script lang="ts">
import { ref } from "vue";

const open = ref(false);

/** Open the keyboard sheet from anywhere (the `?` key or the top bar). */
export function showShortcutSheet(): void {
  open.value = true;
}
</script>

<script setup lang="ts">
import { computed, nextTick, useTemplateRef, watch } from "vue";
import { keyLabel, SHORTCUTS, useShortcuts } from "../lib/shortcuts";
import { strings } from "../strings";

const dialog = useTemplateRef<HTMLDialogElement>("dialog");

const groups = computed(() => {
  const out = new Map<string, typeof SHORTCUTS[number][]>();
  for (const s of SHORTCUTS) out.set(s.context, [...(out.get(s.context) ?? []), s]);
  return [...out];
});

useShortcuts({
  help: () => {
    open.value = !open.value;
  },
});

watch(open, async (isOpen) => {
  await nextTick();
  const d = dialog.value;
  if (!d) return;
  if (isOpen && !d.open) d.showModal?.();
  if (!isOpen && d.open) d.close?.();
});
</script>

<template>
  <dialog ref="dialog" class="dialog sheet" aria-labelledby="shortcut-title" @close="open = false" @cancel="open = false">
    <div class="stack">
      <div class="row">
        <h2 id="shortcut-title">{{ strings.shortcuts.title }}</h2>
        <span class="spacer" />
        <button type="button" class="btn btn-ghost btn-sm" @click="open = false">{{ strings.app.close }}</button>
      </div>
      <p class="muted">{{ strings.shortcuts.lead }}</p>
      <section v-for="[context, list] in groups" :key="context" class="stack-sm">
        <h3>{{ context }}</h3>
        <dl class="keys">
          <template v-for="s in list" :key="s.id">
            <dt>
              <template v-for="(k, i) in s.keys" :key="k">
                <span v-if="i > 0" aria-hidden="true"> + </span><kbd>{{ keyLabel(k) }}</kbd>
              </template>
            </dt>
            <dd>{{ s.description }}</dd>
          </template>
        </dl>
      </section>
    </div>
  </dialog>
</template>

<style scoped>
.sheet {
  inline-size: 34rem;
}
.keys {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: var(--kl-space-2) var(--kl-space-4);
  margin: 0;
  align-items: center;
}
.keys dd {
  margin: 0;
}
</style>
