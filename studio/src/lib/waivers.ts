/**
 * Pure helpers for the waiver screen (RFC 0005 §2.3, §9).
 *
 * A waiver is the only way a finding stops failing a check, so the list
 * of them is the whole of what a project has agreed to live with. Two
 * numbers matter beyond "how many": the waivers that accept nothing any
 * stored finding still carries — nobody is watching what they let
 * through — and the ones about to expire, which is a check that is
 * about to get stricter without anybody deciding to.
 */
import type { Waiver } from "../api/quality-schemas";

/** What the URL carries, so a filtered list is bookmarkable. Empty means "any". */
export interface WaiverFilterState {
  layer: string;
  code: string;
  fingerprint: string;
  /** "", `active` or `inactive`. */
  state: string;
}

export const EMPTY_WAIVER_FILTER: WaiverFilterState = { layer: "", code: "", fingerprint: "", state: "" };

/** A waiver stands now: neither revoked nor expired. */
export const stands = (w: Waiver): boolean => w.active && !w.revoked_at;

/**
 * The filters applied here rather than at the API, because the screen
 * already holds every waiver it is allowed to read and filtering in the
 * browser keeps the counts above the table true for the whole project
 * rather than for the current query.
 */
export function filterWaivers(waivers: readonly Waiver[], f: WaiverFilterState): Waiver[] {
  const code = f.code.trim().toLowerCase();
  const fingerprint = f.fingerprint.trim().toLowerCase();
  return waivers.filter((w) => {
    if (f.state === "active" && !stands(w)) return false;
    if (f.state === "inactive" && stands(w)) return false;
    if (f.layer && w.accepts?.layer !== f.layer) return false;
    if (code && !(w.accepts?.code ?? "").toLowerCase().includes(code)) return false;
    if (fingerprint && !w.fingerprint.toLowerCase().includes(fingerprint)) return false;
    return true;
  });
}

/** How many days from now a waiver counts as "expiring soon". */
export const EXPIRING_SOON_DAYS = 30;

export interface WaiverTotals {
  standing: number;
  past: number;
  /** Standing waivers no stored finding carries any more: nobody is watching what they let through. */
  unexamined: number;
  /** Standing waivers whose expiry is within EXPIRING_SOON_DAYS. */
  expiringSoon: number;
}

export function waiverTotals(waivers: readonly Waiver[], now: number = Date.now()): WaiverTotals {
  const soon = now + EXPIRING_SOON_DAYS * 24 * 60 * 60 * 1000;
  let standing = 0;
  let unexamined = 0;
  let expiringSoon = 0;
  for (const w of waivers) {
    if (!stands(w)) continue;
    standing += 1;
    if (!w.accepts?.code) unexamined += 1;
    if (w.expires_at) {
      const at = Date.parse(w.expires_at);
      if (!Number.isNaN(at) && at <= soon) expiringSoon += 1;
    }
  }
  return { standing, past: waivers.length - standing, unexamined, expiringSoon };
}
