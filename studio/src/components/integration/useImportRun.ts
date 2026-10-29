/**
 * One import from start to end: create the job, upload the file with
 * progress, then read the job until it ends. Used by the wizard, the
 * workspace's memory and termbase screen, and "Apply" on a dry run's
 * results; they then show the finished job. A TMX or TBX file without a
 * project goes to the workspace's own routes (`tm-import-jobs`,
 * `termbase-import-jobs`).
 */
import { computed, onBeforeUnmount, ref, shallowRef, type Ref } from "vue";
import type { ImportRequest, IntegrationPort } from "../../api/integration";
import type { ImportJob } from "../../api/integration-schemas";
import { isTerminal, polling, pollUntil, rememberFile } from "../../lib/integration";
import { strings } from "../../strings";

export type RunPhase = "idle" | "uploading" | "processing";

export function useImportRun(port: IntegrationPort, tenant: Ref<string>) {
  const phase = ref<RunPhase>("idle");
  const sent = ref(0);
  const job = shallowRef<ImportJob>();
  const error = ref<unknown>(null);
  let controller: AbortController | undefined;

  /** Resolves with the finished job, or undefined when stopped or failed (see `error`). */
  async function run(body: ImportRequest, file: File): Promise<ImportJob | undefined> {
    controller?.abort();
    const ctrl = (controller = new AbortController());
    error.value = null;
    sent.value = 0;
    job.value = undefined;
    phase.value = "uploading";
    try {
      const key = globalThis.crypto.randomUUID();
      const workspace = !body.project_id && (body.format === "tmx" || body.format === "tbx");
      let j = workspace
        ? await port.createKnowledgeImport(tenant.value, body.format === "tmx" ? "tm" : "termbase", { ...(body.mode ? { mode: body.mode } : {}), ...(body.file_name ? { file_name: body.file_name } : {}) }, key)
        : await port.createImport(tenant.value, body, key);
      job.value = j;
      if (j.mode === "dry_run") rememberFile(j.id, file);
      j = await port.upload(tenant.value, j, file, (loaded, total) => (sent.value = total ? loaded / total : 0), ctrl.signal);
      job.value = j;
      phase.value = "processing";
      const id = j.id;
      return await pollUntil(() => port.importJob(tenant.value, id), (x) => isTerminal(x.state), (x) => (job.value = x), polling.ms, ctrl.signal);
    } catch (e) {
      if (!(e instanceof DOMException && e.name === "AbortError")) error.value = e;
      return undefined;
    } finally {
      if (controller === ctrl) phase.value = "idle";
    }
  }

  /** Stop the upload or the waiting; a job that exists is cancelled on the server too. */
  async function cancel(): Promise<void> {
    controller?.abort();
    controller = undefined;
    const j = job.value;
    phase.value = "idle";
    if (j && !isTerminal(j.state)) {
      try {
        job.value = await port.cancelImport(tenant.value, j.id);
      } catch (e) {
        error.value = e;
      }
    }
  }

  onBeforeUnmount(() => controller?.abort());

  const w = strings.integration.wizard;
  const progressText = computed(() => {
    const j = job.value;
    if (phase.value === "uploading") return w.uploading(Math.round(sent.value * 100));
    if (!j || j.state === "queued" || j.state === "awaiting_upload") return w.queued;
    if (j.cancel_requested) return strings.integration.cancelRequested;
    return w.running(j.processed_items, j.total_items);
  });
  /** 0–1 while it is known; undefined for an indeterminate bar. */
  const progressValue = computed(() => {
    const j = job.value;
    if (phase.value === "uploading") return sent.value;
    if (j && j.total_items > 0) return Math.min(1, j.processed_items / j.total_items);
    return undefined;
  });

  return { phase, job, error, run, cancel, progressText, progressValue };
}
