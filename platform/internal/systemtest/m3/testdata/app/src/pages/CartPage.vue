<!-- Written by `go generate ./internal/systemtest/m3/...` — change the generator, not this file. -->
<script setup lang="ts">
import { GlossaText } from "@klarlabs-studio/glossa/vue";
</script>

<template>
  <section class="page">
    <h1>Warenkorb</h1>
    <p class="line">{{ $t("cart.order.activity", { name: "Lina" }) }}</p>
    <p class="line">{{ $t("cart.order.title") }}</p>
    <p class="line">{{ $t("cart.bagel.title") }}</p>
    <p class="line"><GlossaText id="cart.croissant.label" /></p>
    <p class="line">{{ $t("cart.subscription.title") }}</p>
    <p class="line">{{ $t("cart.flour.count", { count: 3 }) }}</p>
    <p class="line">{{ $t("cart.plait.activity", { name: "Lina" }) }}</p>
    <p class="line">{{ $t("cart.invoice.title") }}</p>
    <p class="line"><input class="field" :placeholder="$t('cart.oven.placeholder')" /></p>
    <p class="line"><GlossaText id="cart.invoice.label" /></p>
    <p class="line">{{ $t("cart.basket.title") }}</p>
    <p class="line"><GlossaText id="cart.review.help" /></p>
    <p class="line">{{ $t("cart.branch.activity", { name: "Lina" }) }}</p>
    <p class="line">{{ $t("cart.flour.title") }}</p>
    <p class="line">{{ $t("cart.shipping.title") }}</p>
    <p class="line"><GlossaText id="cart.bread.label" /></p>
    <p class="line">{{ $t("cart.tray.title") }}</p>
    <p class="line">{{ $t("cart.tart.count", { count: 3 }) }}</p>
  </section>
</template>
