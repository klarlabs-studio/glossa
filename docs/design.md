# Glossa — Design Document

**Version**: 0.1 (planning)
**Last updated**: 2026-05-23
**Status**: Historical — describes the v0.1–v0.3 build. Direction now comes from [`product-intent.md`](./product-intent.md); [RFC 0001](./rfcs/0001-platform-foundation.md) §6 lists the statements here that it supersedes (notably §2.2 "roll own ICU subset" and §2.5 "same JSON bundles server-side").

## 1. Problem

Multiple personal/professional projects (IRI, Brotwerk, future) need German-first UI with eventual EN/FR/IT expansion. Current pattern (hardcoded strings inline, ~170 files per project) is:

- High-cost to add a new locale (re-edit every file)
- Inconsistent (`common.save` translated differently in each project)
- No translator workflow (every change goes through engineer + PR review)
- No live update path (deploy required for any text change)

Off-the-shelf options (Tolgee, Lokalise, Crowdin, Phrase):

- Tolgee — closest fit; self-hostable; but adds another vendor surface; want full ownership
- Lokalise / Phrase — SaaS-only; expensive per-seat; lock-in
- Crowdin — generous free tier but no Lit-component story; per-project workflow

**Decision**: build own. Cross-project shared translation memory + brand-consistent admin UI + control over component API justify the build cost.

## 2. Product principles

1. **Framework-agnostic on the client.** Lit web components drop into any frontend (Vue, React, Svelte, Astro, plain HTML).
2. **Smallest possible runtime.** Roll own ICU subset (~3kb) using `Intl.PluralRules` instead of pulling in `@formatjs/intl-messageformat` (5kb+).
3. **Fallback always wins.** If API unreachable or key missing, slot content renders. Apps work offline.
4. **Multi-tenant from day one.** Tenant per project, per-tenant API keys. No "single user" mode that bleeds tenant assumptions everywhere.
5. **Same key structure server- and client-side.** Go service uses the same JSON locale bundles; one source of truth.

## 3. Architecture

### 3.1 High-level

```
[ Consumer apps ] ──Lit components──> [ Glossa SDK ] ──HTTPS+SSE──> [ Go API ]
                                                                       │
                                                                   [ Postgres ]
                                                                       │
                                                              [ Admin UI (Lit) ]
                                                                       │
                                                                  [ Translators ]
```

### 3.2 Component boundaries

| Component | Owns | Doesn't own |
|---|---|---|
| `apps/api` | REST + SSE endpoints, auth, persistence, per-tenant access control | UI, translation rendering |
| `apps/admin` | Translator UX, project management, JSON bulk ops | Backend logic; uses the SDK |
| `packages/elements` | Lit web components, fallback rendering, locale switching reactivity | Network code (delegated to SDK), formatting (delegated to format) |
| `packages/format` | ICU subset parsing + evaluation (vars, plurals, select, nesting) | Network, components, persistence |
| `packages/sdk` | HTTP fetch, in-memory cache, SSE subscription, build-time pull | Component lifecycle, rendering |
| `packages/cli` | `init / scan / pull / push` for build-pipeline integration | Runtime behavior |

### 3.3 Data flow at runtime

1. App boot: `<glossa-provider>` connects to API, fetches initial locale bundle, opens SSE channel
2. `<glossa-text key="...">` reads from provider's in-memory message map; renders slot fallback if missing
3. Translator edits in admin UI → POST to API → API broadcasts SSE → providers receive update → components re-render via Lit reactivity
4. Build-time alternative: CLI pulls JSON, bakes into static bundle (no runtime API call); SSE updates layer on top

### 3.4 Multi-tenancy

Every API request scopes to a `tenant_id` resolved from the API key. Postgres schema enforces tenant isolation:

```sql
projects.tenant_id FK → tenants.id
locales.project_id → projects (inherits tenant)
keys.project_id → projects
translations.key_id × locale_id (inherits tenant via key's project)
```

