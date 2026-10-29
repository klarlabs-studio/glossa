/**
 * An in-memory DevicePort for component tests, with the server's rules
 * in miniature (RFC 0006 §7.2): a code is matched in any case, with or
 * without its hyphen; an unknown or already decided code is
 * `device_authorization_not_found`. Test-only: nothing in the app
 * imports it.
 */
import { ApiError } from "../api/errors";
import type { DevicePort } from "../api/device";
import type { DeviceAuthorizationView, DeviceDecision } from "../api/device-schemas";
import { formatUserCode, normalizeUserCode } from "../lib/device-code";

export interface FakeDevice extends DevicePort {
  readonly calls: Array<[string, ...unknown[]]>;
  /** Pending codes, keyed by their normalized form. */
  readonly pending: Map<string, DeviceAuthorizationView>;
  /** The decisions made, keyed by normalized code. */
  readonly decided: Map<string, DeviceDecision>;
  /** Set to make every call fail with this status and code. */
  failWith: { status: number; code: string } | undefined;
}

export function deviceAuthorization(over: Partial<DeviceAuthorizationView> = {}): DeviceAuthorizationView {
  return {
    user_code: "BCDF-GHJK",
    client_name: "glossa CLI on build-01",
    requested_at: "2026-09-20T10:00:00Z",
    expires_at: "2026-09-20T10:15:00Z",
    ...over,
  };
}

const notFound = () => new ApiError(404, "device_authorization_not_found", "device_authorization_not_found");

export function fakeDevice(...codes: DeviceAuthorizationView[]): FakeDevice {
  const pending = new Map(codes.map((c) => [normalizeUserCode(c.user_code), c]));
  const fake: FakeDevice = {
    calls: [],
    pending,
    decided: new Map(),
    failWith: undefined,

    async lookup(userCode) {
      fake.calls.push(["lookup", userCode]);
      if (fake.failWith) throw new ApiError(fake.failWith.status, fake.failWith.code, fake.failWith.code);
      const found = pending.get(normalizeUserCode(userCode));
      if (!found) throw notFound();
      return { ...found, user_code: formatUserCode(found.user_code) };
    },

    async decide(userCode, decision) {
      fake.calls.push(["decide", userCode, decision]);
      if (fake.failWith) throw new ApiError(fake.failWith.status, fake.failWith.code, fake.failWith.code);
      const key = normalizeUserCode(userCode);
      if (!pending.has(key)) throw notFound();
      pending.delete(key);
      fake.decided.set(key, decision);
    },
  };
  return fake;
}
