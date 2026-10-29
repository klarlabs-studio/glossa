/**
 * The message list's context filters (RFC 0004 §3.4): route, component,
 * file, `unused` and `not captured`. Each resolves to the message IDs it
 * allows, and the list shows the intersection — the way the search box
 * narrows what the server already filtered.
 *
 * Route, component and file are resolved by the server (`GET …/usages`),
 * `unused` by `GET …/unused-messages`. `not captured` has no endpoint of
 * its own yet: the API reports a project's capture coverage only as a
 * metric, so Studio asks per message (`GET …/messages/{key}/captures`)
 * for the messages that have a usage, up to UNCAPTURED_PROBE_LIMIT, and
 * says how far it looked.
 */
import { computed, ref, shallowRef, watch, type ComputedRef, type Ref } from "vue";
import { useContextPort, type ContextPort } from "../../api/context";
import type { ContextUsage } from "../../api/context-schemas";

/** How many messages `not captured` probes at most. */
export const UNCAPTURED_PROBE_LIMIT = 500;
/** How many capture probes run at once. */
const PROBE_CONCURRENCY = 6;

export interface ContextFilterState {
  route: string;
  component: string;
  file: string;
  /** Extra coverage filter: nothing, unused messages, or messages without a screenshot. */
  only: "" | "unused" | "uncaptured";
}

export const emptyContextFilter: ContextFilterState = { route: "", component: "", file: "", only: "" };
export const isContextFiltered = (f: ContextFilterState): boolean => !!(f.route || f.component || f.file || f.only);

/** What a project's current builds offer to filter by. */
export interface UsageIndex {
  routes: string[];
  components: string[];
  files: string[];
  /** Message IDs with at least one current usage. */
  used: Set<string>;
  /** Their keys, to ask for captures. */
  keyOf: Map<string, string>;
  /** Whether anything was ever uploaded. */
  hasData: boolean;
}

const sorted = (values: Iterable<string>) => [...new Set(values)].sort((a, b) => a.localeCompare(b));

export function indexOf(usages: readonly ContextUsage[], hasData: boolean): UsageIndex {
  const used = new Set<string>();
  const keyOf = new Map<string, string>();
  for (const u of usages) {
    if (!u.message_id) continue;
    used.add(u.message_id);
    keyOf.set(u.message_id, u.key);
  }
  return {
    routes: sorted(usages.map((u) => u.route ?? "").filter(Boolean)),
    components: sorted(usages.map((u) => u.component ?? "").filter(Boolean)),
    files: sorted(usages.map((u) => u.file)),
    used,
    keyOf,
    hasData,
  };
}

/** Runs `fn` over items, `limit` at a time, reporting after each. */
async function eachLimit<T>(items: readonly T[], limit: number, fn: (item: T) => Promise<void>, done: () => void): Promise<void> {
  let next = 0;
  const worker = async (): Promise<void> => {
    for (let i = next++; i < items.length; i = next++) {
      await fn(items[i]!);
      done();
    }
  };
  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, worker));
}

export interface ContextFilterOptions {
  tenant: Ref<string> | ComputedRef<string>;
  projectId: Ref<string> | ComputedRef<string>;
  filter: ComputedRef<ContextFilterState>;
  branch?: Ref<string | undefined> | ComputedRef<string | undefined>;
  /** The port to use; the API adapter by default. */
  port?: ContextPort;
}

