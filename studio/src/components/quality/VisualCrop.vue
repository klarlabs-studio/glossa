<script setup lang="ts">
/**
 * A visual finding's evidence (RFC 0005 §5.2): the capture it names,
 * cropped around the message's region and outlined — the "Where it
 * appears" pane pointed at a finding instead of a message. A whole
 * screenshot would make a reader hunt for the button that clipped.
 *
 * The finding carries `locus.capture` and `locus.region`; the boxes
 * live in Context, so the capture is read back through the Context port
 * by the message's key and matched on the capture's id. `locus.region`
 * is not resolvable on its own — the Context API's regions carry no id
 * — so what is outlined is every region of that message on that
 * capture, which is what the finding is about.
 */
import { onBeforeUnmount, shallowRef, watch } from "vue";
import { useContextPort } from "../../api/context";
import type { MessageCapture } from "../../api/context-schemas";
import type { Finding } from "../../api/quality-schemas";
import { cropTarget } from "../../lib/quality";
import { strings } from "../../strings";
import ErrorAlert from "../ErrorAlert.vue";
import CaptureShot from "../context/CaptureShot.vue";

const props = defineProps<{ tenant: string; projectId: string; finding: Finding }>();
const s = strings.quality;

const capture = shallowRef<MessageCapture>();
const loading = shallowRef(false);
const gone = shallowRef(false);
const error = shallowRef<unknown>(null);
const port = useContextPort();
let abort: AbortController | undefined;

async function load(): Promise<void> {
  abort?.abort();
  capture.value = undefined;
  gone.value = false;
  error.value = null;
  const target = cropTarget(props.finding);
  if (!target) return;
  const a = (abort = new AbortController());
  loading.value = true;
  try {
    const got = await port.messageCaptures(props.tenant, props.projectId, target.key, {}, a.signal);
    if (a.signal.aborted) return;
    const found = got.captures.find((c) => c.id === target.capture);
    // A capture outside the current builds is not served any more; say so
    // rather than showing another screen as if it were the evidence.
    if (found) capture.value = found;
    else gone.value = true;
  } catch (e) {
    if (!a.signal.aborted) error.value = e;
  } finally {
    if (!a.signal.aborted) loading.value = false;
  }
}
watch(() => [props.finding.fingerprint, props.tenant, props.projectId], load, { immediate: true });
onBeforeUnmount(() => abort?.abort());
</script>

<template>
  <div class="crop stack-sm" data-testid="visual-crop">
    <ErrorAlert :error="error" />
    <p v-if="loading" class="hint" role="status">{{ s.cropLoading }}</p>
    <p v-else-if="gone" class="hint" data-testid="crop-gone">{{ s.cropGone }}</p>
    <figure v-else-if="capture" class="stack-sm">
      <CaptureShot :capture="capture" fit="crop" :alt="s.cropAlt(capture.route, capture.locale)" />
      <figcaption>{{ s.cropCaption(capture.route, capture.viewport.width, capture.viewport.height, capture.locale) }}</figcaption>
    </figure>
  </div>
</template>

<style scoped>
.crop {
  max-inline-size: 28rem;
}
figure {
  margin: 0;
}
figcaption {
  font-size: var(--kl-text-sm);
  color: var(--kl-ink-secondary);
}
</style>
