/**
 * zod schemas for in-context editing (RFC 0004 §5.2): the origins a
 * project's in-product editor may run on, and the short-lived grants
 * Studio's authorize popup mints for them. Kept out of ./schemas so only
 * the settings card and the popup load them; the compile-time assertions
 * at the bottom tie each to the contract, so a change to
 * `platform/api/openapi.yaml` that breaks one fails `pnpm lint`.
 */
import { z } from "zod";
import type { components } from "./schema";

const timestamp = z.string().min(1);
const id = z.string().min(1);

export const PreviewOrigin = z.object({
  id,
  origin: z.string(),
  label: z.string(),
  /** A plain-http loopback origin: a developer's own machine. */
  development: z.boolean(),
  created_by: z.string(),
  created_at: timestamp,
});

export const InContextPermission = z.object({
  permission: z.enum([
    "catalog.read",
    "knowledge.read",
    "translations.read",
    "translations.write",
    "intelligence.read",
    "intelligence.translate",
  ]),
  /** Absent means every locale. */
  locales: z.array(z.string()).optional(),
});

export const InContextGrant = z.object({
  /** Shown once, here. It never reaches storage — only the popup's `postMessage`. */
  token: z.string(),
  expires_at: timestamp,
  project_id: id,
  origin: z.string(),
  person_id: id,
  permissions: z.array(InContextPermission),
});

export type PreviewOrigin = z.infer<typeof PreviewOrigin>;
export type InContextPermission = z.infer<typeof InContextPermission>;
export type InContextGrant = z.infer<typeof InContextGrant>;

// ── contract alignment (compile time only) ─────────────────────────────
type C = components["schemas"];
type Fits<A, B> = [A] extends [B] ? true : false;
type Assert<T extends true> = T;
export type InContextContractAlignment = [
  Assert<Fits<PreviewOrigin, C["PreviewOrigin"]>>,
  Assert<Fits<InContextGrant, C["InContextGrant"]>>,
  Assert<Fits<InContextPermission, C["InContextPermission"]>>,
];
