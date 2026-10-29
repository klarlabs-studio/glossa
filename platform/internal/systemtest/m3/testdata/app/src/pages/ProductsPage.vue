<!-- Written by `go generate ./internal/systemtest/m3/...` — change the generator, not this file. -->
<script setup lang="ts">
import { GlossaText } from "@klarlabs-studio/glossa/vue";
</script>

<template>
  <section class="page">
    <h1>Sortiment</h1>
    <p class="line">{{ $t("products.bun.title") }}</p>
    <p class="line"><GlossaText id="products.bagel.help" /></p>
    <p class="line">{{ $t("products.salt.activity", { name: "Lina" }) }}</p>
    <p class="line">{{ $t("products.salt.title") }}</p>
    <p class="line">{{ $t("products.flour.title") }}</p>
    <p class="line"><GlossaText id="products.pickup.label" /></p>
    <p class="line">{{ $t("products.address.title") }}</p>
    <p class="line">{{ $t("products.tart.count", { count: 3 }) }}</p>
    <p class="line">{{ $t("products.question.activity", { name: "Lina" }) }}</p>
    <p class="line">{{ $t("products.cake.title") }}</p>
    <p class="line"><input class="field" :placeholder="$t('products.voucher.placeholder')" /></p>
    <p class="line"><GlossaText id="products.stollen.label" /></p>
    <p class="line">{{ $t("products.basket.title") }}</p>
    <p class="line"><GlossaText id="products.basket.help" /></p>
    <p class="line">{{ $t("products.subscription.activity", { name: "Lina" }) }}</p>
    <p class="line">{{ $t("products.question.title") }}</p>
    <p class="line">{{ $t("products.sourdough.title") }}</p>
    <p class="line"><GlossaText id="products.tray.label" /></p>
    <p class="line">{{ $t("products.order.title") }}</p>
    <p class="line">{{ $t("products.flour.count", { count: 3 }) }}</p>
  </section>
</template>
