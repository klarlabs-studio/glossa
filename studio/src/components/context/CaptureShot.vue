<script setup lang="ts">
/**
 * One capture with the message outlined and its surroundings dimmed
 * (RFC 0004 §3.4). `crop` shows the window around the message's regions,
 * `page` the whole page, scrolled to the message. Regions are CSS-pixel
 * boxes relative to the full-page image, so everything is placed in
 * percentages of the window and scales with whatever width it gets.
 *
 * The image comes from the API on Studio's own origin, so the session
 * cookie travels with the request and the browser caches it (the image
 * is content-addressed and immutable).
 */
import { computed, nextTick, onMounted, useTemplateRef } from "vue";
import type { MessageCapture } from "../../api/context-schemas";
import { aspectRatio, boxStyle, cropFor, imageStyle, pageSize, visibleRegions } from "../../lib/context";

const props = defineProps<{ capture: MessageCapture; fit: "crop" | "page"; alt: string }>();

const page = computed(() => pageSize(props.capture));
const crop = computed(() => (props.fit === "crop" ? (cropFor(props.capture) ?? page.value) : page.value));
const regions = computed(() => visibleRegions(props.capture.regions));
/** One dimming layer for all of them: a hole over the message, the rest shaded. */
const spotlight = computed(() => {
  const boxes = regions.value.map((r) => r.box);
  if (!boxes.length) return undefined;
  const x = Math.min(...boxes.map((b) => b.x));
  const y = Math.min(...boxes.map((b) => b.y));
  return {
    x,
    y,
    width: Math.max(...boxes.map((b) => b.x + b.width)) - x,
    height: Math.max(...boxes.map((b) => b.y + b.height)) - y,
  };
});

const scroller = useTemplateRef<HTMLDivElement>("scroller");
onMounted(async () => {
  // The full page opens at the message, not at the top of a long screenshot.
  await nextTick();
  const el = scroller.value;
  const box = regions.value[0]?.box;
  if (!el || !box || props.fit !== "page") return;
  el.scrollTop = Math.max(0, (box.y / page.value.height) * el.scrollHeight - el.clientHeight / 2);
});
</script>

<template>
  <div ref="scroller" class="scroller" :class="fit">
    <div class="shot" :style="{ aspectRatio: aspectRatio(crop) }" data-testid="capture-shot">
      <img class="page" :src="capture.image.url" :alt="alt" :style="imageStyle(crop, capture)" decoding="async" />
      <div v-if="spotlight" class="dim" :style="boxStyle(spotlight, crop)" aria-hidden="true" />
      <div v-for="(r, i) in regions" :key="i" class="region" :style="boxStyle(r.box, crop)" aria-hidden="true" data-testid="capture-region" />
    </div>
  </div>
</template>

<style scoped>
.scroller {
  background: var(--kl-surface-muted);
}
.scroller.page {
  max-block-size: 65vh;
  overflow: auto;
  overscroll-behavior: contain;
}
.shot {
  position: relative;
  overflow: hidden;
  inline-size: 100%;
}
.page {
  position: absolute;
  block-size: auto;
  max-inline-size: none;
  image-rendering: auto;
}
.dim {
  position: absolute;
  /* The shade covers everything but the message's own box. */
  box-shadow: 0 0 0 100vmax rgb(0 0 0 / 55%);
}
.region {
  position: absolute;
  outline: 2px solid var(--kl-accent);
  outline-offset: 1px;
  border-radius: 2px;
}
</style>
