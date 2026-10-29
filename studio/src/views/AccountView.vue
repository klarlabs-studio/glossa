<script setup lang="ts">
/**
 * The signed-in person's credentials: every passkey registered on any
 * device (from the server, not guessed per browser) with its last use,
 * removable; the authenticator app; sign out everywhere.
 */
import { computed, onMounted, ref } from "vue";
import { useRouter } from "vue-router";
import { me as meApi } from "../api/endpoints";
import type { Passkey, TotpEnrollment } from "../api/schemas";
import ErrorAlert from "../components/ErrorAlert.vue";
import ModalDialog from "../components/ModalDialog.vue";
import { absoluteTime, relativeTime } from "../lib/time";
import { createCredential, passkeysSupported } from "../lib/webauthn";
import { useMeta } from "../session/meta";
import { refreshSession, signOut, useSession } from "../session/session";
import { strings } from "../strings";

const router = useRouter();
const { person } = useSession();
const { passkey: passkeysOn } = useMeta();
const s = strings.account;

const supported = passkeysSupported();
const canAdd = computed(() => supported && passkeysOn.value);
const passkeys = ref<Passkey[]>();
const passkeyName = ref<string>(s.passkeyNameDefault);
const passkeyBusy = ref(false);
const passkeyError = ref<unknown>(null);
const passkeyDone = ref("");

async function loadPasskeys(): Promise<void> {
  try {
    passkeys.value = await meApi.passkeys();
  } catch (e) {
    passkeyError.value = e;
  }
}
onMounted(loadPasskeys);

async function addPasskey(): Promise<void> {
  passkeyError.value = null;
  passkeyDone.value = "";
  passkeyBusy.value = true;
  try {
    const { options } = await meApi.beginPasskey();
    const pk = await meApi.finishPasskey(passkeyName.value.trim() || s.passkeyNameDefault, await createCredential(options));
    passkeyDone.value = s.passkeyAdded(pk.name);
    await loadPasskeys();
  } catch (e) {
    passkeyError.value = e;
  } finally {
    passkeyBusy.value = false;
  }
}

const removing = ref<Passkey | null>(null);
const removeBusy = ref(false);
const removeError = ref<unknown>(null);

function askRemove(pk: Passkey): void {
  removeError.value = null;
  passkeyDone.value = "";
  removing.value = pk;
}

async function removePasskey(): Promise<void> {
  const pk = removing.value;
  if (!pk) return;
  removeBusy.value = true;
  removeError.value = null;
  try {
    await meApi.deletePasskey(pk.id);
    removing.value = null;
    passkeyDone.value = s.passkeyRemoved(pk.name);
    await loadPasskeys();
  } catch (e) {
    removeError.value = e;
  } finally {
    removeBusy.value = false;
  }
}

const enrollment = ref<TotpEnrollment>();
const code = ref("");
const totpBusy = ref(false);
const totpError = ref<unknown>(null);
const totpDone = ref("");

async function totpStep(action: () => Promise<void>): Promise<void> {
  totpError.value = null;
  totpDone.value = "";
  totpBusy.value = true;
  try {
    await action();
  } catch (e) {
    totpError.value = e;
  } finally {
    totpBusy.value = false;
  }
}
const startTotp = () => totpStep(async () => void (enrollment.value = await meApi.beginTotp()));
const confirmTotp = () =>
  totpStep(async () => {
    await meApi.confirmTotp(code.value);
    enrollment.value = undefined;
    code.value = "";
    await refreshSession();
    totpDone.value = s.totpEnabled;
  });
const disableTotp = () =>
  totpStep(async () => {
    await meApi.disableTotp(code.value);
    code.value = "";
    await refreshSession();
    totpDone.value = s.totpDisabled;
  });

async function everywhere(): Promise<void> {
  await signOut(true).catch(() => undefined);
  await router.push({ name: "sign-in" });
}
</script>

