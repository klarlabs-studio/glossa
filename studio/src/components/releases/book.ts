/**
 * What the Releases screens share: the project's environments, the
 * releases loaded so far (plus any fetched by ID for labels), and each
 * environment's latest deployment (when and by whom it last changed).
 */
import { computed, ref, shallowRef, type ComputedRef, type Ref } from "vue";
import type { ProjectRef, ReleasesPort } from "../../api/releases";
import type { Deployment, EnvironmentPolicy, Environment, Release } from "../../api/schemas";
import { sortEnvironments } from "../../lib/releases";
import { strings } from "../../strings";

export interface ReleaseBook {
  environments: Ref<Environment[]>;
  releases: Ref<Release[]>;
  hasMore: ComputedRef<boolean>;
  byId: ComputedRef<Map<string, Release>>;
  latest: Ref<Map<string, Deployment>>;
  loaded: Ref<boolean>;
  load(): Promise<void>;
  more(): Promise<void>;
  /** Fetch releases not loaded yet (for labels and diffs). */
  ensure(ids: ReadonlyArray<string | undefined>): Promise<void>;
  env(name: string): Environment | undefined;
  label(id: string | undefined): string;
}

export function createReleaseBook(port: ReleasesPort, project: () => ProjectRef): ReleaseBook {
  const environments = ref<Environment[]>([]);
  const releases = ref<Release[]>([]);
  const next = ref<string>();
  const extra = shallowRef(new Map<string, Release>());
  const latest = ref(new Map<string, Deployment>());
  const loaded = ref(false);

  const byId = computed(() => {
    const m = new Map(extra.value);
    for (const r of releases.value) m.set(r.id, r);
    return m;
  });

  async function ensure(ids: ReadonlyArray<string | undefined>): Promise<void> {
    const missing = [...new Set(ids.filter((id): id is string => !!id && !byId.value.has(id)))];
    if (!missing.length) return;
    const got = await Promise.all(missing.map((id) => port.release(project(), id)));
    const m = new Map(extra.value);
    for (const r of got) m.set(r.id, r);
    extra.value = m;
  }

  async function load(): Promise<void> {
    const p = project();
    const [envs, page] = await Promise.all([port.environments(p), port.releases(p)]);
    environments.value = sortEnvironments(envs);
    releases.value = page.items;
    next.value = page.next;
    const heads = await Promise.all(envs.map(async (e) => [e.name, (await port.deployments(p, e.name, 1))[0]] as const));
    latest.value = new Map(heads.filter((h): h is readonly [string, Deployment] => h[1] !== undefined));
    await ensure(envs.map((e) => e.current_release_id));
    loaded.value = true;
  }

  async function more(): Promise<void> {
    if (!next.value) return;
    const page = await port.releases(project(), next.value);
    releases.value = [...releases.value, ...page.items];
    next.value = page.next;
  }

  return {
    environments,
    releases,
    hasMore: computed(() => next.value !== undefined),
    byId,
    latest,
    loaded,
    load,
    more,
    ensure,
    env: (name) => environments.value.find((e) => e.name === name),
    label: (id) => {
      if (!id) return strings.releases.noneYet;
      const r = byId.value.get(id);
      return r ? strings.releases.version(r.version) : "…";
    },
  };
}

/** "Approved; outdated included" */
export function policyText(p: EnvironmentPolicy): string {
  const s = strings.releases;
  const states = p.states.map((st) => s.state[st] ?? st).join(", ");
  return s.policyText(states, p.include_outdated ? s.outdatedIncluded : s.outdatedExcluded);
}

/** "1.2 MB" */
export function bytesText(n: number): string {
  const units = ["B", "kB", "MB", "GB"];
  let v = n;
  let i = 0;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  return `${i === 0 ? v : v.toFixed(1)} ${units[i]}`;
}
