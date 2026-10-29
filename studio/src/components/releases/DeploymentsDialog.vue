<script setup lang="ts">
import { ref, watch } from "vue";
import { RouterLink } from "vue-router";
import type { ProjectRef, ReleasesPort } from "../../api/releases";
import type { Deployment } from "../../api/schemas";
import { absoluteTime, relativeTime } from "../../lib/time";
import { strings } from "../../strings";
import ErrorAlert from "../ErrorAlert.vue";
import ModalDialog from "../ModalDialog.vue";
import { type ReleaseBook } from "./book";

const props = defineProps<{
  open: boolean;
  port: ReleasesPort;
  project: ProjectRef;
  book: ReleaseBook;
  environment: string;
  person: (principal: string) => string;
  releaseRoute: (id: string) => object;
}>();
const emit = defineEmits<{ close: [] }>();
const s = strings.releases;
const c = s.historyColumns;

const items = ref<Deployment[]>([]);
const loading = ref(false);
const error = ref<unknown>(null);

watch(
  () => props.open,
  async (open) => {
    if (!open) return;
    error.value = null;
    items.value = [];
    loading.value = true;
    try {
      const list = await props.port.deployments(props.project, props.environment, 100);
      await props.book.ensure(list.flatMap((d) => [d.release_id, d.previous_release_id]));
      items.value = list;
    } catch (e) {
      error.value = e;
    } finally {
      loading.value = false;
    }
  },
  { immediate: true },
);
</script>

<template>
  <ModalDialog :open="open" :title="s.historyTitle(environment)" wide @close="emit('close')">
    <p v-if="loading" class="muted" role="status">{{ strings.app.loading }}</p>
    <p v-else-if="!items.length && !error" class="muted">{{ s.historyEmpty }}</p>
    <div v-else-if="items.length" class="scroll">
      <table class="table" data-testid="deployments">
        <caption class="visually-hidden">{{ s.historyTitle(environment) }}</caption>
        <thead>
          <tr>
            <th scope="col">{{ c.number }}</th>
            <th scope="col">{{ c.action }}</th>
            <th scope="col">{{ c.release }}</th>
            <th scope="col">{{ c.by }}</th>
            <th scope="col">{{ c.when }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="d in items" :key="d.number">
            <td>{{ d.number }}</td>
            <td>{{ s.lastChange[d.action] ?? d.action }}</td>
            <td>
              <RouterLink :to="releaseRoute(d.release_id)" @click="emit('close')">{{ book.label(d.release_id) }}</RouterLink>
              <span v-if="d.previous_release_id" class="muted">{{ " " + s.from(book.label(d.previous_release_id)) }}</span>
            </td>
            <td>{{ person(d.author) }}</td>
            <td><time :datetime="d.created_at" :title="absoluteTime(d.created_at)">{{ relativeTime(d.created_at) }}</time></td>
          </tr>
        </tbody>
      </table>
    </div>
    <ErrorAlert :error="error" />
    <template #actions>
      <button type="button" class="btn" @click="emit('close')">{{ strings.app.close }}</button>
    </template>
  </ModalDialog>
</template>

<style scoped>
.scroll {
  overflow-x: auto;
}
</style>
