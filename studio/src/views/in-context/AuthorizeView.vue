<script setup lang="ts">
/**
 * The in-product editor's authorization popup (RFC 0004 §5.2).
 *
 * The overlay runs on the product's own preview deployment and cannot
 * use Studio's session cookie, so it opens this page — which does have
 * the session — and waits for an answer by `postMessage`. The page says
 * plainly what the editor will be allowed to do and who it will act as,
 * because that is the whole decision being asked for: a page on another
 * origin is about to borrow this person's permissions.
 *
 * Nothing is minted until the person confirms. The token is posted to
 * the exact origin in the query and to no other, the window closes, and
 * the token is never written anywhere — not to storage, not to the URL,
 * not to the log.
 */
import { computed, onMounted, ref } from "vue";
import { useRoute } from "vue-router";
import { useInContext } from "../../api/in-context";
import type { InContextPermission } from "../../api/in-context-schemas";
import { isApiError } from "../../api/errors";
import AuthLayout from "../../components/AuthLayout.vue";
import ErrorAlert from "../../components/ErrorAlert.vue";
import { IN_CONTEXT_MESSAGE, type InContextMessage } from "../../lib/in-context-message";
import { useSession } from "../../session/session";
import { strings } from "../../strings";

const route = useRoute();
const api = useInContext();
const { person } = useSession();
const s = strings.inContext;

const tenant = String(route.query.tenant ?? "");
const project = String(route.query.project ?? "");
const channel = String(route.query.channel ?? "");
const requested = String(route.query.origin ?? "");

/**
 * The origin is parsed here, not trusted: it becomes a `postMessage`
 * target, so anything that is not a plain scheme-host-port is refused
 * before the page offers to mint anything. The server checks it against
 * the project's registrations as well; this is the check that stops the
 * *page* from posting somewhere it shouldn't.
 */
const origin = computed(() => {
  try {
    const u = new URL(requested);
    if (u.pathname !== "/" || u.search || u.hash || u.username || u.password) return "";
    return u.origin === "null" ? "" : u.origin;
  } catch {
    return "";
  }
});

const busy = ref(false);
const error = ref<unknown>(null);
const done = ref<"granted" | "denied" | "">("");
const permissions = ref<InContextPermission[]>([]);

/**
 * What the editor will be allowed to do, shown before minting. It is the
 * ceiling the server cuts every grant to, narrowed by what this person
 * actually holds — the popup asks the server for the real list only when
 * the person confirms, so this is the plain-language version of it.
 */
const willAllow = computed(() => [s.mayInspect, s.mayEdit, s.mayAsk, s.mayTerms]);

/** Whether the opener is still there to answer. */
const opener = (): Window | null => (typeof window === "undefined" ? null : window.opener);

function post(message: InContextMessage): void {
  const target = origin.value;
  const to = opener();
  if (!target || !to) return;
  // The exact origin that asked, never "*": this message carries a
  // credential.
  to.postMessage(message, target);
}

function deny(code: string, message: string): void {
  post({ type: IN_CONTEXT_MESSAGE, channel, ok: false, code, message });
  done.value = "denied";
  window.close();
}

async function authorize(): Promise<void> {
  error.value = null;
  busy.value = true;
  try {
    const grant = await api.mint(tenant, project, origin.value);
    permissions.value = grant.permissions;
    post({
      type: IN_CONTEXT_MESSAGE,
      channel,
      ok: true,
      token: grant.token,
      expires_at: grant.expires_at,
      project_id: grant.project_id,
      origin: grant.origin,
    });
    done.value = "granted";
    // The editor has what it needs; nothing here should outlive it.
    window.close();
  } catch (e) {
    error.value = e;
    if (isApiError(e)) post({ type: IN_CONTEXT_MESSAGE, channel, ok: false, code: e.code, message: e.message });
  } finally {
    busy.value = false;
  }
}

function cancel(): void {
  deny("cancelled", s.cancelled);
}

/** A request missing any of the four is not one of ours. */
const valid = computed(() => tenant !== "" && project !== "" && channel !== "" && origin.value !== "");

onMounted(() => {
  if (!valid.value) error.value = new Error(s.badRequest);
});
</script>

<template>
  <AuthLayout :title="s.title" :lead="s.lead">
    <ErrorAlert :error="error" />

    <template v-if="done === 'granted'">
      <p role="status">{{ s.granted }}</p>
    </template>

    <template v-else-if="valid">
      <dl class="facts">
        <dt>{{ s.actingAs }}</dt>
        <dd>{{ person?.email ?? s.you }}</dd>
        <dt>{{ s.onOrigin }}</dt>
        <dd><code>{{ origin }}</code></dd>
      </dl>

      <section aria-labelledby="ic-allow" class="stack-sm">
        <h2 id="ic-allow" class="h3">{{ s.willAllow }}</h2>
        <ul class="allow">
          <li v-for="line in willAllow" :key="line">{{ line }}</li>
        </ul>
        <p class="hint">{{ s.ceiling }}</p>
      </section>

      <p class="hint">{{ s.shortLived }}</p>

      <div class="actions">
        <button type="button" class="btn btn-primary" :disabled="busy" @click="authorize">
          {{ busy ? s.authorizing : s.authorize }}
        </button>
        <button type="button" class="btn" :disabled="busy" @click="cancel">{{ s.cancel }}</button>
      </div>
    </template>
  </AuthLayout>
</template>

<style scoped>
.facts {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: var(--kl-space-1) var(--kl-space-4);
  margin: 0;
}
.facts dt {
  color: var(--kl-ink-secondary);
}
.facts dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.allow {
  margin: 0;
  padding-inline-start: var(--kl-space-5);
}
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--kl-space-3);
}
</style>
