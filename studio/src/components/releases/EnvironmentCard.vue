<script setup lang="ts">
import { computed } from "vue";
import { RouterLink } from "vue-router";
import type { Rollout } from "../../api/release-ops-schemas";
import type { Deployment, Environment, Release } from "../../api/schemas";
import { requirementText, type PartyNames } from "../../lib/release-ops";
import { absoluteTime, relativeTime } from "../../lib/time";
import { strings } from "../../strings";
import { policyText } from "./book";

const props = defineProps<{
  env: Environment;
  release: Release | undefined;
  latest: Deployment | undefined;
  canPublish: boolean;
  person: (principal: string) => string;
  releaseRoute: (id: string) => object;
  /** The environment's page: its approval requirement and rollouts (RFC 0006 §5). */
  envRoute?: object | undefined;
  /** Its release requests. */
  requestsRoute?: object | undefined;
  /** Pending release requests here; undefined when not known. */
  pending?: number | undefined;
  /** The running rollout, if any. */
  rollout?: Rollout | undefined;
  /** Its candidate's label ("v5"). */
  rolloutLabel?: string | undefined;
  partyNames?: PartyNames | undefined;
}>();
const emit = defineEmits<{ promote: []; rollback: []; history: []; policy: [] }>();
const s = strings.releases;
const ro = strings.releaseOps;
const headingId = computed(() => `env-${props.env.name}`);
</script>

<template>
  <article class="card env stack-sm" :aria-labelledby="headingId" :data-testid="`env-${env.name}`">
    <header class="row">
      <h3 :id="headingId">{{ env.name }}</h3>
      <span class="spacer" />
      <RouterLink v-if="release" :to="releaseRoute(release.id)" class="pill pill-accent version" data-testid="env-version">
        {{ s.serving(s.version(release.version)) }}
      </RouterLink>
      <span v-else class="pill pill-neutral" data-testid="env-version">{{ s.nothingServed }}</span>
    </header>
    <p v-if="latest" class="muted when">
      {{ s.byWhen(s.lastChange[latest.action] ?? latest.action, person(latest.author)) }},
      <time :datetime="latest.created_at" :title="absoluteTime(latest.created_at)">{{ relativeTime(latest.created_at) }}</time>
    </p>
    <p class="policy"><span class="label">{{ s.ships }}:</span> {{ policyText(env.policy) }}</p>
    <p v-if="env.kind !== 'branch'" class="policy" data-testid="env-approval">
      <span class="label">{{ ro.approval }}:</span> {{ env.approval ? requirementText(env.approval, partyNames) : ro.noApproval }}
    </p>
    <p v-if="rollout" class="policy" data-testid="env-rollout">
      <span class="pill pill-warn">{{ ro.rolloutSummary(rollout.percent, rolloutLabel ?? "…") }}</span>
    </p>
    <p v-if="envRoute || (requestsRoute && pending)" class="row links">
      <RouterLink v-if="envRoute" :to="envRoute" data-testid="env-page">
        {{ ro.envPage }}<span class="visually-hidden">{{ s.ofEnv(env.name) }}</span>
      </RouterLink>
      <RouterLink v-if="requestsRoute && pending" :to="requestsRoute" data-testid="env-requests">
        {{ ro.envRequests }}<span class="visually-hidden">{{ s.ofEnv(env.name) }}</span>: {{ ro.pendingCount(pending) }}
      </RouterLink>
    </p>
    <div class="row actions">
      <template v-if="canPublish">
        <button type="button" class="btn btn-sm" @click="emit('promote')">
          {{ s.promote }}<span class="visually-hidden">{{ s.toEnv(env.name) }}</span>
        </button>
        <button type="button" class="btn btn-sm" :disabled="!release" @click="emit('rollback')">
          {{ s.rollback }}<span class="visually-hidden">{{ " " + env.name }}</span>
        </button>
      </template>
      <button type="button" class="btn btn-sm btn-ghost" @click="emit('history')">
        {{ s.history }}<span class="visually-hidden">{{ s.ofEnv(env.name) }}</span>
      </button>
      <button v-if="canPublish" type="button" class="btn btn-sm btn-ghost" @click="emit('policy')">
        {{ s.editPolicy }}<span class="visually-hidden">{{ s.ofEnv(env.name) }}</span>
      </button>
    </div>
  </article>
</template>

<style scoped>
.env {
  padding: var(--kl-space-5);
}
.version {
  text-decoration: none;
}
.when,
.policy,
.links {
  font-size: var(--kl-text-sm);
}
.actions {
  margin-block-start: auto;
  gap: var(--kl-space-2);
}
</style>
