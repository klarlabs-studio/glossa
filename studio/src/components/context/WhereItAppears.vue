<script setup lang="ts">
/**
 * "Where it appears" (RFC 0004 §3.4): the message's usages grouped
 * application → route → component with `file:line`, and the screenshots
 * that show it — cropped around the message, with a full-page lightbox
 * and a toggle between the locales it was captured in.
 *
 * Three empty states are explicit, because they mean different things:
 * nothing was ever uploaded ("no usage data yet"), the collectors saw
 * the message nowhere ("unused"), and no screenshot shows it ("not
 * captured").
 */
import { computed, onBeforeUnmount, ref, shallowRef, watch } from "vue";
import { useContextPort, type ContextCoverage } from "../../api/context";
import type { MessageCapture, MessageCaptures, MessageUsages } from "../../api/context-schemas";
import type { Application, Message, ProjectLocale } from "../../api/schemas";
import { localeName } from "../../lib/bcp47";
import { captureFor, groupUsages, localesOf, repositoryLink, screensOf, usageLocation, type GitConnection } from "../../lib/context";
import { strings } from "../../strings";
import ErrorAlert from "../ErrorAlert.vue";
import ModalDialog from "../ModalDialog.vue";
import CaptureShot from "./CaptureShot.vue";

const props = defineProps<{
  tenant: string;
  projectId: string;
  message: Message;
  source: ProjectLocale;
  target: ProjectLocale;
  /** The project's applications, to name them; their IDs show while it is empty. */
  applications?: Application[];
  /**
   * A branch view (RFC 0004 §4.1); the default branch's builds without
   * one. The branch switcher is a later slice.
   */
  branch?: string | undefined;
  /**
   * The repository behind the code, once a Git connection exists
   * (RFC 0004 §6). Without one, every usage stays plain text.
   */
  repository?: GitConnection | undefined;
}>();
const s = strings.where;
const port = useContextPort();

/** How many screens show before "Show n more". */
const FIRST_SCREENS = 2;

const usages = shallowRef<MessageUsages>();
const captures = shallowRef<MessageCaptures>();
const coverage = shallowRef<ContextCoverage>();
const loading = ref(false);
const error = ref<unknown>(null);
/** The locale the screenshots show; unset until the translator picks one. */
const wantedLocale = ref<string>();
const expanded = ref(false);
let abort: AbortController | undefined;

async function load(): Promise<void> {
  abort?.abort();
  const a = (abort = new AbortController());
  loading.value = true;
  error.value = null;
  usages.value = undefined;
  captures.value = undefined;
  coverage.value = undefined;
  wantedLocale.value = undefined;
  expanded.value = false;
  const q = { branch: props.branch };
  try {
    const [u, c] = await Promise.all([
      port.messageUsages(props.tenant, props.projectId, props.message.key, q, a.signal),
      port.messageCaptures(props.tenant, props.projectId, props.message.key, q, a.signal),
    ]);
    if (a.signal.aborted) return;
    usages.value = u;
    captures.value = c;
    // Nothing here can mean two things; the project's coverage tells them apart.
    if (!u.usages.length && !c.captures.length) {
      coverage.value = await port.coverage(props.tenant, props.projectId, props.branch, a.signal);
    }
  } catch (e) {
    if (!a.signal.aborted) error.value = e;
  } finally {
    if (!a.signal.aborted) loading.value = false;
  }
}
watch(() => [props.message.id, props.branch], load, { immediate: true });
onBeforeUnmount(() => abort?.abort());

const applicationName = (id: string) => props.applications?.find((a) => a.id === id)?.name ?? id;
const groups = computed(() => groupUsages(usages.value?.usages ?? [], applicationName));
const screens = computed(() => screensOf(captures.value?.captures ?? []));
const captureLocales = computed(() => localesOf(captures.value?.captures ?? []));

