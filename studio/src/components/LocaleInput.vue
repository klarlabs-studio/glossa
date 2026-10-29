<script setup lang="ts">
import { computed } from "vue";
import { checkLocale, localeName } from "../lib/bcp47";
import { strings } from "../strings";

const model = defineModel<string>({ required: true });
const props = defineProps<{ id: string; label: string; hint?: string; required?: boolean }>();

const check = computed(() => checkLocale(model.value));
const message = computed(() => {
  const c = check.value;
  if (c.ok) return strings.locales.preview(c.tag, localeName(c.tag), c.direction);
  if (c.reason === "empty") return props.hint ?? strings.locales.codeHint;
  return { invalid: strings.locales.invalid, extension: strings.locales.extension, too_long: strings.locales.tooLong }[c.reason];
});
const invalid = computed(() => !check.value.ok && check.value.reason !== "empty");

defineExpose({ check });
</script>

<template>
  <div class="field">
    <label :for="id">{{ label }}</label>
    <input
      :id="id"
      v-model="model"
      class="mono"
      autocomplete="off"
      autocapitalize="off"
      spellcheck="false"
      maxlength="64"
      :required="required"
      :aria-invalid="invalid ? 'true' : undefined"
      :aria-describedby="`${id}-msg`"
    />
    <span :id="`${id}-msg`" :class="invalid ? 'field-error' : 'hint'" aria-live="polite">{{ message }}</span>
  </div>
</template>
