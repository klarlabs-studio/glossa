/**
 * An in-memory InContextPort for component tests, with the server's
 * rules in miniature (RFC 0004 §5.2): an origin is canonicalized on the
 * way in, http is refused off loopback, the same origin twice is a
 * conflict, minting refuses an origin the project hasn't registered, and
 * removing an origin takes its grants with it. Test-only: nothing in the
 * app imports it.
 */
import { ApiError } from "../api/errors";
import type { InContextPort } from "../api/in-context";
import type { InContextGrant, InContextPermission, PreviewOrigin } from "../api/in-context-schemas";

export interface FakeInContext extends InContextPort {
  readonly calls: Array<[string, ...unknown[]]>;
  originList: PreviewOrigin[];
  /** What a minted grant says it allows. */
  permissions: InContextPermission[];
  /** Set to make every call fail with this code. */
  failWith: string | undefined;
  /** The grants handed out, so a test can see one ended with its origin. */
  readonly minted: InContextGrant[];
}

const NOW = "2026-09-20T10:00:00Z";
const LATER = "2026-09-20T10:15:00Z";

export function previewOrigin(over: Partial<PreviewOrigin> = {}): PreviewOrigin {
  return {
    id: "po-1",
    origin: "https://preview.example.com",
    label: "shared preview",
    development: false,
    created_by: "person:one",
    created_at: NOW,
    ...over,
  };
}

const LOOPBACK = /^(localhost|127\.0\.0\.1|\[::1\]|[a-z0-9.-]+\.localhost)$/;

/** The server's ParseOrigin, in miniature; "" means it would be refused. */
export function canonicalOrigin(raw: string): string {
  let u: URL;
  try {
    u = new URL(raw.trim());
  } catch {
    return "";
  }
  if (u.pathname !== "/" || u.search || u.hash || u.username || u.password) return "";
  const host = u.hostname.toLowerCase();
  if (!host) return "";
  if (u.protocol === "http:" && !LOOPBACK.test(host)) return "";
  if (u.protocol !== "http:" && u.protocol !== "https:") return "";
  return u.origin;
}

function problem(status: number, code: string): ApiError {
  return new ApiError(status, code, code);
}

export function fakeInContext(): FakeInContext {
  const calls: Array<[string, ...unknown[]]> = [];
  const minted: InContextGrant[] = [];
  let next = 2;

  const fake: FakeInContext = {
    calls,
    minted,
    originList: [],
    permissions: [
      { permission: "catalog.read" },
      { permission: "knowledge.read" },
      { permission: "translations.read" },
      { permission: "translations.write" },
      { permission: "intelligence.read" },
      { permission: "intelligence.translate" },
    ],
    failWith: undefined,

    async origins(tenant, project) {
      calls.push(["origins", tenant, project]);
      if (fake.failWith) throw problem(500, fake.failWith);
      return [...fake.originList].sort((a, b) => a.origin.localeCompare(b.origin));
    },

    async register(tenant, project, body, key) {
      calls.push(["register", tenant, project, body, key]);
      if (fake.failWith) throw problem(500, fake.failWith);
      const origin = canonicalOrigin(body.origin);
      if (!origin) throw problem(400, "invalid_origin");
      if (fake.originList.length >= 20) throw problem(409, "too_many_preview_origins");
      if (fake.originList.some((o) => o.origin === origin)) throw problem(409, "origin_registered");
      const added = previewOrigin({
        id: `po-${next++}`,
        origin,
        label: body.label ?? "",
        development: origin.startsWith("http://"),
      });
      fake.originList.push(added);
      return added;
    },

    async unregister(tenant, project, id) {
      calls.push(["unregister", tenant, project, id]);
      if (fake.failWith) throw problem(500, fake.failWith);
      const i = fake.originList.findIndex((o) => o.id === id);
      if (i < 0) throw problem(404, "not_found");
      const [gone] = fake.originList.splice(i, 1);
      // Removing an origin ends the sessions on it, as the server does.
      for (let j = minted.length - 1; j >= 0; j--) {
        if (minted[j]!.origin === gone!.origin) minted.splice(j, 1);
      }
    },

    async mint(tenant, project, origin) {
      calls.push(["mint", tenant, project, origin]);
      if (fake.failWith) throw problem(500, fake.failWith);
      const canonical = canonicalOrigin(origin);
      if (!canonical) throw problem(400, "invalid_origin");
      if (!fake.originList.some((o) => o.origin === canonical)) throw problem(403, "origin_not_registered");
      const grant: InContextGrant = {
        token: `glossa_ctx_${"t".repeat(43)}`,
        expires_at: LATER,
        project_id: project,
        origin: canonical,
        person_id: "person-one",
        permissions: fake.permissions,
      };
      minted.push(grant);
      return grant;
    },
  };
  return fake;
}
