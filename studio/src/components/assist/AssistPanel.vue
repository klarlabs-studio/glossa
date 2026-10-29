<script setup lang="ts">
/**
 * What the platform knows about the message being translated (intent
 * §68): the AI suggestion, translation-memory matches, the termbase
 * terms in the source, the style that applies, and concordance search.
 */
import { computed, useTemplateRef } from "vue";
import type { TermFinding, TermRecognition } from "../../api/knowledge-schemas";
import type { AISuggestion } from "../../api/intelligence-schemas";
import type { Application, Message, ProjectLocale, Syntax } from "../../api/schemas";
import { allows, allowsFor, type Grant } from "../../session/permissions";
import WhereItAppears from "../context/WhereItAppears.vue";
import Concordance from "./Concordance.vue";
import StylePane from "./StylePane.vue";
import SuggestionPanel from "./SuggestionPanel.vue";
import TermsPane from "./TermsPane.vue";
import TmMatches, { type MatchText } from "./TmMatches.vue";

const props = defineProps<{
  tenant: string;
  projectId: string;
  message: Message;
  source: ProjectLocale;
  target: ProjectLocale;
  grant: Grant;
  recognition: TermRecognition | undefined;
  recognitionError: unknown;
  findings: TermFinding[];
  checkState: "idle" | "checking" | "stale";
  hasDraft: boolean;
  /** The syntax the translation is being written in. */
  targetSyntax: Syntax;
  /** The project's applications, to name them in "Where it appears". */
  applications?: Application[];
}>();
const emit = defineEmits<{ insert: [match: MatchText]; accepted: [suggestion: AISuggestion] }>();

const canInsert = computed(() => allowsFor(props.grant, "translations.write", props.target.code));
const canKnow = computed(() => allows(props.grant, "knowledge.read"));
const canSeeContext = computed(() => allows(props.grant, "catalog.read"));
const canAI = computed(() => allows(props.grant, "intelligence.read"));

const tm = useTemplateRef<InstanceType<typeof TmMatches>>("tm");
const ai = useTemplateRef<InstanceType<typeof SuggestionPanel>>("ai");

defineExpose({
  matchTarget: (n: number) => tm.value?.matchTarget(n),
  acceptSuggestion: () => ai.value?.accept(),
  editSuggestion: () => ai.value?.edit(),
  refresh: () => {
    void ai.value?.reload();
    void tm.value?.reload();
  },
});
</script>

<template>
  <div class="assist stack">
    <SuggestionPanel v-if="canAI" ref="ai" :tenant="tenant" :project-id="projectId" :message="message" :target="target" :grant="grant" :can-insert="canInsert" @accepted="emit('accepted', $event)" @insert="emit('insert', $event)" />
    <template v-if="canKnow">
      <TmMatches ref="tm" :tenant="tenant" :project-id="projectId" :message="message" :source="source" :target="target" :target-syntax="targetSyntax" :can-insert="canInsert" @insert="emit('insert', $event)" />
      <TermsPane :recognition="recognition" :error="recognitionError" :findings="findings" :check-state="checkState" :has-draft="hasDraft" :source="source" :target="target" />
    </template>
    <WhereItAppears v-if="canSeeContext" :tenant="tenant" :project-id="projectId" :message="message" :source="source" :target="target" :applications="applications" />
    <template v-if="canKnow">
      <StylePane :tenant="tenant" :project-id="projectId" :locale="target.code" :namespace="message.namespace" />
      <Concordance :tenant="tenant" :project-id="projectId" :source="source" :target="target" />
    </template>
  </div>
</template>

<style scoped>
.assist {
  padding: var(--kl-space-4);
}
.assist :deep(.pane) {
  padding-block-end: var(--kl-space-4);
  border-block-end: 1px solid var(--kl-border);
}
.assist :deep(.pane:last-child) {
  border-block-end: none;
}
</style>
