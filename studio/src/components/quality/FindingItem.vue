<script setup lang="ts">
/**
 * One finding, whatever layer found it (RFC 0005 §2.1): its severity,
 * its rule, what it says, and where you would look — the key, the
 * locale, the file and line Context filled in at report time.
 *
 * A waived finding is rendered here too, marked and with the reason it
 * was accepted for, never dropped from the list: hiding it would make
 * the number go down without the product getting better, which is the
 * failure mode of every suppression system (RFC 0005 §14 decision 5).
 */
import { computed } from "vue";
import type { Finding, Waiver } from "../../api/quality-schemas";
import { findingPlace, cropTarget } from "../../lib/quality";
import { absoluteTime, relativeTime } from "../../lib/time";
import type { PersonLabel } from "../../session/people";
import { strings } from "../../strings";
import VisualCrop from "./VisualCrop.vue";

const props = defineProps<{
  tenant: string;
  projectId: string;
  finding: Finding;
  /** The waiver that accepted it, where this page loaded it. */
  waiver: Waiver | undefined;
  canWaive: boolean;
  busy: boolean;
  person: PersonLabel;
}>();
const emit = defineEmits<{ waive: [finding: Finding]; revoke: [waiver: Waiver, finding: Finding] }>();
const s = strings.quality;

const waived = computed(() => props.finding.severity === "waived");
const place = computed(() => findingPlace(props.finding.locus));
const crop = computed(() => (props.finding.layer === "visual" ? cropTarget(props.finding) : undefined));
const tone = computed(() => (props.finding.severity === "error" ? "pill-err" : props.finding.severity === "warning" ? "pill-warn" : "pill-neutral"));
/** Names the finding for a button's accessible name, so "Accept" alone is never all a screen reader hears. */
const names = computed(() => [props.finding.code, props.finding.locus.key].filter(Boolean).join(" on "));
const fixText = computed(() => {
  const fix = props.finding.fix;
  if (!fix) return "";
  if (fix.hint) return s.fixHint(fix.hint);
  if (fix.kind === "shorten" && fix.to !== undefined) return s.fixShorten(fix.to);
  return s.fixKind[fix.kind] ?? "";
});
</script>

<template>
  <li class="finding stack-sm" :class="{ waived }" :data-fingerprint="finding.fingerprint" data-testid="finding">
    <p class="head">
      <span class="pill" :class="tone" data-testid="finding-severity">{{ s.severityName[finding.severity] ?? finding.severity }}</span>
      <code class="code">{{ finding.code }}</code>
      <span v-if="finding.locus.key" class="key"><code>{{ finding.locus.key }}</code></span>
      <span v-if="finding.locus.locale" class="muted">{{ s.inLocale(finding.locus.locale) }}</span>
    </p>
    <p class="msg">{{ finding.message }}</p>
    <p v-if="finding.detail" class="hint">{{ finding.detail }}</p>
    <p v-if="fixText" class="hint" data-testid="finding-fix">{{ fixText }}</p>
    <p v-if="place || finding.locus.route || finding.locus.component" class="where hint">
      <code v-if="place" data-testid="finding-place">{{ place }}</code>
      <code v-if="finding.locus.route">{{ finding.locus.route }}</code>
      <span v-if="finding.locus.component">{{ finding.locus.component }}</span>
    </p>

    <!-- The evidence for a visual finding: the capture, cropped around the region. -->
    <VisualCrop v-if="crop" :tenant="tenant" :project-id="projectId" :finding="finding" />

    <!-- Waived, and still here. The reason is the whole mechanism, so it is the thing shown. -->
    <div v-if="waived" class="accepted stack-sm" data-testid="finding-waiver">
      <p>
        <span class="pill pill-neutral">{{ s.waived }}</span>
        <template v-if="waiver">{{ s.waivedBecause(waiver.reason) }}</template>
        <template v-else>{{ s.waivedUnknown }}</template>
      </p>
      <p v-if="waiver" class="hint">
        {{ s.waivedBy(person(waiver.created_by), relativeTime(waiver.created_at)) }}
        <template v-if="waiver.scope === 'branch' && waiver.ref"> · {{ s.waivedScopeBranch(waiver.ref) }}</template>
        <template v-if="waiver.expires_at"> · {{ s.waivedExpires(absoluteTime(waiver.expires_at)) }}</template>
      </p>
      <div v-if="waiver && canWaive">
        <button type="button" class="btn btn-sm" :disabled="busy" data-testid="finding-revoke" @click="emit('revoke', waiver, finding)">
          {{ s.revoke }}<span class="visually-hidden">{{ s.revokeFor(names) }}</span>
        </button>
      </div>
    </div>
    <div v-else-if="canWaive">
      <button type="button" class="btn btn-sm" :disabled="busy" data-testid="finding-waive" @click="emit('waive', finding)">
        {{ s.waive }}<span class="visually-hidden">{{ s.waiveFor(names) }}</span>
      </button>
    </div>
  </li>
</template>

<style scoped>
.finding {
  padding: var(--kl-space-3);
  border: 1px solid var(--kl-border);
  border-radius: var(--kl-radius-md);
  background: var(--kl-surface);
}
/*
 * A waived finding reads as accepted, not as absent: same surface, same
 * size, same text, only a different frame — and the "Waived" pill says
 * it in words, so the frame is never the only thing carrying it.
 */
.finding.waived {
  border-style: dashed;
}
.head {
  display: flex;
  flex-wrap: wrap;
  gap: var(--kl-space-2);
  align-items: baseline;
}
.msg {
  overflow-wrap: anywhere;
}
.where {
  display: flex;
  flex-wrap: wrap;
  gap: var(--kl-space-3);
}
.where code,
.key code {
  overflow-wrap: anywhere;
}
.accepted {
  padding: var(--kl-space-3);
  border-inline-start: 3px solid var(--kl-border);
  /* The same ink-on-muted pair `.pill-neutral` already uses across Studio. */
  background: var(--kl-surface-muted);
  border-radius: var(--kl-radius-sm);
}
</style>
