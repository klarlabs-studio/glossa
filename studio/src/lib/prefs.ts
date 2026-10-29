/** Per-browser preferences in localStorage; storage may be blocked, so every access is guarded. */

function storage(): Storage | undefined {
  try {
    return globalThis.localStorage;
  } catch {
    return undefined;
  }
}

export function getPref(key: string): string | undefined {
  try {
    return storage()?.getItem(`glossa:${key}`) ?? undefined;
  } catch {
    return undefined;
  }
}

export function setPref(key: string, value: string | undefined): void {
  try {
    if (value === undefined) storage()?.removeItem(`glossa:${key}`);
    else storage()?.setItem(`glossa:${key}`, value);
  } catch {
    // storage full or blocked: preferences are a convenience
  }
}

/** The person dismissed the passkey promo in this browser. */
export const PASSKEY_PROMO_DISMISSED = "passkey-promo-dismissed";