<template>
  <div class="page stack">
    <h1>{{ s.title }}</h1>

    <section class="card stack" aria-labelledby="profile-h">
      <h2 id="profile-h">{{ s.profile }}</h2>
      <p>
        <strong>{{ person?.display_name || person?.email }}</strong>
        <span v-if="person?.display_name" class="muted"> · {{ person.email }}</span>
        <span class="pill" :class="person?.email_verified ? 'pill-ok' : 'pill-warn'">
          {{ person?.email_verified ? s.emailVerified : s.emailUnverified }}
        </span>
      </p>
    </section>

    <section id="passkeys" class="card stack" aria-labelledby="passkeys-h">
      <h2 id="passkeys-h">{{ s.passkeys }}</h2>
      <p class="muted">{{ s.passkeysLead }}</p>
      <p v-if="passkeys && !passkeys.length" class="muted" data-testid="no-passkeys">{{ s.noPasskeys }}</p>
      <div v-else-if="passkeys" class="scroll">
        <table class="table" data-testid="passkeys">
          <caption class="visually-hidden">{{ s.passkeys }}</caption>
          <thead>
            <tr>
              <th scope="col">{{ s.passkeyColumns.name }}</th>
              <th scope="col">{{ s.passkeyColumns.added }}</th>
              <th scope="col">{{ s.passkeyColumns.lastUsed }}</th>
              <th scope="col"><span class="visually-hidden">{{ s.passkeyColumns.actions }}</span></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="pk in passkeys" :key="pk.id">
              <th scope="row">{{ pk.name }}</th>
              <td><time :datetime="pk.created_at" :title="absoluteTime(pk.created_at)">{{ relativeTime(pk.created_at) }}</time></td>
              <td>
                <time v-if="pk.last_used_at" :datetime="pk.last_used_at" :title="absoluteTime(pk.last_used_at)">{{ relativeTime(pk.last_used_at) }}</time>
                <span v-else class="muted">{{ s.neverUsed }}</span>
              </td>
              <td class="num">
                <button type="button" class="btn btn-sm btn-danger" :aria-label="s.removePasskeyLabel(pk.name)" @click="askRemove(pk)">{{ s.removePasskey }}</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-if="!supported" class="alert alert-warn">{{ s.passkeysUnsupported }}</p>
      <p v-else-if="!passkeysOn" class="muted">{{ s.passkeysOff }}</p>
      <form v-if="canAdd" class="row" @submit.prevent="addPasskey">
        <div class="field">
          <label for="passkey-name">{{ s.passkeyName }}</label>
          <input id="passkey-name" v-model="passkeyName" maxlength="100" />
        </div>
        <button type="submit" class="btn btn-primary add" :disabled="passkeyBusy">{{ s.addPasskey }}</button>
      </form>
      <p v-if="passkeyDone" class="alert alert-ok" role="status">{{ passkeyDone }}</p>
      <ErrorAlert :error="passkeyError" />
    </section>

    <section class="card stack" aria-labelledby="totp-h">
      <div class="row">
        <h2 id="totp-h">{{ s.totp }}</h2>
        <span class="pill" :class="person?.totp_enabled ? 'pill-ok' : 'pill-neutral'">{{ person?.totp_enabled ? s.totpOn : s.totpOff }}</span>
      </div>
      <p class="muted">{{ s.totpLead }}</p>
      <template v-if="enrollment">
        <p>{{ s.totpSecret }}</p>
        <p><code class="secret">{{ enrollment.secret }}</code></p>
        <p><a :href="enrollment.otpauth_uri">{{ s.totpOpen }}</a></p>
      </template>
      <form v-if="enrollment || person?.totp_enabled" class="row" @submit.prevent="enrollment ? confirmTotp() : disableTotp()">
        <div class="field">
          <label for="totp-code">{{ strings.auth.totp }}</label>
          <input id="totp-code" v-model="code" inputmode="numeric" pattern="[0-9]{6}" maxlength="6" autocomplete="one-time-code" required />
        </div>
        <button type="submit" class="btn add" :class="enrollment ? 'btn-primary' : 'btn-danger'" :disabled="totpBusy">
          {{ enrollment ? s.totpConfirm : s.totpDisable }}
        </button>
      </form>
      <div v-else>
        <button type="button" class="btn" :disabled="totpBusy" @click="startTotp">{{ s.totpStart }}</button>
      </div>
      <p v-if="totpDone" class="alert alert-ok" role="status">{{ totpDone }}</p>
      <ErrorAlert :error="totpError" />
    </section>

    <section class="card stack" aria-labelledby="sessions-h">
      <h2 id="sessions-h">{{ s.sessions }}</h2>
      <p class="muted">{{ s.sessionsLead }}</p>
      <div><button type="button" class="btn btn-danger" @click="everywhere">{{ strings.nav.signOutEverywhere }}</button></div>
    </section>

    <ModalDialog :open="removing !== null" :title="s.removeTitle(removing?.name ?? '')" @close="removing = null">
      <p>{{ s.removeLead }}</p>
      <ErrorAlert :error="removeError" />
      <template #actions>
        <button type="button" class="btn" :disabled="removeBusy" @click="removing = null">{{ strings.app.cancel }}</button>
        <button type="button" class="btn btn-danger" :disabled="removeBusy" @click="removePasskey">{{ s.removeConfirm }}</button>
      </template>
    </ModalDialog>
  </div>
</template>

<style scoped>
.page {
  max-inline-size: var(--kl-content-md);
}
.add {
  align-self: flex-end;
}
.secret {
  word-break: break-all;
  font-size: var(--kl-text-base);
}
.num {
  text-align: end;
}
</style>
