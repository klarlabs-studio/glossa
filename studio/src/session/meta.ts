/**
 * What this deployment offers (GET /v1/meta): which sign-in methods
 * exist, whether it sends email, and glossa-edge's public URL. Read once
 * and shared; screens adapt to it instead of guessing per browser.
 */
import { computed, readonly, shallowRef } from "vue";
import { meta as metaApi } from "../api/endpoints";
import type { Meta, SignInMethod } from "../api/schemas";

/**
 * Assumed until the server answers, and kept if it can't: what servers
 * offered before /v1/meta existed. A wrong guess only shows a method the
 * server then refuses with a clear problem.
 */
export const FALLBACK_META: Meta = { sign_in_methods: ["password", "magic_link"], email_delivery: true };

const current = shallowRef<Meta>(FALLBACK_META);
const loaded = shallowRef(false);
let loading: Promise<Meta> | undefined;

/** Load the deployment's facts once; later calls share the first request. */
export function loadMeta(): Promise<Meta> {
  loading ??= metaApi
    .get()
    .catch(() => FALLBACK_META)
    .then((m) => {
      current.value = m;
      loaded.value = true;
      return m;
    });
  return loading;
}

/** Tests only: forget what was loaded. */
export function resetMeta(value: Meta = FALLBACK_META): void {
  loading = undefined;
  current.value = value;
  loaded.value = false;
}

export function useMeta() {
  void loadMeta();
  const has = (m: SignInMethod) => current.value.sign_in_methods.includes(m);
  return {
    meta: readonly(current),
    loaded: readonly(loaded),
    magicLink: computed(() => has("magic_link")),
    password: computed(() => has("password")),
    passkey: computed(() => has("passkey")),
    email: computed(() => current.value.email_delivery),
  };
}
