/**
 * zod schemas for device sign-in (RFC 0006 §7.2, the OAuth 2.0 device
 * authorization grant): what a user code would sign in, as Studio's
 * `/device` page shows it before the person decides. The compile-time
 * assertions at the bottom tie them to the contract, so a change to
 * `platform/api/openapi.yaml` that breaks one fails `pnpm lint`.
 */
import { z } from "zod";
import type { components } from "./schema";

const timestamp = z.string().min(1);

export const DeviceAuthorizationView = z.object({
  user_code: z.string().min(1),
  /** What the device calls itself — its own claim, shown as such. */
  client_name: z.string(),
  requested_at: timestamp,
  expires_at: timestamp,
});

export const DeviceDecision = z.enum(["approved", "denied"]);

export type DeviceAuthorizationView = z.infer<typeof DeviceAuthorizationView>;
export type DeviceDecision = z.infer<typeof DeviceDecision>;

// ── contract alignment (compile time only) ─────────────────────────────
type C = components["schemas"];
type Fits<A, B> = [A] extends [B] ? true : false;
type Assert<T extends true> = T;
export type DeviceContractAlignment = [
  Assert<Fits<DeviceAuthorizationView, C["DeviceAuthorizationView"]>>,
  Assert<Fits<DeviceDecision, C["DeviceApproval"]["decision"]>>,
];
