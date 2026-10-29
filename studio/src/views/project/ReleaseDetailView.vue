<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { RouterLink, useRoute } from "vue-router";
import { useReleases } from "../../api/releases";
import type { Release, ReleaseDiff } from "../../api/schemas";
import ErrorAlert from "../../components/ErrorAlert.vue";
import { bytesText, createReleaseBook, policyText } from "../../components/releases/book";
import LocaleTable from "../../components/releases/LocaleTable.vue";
import PromoteDialog from "../../components/releases/PromoteDialog.vue";
import { servingMap } from "../../lib/releases";
import { absoluteTime, relativeTime } from "../../lib/time";
import { allows } from "../../session/permissions";
import { usePeople } from "../../session/people";
import { strings } from "../../strings";
import { useProject } from "./context";

const port = useReleases();
const route = useRoute();
const { tenant, projectId, grant } = useProject();
const s = strings.releases;
const project = () => ({ tenant: tenant.value, project: projectId.value });
const canPublish = computed(() => allows(grant.value, "releases.publish"));
const person = usePeople(() => tenant.value);
const book = createReleaseBook(port, project);
const releaseId = computed(() => String(route.params.release));

const release = ref<Release>();
const diff = ref<ReleaseDiff>();
const error = ref<unknown>(null);
const status = ref("");
const promoting = ref(false);
const serving = computed(() => servingMap(book.environments.value).get(releaseId.value) ?? []);

async function load(): Promise<void> {
  error.value = null;
  release.value = undefined;
  diff.value = undefined;
  try {
    const [r] = await Promise.all([port.release(project(), releaseId.value), book.load()]);
    release.value = r;
    await book.ensure([r.id, r.parent_id]);
    if (r.parent_id) diff.value = await port.diff(project(), r.id);
  } catch (e) {
    error.value = e;
  }
}
watch([projectId, releaseId], load, { immediate: true });

async function promoted(message: string): Promise<void> {
  promoting.value = false;
  status.value = message;
  await book.load();
}
</script>

<template>
  <div class="page stack">
    <RouterLink :to="{ name: 'releases', params: { tenant, project: projectId } }" class="back">← {{ s.backToReleases }}</RouterLink>
    <ErrorAlert :error="error" />
    <p v-if="!release && !error" class="muted" role="status">{{ strings.app.loading }}</p>
    <template v-if="release">
      <div class="page-header">
        <div class="row">
          <h1>{{ s.detailTitle(s.version(release.version)) }}</h1>
          <span v-for="env in serving" :key="env" class="pill pill-accent">{{ s.serving(env) }}</span>
        </div>
        <button v-if="canPublish" type="button" class="btn btn-primary" @click="promoting = true">
          {{ s.promote }}<span class="visually-hidden">{{ s.ofRelease(s.version(release.version)) }}</span>
        </button>
      </div>
      <p v-if="status" class="alert alert-ok" role="status">{{ status }}</p>
      <p v-if="release.note" class="note">{{ release.note }}</p>

      <dl class="meta card">
        <dt>{{ s.builtFor }}</dt>
        <dd>{{ release.environment }}</dd>
        <dt>{{ s.policy }}</dt>
        <dd>{{ policyText(release.policy) }}</dd>
        <dt>{{ s.publishedBy }}</dt>
        <dd>
          <time :datetime="release.created_at" :title="absoluteTime(release.created_at)">{{ relativeTime(release.created_at) }}</time>
          · {{ person(release.author) }}
        </dd>
        <dt>{{ s.parent }}</dt>
        <dd>
          <RouterLink v-if="release.parent_id" :to="{ name: 'release', params: { tenant, project: projectId, release: release.parent_id } }">
            {{ book.label(release.parent_id) }}
          </RouterLink>
          <span v-else>{{ s.noParent }}</span>
        </dd>
        <dt>{{ s.artifacts }}</dt>
        <dd>{{ s.artifactsText(release.counts.artifacts, release.counts.new_artifacts, bytesText(release.counts.bytes)) }}</dd>
        <dt>{{ s.digest }}</dt>
        <dd><code class="digest" :title="release.manifest_digest">{{ release.manifest_digest.slice(0, 16) }}…</code></dd>
      </dl>

      <section class="stack-sm" aria-labelledby="per-locale-h" data-testid="release-locales">
        <h2 id="per-locale-h">{{ s.perLocale }}</h2>
        <p class="muted">{{ release.parent_id ? s.diffTo(book.label(release.parent_id)) : s.diffToNothing }}</p>
        <LocaleTable
          :locales="release.locales.map((l) => l.code)"
          :source-locale="release.source_locale"
          :counts="release.counts.locales"
          :diff="diff"
          :caption="s.perLocale"
        />
      </section>

      <PromoteDialog :open="promoting" :port="port" :project="project()" :book="book" :release-id="release.id" @close="promoting = false" @done="promoted" />
    </template>
  </div>
</template>

<style scoped>
.back {
  align-self: flex-start;
}
.page-header {
  margin-block-end: 0;
}
.note {
  max-inline-size: var(--kl-content-md);
}
.meta {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: var(--kl-space-2) var(--kl-space-6);
  margin: 0;
}
.meta dt {
  color: var(--kl-ink-secondary);
}
.meta dd {
  margin: 0;
}
.digest {
  overflow-wrap: anywhere;
}
</style>
