<script setup lang="ts">
import { computed } from "vue";
import { GlossaText, useGlossa } from "@klarlabs-studio/glossa/vue";
import { useTypedMessages } from "../glossa/glossa-vue";

const props = defineProps<{ total: number; count: number }>();
const { t } = useGlossa();
const m = useTypedMessages();

const payLabel = computed(() => t("checkout.pay", { amount: props.total }));
const failed = computed(() => m.checkout.paymentFailed({ reason: "card" }));
</script>

<template>
  <footer :aria-label="$t('checkout.footer.label')">
    <p>{{ $t("cart.items", { count }) }}</p>
    <GlossaText id="cart.checkout">Zur Kasse</GlossaText>
    <GlossaText
      id="checkout.terms"
      :values="{ total }"
    />
    <GlossaText :id="'checkout.help'" />
    <glossa-text message="checkout.secure">Sicher bezahlen</glossa-text>
    <button type="submit">{{ payLabel }}</button>
    <p v-if="failed">{{ failed }}</p>
  </footer>
</template>
