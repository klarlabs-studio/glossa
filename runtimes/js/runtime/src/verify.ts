/**
 * Integrity and authenticity (runtimes/SPEC.md §1.3), on WebCrypto only:
 * SHA-256 over an artifact's exact bytes, and Ed25519 over the RFC 8785
 * (JCS) form of the manifest without `signatures`.
 */
import type { Manifest } from "./manifest.js";

/** A trusted manifest-signing key: base64url raw Ed25519 public key. */
export interface PublicKey {
  keyId: string;
  key: string;
}

const utf8 = (s: string) => new TextEncoder().encode(s);

const b64url = (s: string) =>
  Uint8Array.from(atob(s.replace(/-/g, "+").replace(/_/g, "/")), (c) => c.charCodeAt(0));

/** Lowercase hex of `bytes`. */
export const hex = (bytes: Uint8Array) =>
  Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");

/** Lowercase hex SHA-256 of the UTF-8 bytes of `text`. */
export async function sha256Hex(text: string): Promise<string> {
  return hex(new Uint8Array(await crypto.subtle.digest("SHA-256", utf8(text))));
}

/**
 * The staged-rollout cohort of `key` under `salt` (SPEC §1.4), 0–9999: the
 * first four bytes of SHA-256(UTF-8(salt) ‖ UTF-8(key)) as a big-endian
 * unsigned integer, mod 10000. Both are hashed as given, never normalized.
 */
export const cohort = async (salt: string, key: string) =>
  parseInt((await sha256Hex(salt + key)).slice(0, 8), 16) % 1e4;

/**
 * RFC 8785 JSON Canonicalization Scheme for parsed JSON values. JCS is
 * defined in terms of ECMAScript serialization, so strings and numbers are
 * exactly `JSON.stringify`'s; object keys sort by UTF-16 code units, which is
 * `Array.prototype.sort`'s default order.
 */
export function jcs(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(jcs).join(",")}]`;
  if (value && typeof value === "object") {
    const o = value as Record<string, unknown>;
    return `{${Object.keys(o)
      .sort()
      .map((k) => `${JSON.stringify(k)}:${jcs(o[k])}`)
      .join(",")}}`;
  }
  return JSON.stringify(value);
}

/**
 * Why a manifest can't be used, or `undefined`. A different major schema
 * version is rejected (SPEC §1.1), and so is a manifest for another
 * environment than `env` (SPEC §3); unknown fields are ignored.
 */
export function checkManifest(m: unknown, env?: string): string | undefined {
  const x = m as Partial<Manifest> | null;
  if (!/^glossa\.manifest\/v1\b/.test(String(x?.schema))) {
    return `unsupported manifest schema ${String(x?.schema)}`;
  }
  if (env !== undefined && x!.environment !== env) {
    return `manifest is for environment ${String(x!.environment)}, not ${env}`;
  }
  const ok =
    typeof x!.release?.id === "string" &&
    typeof x!.sourceLocale === "string" &&
    Array.isArray(x!.locales) &&
    !!x!.artifacts &&
    typeof x!.artifacts === "object";
  return ok ? undefined : "malformed manifest";
}

/**
 * Why a manifest's signatures don't verify against `keys`, or `undefined`
 * when one of them does. Keys are matched by `keyId`. Malformed keys or
 * signatures, and platforms without WebCrypto Ed25519, count as invalid.
 */
export async function verifySignature(
  m: Manifest,
  keys: readonly PublicKey[],
): Promise<string | undefined> {
  const { signatures, ...unsigned } = m;
  if (!signatures?.length) return "manifest is not signed";
  const data = utf8(jcs(unsigned));
  for (const s of signatures) {
    for (const k of keys) {
      if (s.alg !== "Ed25519" || s.keyId !== k.keyId) continue;
      try {
        const key = await crypto.subtle.importKey("raw", b64url(k.key), "Ed25519", false, [
          "verify",
        ]);
        if (await crypto.subtle.verify("Ed25519", key, b64url(s.sig), data)) return undefined;
      } catch {
        // Malformed key or signature, or no Ed25519 support: not valid.
      }
    }
  }
  return "no valid signature from a configured key";
}
