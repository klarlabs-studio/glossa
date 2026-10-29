<!-- Written by `go generate ./internal/systemtest/m3/...` — change the generator, not this file. -->
<script setup lang="ts">
import { GlossaText } from "@klarlabs-studio/glossa/vue";
</script>

<template>
  <section class="page">
    <h1>Hilfe</h1>
    <p class="line">{{ $t("help.note.activity", { name: "Lina" }) }}</p>
    <p class="line">{{ $t("help.voucher.title") }}</p>
    <p class="line"><input class="field" :placeholder="$t('help.address.placeholder')" /></p>
    <p class="line"><GlossaText id="help.biscuit.label" /></p>
    <p class="line">{{ $t("help.sourdough.title") }}</p>
    <p class="line"><GlossaText id="help.crust.help" /></p>
    <p class="line">{{ $t("help.invoice.activity", { name: "Lina" }) }}</p>
    <p class="line">{{ $t("help.oven.title") }}</p>
    <p class="line">{{ $t("help.cake.title") }}</p>
    <p class="line"><GlossaText id="help.review.label" /></p>
    <p class="line">{{ $t("help.bread.title") }}</p>
    <p class="line">{{ $t("help.address.count", { count: 3 }) }}</p>
    <p class="line">{{ $t("help.flour.activity", { name: "Lina" }) }}</p>
    <p class="line">{{ $t("help.loaf.title") }}</p>
  </section>
</template>
