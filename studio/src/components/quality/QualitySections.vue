<script setup lang="ts">
/**
 * The three quality screens (RFC 0005 §9), as one navigation: the
 * findings, the policy that grades them and the waivers that accept
 * them. They are one subject and three pages, so the project's top
 * navigation keeps one tab and this sits under it.
 *
 * `aria-current="page"` comes from RouterLink's exact-active class, so
 * the current section is announced and not only underlined.
 */
import { useRoute } from "vue-router";
import { RouterLink } from "vue-router";
import { strings } from "../../strings";

defineProps<{ tenant: string; projectId: string }>();
const route = useRoute();
const s = strings.quality;

const sections = [
  { name: "quality", label: s.sectionFindings },
  { name: "check-policy", label: s.sectionPolicy },
  { name: "waivers", label: s.sectionWaivers },
] as const;
</script>

<template>
  <nav :aria-label="s.sections" class="sections" data-testid="quality-sections">
    <RouterLink
      v-for="x in sections"
      :key="x.name"
      :to="{ name: x.name, params: { tenant, project: projectId } }"
      class="section"
      :aria-current="route.name === x.name ? 'page' : undefined"
    >
      {{ x.label }}
    </RouterLink>
  </nav>
</template>

<style scoped>
.sections {
  display: flex;
  flex-wrap: wrap;
  gap: var(--kl-space-1);
  border-block-end: 1px solid var(--kl-border);
}
.section {
  padding: var(--kl-space-2) var(--kl-space-3);
  color: var(--kl-ink-secondary);
  text-decoration: none;
  border-block-end: 2px solid transparent;
  margin-block-end: -1px;
}
.section:hover {
  color: var(--kl-ink);
}
.section[aria-current="page"] {
  color: var(--kl-ink);
  border-block-end-color: var(--kl-accent);
  font-weight: var(--kl-weight-medium);
}
</style>
