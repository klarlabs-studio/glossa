<script setup lang="ts">
import { GlossaText, useGlossa } from "@klarlabs-studio/glossa/vue";

const props = defineProps<{ dynamicId: string; section: string }>();
const { t } = useGlossa();
// const unused = t("fake.script-comment");
const hint = "t('fake.script-string')";
</script>

<template>
  <section :title="hint">
    <!-- <GlossaText id="fake.html-comment" /> {{ $t("fake.html-comment-call") }} -->
    <GlossaText :id="props.dynamicId" />
    <GlossaText :id="`nav.${props.section}`" />
    <p>Use t("fake.template-text") in your code.</p>
    <p>{{ $t(props.dynamicId) }}</p>
    <glossa-text id="hero"></glossa-text>
    <p>{{ t("nav.home") }}</p>
  </section>
</template>
