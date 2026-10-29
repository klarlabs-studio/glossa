<!-- Written by `go generate ./internal/systemtest/m3/...` — change the generator, not this file. -->
<script setup lang="ts">
import { GlossaText } from "@klarlabs-studio/glossa/vue";
</script>

<template>
  <section class="page">
    <h1>Rechnung</h1>
    <p class="line"><input class="field" :placeholder="$t('invoice.croissant.placeholder')" /></p>
    <p class="line"><GlossaText id="invoice.biscuit.label" /></p>
    <p class="line">{{ $t("invoice.address.title") }}</p>
    <p class="line"><GlossaText id="invoice.branch.help" /></p>
    <p class="line">{{ $t("invoice.sourdough.activity", { name: "Lina" }) }}</p>
    <p class="line">{{ $t("invoice.account.title") }}</p>
    <p class="line">{{ $t("invoice.basket.title") }}</p>
    <p class="line"><GlossaText id="invoice.flour.label" /></p>
    <p class="line">{{ $t("invoice.branch.title") }}</p>
    <p class="line">{{ $t("invoice.crust.count", { count: 3 }) }}</p>
    <p class="line">{{ $t("invoice.oven.activity", { name: "Lina" }) }}</p>
    <p class="line">{{ $t("invoice.salt.title") }}</p>
  </section>
</template>
