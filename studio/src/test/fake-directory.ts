/**
 * An in-memory DirectoryPort for component tests, with the API's rules in
 * miniature (RFC 0006 §3.3, §4.1, §4.3): every change carries the
 * resource's ETag (412 when it moved on), an unknown id is `not_found`,
 * a vendor with members can't be deleted (`vendor_has_members`), a group
 * name is 1–100 characters (`invalid_group_name`), and taking a member out
 * of a group they aren't in is `not_in_group`.
 *
 * `fail` makes a whole method fail; `hold` keeps its answer pending, for
 * the loading state. `refuse` makes the next call of a method fail once,
 * the way the server refuses one request.
 *
 * Test-only: nothing in the app imports it.
 */
import type { DirectoryPort } from "../api/directory";
import type { Vendor } from "../api/directory-schemas";
import { ApiError, type Versioned } from "../api/errors";
import type { Member } from "../api/schemas";
import type { Group } from "../api/work-schemas";

const NOW = "2026-09-19T08:00:00Z";

export function member(over: Partial<Member> = {}): Member {
  return {
    id: "m1",
    email: "ana@example.com",
    person_id: "ana",
    display_name: "Ana",
    status: "active",
    roles: ["translator"],
    locales: [],
    projects: [],
    visibility: "all",
    created_at: NOW,
    updated_at: NOW,
    ...over,
  };
}

export function group(over: Partial<Group> = {}): Group {
  return { id: "g1", name: "de reviewers", members: [], created_at: NOW, updated_at: NOW, ...over };
}

export function vendor(over: Partial<Vendor> = {}): Vendor {
  return { id: "v1", name: "Lingua GmbH", contact: "pm@lingua.example", locales: ["de", "fr"], created_at: NOW, updated_at: NOW, ...over };
}

type Method = keyof DirectoryPort;

export interface FakeDirectory extends DirectoryPort {
  readonly calls: Array<[Method, ...unknown[]]>;
  readonly state: { members: Member[]; groups: Group[]; vendors: Vendor[]; etags: Map<string, number> };
  fail: Partial<Record<Method, ApiError>>;
  /** Fails the next call of a method once. */
  refuse: Partial<Record<Method, ApiError>>;
  hold: Set<Method>;
}

const never = <T>() => new Promise<T>(() => undefined);