/** The locale shown: the pick, else the target's where it was captured, else the source's. */
const shownLocale = computed(
  () => wantedLocale.value ?? [props.target.code, props.source.code].find((l) => captureLocales.value.includes(l)) ?? captureLocales.value[0],
);
/** One card per screen: the capture shown for it, and what is missing about it. */
const cards = computed(() => {
  const all = screens.value.map((screen) => ({ screen, capture: captureFor(screen, shownLocale.value, props.source.code) }));
  const shown = all.filter((c): c is { screen: (typeof screens.value)[number]; capture: MessageCapture } => !!c.capture);
  return expanded.value ? shown : shown.slice(0, FIRST_SCREENS);
});
const hidden = computed(() => Math.max(0, screens.value.length - cards.value.length));
const visibleHere = (c: MessageCapture) => c.regions.some((r) => r.visible && r.box.width > 0 && r.box.height > 0);

const lightbox = shallowRef<MessageCapture>();
const localeLabel = (code: string) => `${code} — ${localeName(code)}`;
const shot = (c: MessageCapture) => s.screen(c.route, c.viewport.width, c.viewport.height, c.locale);

/** Everything the collectors know about this message, so far: nothing. */
const empty = computed(() => !!usages.value && !usages.value.usages.length && !captures.value?.captures.length);
/** Nothing was ever uploaded, so "unused" would be a lie. */
const noData = computed(() => empty.value && coverage.value?.current_builds === 0);
</script>

