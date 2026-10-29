<!-- Written by `go generate ./internal/systemtest/m3/...` — change the generator, not this file. -->
<script setup lang="ts">
import { GlossaText } from "@klarlabs-studio/glossa/vue";
</script>

<template>
  <footer class="chrome">
    <p class="line">{{ $t("layout.bread.title") }}</p>
    <p class="line"><GlossaText id="layout.cake.help" /></p>
    <p class="line">{{ $t("layout.croissant.activity", { name: "Lina" }) }}</p>
    <p class="line">{{ $t("layout.question.title") }}</p>
    <p class="line">{{ $t("layout.subscription.title") }}</p>
    <p class="line"><GlossaText id="layout.dough.label" /></p>
  </footer>
</template>
