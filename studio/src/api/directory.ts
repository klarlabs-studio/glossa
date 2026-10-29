/**
 * The directory port (RFC 0006 §3.3, §4.1, §4.3): the organization's
 * groups and vendors, and the members they name — with the restriction
 * each member carries (project scope, vendor, `assigned` visibility).
 *
 * `apiDirectory` implements it through the generated client; component
 * tests hand screens an in-memory one (../test/fake-directory.ts).
 */
import { inject, type InjectionKey } from "vue";
import { client } from "./client";
import { all } from "./endpoints";
import { done, read, type Versioned } from "./errors";
import { Member, page, type Role } from "./schemas";
import { Group } from "./work-schemas";
import { Vendor, type Visibility } from "./directory-schemas";

const PAGE = 100;
const value = async <T>(p: Promise<Versioned<T>>): Promise<T> => (await p).value;
const opt = (signal?: AbortSignal) => (signal ? { signal } : {});

export interface VendorInput {
  name: string;
  contact?: string;
  locales?: string[];
}

/** A member's restriction (RFC 0006 §3.3, §4.1) — never together with roles or locales. */
export interface Restriction {
  /** Empty: every project. */
  projects?: string[];
  /** A vendor id, or `""` to take the member off their vendor. */
  vendor_id?: string;
  visibility?: Visibility;
}

export interface VendorMemberInvite {
  email: string;
  vendor_id: string;
  /** A vendor's member is a translator who reads only what is assigned. */
  roles: Role[];
  locales?: string[];
  /** Empty: every project. */
  projects?: string[];
}

export interface DirectoryPort {
  members(tenant: string, signal?: AbortSignal): Promise<Member[]>;
  /** One member with its ETag, for a restriction change. */
  member(tenant: string, id: string, signal?: AbortSignal): Promise<Versioned<Member>>;
  restrict(tenant: string, id: string, restriction: Restriction, etag: string): Promise<Versioned<Member>>;
  /** An invitation as the vendor's member: `visibility: assigned`, as the server requires. */
  inviteVendorMember(tenant: string, invite: VendorMemberInvite, idempotencyKey: string): Promise<Member>;

  groups(tenant: string, signal?: AbortSignal): Promise<Group[]>;
  group(tenant: string, id: string, signal?: AbortSignal): Promise<Versioned<Group>>;
  createGroup(tenant: string, name: string, idempotencyKey: string): Promise<Group>;
  renameGroup(tenant: string, id: string, name: string, etag: string): Promise<Versioned<Group>>;
  deleteGroup(tenant: string, id: string): Promise<void>;
  /** Idempotent: a member already in the group stays. */
  addToGroup(tenant: string, group: string, member: string): Promise<Group>;
  removeFromGroup(tenant: string, group: string, member: string): Promise<void>;

  vendors(tenant: string, signal?: AbortSignal): Promise<Vendor[]>;
  vendor(tenant: string, id: string, signal?: AbortSignal): Promise<Versioned<Vendor>>;
  createVendor(tenant: string, input: VendorInput, idempotencyKey: string): Promise<Vendor>;
  updateVendor(tenant: string, id: string, input: Partial<VendorInput>, etag: string): Promise<Versioned<Vendor>>;
  /** Refused (`vendor_has_members`) while anyone still works for it. */
  deleteVendor(tenant: string, id: string): Promise<void>;
}

const MEMBER = "/v1/tenants/{tenant}/members/{member}" as const;
const GROUP = "/v1/tenants/{tenant}/groups/{group}" as const;
const VENDOR = "/v1/tenants/{tenant}/vendors/{vendor}" as const;

export const apiDirectory: DirectoryPort = {
  members: (tenant, signal) =>
    all((page_token) =>
      value(read(client.GET("/v1/tenants/{tenant}/members", { params: { path: { tenant }, query: { page_size: PAGE, page_token } }, ...opt(signal) }), page(Member))),
    ),
  member: (tenant, member, signal) => read(client.GET(MEMBER, { params: { path: { tenant, member } }, ...opt(signal) }), Member),
  restrict: (tenant, member, restriction, etag) =>
    read(client.PATCH(MEMBER, { params: { path: { tenant, member }, header: { "If-Match": etag } }, body: restriction }), Member),
  inviteVendorMember: (tenant, invite, key) =>
    value(
      read(
        client.POST("/v1/tenants/{tenant}/members", {
          params: { path: { tenant }, header: { "Idempotency-Key": key } },
          body: {
            email: invite.email,
            roles: invite.roles,
            vendor_id: invite.vendor_id,
            visibility: "assigned",
            ...(invite.locales?.length ? { locales: invite.locales } : {}),
            ...(invite.projects?.length ? { projects: invite.projects } : {}),
          },
        }),
        Member,
      ),
    ),

  groups: (tenant, signal) =>
    all((page_token) =>
      value(read(client.GET("/v1/tenants/{tenant}/groups", { params: { path: { tenant }, query: { page_size: PAGE, page_token } }, ...opt(signal) }), page(Group))),
    ),
  group: (tenant, group, signal) => read(client.GET(GROUP, { params: { path: { tenant, group } }, ...opt(signal) }), Group),
  createGroup: (tenant, name, key) =>
    value(read(client.POST("/v1/tenants/{tenant}/groups", { params: { path: { tenant }, header: { "Idempotency-Key": key } }, body: { name } }), Group)),
  renameGroup: (tenant, group, name, etag) =>
    read(client.PATCH(GROUP, { params: { path: { tenant, group }, header: { "If-Match": etag } }, body: { name } }), Group),
  deleteGroup: (tenant, group) => done(client.DELETE(GROUP, { params: { path: { tenant, group } } })),
  addToGroup: (tenant, group, member) => value(read(client.PUT(`${GROUP}/members/{member}`, { params: { path: { tenant, group, member } } }), Group)),
  removeFromGroup: (tenant, group, member) => done(client.DELETE(`${GROUP}/members/{member}`, { params: { path: { tenant, group, member } } })),

  vendors: (tenant, signal) =>
    all((page_token) =>
      value(read(client.GET("/v1/tenants/{tenant}/vendors", { params: { path: { tenant }, query: { page_size: PAGE, page_token } }, ...opt(signal) }), page(Vendor))),
    ),
  vendor: (tenant, vendor, signal) => read(client.GET(VENDOR, { params: { path: { tenant, vendor } }, ...opt(signal) }), Vendor),
  createVendor: (tenant, input, key) =>
    value(read(client.POST("/v1/tenants/{tenant}/vendors", { params: { path: { tenant }, header: { "Idempotency-Key": key } }, body: input }), Vendor)),
  updateVendor: (tenant, vendor, input, etag) =>
    read(client.PATCH(VENDOR, { params: { path: { tenant, vendor }, header: { "If-Match": etag } }, body: input }), Vendor),
  deleteVendor: (tenant, vendor) => done(client.DELETE(VENDOR, { params: { path: { tenant, vendor } } })),
};

export const DIRECTORY: InjectionKey<DirectoryPort> = Symbol("directory");

export function useDirectory(): DirectoryPort {
  return inject(DIRECTORY, apiDirectory);
}
