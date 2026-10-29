<script setup lang="ts">
/**
 * Sign-in adapts to what the server offers (GET /v1/meta): with email,
 * the emailed link leads and password waits under "Other ways"; without
 * email, password and passkey are the way in. A passkey button shows
 * wherever the server has passkeys and the browser supports them.
 */
import { computed, onMounted, ref } from "vue";
import { RouterLink, useRoute, useRouter } from "vue-router";
import { auth } from "../../api/endpoints";
import { isApiError } from "../../api/errors";
import AuthLayout from "../../components/AuthLayout.vue";
import ErrorAlert from "../../components/ErrorAlert.vue";
import { forgetHash, safeNext, tokenFromHash } from "../../lib/links";
import { getAssertion, passkeysSupported } from "../../lib/webauthn";
import { useMeta } from "../../session/meta";
import { establishSession } from "../../session/session";
import { strings } from "../../strings";

const route = useRoute();
const router = useRouter();
const { magicLink, passkey, email: sendsEmail } = useMeta();
const s = strings.auth;

const email = ref("");
const password = ref("");
const totp = ref("");
const needTotp = ref(false);
const busy = ref<"" | "link" | "password" | "passkey" | "redeem">("");
const error = ref<unknown>(null);
const sentTo = ref<string>();

const canPasskey = computed(() => passkey.value && passkeysSupported());

async function finish(session: Parameters<typeof establishSession>[0]): Promise<void> {
  await establishSession(session);
  await router.replace(safeNext(route.query.next) ?? { name: "home" });
}

async function sendLink(): Promise<void> {
  error.value = null;
  busy.value = "link";
  try {
    await auth.requestMagicLink(email.value.trim());
    sentTo.value = email.value.trim();
  } catch (e) {
    error.value = e;
  } finally {
    busy.value = "";
  }
}

async function withPassword(): Promise<void> {
  error.value = null;
  busy.value = "password";
  try {
    const body = { email: email.value.trim(), password: password.value, ...(needTotp.value && totp.value ? { totp_code: totp.value } : {}) };
    await finish(await auth.signInWithPassword(body));
  } catch (e) {
    if (isApiError(e, "totp_required")) needTotp.value = true;
    error.value = e;
  } finally {
    busy.value = "";
  }
}

async function withPasskey(): Promise<void> {
  error.value = null;
  busy.value = "passkey";
  try {
    const { options } = await auth.beginPasskeySignIn(email.value.trim());
    await finish(await auth.finishPasskeySignIn(await getAssertion(options)));
  } catch (e) {
    error.value = e;
  } finally {
    busy.value = "";
  }
}

onMounted(async () => {
  const token = tokenFromHash(location.hash);
  if (!token) return;
  forgetHash();
  busy.value = "redeem";
  try {
    await finish(await auth.redeemMagicLink(token));
  } catch (e) {
    error.value = isApiError(e, "link_invalid") ? new Error(s.linkInvalid) : e;
  } finally {
    busy.value = "";
  }
});
</script>

<template>
  <AuthLayout v-if="busy === 'redeem'" :title="s.redeeming">
    <p role="status" class="muted">{{ strings.app.loading }}</p>
  </AuthLayout>

  <AuthLayout v-else-if="sentTo" :title="s.linkSentTitle">
    <p role="status">{{ s.linkSent(sentTo) }}</p>
    <button type="button" class="btn" @click="sentTo = undefined">{{ s.useDifferentEmail }}</button>
  </AuthLayout>

  <AuthLayout v-else :title="s.signInTitle" :lead="magicLink ? s.signInLead : s.signInLeadNoEmail">
    <ErrorAlert :error="error" />

    <!-- With email: the link leads; passkey beside it; password under "Other ways". -->
    <template v-if="magicLink">
      <form class="stack" @submit.prevent="sendLink">
        <div class="field">
          <label for="email">{{ s.email }}</label>
          <input id="email" v-model="email" type="email" name="email" autocomplete="username webauthn" required />
        </div>
        <button type="submit" class="btn btn-primary" :disabled="busy !== ''">{{ busy === "link" ? s.sending : s.sendLink }}</button>
        <button v-if="canPasskey" type="button" class="btn" :disabled="busy !== '' || !email" @click="withPasskey">{{ s.passkey }}</button>
      </form>
      <details class="others">
        <summary>{{ s.otherMethods }}</summary>
        <div class="stack others-body">
          <form class="stack" data-testid="password-form" @submit.prevent="withPassword">
            <div class="field">
              <label for="password">{{ s.password }}</label>
              <input id="password" v-model="password" type="password" name="password" autocomplete="current-password" required />
            </div>
            <div v-if="needTotp" class="field">
              <label for="totp">{{ s.totp }}</label>
              <input id="totp" v-model="totp" inputmode="numeric" pattern="[0-9]{6}" maxlength="6" autocomplete="one-time-code" aria-describedby="totp-hint" required />
              <span id="totp-hint" class="hint">{{ s.totpHint }}</span>
            </div>
            <button type="submit" class="btn" :disabled="busy !== '' || !email">{{ s.signInWithPassword }}</button>
          </form>
          <RouterLink v-if="sendsEmail" :to="{ name: 'reset-password' }">{{ s.forgotPassword }}</RouterLink>
        </div>
      </details>
    </template>

    <!-- Without email: password and passkey are the way in. -->
    <template v-else>
      <form class="stack" data-testid="password-form" @submit.prevent="withPassword">
        <div class="field">
          <label for="email">{{ s.email }}</label>
          <input id="email" v-model="email" type="email" name="email" autocomplete="username webauthn" required />
        </div>
        <div class="field">
          <label for="password">{{ s.password }}</label>
          <input id="password" v-model="password" type="password" name="password" autocomplete="current-password" required />
        </div>
        <div v-if="needTotp" class="field">
          <label for="totp">{{ s.totp }}</label>
          <input id="totp" v-model="totp" inputmode="numeric" pattern="[0-9]{6}" maxlength="6" autocomplete="one-time-code" aria-describedby="totp-hint" required />
          <span id="totp-hint" class="hint">{{ s.totpHint }}</span>
        </div>
        <button type="submit" class="btn btn-primary" :disabled="busy !== ''">{{ s.signInWithPassword }}</button>
        <button v-if="canPasskey" type="button" class="btn" :disabled="busy !== '' || !email" @click="withPasskey">{{ s.passkey }}</button>
      </form>
      <p class="hint">{{ s.noEmailHint }}</p>
    </template>

    <template #footer>
      {{ s.noAccount }} <RouterLink :to="{ name: 'register' }">{{ s.register }}</RouterLink>
    </template>
  </AuthLayout>
</template>

<style scoped>
.others summary {
  cursor: pointer;
  color: var(--kl-ink-secondary);
}
.others-body {
  margin-block-start: var(--kl-space-4);
}
</style>
