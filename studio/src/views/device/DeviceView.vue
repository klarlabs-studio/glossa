<script setup lang="ts">
/**
 * Device sign-in (RFC 0006 §7.2, the OAuth 2.0 device authorization
 * grant of RFC 8628). `glossa login` shows a code and this page's
 * address; the signed-in person enters the code here — or follows
 * `/device?code=…`, which fills it in — sees what is asking, and
 * approves or denies it.
 *
 * Approving signs that device in as this person with everything they
 * can do, so the page always shows the request before the buttons and
 * says plainly to approve only a sign-in they started: a code read out
 * by someone else is the attack this flow invites (RFC 8628 §5.4).
 *
 * The page needs a session: the router sends an anonymous visitor to
 * sign in with `next` set to this address, query included, and brings
 * them back with the code still filled in.
 */
import { computed, nextTick, onMounted, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { useDevice } from "../../api/device";
import type { DeviceAuthorizationView, DeviceDecision } from "../../api/device-schemas";
import { isApiError } from "../../api/errors";
import AuthLayout from "../../components/AuthLayout.vue";
import ErrorAlert from "../../components/ErrorAlert.vue";
import { formatUserCode, isUserCode, normalizeUserCode } from "../../lib/device-code";
import { absoluteTime, relativeTime } from "../../lib/time";
import { strings } from "../../strings";

const route = useRoute();
const router = useRouter();
const api = useDevice();
const s = strings.device;

const code = ref("");
const codeInvalid = ref(false);
const found = ref<DeviceAuthorizationView | null>(null);
const outcome = ref<DeviceDecision | "">("");
const busy = ref<"" | "lookup" | DeviceDecision>("");
const error = ref<unknown>(null);

const codeInput = ref<HTMLInputElement | null>(null);
const requestHeading = ref<HTMLElement | null>(null);
const outcomeText = ref<HTMLElement | null>(null);

const title = computed(() => (outcome.value === "approved" ? s.approvedTitle : outcome.value === "denied" ? s.deniedTitle : s.title));

/** What the polite live region says while a call is in flight. */
const status = computed(() => (busy.value === "lookup" ? s.lookingUp : busy.value === "approved" ? s.approving : busy.value === "denied" ? s.denying : ""));

const when = (iso: string) => s.when(relativeTime(iso), absoluteTime(iso));

async function focus(el: { value: HTMLElement | null }): Promise<void> {
  await nextTick();
  el.value?.focus();
}

function onInput(event: Event): void {
  const input = event.target as HTMLInputElement;
  code.value = formatUserCode(input.value);
  // Keep the field showing the formatted code even when the model didn't change.
  input.value = code.value;
  codeInvalid.value = false;
}

/** The session ended between page load and this call: sign in again and come back here, code and all. */
async function signInAgain(): Promise<void> {
  const next = router.resolve({ name: "device", query: code.value ? { code: code.value } : {} }).fullPath;
  await router.replace({ name: "sign-in", query: { next } });
}

async function lookUp(): Promise<void> {
  error.value = null;
  if (!isUserCode(code.value)) {
    codeInvalid.value = true;
    await focus(codeInput);
    return;
  }
  busy.value = "lookup";
  try {
    found.value = await api.lookup(normalizeUserCode(code.value));
    await focus(requestHeading);
  } catch (e) {
    if (isApiError(e) && e.status === 401) return signInAgain();
    error.value = e;
    await focus(codeInput);
  } finally {
    busy.value = "";
  }
}

async function decide(decision: DeviceDecision): Promise<void> {
  const request = found.value;
  if (!request) return;
  error.value = null;
  busy.value = decision;
  try {
    await api.decide(normalizeUserCode(request.user_code), decision);
    outcome.value = decision;
    await focus(outcomeText);
  } catch (e) {
    if (isApiError(e) && e.status === 401) return signInAgain();
    error.value = e;
    // The code is gone (expired, or decided elsewhere): back to the field.
    if (isApiError(e, "device_authorization_not_found")) {
      found.value = null;
      await focus(codeInput);
    }
  } finally {
    busy.value = "";
  }
}

async function startOver(): Promise<void> {
  found.value = null;
  error.value = null;
  code.value = "";
  await focus(codeInput);
}

onMounted(async () => {
  const q = Array.isArray(route.query.code) ? route.query.code[0] : route.query.code;
  if (typeof q === "string" && q.trim() !== "") {
    code.value = formatUserCode(q);
    await lookUp();
  } else {
    await focus(codeInput);
  }
});
</script>

<template>
  <AuthLayout :title="title" :lead="outcome ? undefined : s.lead">
    <p class="visually-hidden" role="status" aria-live="polite" data-testid="device-status">{{ status }}</p>

    <template v-if="outcome">
      <p ref="outcomeText" tabindex="-1" role="status" :class="['alert', outcome === 'approved' ? 'alert-ok' : 'alert-warn']" data-testid="device-outcome">
        {{ outcome === "approved" ? s.approved : s.denied }}
      </p>
    </template>

    <template v-else>
      <ErrorAlert :error="error" />

      <section v-if="found" class="stack" aria-labelledby="device-request">
        <h2 id="device-request" ref="requestHeading" tabindex="-1" class="h3">{{ s.found }}</h2>
        <dl class="facts">
          <dt>{{ s.device }}</dt>
          <dd>
            <strong data-testid="device-client">{{ found.client_name }}</strong>
            <span class="hint">{{ s.deviceHint }}</span>
          </dd>
          <dt>{{ s.code }}</dt>
          <dd><code>{{ formatUserCode(found.user_code) }}</code></dd>
          <dt>{{ s.requested }}</dt>
          <dd><time :datetime="found.requested_at">{{ when(found.requested_at) }}</time></dd>
          <dt>{{ s.expires }}</dt>
          <dd><time :datetime="found.expires_at">{{ when(found.expires_at) }}</time></dd>
        </dl>

        <div class="alert alert-warn" data-testid="device-warning">
          <p class="alert-title">{{ s.warningTitle }}</p>
          <p>{{ s.warning }}</p>
        </div>

        <div class="actions">
          <button type="button" class="btn btn-primary" :disabled="busy !== ''" @click="decide('approved')">
            {{ busy === "approved" ? s.approving : s.approve }}
          </button>
          <button type="button" class="btn btn-danger" :disabled="busy !== ''" @click="decide('denied')">
            {{ busy === "denied" ? s.denying : s.deny }}
          </button>
          <button type="button" class="btn btn-ghost" :disabled="busy !== ''" @click="startOver">{{ s.otherCode }}</button>
        </div>
      </section>

      <form v-else class="stack" novalidate @submit.prevent="lookUp">
        <div class="field">
          <label for="device-code">{{ s.code }}</label>
          <input
            id="device-code"
            ref="codeInput"
            :value="code"
            class="mono code-input"
            name="device-code"
            type="text"
            inputmode="text"
            autocomplete="off"
            autocapitalize="characters"
            autocorrect="off"
            spellcheck="false"
            placeholder="XXXX-XXXX"
            :aria-invalid="codeInvalid"
            :aria-describedby="codeInvalid ? 'device-code-error' : 'device-code-hint'"
            required
            @input="onInput"
          />
          <span v-if="codeInvalid" id="device-code-error" class="field-error">{{ s.codeInvalid }}</span>
          <span v-else id="device-code-hint" class="hint">{{ s.codeHint }}</span>
        </div>
        <button type="submit" class="btn btn-primary" :disabled="busy !== ''">
          {{ busy === "lookup" ? s.lookingUp : s.lookUp }}
        </button>
      </form>
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
  display: flex;
  flex-direction: column;
  overflow-wrap: anywhere;
}
.code-input {
  font-size: var(--kl-text-lg, 1.125rem);
  letter-spacing: 0.15em;
  text-transform: uppercase;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--kl-space-3);
}
</style>
