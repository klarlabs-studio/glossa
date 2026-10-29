<script setup lang="ts">
import { strings } from "../strings";
</script>

<template>
  <span class="brand">
    <svg class="logo" viewBox="0 0 32 32" aria-hidden="true">
      <rect width="32" height="32" rx="7" fill="var(--kl-accent)" />
      <path d="M21.5 11.2a7 7 0 1 0 1.3 7.3H16" fill="none" stroke="var(--gs-on-accent)" stroke-width="3" stroke-linecap="round" />
    </svg>
    <span class="name">Glossa <span class="product">Studio</span></span>
    <kl-badge variant="accent" size="sm" class="beta">{{ strings.app.beta }}</kl-badge>
  </span>
</template>

<style scoped>
.brand {
  display: inline-flex;
  align-items: center;
  gap: var(--kl-space-2);
  font-weight: var(--kl-weight-semibold);
  letter-spacing: var(--kl-tracking-tight);
  color: var(--kl-ink);
}
.logo {
  inline-size: 1.5rem;
  block-size: 1.5rem;
}
.product {
  font-weight: var(--kl-weight-normal);
  color: var(--kl-ink-secondary);
}
.beta::part(badge) {
  /* The accent variant colours its 0.64rem label with --kl-accent, which is
     under 4.5:1 on its own tint; the darker shade passes AA. */
  color: var(--gs-accent-ink);
}
</style>
