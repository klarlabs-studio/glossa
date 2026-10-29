/**
 * Passkey ceremonies. The server sends WebAuthn options as JSON with
 * base64url binary fields (go-webauthn); the browser API wants
 * ArrayBuffers, and the server wants the credential back as JSON.
 */

export function b64urlToBytes(s: string): Uint8Array<ArrayBuffer> {
  const b64 = s.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(s.length / 4) * 4, "=");
  const bin = atob(b64);
  const out = new Uint8Array(new ArrayBuffer(bin.length));
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

export function bytesToB64url(buf: ArrayBuffer | ArrayBufferView): string {
  const bytes = buf instanceof ArrayBuffer ? new Uint8Array(buf) : new Uint8Array(buf.buffer, buf.byteOffset, buf.byteLength);
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

type Json = Record<string, unknown>;
const obj = (v: unknown): Json => (v && typeof v === "object" ? (v as Json) : {});
const publicKeyOf = (options: Json): Json => obj(options.publicKey ?? options);

function descriptors(list: unknown): PublicKeyCredentialDescriptor[] | undefined {
  if (!Array.isArray(list)) return undefined;
  return list.map((d) => {
    const o = obj(d);
    return { ...o, type: "public-key", id: b64urlToBytes(String(o.id)) } as unknown as PublicKeyCredentialDescriptor;
  });
}

export function requestOptions(options: Json): PublicKeyCredentialRequestOptions {
  const pk = publicKeyOf(options);
  const out = { ...pk, challenge: b64urlToBytes(String(pk.challenge)) } as unknown as PublicKeyCredentialRequestOptions;
  const allow = descriptors(pk.allowCredentials);
  if (allow) out.allowCredentials = allow;
  return out;
}

export function creationOptions(options: Json): PublicKeyCredentialCreationOptions {
  const pk = publicKeyOf(options);
  const user = obj(pk.user);
  const out = {
    ...pk,
    challenge: b64urlToBytes(String(pk.challenge)),
    user: { ...user, id: b64urlToBytes(String(user.id)) },
  } as unknown as PublicKeyCredentialCreationOptions;
  const exclude = descriptors(pk.excludeCredentials);
  if (exclude) out.excludeCredentials = exclude;
  return out;
}

/** A credential as the JSON the server parses. */
export function credentialJSON(cred: PublicKeyCredential): Json {
  const r = cred.response as AuthenticatorResponse & Partial<AuthenticatorAttestationResponse & AuthenticatorAssertionResponse>;
  const response: Json = { clientDataJSON: bytesToB64url(r.clientDataJSON) };
  if (r.attestationObject) response.attestationObject = bytesToB64url(r.attestationObject);
  if (typeof r.getTransports === "function") response.transports = r.getTransports();
  if (r.authenticatorData) response.authenticatorData = bytesToB64url(r.authenticatorData);
  if (r.signature) response.signature = bytesToB64url(r.signature);
  if (r.userHandle) response.userHandle = bytesToB64url(r.userHandle);
  return {
    id: cred.id,
    rawId: bytesToB64url(cred.rawId),
    type: cred.type,
    response,
    clientExtensionResults: cred.getClientExtensionResults?.() ?? {},
    ...(cred.authenticatorAttachment ? { authenticatorAttachment: cred.authenticatorAttachment } : {}),
  };
}

export const passkeysSupported = (): boolean =>
  typeof globalThis.PublicKeyCredential === "function" && typeof navigator.credentials?.get === "function";

export async function getAssertion(options: Json): Promise<Json> {
  const cred = await navigator.credentials.get({ publicKey: requestOptions(options) });
  if (!(cred instanceof PublicKeyCredential)) throw new Error("No passkey was chosen.");
  return credentialJSON(cred);
}

export async function createCredential(options: Json): Promise<Json> {
  const cred = await navigator.credentials.create({ publicKey: creationOptions(options) });
  if (!(cred instanceof PublicKeyCredential)) throw new Error("No passkey was created.");
  return credentialJSON(cred);
}
