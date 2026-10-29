<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { RouterLink, RouterView, useRoute, useRouter } from "vue-router";
import { me as meApi } from "../api/endpoints";
import { passkeysSupported } from "../lib/webauthn";
import { getPref, PASSKEY_PROMO_DISMISSED, setPref } from "../lib/prefs";
import { useMeta } from "../session/meta";
import { signOut, useGrant, useSession } from "../session/session";
import { strings } from "../strings";
import BrandMark from "./BrandMark.vue";
import { showShortcutSheet } from "./ShortcutSheet.vue";

const route = useRoute();
const router = useRouter();
const { person, memberships } = useSession();

const tenant = computed(() => (typeof route.params.tenant === "string" ? route.params.tenant : undefined));
const personal = computed(() => memberships.value.filter((m) => m.tenant.kind === "individual"));
const organizations = computed(() => memberships.value.filter((m) => m.tenant.kind === "organization"));
const NEW_ORG = "__new__";
const grant = useGrant(() => tenant.value);
/** Approving is a reviewer's (RFC 0006 §3.2): the inbox is offered to whoever may decide in some locale. */
const mayApprove = computed(() => grant.value.get("approvals.decide") !== undefined);

function switchTenant(e: Event): void {
  const value = (e.target as HTMLSelectElement).value;
  if (value === NEW_ORG) void router.push({ name: "new-organization" });
  else void router.push({ name: "projects", params: { tenant: value } });
}

// The promo shows while the person has no passkey on any device (asked
// of the server) and the server has passkeys, until dismissed.
const { passkey: passkeysOn } = useMeta();
const promoHidden = ref(!!getPref(PASSKEY_PROMO_DISMISSED));
const hasPasskey = ref<boolean>();
async function checkPasskeys(): Promise<void> {
  if (!person.value || !passkeysOn.value || promoHidden.value || !passkeysSupported()) return;
  // On failure, assume one exists: the promo is a nudge, not a gate.
  hasPasskey.value = (await meApi.passkeys().catch(() => [{}])).length > 0;
}
watch(() => [person.value?.id, passkeysOn.value] as const, checkPasskeys, { immediate: true });
// Back from the account page, where one may have been added.
watch(
  () => route.name,
  (_to, from) => {
    if (from === "account") void checkPasskeys();
  },
);
const showPasskeyPromo = computed(() => !promoHidden.value && route.name !== "account" && passkeysOn.value && passkeysSupported() && hasPasskey.value === false);
function dismissPromo(): void {
  setPref(PASSKEY_PROMO_DISMISSED, "1");
  promoHidden.value = true;
}

async function doSignOut(everywhere: boolean): Promise<void> {
  await signOut(everywhere).catch(() => undefined);
  await router.push({ name: "sign-in" });
}
</script>

