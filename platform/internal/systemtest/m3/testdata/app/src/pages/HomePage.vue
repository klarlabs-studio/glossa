<!-- Written by `go generate ./internal/systemtest/m3/...` — change the generator, not this file. -->
<script setup lang="ts">
import { GlossaText } from "@klarlabs-studio/glossa/vue";
</script>

<template>
  <section class="page">
    <h1>Brotwerk</h1>
    <p class="line">{{ $t("home.allergen.title") }}</p>
    <p class="line">{{ $t("home.tray.count", { count: 3 }) }}</p>
    <p class="line">{{ $t("home.ingredient.activity", { name: "Lina" }) }}</p>
    <p class="line">{{ $t("home.oven.title") }}</p>
    <p class="line"><input class="field" :placeholder="$t('home.flour.placeholder')" /></p>
    <p class="line"><GlossaText id="home.cake.label" /></p>
    <p class="line">{{ $t("home.voucher.title") }}</p>
    <p class="line"><GlossaText id="home.tray.help" /></p>
    <p class="line">{{ $t("home.yeast.activity", { name: "Lina" }) }}</p>
    <p class="line">{{ $t("home.branch.title") }}</p>
    <p class="line">{{ $t("home.review.title") }}</p>
    <p class="line"><GlossaText id="home.tart.label" /></p>
    <p class="line">{{ $t("home.croissant.title") }}</p>
    <p class="line">{{ $t("home.bagel.count", { count: 3 }) }}</p>
    <p class="line">{{ $t("home.grain.activity", { name: "Lina" }) }}</p>
    <p class="line">{{ $t("home.basket.title") }}</p>
    <p class="line"><input class="field" :placeholder="$t('home.stollen.placeholder')" /></p>
    <p class="line"><GlossaText id="home.delivery.label" /></p>
  </section>
</template>
