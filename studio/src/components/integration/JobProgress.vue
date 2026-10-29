<script setup lang="ts">
/** A job on its way: what it's doing in words, a progress bar (indeterminate while unknown) and, optionally, cancel. */
import { useId } from "vue";
import { strings } from "../../strings";

defineProps<{ text: string; value?: number | undefined; label: string; cancellable?: boolean }>();
const emit = defineEmits<{ cancel: [] }>();
const id = useId();
</script>

<template>
  <div class="card stack-sm job-progress" data-testid="job-progress">
    <p :id="id" role="status" aria-live="polite">{{ text }}</p>
    <div class="row">
      <progress class="bar" :aria-label="label" :aria-describedby="id" :value="value === undefined ? undefined : value" max="1" />
      <button v-if="cancellable" type="button" class="btn btn-sm" @click="emit('cancel')">{{ strings.integration.cancel }}</button>
    </div>
  </div>
</template>

<style scoped>
.bar {
  flex: 1;
  min-inline-size: 10rem;
  block-size: 0.6rem;
  accent-color: var(--kl-accent);
}
</style>
