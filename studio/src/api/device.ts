/**
 * The device sign-in port (RFC 0006 §7.2, RFC 8628): the signed-in
 * person looks up the code a device — the Glossa CLI — shows, and
 * approves or denies it. Approving signs that device in as the person,
 * so the lookup comes first and the page shows what is asking.
 *
 * Both calls ride the browser session; the decision is an unsafe request
 * and carries `X-CSRF-Token` through the shared client. `apiDevice`
 * implements the port over the generated /v1 client with the response
 * checked by zod; component tests provide an in-memory fake
 * (src/test/fake-device.ts).
 */
import { inject, type InjectionKey } from "vue";
import { client } from "./client";
import * as D from "./device-schemas";
import { done, read } from "./errors";

export interface DevicePort {
  /** What the code would sign in. Unknown, expired or decided codes are `device_authorization_not_found`. */
  lookup(userCode: string): Promise<D.DeviceAuthorizationView>;
  /** Approve or deny the code. */
  decide(userCode: string, decision: D.DeviceDecision): Promise<void>;
}

export const apiDevice: DevicePort = {
  lookup: async (user_code) =>
    (
      await read(
        client.GET("/v1/auth/device-authorizations/{user_code}", { params: { path: { user_code } } }),
        D.DeviceAuthorizationView,
      )
    ).value,
  decide: (user_code, decision) => done(client.POST("/v1/auth/device-approvals", { body: { user_code, decision } })),
};

export const DEVICE: InjectionKey<DevicePort> = Symbol("device");

/** The provided port, else the API adapter. */
export function useDevice(): DevicePort {
  return inject(DEVICE, apiDevice);
}