Row-Level Security policies in Postgres enforce: queries always carry `current_setting('app.current_tenant')`; cross-tenant reads impossible at the DB layer even on a buggy handler.

## 4. ICU subset

### 4.1 Scope

Implement (~200 LOC + ~150 LOC tests):

- Variable interpolation `{name}`
- Pluralization `{count, plural, one {# Athlet:in} other {# Athlet:innen}}` — backed by `Intl.PluralRules` (browser built-in, ~100 languages free)
- Selection `{gender, select, female {Athletin} male {Athlet} other {Athlet:in}}`
- Nested combinations
- Apostrophe escaping per ICU spec (`'{'` literal)

### 4.2 Defer to browser built-ins

- Number formatting → `Intl.NumberFormat`
- Date / time formatting → `Intl.DateTimeFormat`
- Currency → `Intl.NumberFormat({style:'currency'})`
- Relative time → `Intl.RelativeTimeFormat`
- List formatting → `Intl.ListFormat`

### 4.3 Out of scope for MVP

- Custom formatters (extension API)
- Complex grammatical gender beyond binary + neutral
- Bi-di / RTL string handling (defer until Arabic/Hebrew tenant exists)

## 5. API surface

### 5.1 REST

```
GET    /api/v1/projects
POST   /api/v1/projects
GET    /api/v1/projects/:slug/locales
POST   /api/v1/projects/:slug/locales
GET    /api/v1/projects/:slug/locales/:loc/messages   # full bundle
PATCH  /api/v1/projects/:slug/locales/:loc/keys/:key  # translator update
POST   /api/v1/projects/:slug/keys:scan               # CLI batch upsert
GET    /api/v1/projects/:slug/sse                     # live updates

POST   /api/v1/auth/login                             # admin login
POST   /api/v1/auth/api-keys                          # rotate per-tenant key
```

### 5.2 SSE event shape

```json
{
  "type": "translation.updated",
  "project": "iri",
  "locale": "de",
  "key": "coach.plan.approve",
  "value": "Freigeben",
  "status": "approved"
}
```

## 6. Database schema

```sql
CREATE TABLE tenants (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  slug VARCHAR(50) UNIQUE NOT NULL,
  name VARCHAR(200) NOT NULL,
  created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE projects (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  slug VARCHAR(50) NOT NULL,
  name VARCHAR(200) NOT NULL,
  default_locale VARCHAR(8) NOT NULL DEFAULT 'de',
  api_key_hash BYTEA NOT NULL,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  UNIQUE(tenant_id, slug)
);

CREATE TABLE locales (
  id UUID PRIMARY KEY,
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  code VARCHAR(8) NOT NULL,
  label VARCHAR(50) NOT NULL,
  enabled BOOLEAN DEFAULT TRUE,
  UNIQUE(project_id, code)
);

CREATE TABLE keys (
  id UUID PRIMARY KEY,
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  key VARCHAR(255) NOT NULL,
  description TEXT,
  first_seen_at TIMESTAMPTZ DEFAULT NOW(),
  UNIQUE(project_id, key)
);

CREATE TABLE translations (
  id UUID PRIMARY KEY,
  key_id UUID NOT NULL REFERENCES keys(id) ON DELETE CASCADE,
  locale_id UUID NOT NULL REFERENCES locales(id) ON DELETE CASCADE,
  value TEXT NOT NULL,
  status VARCHAR(20) DEFAULT 'pending',
  updated_by UUID,
  updated_at TIMESTAMPTZ DEFAULT NOW(),
  UNIQUE(key_id, locale_id)
);

CREATE TABLE audit_log (
  id BIGSERIAL PRIMARY KEY,
  translation_id UUID,
  before_value TEXT,
  after_value TEXT,
  changed_by UUID,
  changed_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE users (
  id UUID PRIMARY KEY,
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  email VARCHAR(255) NOT NULL,
  password_hash BYTEA NOT NULL,
  role VARCHAR(20) DEFAULT 'translator',
  locales TEXT[],
  UNIQUE(tenant_id, email)
);

-- Indexes
CREATE INDEX idx_keys_project ON keys(project_id);
CREATE INDEX idx_translations_key_locale ON translations(key_id, locale_id);
CREATE INDEX idx_translations_status ON translations(status) WHERE status != 'approved';

-- Row-Level Security
ALTER TABLE projects ENABLE ROW LEVEL SECURITY;
CREATE POLICY projects_isolation ON projects USING (tenant_id::text = current_setting('app.current_tenant'));
-- (repeat for locales/keys/translations/users via project_id traversal)
```