<template>
  <section class="pane stack-sm" aria-labelledby="where-h" data-testid="where-pane">
    <h3 id="where-h">{{ s.title }}</h3>
    <ErrorAlert :error="error" />
    <p v-if="loading" class="muted" role="status">{{ strings.app.loading }}</p>

    <template v-else-if="!error">
      <!-- Nothing known at all: either no upload yet, or an unused message. -->
      <div v-if="empty" class="stack-sm">
        <template v-if="noData">
          <p class="muted" data-testid="where-no-data">{{ s.noData }}</p>
          <p class="hint">{{ s.noDataHint }}</p>
        </template>
        <template v-else>
          <p data-testid="where-unused"><span class="pill pill-warn">{{ s.unused }}</span> {{ s.unusedLead }}</p>
          <p class="hint">{{ s.unusedHint }}</p>
        </template>
      </div>

      <template v-else>
        <!-- ── usages ─────────────────────────────────────────────── -->
        <h4>{{ s.inCode }}</h4>
        <p v-if="!groups.length" class="muted" data-testid="where-no-usages">{{ s.noUsages }}</p>
        <ul v-else class="apps" data-testid="where-usages">
          <li v-for="app in groups" :key="app.applicationId" class="app stack-sm">
            <p class="app-name">{{ app.application }}</p>
            <ul class="routes">
              <li v-for="r in app.routes" :key="r.route ?? ''">
                <p class="route">
                  <code v-if="r.route">{{ r.route }}</code>
                  <span v-else class="muted">{{ s.noRoute }}</span>
                </p>
                <ul class="components">
                  <li v-for="c in r.components" :key="c.component ?? ''">
                    <p class="component">
                      <strong v-if="c.component">{{ c.component }}</strong>
                      <span v-else class="muted">{{ s.noComponent }}</span>
                    </p>
                    <ul class="places">
                      <li v-for="(u, i) in c.usages" :key="`${u.build_id}:${u.file}:${u.line}:${i}`" data-testid="usage">
                        <!-- A repository link needs a Git connection (RFC 0004 §6); until then the place is plain text. -->
                        <a v-if="repositoryLink(u, props.repository)" class="place" :href="repositoryLink(u, props.repository)" rel="noreferrer noopener">{{ usageLocation(u) }}</a>
                        <code v-else class="place">{{ usageLocation(u) }}</code>
                        <span class="hint">{{ s.kind[u.kind] ?? u.kind }}</span>
                        <span v-if="!u.on_default_branch" class="pill pill-neutral">{{ s.fromBranch(u.branch) }}</span>
                      </li>
                    </ul>
                  </li>
                </ul>
              </li>
            </ul>
          </li>
        </ul>
        <p v-if="usages?.truncated" class="hint">{{ s.moreUsages(usages.usages.length) }}</p>

        <!-- ── screenshots ────────────────────────────────────────── -->
        <h4>{{ s.onScreen }}</h4>
        <div v-if="!screens.length" class="stack-sm">
          <p data-testid="where-not-captured"><span class="pill pill-neutral">{{ s.notCaptured }}</span> {{ s.notCapturedLead }}</p>
          <p class="hint">{{ s.notCapturedHint }}</p>
        </div>
        <template v-else>
          <div v-if="captureLocales.length > 1" class="field locale-field">
            <label for="where-locale">{{ s.screenshotLocale }}</label>
            <select id="where-locale" data-testid="where-locale" :value="shownLocale" @change="wantedLocale = ($event.target as HTMLSelectElement).value">
              <option v-for="l in captureLocales" :key="l" :value="l">{{ localeLabel(l) }}</option>
            </select>
          </div>
          <ul class="screens">
            <li v-for="{ screen, capture } in cards" :key="screen.id" class="screen stack-sm" data-testid="screen">
              <figure class="stack-sm">
                <CaptureShot :capture="capture" fit="crop" :alt="s.alt(screen.route, capture.locale)" />
                <figcaption>{{ shot(capture) }}</figcaption>
              </figure>
              <p v-if="shownLocale && capture.locale !== shownLocale" class="hint" data-testid="locale-fallback">
                {{ s.localeFallback(shownLocale, capture.locale) }}
              </p>
              <p v-if="!visibleHere(capture)" class="hint" data-testid="not-visible">{{ s.notVisible }}</p>
              <div>
                <button type="button" class="btn btn-sm" @click="lightbox = capture">{{ s.openFull }}</button>
              </div>
            </li>
          </ul>
          <div v-if="hidden">
            <button type="button" class="btn btn-sm" data-testid="more-screens" @click="expanded = true">{{ s.moreScreens(hidden) }}</button>
          </div>
          <p v-if="captures?.truncated" class="hint">{{ s.truncatedCaptures(captures.captures.length) }}</p>
        </template>
      </template>
    </template>

    <!-- The lightbox is a native modal: focus is trapped, Escape closes it. -->
    <ModalDialog :open="!!lightbox" wide :title="lightbox ? s.lightbox(lightbox.route, lightbox.locale) : ''" @close="lightbox = undefined">
      <template v-if="lightbox">
        <CaptureShot :capture="lightbox" fit="page" :alt="s.alt(lightbox.route, lightbox.locale)" />
        <p class="hint">{{ shot(lightbox) }}</p>
      </template>
      <template #actions>
        <button type="button" class="btn" @click="lightbox = undefined">{{ strings.app.close }}</button>
      </template>
    </ModalDialog>
  </section>
</template>

<style scoped>
.apps,
.routes,
.components,
.places,
.screens {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-2);
}
.app {
  padding: var(--kl-space-3);
  border: 1px solid var(--kl-border);
  border-radius: var(--kl-radius-md);
  background: var(--kl-surface);
}
.app-name {
  font-weight: var(--kl-weight-semibold);
}
.routes {
  gap: var(--kl-space-3);
}
.route code {
  font-size: var(--kl-text-sm);
}
.components {
  padding-inline-start: var(--kl-space-3);
  border-inline-start: 2px solid var(--kl-border);
  gap: var(--kl-space-2);
}
.places li {
  display: flex;
  gap: var(--kl-space-2);
  align-items: baseline;
  flex-wrap: wrap;
}
.place {
  font-family: var(--kl-font-mono);
  font-size: var(--kl-text-sm);
  overflow-wrap: anywhere;
}
.locale-field {
  max-inline-size: 16rem;
}
.screen {
  padding: var(--kl-space-3);
  border: 1px solid var(--kl-border);
  border-radius: var(--kl-radius-md);
  background: var(--kl-surface);
}
figure {
  margin: 0;
}
figcaption {
  font-size: var(--kl-text-sm);
  color: var(--kl-ink-secondary);
}
</style>
