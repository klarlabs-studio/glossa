<!-- Written by `go generate ./internal/systemtest/m3/...` — change the generator, not this file. -->
<script setup lang="ts">
import { GlossaText } from "@klarlabs-studio/glossa/vue";
</script>

<template>
  <section class="page">
    <h1>Produkt</h1>
    <p class="line">{{ $t("product.flour.activity", { name: "Lina" }) }}</p>
    <p class="line">{{ $t("product.address.title") }}</p>
    <p class="line"><input class="field" :placeholder="$t('product.invoice.placeholder')" /></p>
    <p class="line"><GlossaText id="product.cake.label" /></p>
    <p class="line">{{ $t("product.cake.title") }}</p>
    <p class="line"><GlossaText id="product.cake.help" /></p>
    <p class="line">{{ $t("product.invoice.activity", { name: "Lina" }) }}</p>
    <p class="line">{{ $t("product.croissant.title") }}</p>
    <p class="line">{{ $t("product.review.title") }}</p>
    <p class="line"><GlossaText id="product.subscription.label" /></p>
    <p class="line">{{ $t("product.basket.title") }}</p>
    <p class="line">{{ $t("product.cake.count", { count: 3 }) }}</p>
    <p class="line">{{ $t("product.basket.activity", { name: "Lina" }) }}</p>
    <p class="line">{{ $t("product.plait.title") }}</p>
    <p class="line"><input class="field" :placeholder="$t('product.review.placeholder')" /></p>
    <p class="line"><GlossaText id="product.order.label" /></p>
    <p class="line">{{ $t("product.question.title") }}</p>
    <p class="line"><GlossaText id="product.oven.help" /></p>
  </section>
</template>
