# Changelog

All notable changes to Glossa go here.

Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) · SemVer.

## [Unreleased]

Glossa is becoming localization infrastructure: see
[`docs/product-intent.md`](docs/product-intent.md) and
[RFC 0001](docs/rfcs/0001-platform-foundation.md).

### Added

- **Full BCP 47 locale identity** (RFC 0001 D4). Locale codes are parsed,
  validated and canonicalized as BCP 47 tags: `zh-Hant-TW`, `sr-Latn`,
  `es-419` and `ca-ES-valencia` are accepted; `en_us` becomes `en-US`,
  `iw` becomes `he`, `en-Latn-US` becomes `en-US`. Extensions and private
  use are rejected. Locale responses gain `language`, `script`, `region`
  and `direction`. Migration `0006` widens the code columns to 35
  characters.
- **Right-to-left editing in the admin.** The translation field and the
  preview take the locale's `lang` and `dir`.

### Changed

- **Locale URL segments match by canonical form.** `/locales/de-de/messages`
  and `/locales/de_DE/messages` resolve to `de-DE`, and bundle responses,
  SSE events and audit metadata carry the canonical code. Before, a
  spelling that differed from the stored code returned 404.
- **Invalid project default locales are rejected** with 422. Before, the
  project was created and the source-locale row was silently skipped.
- **Translator locale scopes are validated and canonicalized** on create
  and update.

### Fixed

- **`<glossa-*>` elements inside Vue templates always showed their
  fallback.** Vue reserves `key` for its own list reconciliation and never
  renders it as a DOM attribute, so `<glossa-text key="…">` in a `.vue`
  file reached the element with no message ID and rendered the slot content
  in every locale. The elements now also accept `message="…"`; use it in
  Vue templates. `key` keeps working in plain HTML and `.astro` files.

## 0.3.1 — 2026-08-12

### Fixed

- **The admin image had not built since #22.** That change pinned
  `form-data`, `js-yaml`, `nanoid`, `postcss` and `vite` as root dependencies
  to force patched transitive versions, without regenerating `pnpm-lock.yaml`.
  The admin Dockerfile installs with `--frozen-lockfile`, which refuses a
  lockfile whose specifiers disagree with `package.json`, so every admin build
  failed with `ERR_PNPM_OUTDATED_LOCKFILE`. The API image builds from Go and
  was unaffected, so releases looked partly successful rather than broken.
  0.3.0 shipped an API image and no admin image; use this release instead.

## 0.3.0 — 2026-08-12

### Fixed

- **`write`-scoped API keys could not write.** `handlePatchTranslation`
  documents the API-key case as "otherwise zero (CLI / system change)" and
  passes a nil actor; the use case rejected nil as a guard against a broken
  handler. Every CLI or service `PATCH` returned `422 keyId, localeId, and
  updatedBy must all be non-nil`, which reads as a malformed payload rather
  than an unimplemented path. `updated_by` is nullable with no foreign key, so
  nil is now accepted and means "not a person"; `keyId` and `localeId` stay
  required. Pass `updatedBy` to attribute a change to a specific agent.
- **The project in the URL was ignored.** `resolveProject` returned the API
  key's project and never read `:slug`, so `/api/v1/projects/<any-other>/…` —
  and even a slug naming nothing at all — answered `200` with the key's data.
  Not a privilege escalation, since a key still reaches only its own project,
  but a client pointed at the wrong project had no way to tell: a drift checker
  querying one project while holding another's key compared two unrelated key
  sets and reported everything fine.

### Changed

- **A `{slug}` that does not match the API key's project now returns 404** —
  the same status as any unreachable project, so it cannot be used to probe
  which slugs exist. Routes without a `:slug` are unaffected. Callers that
  relied on the segment being ignored must send the key's own project slug.
- `api/openapi.yaml` now documents the `read` / `write` scopes, the 403 a read
  key receives from write endpoints, the slug rule, and `updatedBy` semantics.
  It previously declared one unscoped scheme, which overstated what a
  read-only key could do.

### Added

- **AI translator agents.** New `ai_translation_providers` table (AES-GCM
  encrypted credentials), new `ai_translated` status between `pending` and
  `needs_review`, fan-out worker on source-locale writes. OpenAI /
  Anthropic / Gemini / OpenAI-compatible custom endpoints supported.
  Admin UI tab for provider management + live test calls.
- **`@felixgeelhaar/glossa-ui` design system.** Light / dark / system theme tokens,
  primitives (`gl-button`, `gl-input`, `gl-select`, `gl-textarea`,
  `gl-card`, `gl-badge`, `gl-table`, `gl-tabs`, `gl-toast`, `gl-toolbar`,
  `gl-theme-toggle`). Every admin tab migrated.
- **Email-first login.** `/auth/discover` returns the tenant list for an
  email; multi-tenant users pick which tenant to sign into. Empty result
  is returned for unknown emails to deny enumeration.
- **Docker Compose dev stack.** `docker compose up --build` brings up
  Postgres + migrate + API + admin. Admin nginx proxies `/api` over the
  compose network.
- Audit `before_value` is now correctly populated for translation edits.
- `actor_kind` + `actor_label` columns on `audit_log` for AI vs user
  attribution.
- `/auth/login` rate-limited (5 req/min per IP, burst 10) to defend
  bcrypt against brute force.

### Changed

- Audit log surfaces actor type alongside `changed_by` UUID — AI rows
  show their provider name; user rows show the email.

### Security

- AI provider API keys are AES-256-GCM encrypted at rest with per-row
  nonces. Master key (`GLOSSA_SECRETS_KEY`) is env-only.
- AI translation feature degrades gracefully: empty `GLOSSA_SECRETS_KEY`
  disables provider endpoints with a 503 + explanatory message.

## 0.0.0

Initial repo scaffold: monorepo layout (`apps/api`, `apps/admin`,
`packages/ui`), root `package.json` + `pnpm-workspace.yaml`, top-level
`Makefile`, MIT `LICENSE`, `.editorconfig`, `.gitignore`. Initial design
captured in `docs/design.md`.
