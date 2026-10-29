/**
 * Terminology for the message being translated: the termbase terms
 * recognized in its source (with the target locale's terms), and a live
 * terminology check of the draft as the translator types — debounced,
 * the latest request winning, like the MF1 preview.
 */
import { onBeforeUnmount, ref, shallowRef, watch, type Ref } from "vue";
import type { TermFinding, TermRecognition } from "../../api/knowledge-schemas";
import { useKnowledge } from "../../api/knowledge";
import type { Message, Syntax } from "../../api/schemas";

/** Wait this long after the last keystroke before checking the draft. */
export const TERM_CHECK_DELAY_MS = 300;

export interface TerminologyInputs {
  tenant: Ref<string>;
  projectId: Ref<string>;
  message: Ref<Message | undefined>;
  sourceLocale: Ref<string | undefined>;
  targetLocale: Ref<string | undefined>;
  draft: Ref<{ text: string; syntax: Syntax }>;
}

export function useTerminology(inputs: TerminologyInputs) {
  const port = useKnowledge();
  const recognition = shallowRef<TermRecognition>();
  const recognitionError = ref<unknown>(null);
  const findings = shallowRef<TermFinding[]>([]);
  /** `stale` when the draft doesn't parse: the findings shown are for earlier text. */
  const checkState = ref<"idle" | "checking" | "stale">("idle");

  let recognizeAbort: AbortController | undefined;
  watch(
    [inputs.message, inputs.sourceLocale, inputs.targetLocale],
    async ([m, src, tgt]) => {
      recognizeAbort?.abort();
      recognition.value = undefined;
      recognitionError.value = null;
      if (!m || !src || !tgt) return;
      const abort = (recognizeAbort = new AbortController());
      try {
        const r = await port.recognizeTerms(
          inputs.tenant.value,
          { text: m.source.text, syntax: m.source.syntax, locale: src, target_locale: tgt, project_id: inputs.projectId.value },
          abort.signal,
        );
        if (!abort.signal.aborted) recognition.value = r;
      } catch (e) {
        if (!abort.signal.aborted) recognitionError.value = e;
      }
    },
    { immediate: true },
  );

  let timer: ReturnType<typeof setTimeout> | undefined;
  let checkAbort: AbortController | undefined;
  let seq = 0;

  async function check(): Promise<void> {
    const m = inputs.message.value;
    const src = inputs.sourceLocale.value;
    const tgt = inputs.targetLocale.value;
    const { text, syntax } = inputs.draft.value;
    const n = ++seq;
    checkAbort?.abort();
    if (!m || !src || !tgt || text.trim() === "") {
      findings.value = [];
      checkState.value = "idle";
      return;
    }
    const abort = (checkAbort = new AbortController());
    checkState.value = "checking";
    try {
      // Both texts parse as messages only when they share a syntax (a TM
      // match or AI suggestion is MF2 while the source may be MF1).
      const r = await port.checkTerminology(
        inputs.tenant.value,
        {
          source: m.source.text,
          source_locale: src,
          target: text,
          target_locale: tgt,
          project_id: inputs.projectId.value,
          ...(syntax === m.source.syntax ? { syntax } : {}),
        },
        abort.signal,
      );
      if (n !== seq) return;
      findings.value = r.findings;
      checkState.value = "idle";
    } catch {
      // Text that doesn't parse yet: keep the last findings, marked stale.
      if (n === seq && !abort.signal.aborted) checkState.value = "stale";
    }
  }

  watch(
    [inputs.draft, inputs.message, inputs.targetLocale],
    () => {
      clearTimeout(timer);
      timer = setTimeout(() => void check(), TERM_CHECK_DELAY_MS);
    },
    { deep: true },
  );
  watch([inputs.message, inputs.targetLocale], () => {
    findings.value = [];
  });
  onBeforeUnmount(() => {
    clearTimeout(timer);
    checkAbort?.abort();
    recognizeAbort?.abort();
  });

  return { recognition, recognitionError, findings, checkState };
}
