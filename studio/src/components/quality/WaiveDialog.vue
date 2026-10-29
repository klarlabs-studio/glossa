<script setup lang="ts">
/**
 * Accepting a finding (RFC 0005 §2.3). The reason is required and
 * non-empty — the server refuses a blank one with
 * `waiver_reason_required`, and so does this form, before anyone waits
 * for a round trip. That is the whole mechanism: a suppression nobody
 * had to justify is technical debt with no paper trail.
 *
 * Nothing here deletes anything. The finding stays listed, at severity
 * `waived`, and comes back on its own when the source revision it was
 * accepted against moves.
 */
import { computed, nextTick, reactive, ref, useTemplateRef, watch } from "vue";
import type { ProjectRef } from "../../api/releases";
import type { CreateWaiver, Finding, WaiverScope } from "../../api/quality-schemas";
import type { QualityPort } from "../../api/quality";
import { strings } from "../../strings";
import ErrorAlert from "../ErrorAlert.vue";
import ModalDialog from "../ModalDialog.vue";

const props = defineProps<{
  open: boolean;
  port: QualityPort;
  project: ProjectRef;
  finding: Finding | undefined;
  /** The ref the run graded, the sensible default for a branch-scoped waiver. */
  defaultRef: string | undefined;
}>();
const emit = defineEmits<{ close: []; done: [code: string] }>();
const s = strings.quality;

const form = reactive({ reason: "", scope: "project" as WaiverScope, ref: "", expires: "" });
const busy = ref(false);
const error = ref<unknown>(null);
/** Set once they try to submit, so an empty field isn't shouted at before anyone typed. */
const tried = ref(false);
const reasonInput = useTemplateRef<HTMLTextAreaElement>("reasonInput");

const reasonGiven = computed(() => form.reason.trim().length > 0);
const refGiven = computed(() => form.scope !== "branch" || form.ref.trim().length > 0);
const valid = computed(() => reasonGiven.value && refGiven.value);

watch(
  () => props.open,
  async (open) => {
    if (!open) return;
    error.value = null;
    tried.value = false;
    form.reason = "";
    form.scope = "project";
    form.ref = props.defaultRef ?? "";
    form.expires = "";
    // The reason is the point of the dialog, so that is where the caret lands.
    await nextTick();
    reasonInput.value?.focus();
  },
  { immediate: true },
);

async function submit(): Promise<void> {
  tried.value = true;
  const finding = props.finding;
  if (!finding || !valid.value) {
    reasonInput.value?.focus();
    return;
  }
  busy.value = true;
  error.value = null;
  try {
    const body: CreateWaiver = { fingerprint: finding.fingerprint, reason: form.reason.trim(), scope: form.scope };
    if (form.scope === "branch") body.ref = form.ref.trim();
    // A date input gives a day; the API takes an instant, and the sweep is daily.
    if (form.expires) body.expires_at = new Date(`${form.expires}T23:59:59Z`).toISOString();
    await props.port.createWaiver(props.project, body);
    emit("done", finding.code);
  } catch (e) {
    error.value = e;
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <ModalDialog :open="open" :title="s.waiveTitle" @close="emit('close')">
    <p v-if="finding" class="about">
      <code>{{ finding.code }}</code>
      <span v-if="finding.locus.key"> · <code>{{ finding.locus.key }}</code></span>
      <span v-if="finding.locus.locale" class="muted"> · {{ s.inLocale(finding.locus.locale) }}</span>
    </p>
    <p v-if="finding" class="muted">{{ finding.message }}</p>
    <p class="hint">{{ s.waiveLead }}</p>
    <!-- novalidate: the reason's own message is always there and announced, rather than a bubble that vanishes. -->
    <form id="waive-form" class="stack" novalidate @submit.prevent="submit">
      <div class="field">
        <label for="waive-reason">{{ s.reason }}</label>
        <textarea
          id="waive-reason"
          ref="reasonInput"
          v-model="form.reason"
          rows="3"
          aria-required="true"
          maxlength="1000"
          :disabled="busy"
          :placeholder="s.reasonPlaceholder"
          :aria-invalid="tried && !reasonGiven ? 'true' : undefined"
          aria-describedby="waive-reason-hint waive-reason-error"
        />
        <span id="waive-reason-hint" class="hint">{{ s.reasonHint }}</span>
        <span id="waive-reason-error" class="field-error" role="alert" data-testid="waive-reason-error">{{ tried && !reasonGiven ? s.reasonRequired : "" }}</span>
      </div>
      <fieldset class="stack-sm">
        <legend class="label">{{ s.scope }}</legend>
        <label class="check">
          <input v-model="form.scope" type="radio" value="project" :disabled="busy" />
          <span>{{ s.scopeProject }}</span>
        </label>
        <label class="check">
          <input v-model="form.scope" type="radio" value="branch" :disabled="busy" />
          <span>{{ s.scopeBranch }}</span>
        </label>
      </fieldset>
      <div v-if="form.scope === 'branch'" class="field">
        <label for="waive-ref">{{ s.branchRef }}</label>
        <input id="waive-ref" v-model="form.ref" type="text" aria-required="true" :disabled="busy" aria-describedby="waive-ref-error" />
        <span id="waive-ref-error" class="field-error" role="alert">{{ tried && !refGiven ? s.branchRefRequired : "" }}</span>
      </div>
      <div class="field">
        <label for="waive-expires">{{ s.expires }}</label>
        <input id="waive-expires" v-model="form.expires" type="date" :disabled="busy" aria-describedby="waive-expires-hint" />
        <span id="waive-expires-hint" class="hint">{{ s.expiresHint }}</span>
      </div>
    </form>
    <ErrorAlert :error="error" />
    <template #actions>
      <button type="button" class="btn" :disabled="busy" @click="emit('close')">{{ strings.app.cancel }}</button>
      <!--
        Disabled would leave nothing to explain why: the submit stays
        reachable and answers with the reason it needs.
      -->
      <button type="submit" form="waive-form" class="btn btn-primary" :disabled="busy" data-testid="waive-submit">
        {{ busy ? s.accepting : s.accept }}
      </button>
    </template>
  </ModalDialog>
</template>

<style scoped>
fieldset {
  border: none;
  margin: 0;
  padding: 0;
}
.about {
  overflow-wrap: anywhere;
}
textarea {
  inline-size: 100%;
  font: inherit;
}
.field-error:empty {
  display: none;
}
</style>