export function createFakeDirectory(init: Partial<Omit<FakeDirectory["state"], "etags">> = {}): FakeDirectory {
  const state: FakeDirectory["state"] = { members: [], groups: [], vendors: [], etags: new Map(), ...init };
  const calls: FakeDirectory["calls"] = [];
  const fail: FakeDirectory["fail"] = {};
  const refuse: FakeDirectory["refuse"] = {};
  const hold = new Set<Method>();
  let seq = 100;

  async function gate(m: Method, ...args: unknown[]): Promise<void> {
    calls.push([m, ...args]);
    if (hold.has(m)) await never();
    const f = fail[m];
    if (f) throw f;
    const once = refuse[m];
    if (once) {
      delete refuse[m];
      throw once;
    }
  }
  const etagOf = (id: string) => `"${state.etags.get(id) ?? 1}"`;
  const bump = (id: string) => state.etags.set(id, (state.etags.get(id) ?? 1) + 1);
  const check = (id: string, etag: string) => {
    if (etagOf(id) !== etag) throw new ApiError(412, "precondition_failed", "Changed meanwhile.");
  };
  function find<T extends { id: string }>(list: T[], id: string): T {
    const x = list.find((y) => y.id === id);
    if (!x) throw new ApiError(404, "not_found", "No such resource.");
    return x;
  }
  const versioned = <T>(id: string, v: T): Versioned<T> => ({ value: structuredClone(v), etag: etagOf(id) });
  const groupName = (name: string) => {
    if (!name.trim() || name.length > 100) throw new ApiError(400, "invalid_group_name", "Invalid group name.");
  };

  return {
    calls,
    state,
    fail,
    refuse,
    hold,
    async members(tenant) {
      await gate("members", tenant);
      return structuredClone(state.members);
    },
    async member(tenant, id) {
      await gate("member", tenant, id);
      return versioned(id, find(state.members, id));
    },
    async restrict(tenant, id, r, etag) {
      await gate("restrict", tenant, id, r, etag);
      const m = find(state.members, id);
      check(id, etag);
      if (r.projects !== undefined) m.projects = [...r.projects];
      if (r.vendor_id !== undefined) {
        if (r.vendor_id === "") delete m.vendor_id;
        else m.vendor_id = r.vendor_id;
      }
      if (r.visibility) m.visibility = r.visibility;
      bump(id);
      return versioned(id, m);
    },
    async inviteVendorMember(tenant, invite, key) {
      await gate("inviteVendorMember", tenant, invite, key);
      find(state.vendors, invite.vendor_id);
      if (state.members.some((m) => m.email === invite.email)) throw new ApiError(409, "already_member", "Already a member.");
      const m = member({
        id: `m${++seq}`,
        email: invite.email,
        status: "invited",
        roles: invite.roles,
        locales: invite.locales ?? [],
        projects: invite.projects ?? [],
        vendor_id: invite.vendor_id,
        visibility: "assigned",
      });
      delete m.person_id;
      delete m.display_name;
      state.members.push(m);
      return structuredClone(m);
    },
    async groups(tenant) {
      await gate("groups", tenant);
      return structuredClone(state.groups);
    },
    async group(tenant, id) {
      await gate("group", tenant, id);
      return versioned(id, find(state.groups, id));
    },
    async createGroup(tenant, name, key) {
      await gate("createGroup", tenant, name, key);
      groupName(name);
      const g = group({ id: `g${++seq}`, name: name.trim() });
      state.groups.push(g);
      return structuredClone(g);
    },
    async renameGroup(tenant, id, name, etag) {
      await gate("renameGroup", tenant, id, name, etag);
      const g = find(state.groups, id);
      check(id, etag);
      groupName(name);
      g.name = name.trim();
      bump(id);
      return versioned(id, g);
    },
    async deleteGroup(tenant, id) {
      await gate("deleteGroup", tenant, id);
      find(state.groups, id);
      state.groups = state.groups.filter((g) => g.id !== id);
    },
    async addToGroup(tenant, groupId, memberId) {
      await gate("addToGroup", tenant, groupId, memberId);
      const g = find(state.groups, groupId);
      find(state.members, memberId);
      if (!g.members.includes(memberId)) g.members = [...g.members, memberId].sort();
      bump(groupId);
      return structuredClone(g);
    },
    async removeFromGroup(tenant, groupId, memberId) {
      await gate("removeFromGroup", tenant, groupId, memberId);
      const g = find(state.groups, groupId);
      if (!g.members.includes(memberId)) throw new ApiError(404, "not_in_group", "Not in the group.");
      g.members = g.members.filter((m) => m !== memberId);
      bump(groupId);
    },
    async vendors(tenant) {
      await gate("vendors", tenant);
      return structuredClone(state.vendors);
    },
    async vendor(tenant, id) {
      await gate("vendor", tenant, id);
      return versioned(id, find(state.vendors, id));
    },
    async createVendor(tenant, input, key) {
      await gate("createVendor", tenant, input, key);
      if (!input.name.trim()) throw new ApiError(400, "invalid_vendor_name", "Invalid name.");
      const v = vendor({ id: `v${++seq}`, name: input.name, locales: input.locales ?? [] });
      if (input.contact) v.contact = input.contact;
      else delete v.contact;
      state.vendors.push(v);
      return structuredClone(v);
    },
    async updateVendor(tenant, id, input, etag) {
      await gate("updateVendor", tenant, id, input, etag);
      const v = find(state.vendors, id);
      check(id, etag);
      if (input.name !== undefined) v.name = input.name;
      if (input.contact !== undefined) v.contact = input.contact;
      if (input.locales !== undefined) v.locales = [...input.locales];
      bump(id);
      return versioned(id, v);
    },
    async deleteVendor(tenant, id) {
      await gate("deleteVendor", tenant, id);
      find(state.vendors, id);
      if (state.members.some((m) => m.vendor_id === id)) throw new ApiError(409, "vendor_has_members", "The vendor has members.");
      state.vendors = state.vendors.filter((v) => v.id !== id);
    },
  };
}