## 7. Tech stack

| Concern | Choice | Reason |
|---|---|---|
| HTTP framework | `gin-gonic/gin` | Matches Brotwerk; richer middleware ecosystem than chi for what we need (auth, rate limit, recovery) |
| DB driver / query layer | `pgx/v5` + `sqlc` | Type-safe Go from `.sql` sources; matches Brotwerk/IRI |
| Migrations | `golang-migrate` (CLI + Go embed) | Matches Brotwerk; init-container friendly |
| DB engine | Postgres 16 | RLS for multi-tenant isolation |
| Logging | `felixgeelhaar/bolt` (slog handler) | Own high-perf structured logger; already proven in Brotwerk |
| Resilience | `felixgeelhaar/fortify` | Own library — rate-limit (per-tenant translator broadcasts), circuit-breaker (DeepL passthrough later), timeout, retry, typed errors via `ferrors` |
| Cache | in-process map; Redis later | MVP doesn't need Redis until multi-replica SSE fanout |
| Auth | `golang-jwt/jwt` for admin sessions; SHA-256-hashed API keys for consumer / CLI traffic | JWT proven across IRI + Brotwerk; API keys keep Lit-component install ergonomic |
| Admin UI | Lit + Vite + TS | Dogfood the same components consumers ship |
| Components | Lit + own ICU subset (`@felixgeelhaar/glossa-format`) | Smallest runtime; framework-agnostic |
| Hosting | k3s on `edge-1` (shared with IRI/Brotwerk) | Zero infra cost; mirror IRI deploy patterns |
| TLS | cert-manager + Let's Encrypt | Already configured cluster-wide |

## 8. Deployment

Mirror IRI's `deploy/k3s/` structure:

- `apps/api` builds + pushes to `ghcr.io/klarlabs-studio/glossa-api`
- `apps/admin` builds + pushes to `ghcr.io/klarlabs-studio/glossa-admin`
- StatefulSet for Postgres (20Gi PVC)
- Ingress: `<glossa-domain>` → admin; `<glossa-domain>/api/*` → API
- Daily backup CronJob via existing rclone-config + Hetzner Storage Box pattern

## 9. Open design questions

1. **API key vs JWT for consumer requests** — currently lean API key (simpler for build-time pulls + Lit components). JWT for admin UI only. Reconsider if granular per-user audit needed on translation reads.
2. **Strict mode** — should `<glossa-text key="missing.key">` log a console warning in dev? Probably yes, gated on `<glossa-provider strict>`.
3. **Plural-rules editor in admin UI** — visual builder vs raw textarea with ICU spec linter. Raw + linter for MVP.
4. **Translation memory across tenants** — opt-in cross-tenant TM suggestions. Privacy implication: needs explicit per-tenant consent + per-key opt-out.
5. **Machine translation passthrough** — DeepL the obvious EU choice. Defer to post-MVP.
6. **Versioning of locale bundles** — etag + If-None-Match for cache, or version field per bundle?

## 10. Non-goals (explicit out-of-scope)

- Live in-page editing ("Tolgee in-context editor"). Consider for v2.
- Translation marketplace / paid-translator workflow. Consider when first paid translator needed.
- AI auto-translation (beyond DeepL passthrough). Consider when underway with manual translators.
- Built-in spell-check / grammar-check. Defer to translator's editor.
- Asset (image / video) localization. Strings only.
