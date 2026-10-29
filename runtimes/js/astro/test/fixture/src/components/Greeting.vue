<script setup lang="ts">
import { GlossaText, useGlossa } from "@klarlabs-studio/glossa-vue";

const props = defineProps<{ name: string }>();
const { t, locale } = useGlossa();
</script>

<template>
  <section class="island" :lang="locale">
    <p class="t">{{ t("greeting", { name: props.name }) }}</p>
    <p class="c"><GlossaText id="terms">AGB</GlossaText></p>
    <p class="e"><glossa-text message="home.title">Startseite</glossa-text></p>
  </section>
</template>
