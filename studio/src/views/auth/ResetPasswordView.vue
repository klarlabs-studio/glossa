<script setup lang="ts">
import { onMounted, ref } from "vue";
import { RouterLink } from "vue-router";
import { auth } from "../../api/endpoints";
import AuthLayout from "../../components/AuthLayout.vue";
import ErrorAlert from "../../components/ErrorAlert.vue";
import { forgetHash, tokenFromHash } from "../../lib/links";
import { useMeta } from "../../session/meta";
import { strings } from "../../strings";

const { email: sendsEmail } = useMeta();

const token = ref<string>();
const email = ref("");
const password = ref("");
const busy = ref(false);
const error = ref<unknown>(null);
const done = ref<"" | "sent" | "changed">("");

onMounted(() => {
  token.value = tokenFromHash(location.hash);
  if (token.value) forgetHash();
});

async function run(action: () => Promise<void>, outcome: "sent" | "changed"): Promise<void> {
  error.value = null;
  busy.value = true;
  try {
    await action();
    done.value = outcome;
  } catch (e) {
    error.value = e;
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <AuthLayout v-if="!sendsEmail" :title="strings.auth.resetTitle">
    <p role="status">{{ strings.auth.resetUnavailable }}</p>
    <RouterLink :to="{ name: 'sign-in' }">{{ strings.auth.backToSignIn }}</RouterLink>
  </AuthLayout>
  <AuthLayout v-else-if="done" :title="done === 'sent' ? strings.auth.linkSentTitle : strings.auth.setPasswordTitle">
    <p role="status">{{ done === "sent" ? strings.auth.resetSent(email) : strings.auth.passwordChanged }}</p>
    <RouterLink :to="{ name: 'sign-in' }">{{ strings.auth.backToSignIn }}</RouterLink>
  </AuthLayout>
  <AuthLayout v-else-if="token" :title="strings.auth.setPasswordTitle">
    <ErrorAlert :error="error" />
    <form class="stack" @submit.prevent="run(() => auth.resetPassword(token!, password), 'changed')">
      <div class="field">
        <label for="password">{{ strings.auth.newPassword }}</label>
        <input id="password" v-model="password" type="password" autocomplete="new-password" minlength="12" maxlength="1024" required />
      </div>
      <button type="submit" class="btn btn-primary" :disabled="busy">{{ strings.auth.setPassword }}</button>
    </form>
  </AuthLayout>
  <AuthLayout v-else :title="strings.auth.resetTitle" :lead="strings.auth.resetLead">
    <ErrorAlert :error="error" />
    <form class="stack" @submit.prevent="run(() => auth.requestPasswordReset(email.trim()), 'sent')">
      <div class="field">
        <label for="email">{{ strings.auth.email }}</label>
        <input id="email" v-model="email" type="email" autocomplete="email" required />
      </div>
      <button type="submit" class="btn btn-primary" :disabled="busy">{{ strings.auth.resetSend }}</button>
    </form>
    <template #footer>
      <RouterLink :to="{ name: 'sign-in' }">{{ strings.auth.backToSignIn }}</RouterLink>
    </template>
  </AuthLayout>
</template>
