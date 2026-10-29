<!-- Written by `go generate ./internal/systemtest/m3/...` — change the generator, not this file. -->
<script setup lang="ts">
import { GlossaText } from "@klarlabs-studio/glossa/vue";
</script>

<template>
  <section class="page">
    <h1>Konto</h1>
    <p class="line">{{ $t("account.order.title") }}</p>
    <p class="line"><GlossaText id="account.voucher.label" /></p>
    <p class="line">{{ $t("account.flour.title") }}</p>
    <p class="line">{{ $t("account.flour.count", { count: 3 }) }}</p>
    <p class="line">{{ $t("account.sourdough.activity", { name: "Lina" }) }}</p>
    <p class="line">{{ $t("account.bread.title") }}</p>
    <p class="line"><input class="field" :placeholder="$t('account.baguette.placeholder')" /></p>
    <p class="line"><GlossaText id="account.pickup.label" /></p>
    <p class="line">{{ $t("account.croissant.title") }}</p>
    <p class="line"><GlossaText id="account.order.help" /></p>
    <p class="line">{{ $t("account.account.activity", { name: "Lina" }) }}</p>
    <p class="line">{{ $t("account.biscuit.title") }}</p>
    <p class="line">{{ $t("account.bun.title") }}</p>
    <p class="line"><GlossaText id="account.crust.label" /></p>
    <p class="line">{{ $t("account.note.title") }}</p>
    <p class="line">{{ $t("account.question.count", { count: 3 }) }}</p>
  </section>
</template>
