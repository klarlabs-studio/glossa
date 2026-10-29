/**
 * zod schemas for the organization's directory as M5 shapes it
 * (RFC 0006 §3.3, §4.1, §4.3): vendors, and the restriction a member
 * carries — project scope, vendor and visibility. Groups and members are
 * parsed with the schemas the rest of Studio already uses.
 */
import { z } from "zod";
import type { components } from "./schema";
import { Member } from "./schemas";
import { Group } from "./work-schemas";

const timestamp = z.string().min(1);
const id = z.string().min(1);

export const Vendor = z.object({
  id,
  name: z.string(),
  /** Free text — a name, an address. Shown, never mailed to. */
  contact: z.string().optional(),
  /** The locales the vendor offers. They describe it and grant nothing. */
  locales: z.array(z.string()),
  created_at: timestamp,
  updated_at: timestamp,
});

export const Visibility = z.enum(["all", "assigned"]);

export type Vendor = z.infer<typeof Vendor>;
export type Visibility = z.infer<typeof Visibility>;
export { Group, Member };

type C = components["schemas"];
type Fits<A, B> = [A] extends [B] ? true : false;
type Assert<T extends true> = T;
export type DirectoryContractAlignment = [Assert<Fits<C["Vendor"], Vendor>>, Assert<Fits<C["Visibility"], Visibility>>];
