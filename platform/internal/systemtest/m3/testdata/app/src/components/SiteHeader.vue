<!-- Written by `go generate ./internal/systemtest/m3/...` — change the generator, not this file. -->
<script setup lang="ts">
import { GlossaText } from "@klarlabs-studio/glossa/vue";
</script>

<template>
  <nav class="chrome">
    <p class="line">{{ $t("layout.croissant.title") }}</p>
    <p class="line"><GlossaText id="layout.basket.label" /></p>
    <p class="line">{{ $t("layout.plait.title") }}</p>
    <p class="line">{{ $t("layout.sourdough.count", { count: 3 }) }}</p>
    <p class="line">{{ $t("layout.question.activity", { name: "Lina" }) }}</p>
    <p class="line">{{ $t("layout.salt.title") }}</p>
    <p class="line"><input class="field" :placeholder="$t('layout.grain.placeholder')" /></p>
    <p class="line"><GlossaText id="layout.account.label" /></p>
  </nav>
</template>
