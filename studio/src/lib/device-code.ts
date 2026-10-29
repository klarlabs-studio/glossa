/**
 * The user code of the OAuth 2.0 device authorization grant (RFC 8628,
 * RFC 0006 §7.2): eight characters shown as `XXXX-XXXX`, drawn from
 * twenty consonants — no vowels, so no words; no digits, so no 0/O or
 * 1/I to misread (RFC 8628 §6.1).
 *
 * People type what their terminal shows, in whatever case and with or
 * without the hyphen; the server accepts every spelling, and so does
 * this page.
 */

/** The alphabet the server draws codes from. */
export const USER_CODE_ALPHABET = "BCDFGHJKLMNPQRSTVWXZ";

/** Characters in a code, without the hyphen. */
export const USER_CODE_LENGTH = 8;

const GROUP = USER_CODE_LENGTH / 2;
const VALID = new RegExp(`^[${USER_CODE_ALPHABET}]{${USER_CODE_LENGTH}}$`);

/** Uppercase, with the hyphen and any whitespace removed. */
export function normalizeUserCode(raw: string): string {
  return raw.toUpperCase().replace(/[\s-]+/g, "");
}

/**
 * The code as the terminal shows it, built as the person types: a
 * hyphen appears once the first group is complete, and anything past
 * the eighth character is dropped.
 */
export function formatUserCode(raw: string): string {
  const code = normalizeUserCode(raw).slice(0, USER_CODE_LENGTH);
  return code.length > GROUP ? `${code.slice(0, GROUP)}-${code.slice(GROUP)}` : code;
}

/** Whether a code, in any spelling, could be one the server issued. */
export function isUserCode(raw: string): boolean {
  return VALID.test(normalizeUserCode(raw));
}
