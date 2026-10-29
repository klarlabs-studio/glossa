<script setup lang="ts">
import BrandMark from "./BrandMark.vue";

defineProps<{ title: string; lead?: string }>();
</script>

<template>
  <div class="auth">
    <header class="auth-top">
      <BrandMark />
      <kl-theme-toggle />
    </header>
    <main id="main" class="auth-main">
      <section class="card auth-card" aria-labelledby="auth-title">
        <div class="stack">
          <div class="stack-sm">
            <h1 id="auth-title">{{ title }}</h1>
            <p v-if="lead" class="muted">{{ lead }}</p>
          </div>
          <slot />
        </div>
      </section>
      <div class="auth-foot"><slot name="footer" /></div>
    </main>
  </div>
</template>

<style scoped>
.auth {
  min-block-size: 100vh;
  display: flex;
  flex-direction: column;
  background: var(--kl-surface);
}
.auth-top {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: var(--kl-space-4) var(--kl-space-6);
}
.auth-main {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: var(--kl-space-12) var(--kl-space-4);
  gap: var(--kl-space-4);
}
.auth-card {
  inline-size: min(28rem, 100%);
}
.auth-foot {
  text-align: center;
  color: var(--kl-ink-secondary);
}
</style>