<template>
  <div class="shell">
    <a class="skip" href="#main">{{ strings.app.skip }}</a>
    <header class="topbar">
      <RouterLink :to="{ name: 'home' }" class="home-link"><BrandMark /></RouterLink>
      <label class="visually-hidden" for="tenant-switch">{{ strings.nav.tenant }}</label>
      <select id="tenant-switch" class="tenant" :value="tenant ?? ''" @change="switchTenant">
        <option v-if="!tenant" value="" disabled>{{ strings.nav.tenant }}</option>
        <optgroup :label="strings.nav.individual">
          <option v-for="m in personal" :key="m.tenant.id" :value="m.tenant.id">{{ m.tenant.name }}</option>
        </optgroup>
        <optgroup v-if="organizations.length" :label="strings.nav.organization">
          <option v-for="m in organizations" :key="m.tenant.id" :value="m.tenant.id">{{ m.tenant.name }}</option>
        </optgroup>
        <option :value="NEW_ORG">{{ strings.nav.newOrganization }}</option>
      </select>
      <nav v-if="tenant" class="work-nav" :aria-label="strings.nav.work">
        <RouterLink :to="{ name: 'my-work', params: { tenant } }" class="work-link" data-testid="nav-my-work">{{ strings.nav.myWork }}</RouterLink>
        <RouterLink v-if="mayApprove" :to="{ name: 'approvals', params: { tenant } }" class="work-link" data-testid="nav-approvals">{{ strings.nav.approvals }}</RouterLink>
      </nav>
      <span class="spacer" />
      <button type="button" class="btn btn-ghost btn-icon" :aria-label="strings.nav.shortcuts" :title="strings.nav.shortcuts" @click="showShortcutSheet">
        <kbd aria-hidden="true">?</kbd>
      </button>
      <kl-theme-toggle />
      <details class="menu">
        <summary class="btn btn-ghost">
          <span class="who">{{ person?.display_name || person?.email }}</span>
        </summary>
        <div class="menu-body" role="group" :aria-label="strings.nav.account">
          <RouterLink :to="{ name: 'account' }" class="btn btn-ghost menu-item">{{ strings.nav.account }}</RouterLink>
          <button type="button" class="btn btn-ghost menu-item" @click="doSignOut(false)">{{ strings.nav.signOut }}</button>
          <button type="button" class="btn btn-ghost menu-item" @click="doSignOut(true)">{{ strings.nav.signOutEverywhere }}</button>
        </div>
      </details>
    </header>
    <aside v-if="showPasskeyPromo" class="promo" :aria-label="strings.passkeyPromo.title">
      <strong>{{ strings.passkeyPromo.title }}</strong>
      <span class="muted">{{ strings.passkeyPromo.body }}</span>
      <span class="spacer" />
      <RouterLink :to="{ name: 'account', hash: '#passkeys' }" class="btn btn-sm btn-primary">{{ strings.passkeyPromo.action }}</RouterLink>
      <button type="button" class="btn btn-sm btn-ghost" @click="dismissPromo">{{ strings.passkeyPromo.dismiss }}</button>
    </aside>
    <main id="main" class="shell-main" tabindex="-1">
      <RouterView />
    </main>
  </div>
</template>

<style scoped>
.shell {
  display: flex;
  flex-direction: column;
  min-block-size: 100vh;
  background: var(--kl-surface);
}
.skip {
  position: absolute;
  inset-inline-start: var(--kl-space-2);
  inset-block-start: -3rem;
  padding: var(--kl-space-2) var(--kl-space-3);
  background: var(--kl-accent);
  color: var(--gs-on-accent);
  border-radius: var(--kl-radius-md);
  z-index: var(--kl-z-toast);
}
.skip:focus {
  inset-block-start: var(--kl-space-2);
}
.topbar {
  display: flex;
  align-items: center;
  gap: var(--kl-space-3);
  block-size: var(--gs-topbar-h);
  padding: 0 var(--kl-space-4);
  border-block-end: 1px solid var(--kl-border);
  background: var(--kl-surface-raised);
  position: sticky;
  inset-block-start: 0;
  z-index: var(--kl-z-raised);
}
.home-link {
  text-decoration: none;
}
.work-nav {
  display: flex;
  gap: var(--kl-space-1);
}
.work-link {
  padding: var(--kl-space-2) var(--kl-space-3);
  border-radius: var(--kl-radius-md);
  color: var(--kl-ink-secondary);
  text-decoration: none;
}
.work-link:hover {
  color: var(--kl-ink);
}
.work-link.router-link-active {
  color: var(--kl-ink);
  font-weight: var(--kl-weight-medium);
  background: var(--kl-accent-dim);
}
.tenant {
  min-block-size: 2.25rem;
  max-inline-size: 16rem;
}
.menu {
  position: relative;
}
.menu summary {
  list-style: none;
}
.menu summary::-webkit-details-marker {
  display: none;
}
.who {
  max-inline-size: 14rem;
  overflow: hidden;
  text-overflow: ellipsis;
}
.menu-body {
  position: absolute;
  inset-inline-end: 0;
  inset-block-start: calc(100% + var(--kl-space-1));
  display: flex;
  flex-direction: column;
  min-inline-size: 14rem;
  padding: var(--kl-space-1);
  background: var(--kl-surface);
  border: 1px solid var(--kl-border);
  border-radius: var(--kl-radius-md);
  box-shadow: var(--kl-shadow-lg);
  z-index: var(--kl-z-overlay);
}
.menu-item {
  justify-content: flex-start;
}
.promo {
  display: flex;
  align-items: center;
  gap: var(--kl-space-3);
  flex-wrap: wrap;
  padding: var(--kl-space-2) var(--kl-space-4);
  border-block-end: 1px solid var(--kl-accent-border);
  background: var(--kl-accent-dim);
}
.shell-main {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-block-size: 0;
}
.shell-main:focus {
  outline: none;
}
</style>
