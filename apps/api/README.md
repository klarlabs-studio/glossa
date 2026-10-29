# `apps/api` — Glossa REST + SSE service

Stub. Implementation lands after the Glossa kickoff (gated on IRI v0.2.0 — see top-level `README.md` § Decisions locked).

## Layout (planned)

```
cmd/api/             — main.go entry point
internal/
  domain/            — pure domain types (Project, Locale, Key, Translation)
  app/               — use cases (Translate, ApproveTranslation, RotateAPIKey)
  interfaces/
    httpchi/         — REST + SSE handlers (chi router)
  infra/
    sqlcadapter/     — sqlc-generated repo adapters
  db/                — sqlc-generated model + querier
db/
  migrations/        — golang-migrate up/down pairs
  queries/           — sqlc .sql sources
```

Mirrors IRI/Brotwerk's apps/api layout intentionally — hex-arch, sqlc, golang-migrate.

## Library choices (locked)

- **HTTP**: `gin-gonic/gin` (matches Brotwerk)
- **DB**: `pgx/v5` driver + `sqlc`-generated query types
- **Migrations**: `golang-migrate`
- **Logging**: `felixgeelhaar/bolt` slog handler
- **Resilience**: `felixgeelhaar/fortify` — `ratelimit`, `circuitbreaker`, `timeout`, `retry`, `ferrors` for typed errors
- **Auth**: `golang-jwt/jwt` for admin sessions; SHA-256-hashed API keys for consumer / CLI

## Status

- [ ] `go mod init github.com/klarlabs-studio/glossa/apps/api`
- [ ] Initial migrations (tenants, projects, locales, keys, translations, audit_log, users)
- [ ] Domain layer
- [ ] REST endpoints per `docs/design.md` § 5.1
- [ ] SSE channel per § 5.2
- [ ] RLS policies per § 6
