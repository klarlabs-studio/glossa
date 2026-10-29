<!-- Written by `go generate ./internal/systemtest/m3/...` — change the generator, not this file. -->
<script setup lang="ts">
import { GlossaText } from "@klarlabs-studio/glossa/vue";
import { onBeforeUnmount, onMounted, ref } from "vue";

import { mountPayButton } from "../islands/mount";

const island = ref<HTMLElement | null>(null);
let unmount: (() => void) | undefined;
onMounted(() => {
  if (island.value) unmount = mountPayButton(island.value);
});
onBeforeUnmount(() => unmount?.());
</script>

<template>
  <section class="page">
    <h1>Kasse</h1>
    <p class="line">{{ $t("checkout.voucher.activity", { name: "Lina" }) }}</p>
    <p class="line">{{ $t("checkout.order.title") }}</p>
    <p class="line"><input class="field" :placeholder="$t('checkout.muffin.placeholder')" /></p>
    <p class="line"><GlossaText id="checkout.grain.label" /></p>
    <p class="line">{{ $t("checkout.ingredient.title") }}</p>
    <p class="line"><GlossaText id="checkout.roll.help" /></p>
    <p class="line">{{ $t("checkout.order.activity", { name: "Lina" }) }}</p>
    <p class="line">{{ $t("checkout.plait.title") }}</p>
    <p class="line">{{ $t("checkout.stollen.title") }}</p>
    <p class="line"><GlossaText id="checkout.crust.label" /></p>
    <p class="line">{{ $t("checkout.bagel.title") }}</p>
    <p class="line">{{ $t("checkout.pickup.count", { count: 3 }) }}</p>
    <p class="line">{{ $t("checkout.payment.activity", { name: "Lina" }) }}</p>
    <p class="line">{{ $t("checkout.croissant.title") }}</p>
    <p class="line">{{ $t("pickup.window") }}</p>
    <p class="line">{{ $t("pickup.slots") }}</p>
    <p class="line">{{ $t("pickup.reminder", { count: 3 }) }}</p>
    <p class="line">{{ $t("pickup.branch") }}</p>
    <p class="line">{{ $t("pickup.confirm") }}</p>
    <div ref="island"></div>
  </section>
</template>
