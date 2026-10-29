<script setup lang="ts">
import { computed } from "vue";
import { ApiError } from "../api/errors";
import { problemText } from "../strings";

const props = defineProps<{ error: unknown }>();
const text = computed(() => (props.error == null ? "" : problemText(props.error)));
const fields = computed(() => (props.error instanceof ApiError ? props.error.fieldErrors : []));
</script>

<template>
  <div v-if="error != null" class="alert alert-error" role="alert">
    <p>{{ text }}</p>
    <ul v-if="fields.length" class="fields">
      <li v-for="f in fields" :key="f.pointer"><code>{{ f.pointer }}</code> {{ f.detail }}</li>
    </ul>
  </div>
</template>

<style scoped>
.fields {
  margin: var(--kl-space-2) 0 0;
  padding-inline-start: var(--kl-space-5);
}
</style>
