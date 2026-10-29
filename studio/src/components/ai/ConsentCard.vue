<script setup lang="ts">
/**
 * Consent to send text to AI providers (intent §50, RFC 0003 §7): off by
 * default, explained in plain words, and turned on only after an
 * explicit confirmation. Who changed it and when is shown.
 */
import { ref } from "vue";
import type { AISettings } from "../../api/intelligence-schemas";
import { relativeTime } from "../../lib/time";
import { principalLabel, strings } from "../../strings";
import ModalDialog from "../ModalDialog.vue";

defineProps<{ settings: AISettings | undefined; canManage: boolean; busy: boolean; selfId?: string | undefined }>();
const emit = defineEmits<{ change: [consent: boolean] }>();
const s = strings.aiSettings;
const confirming = ref(false);

function allow(): void {
  confirming.value = false;
  emit("change", true);
}
</script>

<template>
  <section class="card stack-sm" aria-labelledby="consent-h" data-testid="consent">
    <h2 id="consent-h">{{ s.consentTitle }}</h2>
    <p>{{ s.consentLead }}</p>
    <ul class="points">
      <li v-for="(p, i) in s.consentPoints" :key="i">{{ p }}</li>
    </ul>
    <template v-if="settings">
      <p class="row">
        <span class="pill" :class="settings.provider_consent ? 'pill-ok' : 'pill-neutral'" data-testid="consent-state">
          {{ settings.provider_consent ? s.consentOn : s.consentOff }}
        </span>
        <span v-if="settings.consent_changed_by && settings.consent_changed_at" class="hint">
          {{ s.consentChanged(principalLabel(settings.consent_changed_by, selfId), relativeTime(settings.consent_changed_at)) }}
        </span>
      </p>
      <div v-if="canManage">
        <button v-if="settings.provider_consent" type="button" class="btn btn-danger" :disabled="busy" @click="emit('change', false)">{{ s.disallow }}</button>
        <button v-else type="button" class="btn" :disabled="busy" @click="confirming = true">{{ s.allow }}</button>
      </div>
    </template>
    <ModalDialog :open="confirming" :title="s.confirmAllow" @close="confirming = false">
      <p>{{ s.confirmAllowLead }}</p>
      <template #actions>
        <button type="button" class="btn" @click="confirming = false">{{ strings.app.cancel }}</button>
        <button type="button" class="btn btn-primary" @click="allow">{{ s.confirmAllowButton }}</button>
      </template>
    </ModalDialog>
  </section>
</template>

<style scoped>
.points {
  margin: 0;
  padding-inline-start: var(--kl-space-5);
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-1);
}
</style>
