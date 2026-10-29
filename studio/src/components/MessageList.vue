<script setup lang="ts">
/**
 * A virtualised listbox of messages. Only the rows in view are rendered;
 * the active row is always among them (it is scrolled into view first), so
 * `aria-activedescendant` never points at a missing element.
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useTemplateRef, watch } from "vue";
import { scrollTopFor, visibleRange } from "../lib/virtual";
import type { MessageRow } from "../lib/workspace";
import { strings } from "../strings";

/** Row height in px; the CSS below uses the same value. */
const ROW_PX = 56;

const props = defineProps<{ rows: MessageRow[]; active: number; label: string; sourceLang: string; sourceDir: string }>();
const emit = defineEmits<{ select: [index: number] }>();

const box = useTemplateRef<HTMLDivElement>("box");
const scrollTop = ref(0);
const viewport = ref(600);
let observer: ResizeObserver | undefined;

onMounted(() => {
  const el = box.value!;
  viewport.value = el.clientHeight || 600;
  observer = typeof ResizeObserver === "function" ? new ResizeObserver(() => (viewport.value = el.clientHeight || viewport.value)) : undefined;
  observer?.observe(el);
});
onBeforeUnmount(() => observer?.disconnect());

const range = computed(() => visibleRange(scrollTop.value, viewport.value, ROW_PX, props.rows.length));
const windowRows = computed(() => props.rows.slice(range.value.start, range.value.end).map((row, i) => ({ row, index: range.value.start + i })));
const optionId = (i: number) => `message-option-${i}`;
const activeId = computed(() => (props.active >= 0 && props.active < props.rows.length ? optionId(props.active) : undefined));

watch(
  () => props.active,
  async (i) => {
    const el = box.value;
    if (!el || i < 0) return;
    const top = scrollTopFor(i, el.scrollTop, el.clientHeight || viewport.value, ROW_PX);
    if (top !== el.scrollTop) {
      el.scrollTop = top;
      scrollTop.value = top;
      await nextTick();
    }
  },
);

function onKey(e: KeyboardEvent): void {
  const last = props.rows.length - 1;
  const page = Math.max(1, Math.floor(viewport.value / ROW_PX) - 1);
  const to: Record<string, number> = {
    ArrowDown: Math.min(last, props.active + 1),
    ArrowUp: Math.max(0, props.active - 1),
    Home: 0,
    End: last,
    PageDown: Math.min(last, props.active + page),
    PageUp: Math.max(0, props.active - page),
  };
  const next = to[e.key];
  if (next === undefined || last < 0) return;
  e.preventDefault();
  emit("select", next);
}

const statusLabel = (s: MessageRow["status"]) => strings.workspace.status[s];

function focus(): void {
  box.value?.focus();
}
defineExpose({ focus });
</script>

<template>
  <div
    ref="box"
    class="list"
    role="listbox"
    tabindex="0"
    :aria-label="label"
    :aria-activedescendant="activeId"
    @scroll="scrollTop = ($event.target as HTMLElement).scrollTop"
    @keydown="onKey"
  >
    <div class="spacer" :style="{ height: `${rows.length * ROW_PX}px` }">
      <div
        v-for="{ row, index } in windowRows"
        :id="optionId(index)"
        :key="row.key"
        role="option"
        class="option"
        :class="{ active: index === active }"
        :aria-selected="index === active"
        :aria-setsize="rows.length"
        :aria-posinset="index + 1"
        :style="{ transform: `translateY(${index * ROW_PX}px)` }"
        @click="emit('select', index)"
      >
        <span class="line1">
          <code class="key">{{ row.key }}</code>
          <span v-if="row.status !== 'unknown' && row.status !== 'translated'" class="status" :class="`status-${row.status}`">{{ statusLabel(row.status) }}</span>
        </span>
        <span class="text" :lang="sourceLang" :dir="sourceDir">{{ row.text }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.list {
  position: relative;
  overflow-y: auto;
  overscroll-behavior: contain;
  block-size: 100%;
  contain: strict;
}
.list:focus-visible {
  outline-offset: -2px;
}
.spacer {
  position: relative;
}
.option {
  position: absolute;
  inset-inline: 0;
  inset-block-start: 0;
  block-size: 56px;
  padding: 0.45rem var(--kl-space-4);
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 2px;
  border-block-end: 1px solid var(--kl-border);
  border-inline-start: 3px solid transparent;
  cursor: pointer;
  overflow: hidden;
}
.option:hover {
  background: var(--kl-surface-muted);
}
.option.active {
  background: var(--kl-accent-dim);
  border-inline-start-color: var(--kl-accent);
}
.line1 {
  display: flex;
  align-items: center;
  gap: var(--kl-space-2);
  min-inline-size: 0;
}
.key {
  font-size: var(--kl-text-sm);
  color: var(--kl-ink);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.text {
  color: var(--kl-ink-secondary);
  font-size: var(--kl-text-sm);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.status {
  font-size: 0.7rem;
  font-weight: var(--kl-weight-semibold);
  padding: 0 0.4em;
  border-radius: var(--kl-radius-sm);
  flex: none;
}
.status-missing {
  color: var(--gs-err);
  background: var(--gs-err-bg);
}
.status-outdated {
  color: var(--gs-warn);
  background: var(--gs-warn-bg);
}
</style>