export function useContextFilter(options: ContextFilterOptions) {
  const port = options.port ?? useContextPort();
  const index = shallowRef<UsageIndex>();
  const indexError = ref<unknown>(null);
  const loadingIndex = ref(false);
  /** Message IDs the filters allow; undefined while no filter is on (or nothing is resolved yet). */
  const allowed = shallowRef<Set<string>>();
  const resolving = ref(false);
  const error = ref<unknown>(null);
  /** Capture probes: how many messages were checked, of how many, and whether the probe was capped. */
  const probe = ref<{ done: number; total: number; capped: boolean }>();

  const branch = computed(() => options.branch?.value);
  let indexAbort: AbortController | undefined;
  let indexFor = "";
  let resolveAbort: AbortController | undefined;

  /** Load the project's current usages once per project and branch. */
  async function loadIndex(): Promise<void> {
    const want = `${options.tenant.value}/${options.projectId.value}/${branch.value ?? ""}`;
    if (indexFor === want && (index.value || loadingIndex.value)) return;
    indexAbort?.abort();
    const a = (indexAbort = new AbortController());
    indexFor = want;
    loadingIndex.value = true;
    indexError.value = null;
    index.value = undefined;
    try {
      const usages = await port.usages(options.tenant.value, options.projectId.value, { branch: branch.value }, undefined, a.signal);
      // Nothing uploaded at all reads differently from "nothing matches".
      const hasData = usages.length > 0 || (await port.coverage(options.tenant.value, options.projectId.value, branch.value, a.signal)).current_builds > 0;
      if (!a.signal.aborted) index.value = indexOf(usages, hasData);
    } catch (e) {
      if (!a.signal.aborted) {
        indexError.value = e;
        indexFor = "";
      }
    } finally {
      if (!a.signal.aborted) loadingIndex.value = false;
    }
  }

  async function resolve(): Promise<void> {
    resolveAbort?.abort();
    const f = options.filter.value;
    error.value = null;
    probe.value = undefined;
    if (!isContextFiltered(f)) {
      allowed.value = undefined;
      resolving.value = false;
      return;
    }
    const a = (resolveAbort = new AbortController());
    resolving.value = true;
    try {
      const sets: Array<Set<string>> = [];
      if (f.route || f.component || f.file) {
        const usages = await port.usages(
          options.tenant.value,
          options.projectId.value,
          { branch: branch.value, route: f.route || undefined, component: f.component || undefined, file: f.file || undefined },
          undefined,
          a.signal,
        );
        sets.push(new Set(usages.map((u) => u.message_id).filter((id): id is string => !!id)));
      }
      if (f.only === "unused") {
        const unused = await port.unusedMessages(options.tenant.value, options.projectId.value, branch.value, a.signal);
        sets.push(new Set(unused.items.map((m) => m.id)));
      }
      if (f.only === "uncaptured") sets.push(await withoutCaptures(a.signal));
      if (a.signal.aborted) return;
      allowed.value = sets.reduce((acc, s) => new Set([...acc].filter((id) => s.has(id))));
    } catch (e) {
      if (!a.signal.aborted) {
        error.value = e;
        allowed.value = new Set();
      }
    } finally {
      if (!a.signal.aborted) resolving.value = false;
    }
  }

  /** Messages with a usage but no capture, asked for one at a time (no endpoint yet). */
  async function withoutCaptures(signal: AbortSignal): Promise<Set<string>> {
    await loadIndex();
    const used = [...(index.value?.used ?? [])].map((id) => [id, index.value!.keyOf.get(id)!] as const);
    const checked = used.slice(0, UNCAPTURED_PROBE_LIMIT);
    probe.value = { done: 0, total: checked.length, capped: used.length > checked.length };
    const out = new Set<string>();
    await eachLimit(
      checked,
      PROBE_CONCURRENCY,
      async ([id, key]) => {
        if (signal.aborted) return;
        const got = await port.messageCaptures(options.tenant.value, options.projectId.value, key, { branch: branch.value, limit: 1 }, signal);
        if (!got.captures.length) out.add(id);
      },
      () => {
        if (probe.value) probe.value = { ...probe.value, done: probe.value.done + 1 };
      },
    );
    return out;
  }

  watch([options.filter, options.tenant, options.projectId, branch], () => void resolve(), { immediate: true, deep: true });

  return {
    index,
    indexError,
    loadingIndex,
    allowed,
    resolving,
    error,
    probe,
    loadIndex,
    /** True while a filter is on and its message IDs aren't known yet. */
    pending: computed(() => resolving.value && allowed.value === undefined),
  };
}
