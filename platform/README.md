# platform

Go module for Glossa's control and delivery planes (RFC 0002). It holds
the `glossa-server` kernel (configuration, observability, the HTTP edge,
Postgres with forced row-level security, tenancy, the transactional
outbox), the `/v1` API contract, the bounded contexts under
`internal/<context>/` (Identity, Catalog, Localization, Release,
Knowledge, Intelligence, Integration and Context so far) and `glossa-edge`, the
stateless delivery server.

```text
api/openapi.yaml            the /v1 contract (OpenAPI 3.1), source of truth
cmd/glossa-server/          composition root only (plus GET /v1/meta, which gathers deployment facts)
cmd/glossa-edge/            composition root of the delivery plane (no database)
db/migrations/              golang-migrate SQL, embedded into the binary
db/queries/<context>/       sqlc input, one directory per context
sqlc.yaml                   one sqlc entry per context
internal/apiv1/             Go server generated from api/openapi.yaml (oapi-codegen)
internal/kernel/
  config/                   env config, validated at startup
  observability/            bolt logger, OTel tracer, Prometheus, request middleware
  httpserver/               net/http server, /livez /readyz /metrics
  db/                       pgx pool, migrator, unit of work, dbtest harness
  tenancy/                  tenant ID/kind, context, Resolver middleware; tenantpg adapter
  outbox/                   Publish, Registry, Dispatcher, Postgres store
  scheduler/                periodic jobs with a deployment-wide lease (the daily purge)
  problem/                  RFC 9457 error bodies with stable codes
  pagination/               page_size/page_token cursor pagination
  bcp47/                    canonical locale tags and text direction (shared kernel)
  mfcontent/                authored text + canonical MF2 model + derived metadata (shared kernel)
  etag/, idempotency/       ETag/If-Match and Idempotency-Key helpers
  jcs/                      RFC 8785 canonical JSON (artifacts, manifests, signatures)
  sealing/                  AES-256-GCM secrets at rest, bound to their tenant and row
  ratelimit/                in-process token bucket per key (fortify)
  objectstore/              object storage port; dir, memory and S3 (minio-go) adapters
internal/apiv1/apiconv/     message content, QA findings and If-Match on the wire
internal/catalog/           projects, applications, messages, source revisions
internal/localization/      locales, fallback graphs, translations, revisions
internal/release/           environments, releases, artifacts, signing, delivery keys
  delivery/                 the bucket layout and key format glossa-edge shares
internal/knowledge/         translation memory, termbase, style guides (RFC 0003 §2)
  domain/                   TM normalization and derivation, term recognition, terminology QA, style merge
  app/                      use cases, the outbox subscribers, Reader (the port Intelligence consumes)
  adapters/                 postgres (sqlc), sources (Catalog/Localization ports), httpapi
internal/intelligence/      AI translation (RFC 0003 §3–§4, §7)
  domain/                   provider port, routing, prices, budgets, confidence, config, jobs, suggestions
  app/                      the translation agent and Router; the wiring's Service, subscribers and Worker
  adapters/                 anthropic, openaicompat, gemini, resilient, cassette, memory (library);
                            postgres (sqlc), sources, providers, metrics, httpapi (wiring)
  prompts/, evals/          versioned prompts; golden sets, cassettes and the tracked baseline
internal/integration/       interchange (RFC 0003 §5–§6)
  formats/                  pure converters: XLIFF 2.1, JSON, PO (read), TMX, TBX ↔ the exchange model
  domain/                   import/export jobs, options, access snapshot, state capping, merge conflicts, PO keys
  app/                      import and export use cases, the Worker (claims, checkpoints, retention), mapping
  adapters/                 postgres (sqlc), sources (Catalog/Localization/Knowledge ports), httpapi
internal/context/           where messages appear (RFC 0004 §2–§3)
  domain/                   usages documents, builds, captures and regions, current views, retention
  app/                      ingest, current and unused usages, purge, the outbox subscribers
  adapters/                 postgres (sqlc; the system-scope Sweeper), catalog (Catalog's port),
                            httpapi (the Context API), metrics (Prometheus)
internal/preview/           stateless message preview (parse, MF2, format), rate-limited per caller
internal/mcp/               the Model Context Protocol endpoint (RFC 0005 §7), off by default
                            — see its own README for connecting an editor and the tool list
  domain/                   the three session toolsets (read, write, publish), the outcome
                            vocabulary, the argument shape, the endpoint's wire vocabulary
  app/                      the session model, the tool registry and one audited tool call
  tools/                    the tools, declared as data: Read, Write and Publish, one toolset
                            each, over the narrow ports in ports.go
  adapters/                 mcpgo (streamable HTTP on the official Go MCP SDK), identity (the
                            Authenticator over Identity), sources (one adapter per bounded
                            context), postgres (the audit ledger, sqlc), metrics (Prometheus)
internal/audit/             the tenant's tamper-evident trail (RFC 0006 §6)
  domain/                   the entry, its canonical form and hash chain, the verifier, the
                            per-event-type projection table, content-free summaries
  app/                      the outbox subscriber, the Recorder (sign-ins, MCP calls), the backfill
  adapters/postgres/        audit_entries (sqlc), append-only, serialized per tenant
internal/edge/              glossa-edge's handler and server (object storage only)
internal/identity/
  domain/                   Person, Member, roles, locale scopes, Grant, APIToken, events
  app/                      sign-in flows (auth-go) and tenant/member/token use cases
  authz/                    authz.Require: the permission check every context uses
  adapters/postgres/        app + auth-go ports on the unit of work (sqlc)
  adapters/httpapi/         Identity's /v1 handlers, the Guard, the tenancy.Resolver
  adapters/mail/            log (dev) and SMTP mailers
  adapters/passkey/         auth-go's WebAuthn adapter, configured
```

## Running it

You need Postgres 16. Migrations run as the schema **owner**, a
non-superuser with `CREATEROLE`. The server connects as **`glossa_app`**,
which the first migration creates without a login.

```sh
docker run -d --name glossa-pg -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:16-alpine
psql postgres://postgres:postgres@localhost:5432/postgres <<'SQL'
CREATE ROLE glossa_owner LOGIN CREATEROLE PASSWORD 'owner';
CREATE DATABASE glossa OWNER glossa_owner;
SQL

export MIGRATION_DATABASE_URL=postgres://glossa_owner:owner@localhost:5432/glossa?sslmode=disable
export DATABASE_URL=postgres://glossa_app:app@localhost:5432/glossa?sslmode=disable
export GLOSSA_AUTH_SECRET=$(openssl rand -base64 32)    # keep it: it seals TOTP secrets
go run ./cmd/glossa-server -migrate=only                 # creates roles, tables, policies
psql "$MIGRATION_DATABASE_URL" -c "ALTER ROLE glossa_app LOGIN PASSWORD 'app'"
go run ./cmd/glossa-server
```

By default the server sends no email (`GLOSSA_MAIL_DRIVER=none`):
register with a password and sign in with it (see *Identity*, "Without
email"). For magic links in development, ask for the log mailer, which
writes every mail, links included, to the log:

```sh
GLOSSA_MAIL_DRIVER=log go run ./cmd/glossa-server
curl -X POST localhost:8080/v1/auth/magic-links -H 'content-type: application/json' \
  -d '{"email":"you@example.com"}'
# copy the token after #token= from the log, then:
curl -i -X POST localhost:8080/v1/auth/magic-link-redemptions \
  -H 'content-type: application/json' -d '{"token":"…"}'
```

In a deployment, the database owner (with `CREATEROLE`, never a
superuser) runs `glossa-server -migrate=only` as a Job, and a later hook
gives `glossa_app` its login and password (see
`deploy/charts/glossa-platform/README.md` for the order).
The server refuses to start if `DATABASE_URL` is a superuser or
`BYPASSRLS` role, or if that role can't `SET ROLE glossa_system`.

### Environment

| Variable | Default | Meaning |
|---|---|---|
| `DATABASE_URL` | required | App role DSN. Pool sizing goes in the DSN (`pool_max_conns=20`). |
| `MIGRATION_DATABASE_URL` | — | Owner DSN. Required when migrating. |
| `GLOSSA_MIGRATE` | `off` | `off`, `up` (migrate, then serve) or `only` (migrate, then exit). The `-migrate` flag overrides it. |
| `GLOSSA_HTTP_ADDR` | `:8080` | Listen address. |
| `GLOSSA_HTTP_READ_HEADER_TIMEOUT` / `_READ_TIMEOUT` / `_WRITE_TIMEOUT` / `_IDLE_TIMEOUT` | `5s` / `30s` / `30s` / `120s` | Server timeouts. |
| `GLOSSA_HTTP_MAX_BODY_BYTES` | `1048576` | Request body cap. |
| `GLOSSA_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| `GLOSSA_DB_CONNECT_TIMEOUT` | `60s` | How long the server and `-migrate=only` retry the first database connection (exponential backoff, each attempt logged) before giving up. At most `10m`. |
| `GLOSSA_SHUTDOWN_TIMEOUT` | `25s` | Drain budget after SIGTERM. Keep it below the pod's grace period. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | — | Turns on OTLP/HTTP trace export. The other `OTEL_*` variables apply. |
| `OTEL_SERVICE_NAME` | `glossa-server` | Trace resource name. |
| `GLOSSA_OUTBOX_ENABLED` | `true` | Run the dispatcher in this process. |
| `GLOSSA_OUTBOX_POLL_INTERVAL` | `1s` | Idle poll interval. |
| `GLOSSA_OUTBOX_BATCH_SIZE` | `50` | Events claimed per round trip. |
| `GLOSSA_OUTBOX_MAX_ATTEMPTS` | `10` | Deliveries before an event is dead-lettered. |
| `GLOSSA_OUTBOX_LEASE` | `1m` | Claim lease. Must exceed the handler timeout. |
| `GLOSSA_OUTBOX_HANDLER_TIMEOUT` | `30s` | Budget for one subscriber, retries included. |
| `GLOSSA_AUTH_SECRET` | required | Base64 of ≥ 32 random bytes. The CSRF, TOTP-sealing, passkey-state and secret-sealing keys are derived from it (HKDF). Rotating it invalidates CSRF tokens and in-flight passkey ceremonies and makes enrolled TOTP secrets and tenants' AI provider keys unreadable (they are entered again). |
| `GLOSSA_STUDIO_URL` | `http://localhost:5173` | Studio's origin; emailed links point into it. |
| `GLOSSA_SESSION_TTL` | `336h` | Session lifetime, the CLI's device sessions (`glossa login --device`) included. |
| `GLOSSA_TRUSTED_PROXIES` | — | Comma-separated CIDRs (or addresses) of the reverse proxies whose `X-Forwarded-For` names the client. Limits per client address — starting a device sign-in, ten a minute — key on the rightmost untrusted hop; unset, the TCP peer is the client, so behind an ingress set it to the ingress's pod range. |
| `GLOSSA_MAIL_DRIVER` | `none` | `none` (no email: magic links and password reset by email are off, password accounts work unverified), `smtp`, or `log` (development only: mail goes to the log, links included). |
| `GLOSSA_MAIL_FROM` | `Glossa <no-reply@localhost>` | Sender. |
| `GLOSSA_SMTP_ADDR` / `_USERNAME` / `_PASSWORD` | — | Submission server (`host:587`) and AUTH PLAIN credentials. STARTTLS is required. |
| `GLOSSA_SMTP_ALLOW_PLAINTEXT` | `false` | Allow a server without STARTTLS (a local relay only). |
| `GLOSSA_WEBAUTHN_RP_ID` | — | Passkey relying party ID (registrable domain). Passkeys are off while unset. |
| `GLOSSA_WEBAUTHN_RP_NAME` | `Glossa` | Name shown by authenticators. |
| `GLOSSA_WEBAUTHN_ORIGINS` | `GLOSSA_STUDIO_URL` | Comma-separated origins allowed to run passkey ceremonies. |
| `GLOSSA_STORAGE_DRIVER` | `dir` | Object storage for release artifacts: `dir` (a local directory, single node) or `s3`. glossa-edge reads the same storage. |
| `GLOSSA_STORAGE_DIR` | `data/objects` | The directory for `dir`. |
| `GLOSSA_S3_ENDPOINT` / `_BUCKET` / `_REGION` / `_PREFIX` | — / — / `us-east-1` / — | S3-compatible bucket: `host[:port]` without a scheme; the prefix lets deployments share a bucket. |
| `GLOSSA_S3_ACCESS_KEY_ID` / `_SECRET_ACCESS_KEY` | — | Credentials. glossa-edge needs read access only. |
| `GLOSSA_S3_PATH_STYLE` / `_INSECURE` / `_TIMEOUT` | `false` / `false` / `10s` | Path-style requests (MinIO), plain HTTP (local only), per-operation budget. |
| `GLOSSA_RELEASE_SIGNING_KEYS` | derived | `keyId=base64(32-byte Ed25519 seed)`, comma-separated. Every manifest is signed with each. Unset: one key derived from `GLOSSA_AUTH_SECRET` (development only; a warning is logged). |
| `GLOSSA_RELEASE_RETIRED_KEYS` | — | `keyId=base64(public key)`, comma-separated: still published for verification, no longer signing. |
| `GLOSSA_AUDIT_EXPORTS_ENABLED` | `false` | Audit export jobs (RFC 0006 §6.2). `true` without `GLOSSA_AUDIT_SIGNING_KEY` refuses to start. |
| `GLOSSA_AUDIT_SIGNING_KEY` | — | Exactly one `keyId=base64(32-byte Ed25519 seed)`: the audit export key. Its own key — the server refuses a seed that is also a release signing key, and there is no key derived from `GLOSSA_AUTH_SECRET`. See *Audit export format*. |
| `GLOSSA_AUDIT_RETIRED_KEYS` | — | `keyId=base64(public key)`, comma-separated: earlier audit keys, published so the exports they signed keep verifying. |
| `GLOSSA_AUDIT_EXPORT_RETENTION` | `168h` | How long an audit export job's two files are kept; the sweep deletes them afterwards (the job stays). |
| `GLOSSA_EDGE_PUBLIC_URL` | — | glossa-edge's public base URL (`https://edge.example.com`). `GET /v1/meta` announces it, so Studio's snippets and other clients don't guess. |
| `GLOSSA_AI_WORKERS_ENABLED` | `true` | Run AI translation job workers in this process. |
| `GLOSSA_AI_WORKERS` | `2` | Jobs this process runs at once. Tenants' own caps (`max_concurrent_jobs`) apply across replicas. |
| `GLOSSA_AI_POLL_INTERVAL` | `1s` | Idle poll interval of a worker. |
| `GLOSSA_AI_JOB_TIMEOUT` / `_JOB_LEASE` | `10m` / `15m` | Budget for one job (model calls and retries included); how long a claimed job is reserved before another worker takes it over. The lease must be longer. |
| `GLOSSA_AI_PROVIDER_CONCURRENCY` | `4` | Calls in flight per configured provider in this process. |
| `GLOSSA_AI_ALLOW_PRIVATE_ENDPOINTS` | `false` | Let tenants point providers at loopback and private addresses (self-hosted models in the cluster). Off: such base URLs are refused and the provider client won't connect to them. |
| `GLOSSA_INTEGRATION_WORKERS_ENABLED` | `true` | Run import/export job workers (and the retention sweep) in this process. |
| `GLOSSA_INTEGRATION_WORKERS` | `1` | Import/export jobs this process runs at once. A tenant runs at most 2 across replicas. |
| `GLOSSA_INTEGRATION_POLL_INTERVAL` | `1s` | Idle poll interval of an import/export worker. |
| `GLOSSA_INTEGRATION_JOB_TIMEOUT` / `_JOB_LEASE` | `30m` / `35m` | Budget for one attempt of a job; how long a claimed job is reserved before another worker resumes it from its last checkpoint. The lease must be longer. |
| `GLOSSA_INTEGRATION_MAX_UPLOAD_BYTES` | `67108864` | Largest import file (64 MiB; at most 2 GiB). The upload route streams it to object storage instead of taking `GLOSSA_HTTP_MAX_BODY_BYTES`. |
| `GLOSSA_INTEGRATION_UPLOAD_TIMEOUT` | `10m` | Read deadline of an upload and write deadline of a download, instead of the HTTP read/write timeouts. |
| `GLOSSA_INTEGRATION_RETENTION` | `168h` | How long uploaded and exported files are kept; the sweep deletes them afterwards (jobs and results stay). |
| `GLOSSA_PURGE_ENABLED` | `true` | Run the daily purge jobs in this process: Context's retention (the latest 3 builds per application, branch and source, a closed branch's after 7 days, with their captures and unreferenced images) and Catalog's separate proposal sweep (14 days). A lease picks one replica per run, so leave it on everywhere. |
| `GLOSSA_PURGE_INTERVAL` | `24h` | How often the jobs run across the deployment, not per pod. |
| `GLOSSA_PURGE_TIMEOUT` / `_LEASE` | `30m` / `35m` | Budget for one run of one job (it must fit inside the interval); how long a run reserves its job against the other replicas. The lease must be longer. |
| `GLOSSA_PURGE_POLL_INTERVAL` | `5m` | How often a replica asks whether a job is due. It must not exceed the interval. |
| `GLOSSA_PURGE_JITTER` | `0.2` | Fraction of the poll interval (0–1) each poll is spread by, so replicas started together don't ask in lockstep. |
| `GLOSSA_PURGE_BATCH_SIZE` | `100` | Object-store deletes issued at a time while freeing unreferenced capture images. |
| `GLOSSA_WORKFLOW_INSTANCE_RETENTION` | `4320h` | How long a finished workflow instance and its transition log are kept (RFC 0006 §2.5; at least `24h`). The daily `workflow.retention` job, one of the purge jobs, deletes them afterwards, at most 5,000 per tenant a run; running instances are never deleted. |
| `GLOSSA_CONTEXT_STORAGE_QUOTA_BYTES` | `2147483648` | Capture images one tenant may keep in object storage (2 GiB, RFC 0004 §3.3). A capture upload whose new pixels would pass it is refused with `storage_quota_exceeded` (413); retention frees space again. |
| `GLOSSA_BRANCH_PUBLISHER_ENABLED` | `true` | Publish branch preview environments whose debounced request is due. A publish is keyed by its request, so every replica may run it. (The proposal sweep is not here: it is one of the leased `GLOSSA_PURGE_*` jobs.) |
| `GLOSSA_BRANCH_PUBLISH_INTERVAL` | `5s` | How often due branch publishes are looked for (the debounce itself is 30 s). |
| `GLOSSA_MCP_ENABLED` | `false` | Serve MCP at `/mcp` (RFC 0005 §7). Off, the path is not registered at all. A session presents an ordinary tenant API token, so turning it on lets every token that carries `read` drive an agent against its tenant's catalog; turn it on deliberately. |
| `GLOSSA_MCP_SESSION_TIMEOUT` | `30m` | Close an MCP session idle this long, so an agent that walks away does not hold one open. |
| `GLOSSA_MCP_RATE` / `_BURST` | `120` / `240` | Tool calls a minute per tenant, and how many at once. A runaway agent hits the same wall a runaway script does. |

`glossa-edge` reads `GLOSSA_HTTP_*` (listening on `:8081` by default),
`GLOSSA_LOG_LEVEL`, `GLOSSA_SHUTDOWN_TIMEOUT`, `OTEL_*` (service
`glossa-edge`), the `GLOSSA_STORAGE_*`/`GLOSSA_S3_*` variables above, and:

| Variable | Default | Meaning |
|---|---|---|
| `GLOSSA_EDGE_CACHE_BYTES` | `67108864` | In-process cache for manifests and artifacts. |
| `GLOSSA_EDGE_KEY_TTL` | `30s` | How long a key resolution is trusted: the longest a revoked key keeps working at one edge. |
| `GLOSSA_EDGE_MANIFEST_TTL` | `5s` | How long a manifest is served before storage is asked again: the delay of a publish, promote or rollback at one edge (a CDN adds its 60 s max-age). |

```sh
GLOSSA_STORAGE_DIR=data/objects go run ./cmd/glossa-edge   # beside a local glossa-server
```

### Tests

```sh
go test -race ./...                            # unit (incl. contract lint and generated-code freshness)
go test -tags=integration -timeout=300s ./...  # Docker: Postgres 16 via testcontainers
go test -tags=system -timeout=600s ./internal/systemtest/m2/...  # M2 exit test (make system-m2); writes m2/REPORT.md
go test -tags=system -timeout=900s ./internal/systemtest/m3/...  # M3 exit test (make system-m3); also needs a Chrome
go test -tags=system -p 1 ./internal/systemtest/...             # both (make system): -p 1, or they starve each other
go generate ./db/...                           # regenerate sqlc code (sqlc pinned in db/generate.go)
go generate ./internal/apiv1/...               # regenerate the /v1 server (oapi-codegen pinned as a go.mod tool)
go generate ./internal/apiclient/...           # regenerate the CLI's /v1 client from the same spec
```

The integration harness (`internal/kernel/db/dbtest`) provisions the
database the way production does: a `CREATEROLE` owner runs the
migrations, and the tests connect as `glossa_app`.

**MinIO's OSS images were withdrawn in September 2026** — Docker Hub
stopped serving `minio/minio`, `quay.io/minio/*` refuses anonymous pulls
— so the object-storage harness
(`internal/kernel/objectstore/s3store/s3test`) and the chart now run
**Chainguard's builds**, `cgr.dev/chainguard/minio` and
`cgr.dev/chainguard/minio-client` (uid 65532, drop-in otherwise). They
track `:latest` here and are pinned by digest in the chart, which
documents how to refresh the pin. If Chainguard ever gates them too, the
S3-API alternatives are SeaweedFS, Garage, RustFS and Ceph RGW.

## Contract for context authors

### Tenancy and row-level security

- Every tenant-owned table has a `tenant_id uuid NOT NULL` column plus
  this block in its migration. The RLS guard test (`TestRLSGuard`) reads
  the catalog and fails CI for any table that doesn't. A table without
  tenant data that must be reached before a tenant is known (Identity's
  people, sessions, credentials) goes in the guard's `systemTables` with
  a reason: it still forces RLS, is granted to `glossa_system` only, and
  `glossa_app` may read it only through a `PUBLIC … FOR SELECT` policy
  scoped by `app_current_tenant()`. `globalTables` (outside RLS
  entirely) is for bookkeeping like `schema_migrations`.

  ```sql
  ALTER TABLE things ENABLE ROW LEVEL SECURITY;
  ALTER TABLE things FORCE ROW LEVEL SECURITY;
  CREATE POLICY things_tenant_isolation ON things
      USING (tenant_id = app_current_tenant())
      WITH CHECK (tenant_id = app_current_tenant());
  GRANT SELECT, INSERT, UPDATE, DELETE ON things TO glossa_app;  -- only what you need
  ```

- Every query runs inside `uow.InTenantTx(ctx, fn)`. The tenant comes
  from `ctx` and nowhere else. It's put there by `tenancy.Middleware`,
  whose `Resolver` (Identity) takes the `{tenant}` of
  `/v1/tenants/{tenant}/…` and accepts it only if the session's person
  is an active member or the bearer token belongs to it — never from a
  header. Pass the `*db.TenantTx` to your sqlc `New(tx)`. The
  transaction commits when `fn` returns nil. Nested units of work are
  refused. A context's in-process port that must write its own tables
  in its caller's commit joins the open transaction explicitly with
  `uow.InCurrentTenantTx(ctx, fn)` (`ErrNoTx` outside one) — how
  Localization's projection follows Catalog's bulk upsert.
- `uow.InSystemTx(ctx, db.NewSystemScope("ctx.job"), fn)` is for
  tenantless background work only. It refuses a context that carries a
  tenant, and it runs as `glossa_system`, which sees nothing unless a
  migration grants it a table and adds a `TO glossa_system` policy.
  That policy must then be listed in the guard's `systemPolicies`.

### The /v1 API

`api/openapi.yaml` is the contract; its description lists the
conventions every operation follows (`/v1` + kebab-case resources,
tenant-owned resources under `/v1/tenants/{tenant}`, cursor pagination,
problem codes, `ETag`/`If-Match`, `Idempotency-Key`, RFC 3339 UTC, opaque
string IDs, the two security schemes). `api/openapi_test.go` validates
the document and enforces those conventions per operation, so an
operation that skips one fails CI.

To add operations: write them in the spec, run
`go generate ./internal/apiv1/...` (`TestGeneratedCodeIsCurrent` fails
on a stale `apiv1.gen.go`), implement the new methods on your context's
handler type in `internal/<context>/adapters/httpapi`, and embed that
type in `apiServer` (`cmd/glossa-server/api.go`, through an alias: every
context names its type `API`; `var _ apiv1.StrictServerInterface =
apiServer{}` fails the build until every operation has a handler). You don't write auth
code: Identity's `Guard` reads each route's `security` from the
contract and enforces it, and on `/v1/tenants/{tenant}/…` your handler
runs with the tenant and principal on its context. Authorize in the
application layer:

```go
if err := authz.Require(ctx, authz.CatalogWrite); err != nil { return err }       // 403
if err := authz.RequireFor(ctx, authz.TranslationsWrite, locale); err != nil { … } // locale-scoped
```

Return errors, not responses: a `*problem.Details` (with a snake_case
code listed in the spec) passes through as is; Identity's mapping covers
authz and its own errors, and anything unknown is a logged 500.
Paginate with `pagination.Parse` / `pagination.Trim`. The TypeScript
client for Studio will be generated from the same spec.

### Domain events

Event types are `<context>.<aggregate>.<past-tense verb>` in snake_case —
`identity.member.added`, `catalog.message.source_revised`. The middle
segment is the event's `AggregateType`, and `AggregateID` is the
aggregate's ID. Payloads are JSON objects with snake_case keys that
carry IDs and the facts of the change, never secrets. A released name
never changes meaning: a breaking payload change is a new type with a
`.v2` suffix, published alongside the old one until its subscribers
move. Identity publishes `identity.tenant.created`,
`identity.member.{added,activated,access_changed,restriction_changed,removed}`,
`identity.token.{created,revoked}`, `identity.vendor.{created,changed,deleted}`
and `identity.group.{created,renamed,deleted,member_added,member_removed}`.
A member's or token's project scope and a member's vendor and
`assigned` visibility (RFC 0006 §3.3, §4) are stored, published and
enforced by `authz` in every context's read and write paths: a project
outside a principal's scope answers as if it did not exist, and an
`assigned` member reads and writes only the units their assignments
cover (`domain.RestrictionEnforced`).
Catalog and Localization's events
are listed under their sections below.

### Outbox

```go
err := uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
    // … change state with sqlc on tx …
    _, err := outbox.Publish(ctx, tx, outbox.Event{
        Type: "catalog.message.source_revised", AggregateType: "message", AggregateID: id,
        Actor: by, // authz.EventActor(ctx): who caused it — required
        Payload: SourceRevised{Revision: rev},
    })
    return err
})

events.Subscribe("catalog.message.source_revised", "localization.mark_outdated",
    outbox.HandlerFunc(func(ctx context.Context, d outbox.Delivery) error {
        var e SourceRevised
        if err := d.Decode(&e); err != nil { return err } // already Permanent
        return uow.InTenantTx(ctx, markOutdated(d.EventID, e)) // ctx is tenant-scoped
    }))
```

- **Every event names its actor** (RFC 0006 §6.1): `person:<id>`,
  `token:<id>` (CI, the CLI, an MCP agent) or `system:<id>` (background
  work, from `authz.Background` or `authz.SystemEventActor(name)`).
  `Publish` refuses an event without one; nothing defaults it. A handler
  reads it as `d.Actor`, and events recorded before migration 0042 read
  `outbox.ActorUnknown`. `TestEveryEventTypeNamesItsActor` scans the
  code for every `outbox.Event` literal and fails on one that names no
  actor.
- **Every event type has an audit projection** (see *Audit*): a new
  `Event… = "<context>.…"` constant fails
  `TestEveryEventTypeHasAProjection` until `audit/domain.Projections`
  names it.
- **Delivery is at least once.** A handler must be idempotent on
  `d.EventID`, for example by recording processed IDs in its own
  transaction or by upserting.
- If the state change rolls back, the event was never published.
- Each subscriber is retried on its own. A subscriber that already
  succeeded isn't called again. Keep subscriber names stable, because
  they're stored with the event.
- A failing handler is retried in process, then rescheduled with
  exponential backoff. After `GLOSSA_OUTBOX_MAX_ATTEMPTS` deliveries the
  event is dead-lettered (`status = 'dead'`). Wrap an error in
  `outbox.Permanent` to dead-letter it immediately.
- Handlers run under the publisher's trace and the event's tenant.
  There's no ordering guarantee across dispatchers or retries. Within
  one claimed batch, each subscriber sees the events in claim order
  (a delivery that fails is retried after the rest).
- **A hot subscriber takes batches** (#89). `events.SubscribeBatch(name,
  h, types...)` registers an `outbox.BatchHandler`: the dispatcher hands
  `HandleBatch` a claimed batch's deliveries of one tenant at once, and
  it reports one error per delivery. Whatever it reports unhandled is
  delivered again through `HandleEvent`, alone, with the usual retries;
  a `Permanent` error dead-letters only its own delivery. When the whole
  batch fails (its transaction rolled back), report
  `outbox.BatchFailure(err)` for each, so a permanent error one delivery
  caused does not dead-letter the rest. Audit, Localization's message
  projection, Knowledge's TM derivation, the workflow runner and the
  auto-translate triggers are batch subscribers: a 500-item push or
  import costs them a few transactions, not 500. The dispatcher also
  settles a batch in one transaction.
- A handler has a tenant but no principal. To read (or write) another
  context through its application service — which checks permissions
  like any use case — act as a named background principal with exactly
  the permissions it needs:

  ```go
  bg, err := authz.Background(ctx, "knowledge.derive_tm", authz.TranslationsRead, authz.CatalogRead)
  ```

  Its actor is `system:<uuid>` (stable per name). It refuses a context
  that already carries a principal and never grants identity
  administration (`tenant.manage`, `members.manage`, `owners.manage`,
  `tokens.manage`).

## Identity

People, their credentials and sessions are global to the deployment; a
person belongs to their `individual` tenant (created at registration)
and to any number of `organization`s. Memberships and API tokens are
tenant-owned.

| Table | Scope | Why |
|---|---|---|
| `identity_people` | system | A person signs in before choosing a tenant and spans many. Tenant scope reads only the id, email and name of the current tenant's members. |
| `identity_sessions`, `identity_email_links`, `identity_totp`, `identity_passkeys`, `identity_webauthn_ceremonies`, `identity_login_attempts` | system | auth-go's state, all consulted before a tenant exists. Tokens and links are stored as SHA-256 hashes, TOTP secrets AES-256-GCM sealed. |
| `identity_members` | tenant | A person's roles and locales in one tenant. `glossa_system` may SELECT (validating `/v1/tenants/{tenant}`, listing a person's tenants, finding invitations). |
| `identity_api_tokens` | tenant | `glossa_system` may SELECT by hash and bump `last_used_at` (resolving a bearer token's tenant). |

**Sign-in** uses auth-go's domain services (`SessionService`,
`MagicLinkService`, `TOTPService`, `LockoutService`, the WebAuthn
adapter) over Glossa's Postgres adapters: magic link (15 min, creates the
account on first use, verifies the address), password + optional TOTP
(Argon2id, lockout after 5 failures for 15 min), passkeys (ceremony
state server-side, single-use). Registration writes the person, then the
individual tenant and owner membership in one tenant transaction; every
sign-in repairs a missing individual tenant and accepts open invitations
to the verified address. Password reset revokes all sessions.
`GET /v1/me/passkeys` lists the person's passkeys from every device
(name, created, last used; oldest first) and `DELETE
/v1/me/passkeys/{id}` removes one — scoped to the signed-in person in
the query itself, so another person's passkey is a `404`. Both work
while passkeys are not configured, so enrolled ones stay manageable.

**Without email** (`GLOSSA_MAIL_DRIVER=none`, the default; SMTP is
optional for a first install) the email flows are unavailable rather
than silently dropped: requesting or redeeming a magic link and
requesting or redeeming a password reset answer `404 email_disabled`.
Registration creates the account with its password, unverified, and
signing in works at once — password (+ TOTP) and passkeys, with no
verification step. `GET /v1/meta` tells clients which methods exist
(`sign_in_methods`: `passkey` when a relying party is configured,
`password`, `magic_link` only with email; `email_delivery`), and Studio
hides what isn't there. The trade-offs, accepted until SMTP is
configured:

- An address is a claim, not a proof. Anyone can register any address
  first, and its owner then can't register it or reset its password.
- Nobody can reset a forgotten password (there is no admin reset yet);
  enroll a passkey or TOTP and keep the password safe.
- Invitations wait: only a verified address accepts one (so a stranger
  who registers an invited address can't join the organization), and
  without email no address is verified. Configure SMTP before inviting
  people, or they join once it is and they sign in with a magic link.
- Registration answers `202` either way, but registering then signing in
  tells a caller whether someone else already holds the address.

Switching email on later changes nothing retroactively: unverified
accounts then need their address verified (a magic link) before
password sign-in works again. The log mailer (`log`) is for development
only and must be asked for.

**Roles** — `owner`, `admin`, `developer`, `translator`, `reviewer`; the
matrix is pinned by `TestRolePermissionMatrix`. Translators and
reviewers can be limited to canonical BCP 47 locales, which cover their
CLDR descendants (`de` covers `de-AT`). **Token scopes** — `read`,
`write`, `publish`, `admin`; every scope implies read, none grants
review or owner changes, and a token never exceeds its creator.
Every role and the `read` scope hold `knowledge.read`;
`knowledge.write` (curating the termbase, style guides and TM) belongs
to owners, admins, developers and `write` tokens. The AI permissions:
every role and the `read` scope hold `intelligence.read` (configuration
without keys, jobs, suggestions, disclosures, metrics);
`intelligence.manage` (providers, routing, prices, budget, consent,
sensitive namespaces, auto-translate, review routing) belongs to owners,
admins and `admin` tokens; `intelligence.translate` (request fills,
cancel jobs, accept, edit or reject suggestions) is **locale-scoped**
like `translations.write`: translators and reviewers hold it within
their locales; developers, admins, owners and `write` tokens hold it
for every locale. The import/export permissions: every role and the
`read` scope hold `integration.read` (see jobs and results, create
exports and download them); `integration.import` (import translations
from XLIFF, JSON and PO) is **locale-scoped** like `translations.write`;
`integration.manage` (create and revise messages from files, TMX and TBX
imports, overwrite mode) belongs to owners, admins, developers and
`write` tokens.

**API tokens** look like `glossa_api_` + 43 base64url characters.
Register `glossa_api_[A-Za-z0-9_-]{43}` with secret scanners. They're
shown once, stored as SHA-256, revocable, optionally expiring, and track
`last_used_at` (at most one write a minute). Publishable delivery keys
(`glossa_pk_…`) are Release's, below.

### auth-go follow-ups

Gaps found while integrating auth-go v0.7.2. Glossa doesn't work around
them beyond implementing auth-go's own ports; each belongs in auth-go:

1. **Global principals.** `User`, `Session` and `MagicLink` each belong to
   one `TenantID`. Glossa's people are global and pick a tenant per
   request, so every auth-go object carries a fixed realm (`glossa`).
   auth-go could make the tenant optional or call it a realm.
2. **Link purpose.** `MagicLink` has no purpose, so sign-in and password
   reset links are kept apart by giving each purpose its own repository.
   A purpose (or audience) on the link would do it in the domain.
3. **Pending TOTP enrollment.** `TOTPRepository` has one secret per user
   and no pending/confirmed state; Glossa tracks confirmation itself.
4. **Server-side WebAuthn ceremonies.** auth-go returns ceremony state to
   the caller and recommends server-side storage; a `CeremonyStore` port
   with single-use `Take` would make that the default.
5. **Discoverable-credential (usernameless) passkey sign-in.**
   `BeginLogin` needs a user ID, so passkey sign-in starts from an email,
   which tells a caller whether that address has passkeys.
6. **Product API tokens.** `WorkloadKeyService` fixes the token format
   (64 hex, no prefix, so no secret scanning), requires an expiry, is not
   tenant-bound, deletes on revoke (no audit trail) and has no last-used
   tracking. Glossa's tokens reuse auth-go's `NewToken` and `HashToken`
   and own the rest; a prefixed, tenant-bound key type in auth-go would
   let products share it.
7. **pgx.** `pgstore` is `database/sql` with its own schema and no RLS
   story; products on pgx + RLS (Glossa) implement every port themselves.
8. **Password policy.** No length/strength helper beyond the 1024-byte cap;
   Glossa enforces a 12-character minimum.

## Catalog

What the product says (RFC 0002 §4). A **project** (tenant-owned, slug,
fixed source locale, settings) has **applications** (web, api, ios,
android, other) and **messages**. A message has an immutable ID and a
key runtimes use (`checkout.pay`, unique per project); its source is
stored as the canonical MF2 data model plus the authored text and syntax
(ICU MF1 by default), parsed only by the `messageformat` kernel.
Arguments and markup are derived from the model on every change. Only a
model change is a new source revision, so pushing the same catalog
twice, in either syntax, changes nothing. Renaming is explicit and keeps
the ID and all history.

| Table | Scope | Why |
|---|---|---|
| `catalog_projects`, `catalog_applications`, `catalog_messages` | tenant | Ordinary state; the message row is the projection of its latest source revision. |
| `catalog_source_revisions` | tenant | Append-only by grant (`glossa_app`: SELECT, INSERT). History is the domain here (RFC 0002 §4). |
| `catalog_branches`, `catalog_proposals` | tenant | The branch overlay (RFC 0004 §4.1). `glossa_system` reads branch identity and closing time, proposal links and message states to find the proposal sweep's work (system scope `catalog.proposals`, migration 0016). |

Events: `catalog.project.{created,updated,deleted}`,
`catalog.application.{created,updated,deleted}`,
`catalog.message.{created,source_revised,updated,renamed,obsoleted,reactivated,activated,proposed}`,
`catalog.branch.{opened,pushed,closed,merged,reopened}`.
Every message event carries a `message` snapshot (`message_id`,
`project_id`, `key`, `namespace`, `state`, `source_revision`, `version`);
`source_revised` adds `old_revision` and `new_revision`, `renamed` adds
`old_key` and `new_key`. A consumer keeps the snapshot with the highest
`version`, which makes delivery order and duplicates irrelevant.

Permissions: reads need `catalog.read`, writes `catalog.write`
(developers, admins, owners, `write` tokens), deleting a project
`tenant.manage`. `POST …/message-upserts` is the CLI's push: up to 500
items in one transaction, per-item results, `base_revision` to refuse
overwriting a newer revision, obsolete keys reactivated. `GET
…/namespaces` lists a project's namespaces by name with their active
and obsolete message counts (what Studio's export offers to choose
from): one grouped read per page, served by the
`catalog_messages_namespaces` index (migration 0010).

**Branches** (RFC 0004 §4.1) are the overlay a feature branch puts on
the catalog. `POST …/branch-pushes` is CI's push (`glossa push
--branch`): the branch's whole catalog, up to 10000 messages in one
transaction, where a key the project doesn't have becomes a `proposed`
message the branch owns, changed source for a live key a **source
proposal** (never a source revision: the append-only log records merged
history only), and a key whose live source already says this is
`unchanged`. Two open branches proposing one new key with different
source are a `key_conflict` for both. A complete push withdraws what the
branch no longer has and reports the project's live keys it lacks; a
branch never obsoletes anything. The answer, like `GET
…/branches/{branch}`, is the **status report** the pull request check
reports: new keys, source proposals, removed keys, conflicts, and the
current translations per locale a merge would make outdated.

Merging is the **default branch's push** (`message-upserts`), which
activates proposed messages and turns proposals into revisions, so
correctness doesn't wait for a webhook; `…/closure` and `…/merge` only
end the branch and clean up. A closed or merged branch's proposed
messages stay for 14 days (`ProposalRetention`) before `SweepProposals`
obsoletes them with their translations and history intact; a reopened
branch, or a later push of the key, proposes them again. This is not
Context's retention: the builds and captures of that branch go after 7
days (RFC 0004 §2.3), while the text a translator worked on stays 14. The sweep runs
in glossa-server as the daily `catalog.proposals` job (`GLOSSA_PURGE_*`,
leased so one replica leads it); a system-scope query (migration 0016)
finds the tenants that have expired proposals, and the sweep itself runs
in each tenant's scope.

A branch name may hold `/`, and an encoded slash doesn't survive every
proxy, so **URLs address a branch by its ID**: `GET …/branches` lists
them (by name, filtered by `state`, or the one with an exact `name`),
`POST …/branches` upserts by name (what CI knows: head commit, pull
request, preview URL), and the ID-addressed operations are the status
report, `…/proposals`, `…/closure`, `…/merge` and `PUT …/preview`.

Branch tables: `catalog_branches` (one per project and name, with its
state, `closed_at` and the live keys its last complete push lacked) and
`catalog_proposals` (one row per branch and key, with the source it
pushed). Branch events: `catalog.branch.{opened,pushed,closed,merged,reopened}`;
`pushed` carries the counts and the other open branches its conflicts
touch, so their checks run again. Release opens, publishes and destroys
each open branch's preview environment on them.

Project settings: `default_syntax`, `review_required` and
`default_branch` — the repository's default branch (`main` unless set;
a Git connection will set it), validated as a Git branch name
(`invalid_branch`). Settings written without it keep the project's.
The Context context decides by it which uploads are of the default
branch. Settings live in `catalog_projects.settings` (jsonb); a project
saved before `default_branch` existed reads as `main`, and migration
0014 only bounds the stored value.

## Localization

What each locale says. **Locales** are canonical BCP 47 (no extensions
or private use, ≤ 35 characters) with a direction derived from the likely
script; the source locale is one of them from the project's creation.
The **fallback graph** is exactly the manifest's `fallback` object,
validated against the project's locales and refused if cyclic. A
**translation** (message × locale) is the projection of an append-only
**revision** log; every revision carries provenance (`origin`:
human, ai, agent, translation_memory, machine_translation, import,
adaptation; `origin_detail`; the principal) and the source revision it
was made against. `ai` is the platform translating on a person's behalf
and `agent` is an autonomous agent writing through MCP on a long-lived
token (RFC 0005 §7.3): a different act, so a different origin, and one
a query can select on. Outdated is derived — `source_revision <` the message's current
one — and never stored. Every write is checked with
`messageformat.CheckCompat` against the source revision it claims: error
findings reject it (`422 structural_qa_failed` with `findings`),
warnings (and `max-length-exceeded`) are stored and returned. Review
state (`draft`, `needs_review`, `approved`, `rejected`) is data: a
`ReviewFlow` of allowed transitions and reviewer-only states, plus the
project's `review_required` setting, for the workflow engine to replace.

| Table | Scope | Why |
|---|---|---|
| `localization_locales`, `localization_fallback_graphs` | tenant | Ordinary state keyed by Catalog's project ID (no cross-context foreign keys). |
| `localization_messages` | tenant | Localization's own projection of Catalog messages, fed by `catalog.message.*` (highest `version` wins), updated in the same transaction by Catalog's bulk upsert and refreshed synchronously on every translation write. It is what "outdated" and the `missing_in`/`outdated_in` filters read. |
| `localization_translations` | tenant | Projection of the latest revision. |
| `localization_translation_revisions` | tenant | Append-only by grant. |

Localization reads Catalog only through Catalog's application service
(`adapters/catalog`, the `SourceCatalog` port) and answers Catalog's
coverage filter through `adapters/coverage`; neither context touches
the other's tables. **The projection is synchronous for bulk
upserts:** `UpsertMessages` (`message-upserts`, `glossa push`, imports)
hands the messages it changed to Catalog's `MessageProjection` port,
which `adapters/projection` implements with Localization's
`ProjectMessages`. That runs Localization's own projection update —
highest version wins, outdated translations announced — on Localization's
store *inside Catalog's transaction*, joined through the unit of work
(`db.UnitOfWork.InCurrentTenantTx`, an explicit join rather than a
nested unit of work), so listings and fills see a push when it returns
and a failure rolls both back. The outbox subscriber stays as the
idempotent catch-up (and still carries every other message write,
about one poll later): it finds the projection at the event's version
and changes nothing, so an outdated event is published once
(`TestBulkUpsertProjectsSynchronously`). Subscribers (names are stored with events; never
rename them): `localization.track_message` on every
`catalog.message.*`, `localization.add_source_locale` on
`catalog.project.created`, `localization.drop_project` on
`catalog.project.deleted`. Events: `localization.locale.{added,removed}`,
`localization.fallback_graph.changed`,
`localization.translation.{revised,reviewed}`, and
`localization.translation.outdated` — once per translation a source
revision leaves behind, the trigger for re-translation (M2) and review
routing.

**Bulk reads.** `GET …/projects/{project}/translations?locale=de&locale=fr`
lists translations across messages for 1–20 locales, ordered by
message key, message ID and locale, each with the message's `key`,
`namespace` and `message_state`, its `source_revision` and the derived
`outdated`; filters: `state` (repeatable), `outdated`, `namespace`,
`key_prefix`, `message_state`. It is one query per page: keyset
pagination on `(key, message_id, locale)`, driven by the
`localization_messages_project_order` index (0006; `(project_id, key,
message_id) INCLUDE (namespace, state, source_revision)`) and the
translations' `UNIQUE (message_id, locale)`, so a page costs the same
at any depth. Translations of removed locales are not listed.
`GET …/projects/{project}/translation-stats` summarizes every locale
in one query: active messages, `translated` (a usable — not rejected —
translation), `missing` (none or rejected), `outdated` (usable, older
source revision) and translations of active messages per review state;
the source locale counts as fully translated and approved. It is an
aggregate computed per request rather than counters kept on every
write: one source revision changes every locale's outdated count, and a
single grouped pass over the project's rows (about 0.1 s for 100 000
translations) is correct by construction.

Permissions: reads need `translations.read`; locales and the fallback
graph `catalog.write`; writing a translation `translations.write` for
its locale (translators and reviewers are limited to their locale scope,
which covers CLDR descendants); approving or rejecting
`translations.review` for the locale. No token scope grants review, so
with `review_required` a token's writes wait for a human.

One narrow path does not ask for `translations.read`:
`Service.CheckPolicyLocaleCodes`, reached only through
`coverage.PolicyLocales`, which Catalog validates a check policy's
`require_complete` against while saving it. The caller has already
proved `catalog.write` on the project, and that a locale it named
exists is that write's own invariant — not a read of Localization, and
it discloses nothing the caller could not learn from the
`invalid_check_policy` it would get back. It is what lets CI import a
policy that names locales with the CI ceiling (`catalog.read` and
`catalog.write`). It returns locale codes and nothing else, it runs in
tenant scope like every other read, and it is not a general way around
`translations.read`: everything a caller reads still goes through
`ListLocales`.

### What Release reads

A release is built from two application reads, each one tenant
transaction, joined by message ID:

- `catalogapp.Service.ReleaseSource(ctx, project)` → the project (source
  locale) and every **active** message in key order: key, namespace,
  current source content (the source locale's artifact).
- `localizationapp.Service.ReleaseTranslations(ctx, project, states)` →
  the locales with direction (source first), the fallback graph as the
  manifest's `fallback`, and per locale the translations in the eligible
  review states (production: `approved`), each with its canonical model,
  `source_revision` and derived outdated flag.

Release keeps translations of active messages in the project's locales
and writes one artifact per locale and namespace (runtimes/SPEC.md §1).

## Knowledge

The organization's linguistic knowledge (RFC 0003 §2, intent §19):
translation memory, the termbase and style guides — separate
aggregates, never one "AI context" blob. Everything is tenant-owned and
either tenant-wide or scoped to one project.

| Table | Scope | Why |
|---|---|---|
| `knowledge_tm_units` | tenant | TM units, active and retired (history); at most one active unit per translation. |
| `knowledge_tm_derivations` | tenant | The translation revision each translation's units reflect: the idempotency guard of derivation. |
| `knowledge_concepts`, `knowledge_terms` | tenant | The termbase; terms are replaced with their concept. |
| `knowledge_concept_revisions` | tenant | Full snapshots per version, outliving the concept. SELECT, INSERT (DELETE only to erase a deleted project). |
| `knowledge_style_guides` | tenant | One guide per scope (`NULLS NOT DISTINCT` unique index). |
| `knowledge_style_guide_versions` | tenant | Full snapshots per version, outliving the guide; same grants as concept revisions. |

**pg_trgm.** Migration 0007 runs `CREATE EXTENSION IF NOT EXISTS
pg_trgm` for fuzzy matching and substring search (GIN trigram indexes).
It ships with PostgreSQL's contrib modules — in the official images the
chart and the tests use (`postgres:16-alpine`) — and is a trusted
extension, so the database owner that runs `-migrate=only` creates it
without superuser rights. A cluster
built without contrib must provide it first.

**Translation memory.** Units are derived, not curated — or imported
from TMX (origin `import`, `ImportTMUnits`, run by Integration's jobs;
an exact duplicate in its scope is not added again):
`knowledge.derive_tm` subscribes to `localization.translation.revised`
and `.reviewed`, reads the translation's *current* state through
Localization's `TranslationWithSource` (as the background principal
`knowledge.derive_tm` with `translations.read` and `catalog.read`) and
reconciles (`domain.Reconcile`): an approval creates a unit or keeps the
one with the same text; other approved text supersedes it; losing the
approval retires it (`unapproved`), unapproved new text too
(`overwritten`). Units are retired, never deleted. Under a row lock, a
state no newer than the revision already applied changes nothing, so
duplicates and reordering converge (`TestDerivationIsIdempotentAndOrderIndependent`
replays every event newest first).

A unit stores both sides as canonical MF2, the target's data model, and
the **normalized** source: pattern text with placeholders by position
(`{$amount}` → `{1}`, `.local`s resolved to their argument), markup as
tags (`<b>`, `</b>`, `<br/>`), whitespace collapsed, NFC; a select
message becomes `.match {1}` plus one `keys {pattern}` line per variant.
The **signature** lists the placeholders' types (`1:number/plural`).
Lookups (`LookupTM`, `POST …/tm-lookups`) score:

- **101** — exact, approved for the same message key in the same
  namespace of the same project (the context match);
- **100** — same normalized text (SHA-256) and signature;
- **50–99** — `pg_trgm` similarity of the normalized text, floored to
  a percentage and capped at 99 (identical text with other placeholder
  types scores 99). `min_score` sets the similarity threshold of the
  `%` operator, so the trigram index does the filtering.

A lookup sees tenant-wide units and the query's project's
(`all_projects` widens it to the tenant); ties rank the own project
first, then the most recently confirmed unit, and each target appears
once. Targets come back with their variables renamed to the query's
(`domain.AdaptVariables`), so `Pay {$total}` reuses `{$amount} zahlen`
as `{$total} zahlen` — in MF2 (`target`) and in the syntax asked for
(`target_text` in `target_syntax`; by default the query's own syntax,
so an MF1 query reads `{total, number} zahlen`). MF1 is written by
`mfcontent.RenderMF1`, the inverse of the MF1 conversion, and proven by
parsing it back to the same model; what MF1 can't express (markup,
MF2-only options) comes back as MF2 with `target_syntax_fallback`. `TestFuzzyMatchingQuality` pins quality on a
seeded 40-unit en→de memory: 30 near-misses (rewording, plurals,
renamed variables) find the intended unit first 30/30 times, and 0 of 8
unrelated messages match at all. Concordance (`GET …/tm-concordance`)
is an `ILIKE` substring search over either normalized side, ranked by
`word_similarity`. Semantic (vector) matching is a later port (needs
pgvector).

**Termbase.** A concept (definition, domain, note, product reference)
owns its terms: locale, text, status (`preferred`, `admitted`,
`deprecated`, `forbidden`), part of speech, case sensitivity, note.
Replacing it keeps the IDs of terms that stay; every version is a
snapshot. A term applies to its locale and that locale's descendants.
**Recognition** (`domain.Termbase.Recognize`) is deterministic and
dictionary-free: words are runs of letters, marks and digits (hyphens,
apostrophes and script changes separate them); case is folded per rune
(Turkish/Azerbaijani i) unless the term is case-sensitive; a term
matches whole words from a word start, each word allowing a short
inflectional ending (≤ 3 letters from 5-letter words, ≤ 2 for 4-letter
words, none below; Hangul ≤ 3 syllables from 2) — so *workspaces*,
*Rechnungen*, *cartes bancaires* match, but not *Konten* or compounds.
Terms in Han, Hiragana, Katakana, Thai, Lao, Khmer, Myanmar or Tibetan
script match as **substrings**, because those texts have no spaces; a
short term can then match inside a longer word (会議 in 会議室).
Overlaps resolve leftmost-longest; homonyms on one span are all kept.
**Terminology QA** (`domain.CheckTerminology`): `term_missing`
(warning) when a concept found in the source has none of its preferred
or admitted target terms in the translation; `term_forbidden` (error;
warning for deprecated terms) for each forbidden or deprecated target
term — unless the same words are an allowed term of a concept the
source mentions. Messages are checked as `domain.VisibleText`
(placeholders become U+FFFC, so `{$workspace}` is not a word). The API
reports code point offsets. **Project checks**
(`CheckProjectTerminology`, `GET …/projects/{project}/terminology-findings`)
run the same QA server-side over a project's translations in up to 20
locales, each against its message's current source: a page scans
`page_size` translations of active messages in key order (default every
review state but `rejected`) and lists those with findings by message
key, with `checked` counts per locale. A page costs one read per
context — the termbase, Localization's bulk listing and Catalog's
`MessagesByIDs` for the page's sources (the `ProjectTranslations` port)
— never one per translation.

**Style guides** are structured: formality (register + pronoun), tone
tags, punctuation (quotes, dash, spaces before units and punctuation,
serial comma, ellipsis), number and date conventions, and rules with
rationale and good/bad examples. A scope is any combination of project,
locale and namespace (a namespace needs a project). The **effective**
style (`EffectiveStyle`, `GET …/effective-style-guide`) merges the
applicable guides leaf by leaf, the narrowest last — namespace beats
locale, a deeper locale a shallower one, locale beats project, project
beats tenant — a narrower rule replaces a broader one with the same
`id` or disables it, and `sources` names each guide version used, for
provenance.

**The Intelligence read port** (`app.Reader`, implemented by
`*app.Service`), free of HTTP types; each call is authorized with
`knowledge.read` in the tenant on `ctx` (a job acts through
`authz.Background(ctx, name, authz.KnowledgeRead, …)`):

```go
type Reader interface {
	LookupTM(ctx context.Context, q TMQuery) ([]TMMatch, error)                         // tm_lookup
	RecognizeTerms(ctx context.Context, q TermQuery) ([]RecognizedTerm, error)          // term_lookup
	CheckTerminology(ctx context.Context, c TermCheck) ([]domain.TermFinding, error)    // validate
	EffectiveStyle(ctx context.Context, q StyleQuery) (domain.EffectiveStyle, error)   // style_rules
}
```

Events: `knowledge.concept.{created,updated,deleted}`,
`knowledge.style_guide.{created,updated,deleted}` (IDs, scope, version,
actor; TM units are derived state and publish none). Subscribers:
`knowledge.derive_tm` (above), `knowledge.drop_project` on
`catalog.project.deleted` (erases the project's units, concepts, guides
and their history). Permissions: reads, lookups, recognition and checks
need `knowledge.read`; creating, replacing, deleting and retiring
`knowledge.write`.

## Intelligence

AI translation that amplifies accumulated knowledge (RFC 0003 §3–§4,
intent §54). The library — the translation agent (`app.Translator`: gather
→ draft → validate → repair ≤ 2 → assess), the provider port and
adapters, confidence and review routing, prompts and evals — is wired
into the platform here.

| Table | Scope | Why |
|---|---|---|
| `intelligence_providers` | tenant | BYO providers: kind (`anthropic`, `openai_compatible`, `gemini`), base URL, model allow-list, enabled, the API key **sealed** (`kernel/sealing`, AES-256-GCM bound to tenant and row, key derived from `GLOSSA_AUTH_SECRET`). The key is write-only: the API says `api_key_set`, never the key. |
| `intelligence_settings` | tenant | Consent (`provider_consent`, off by default, who and when), `max_concurrent_jobs`, `monthly_budget_micro_usd` (0: no provider calls), price overrides. `glossa_system` may SELECT (the claim's cap). |
| `intelligence_project_settings` | tenant | Namespace tags (`sensitive`, `legal`, `marketing`), auto-translate locales (none by default), review routing. |
| `intelligence_routing_policies` | tenant | The tenant's (`project_id` NULL) and projects' routing; none stored means `DefaultRouting()`. |
| `intelligence_spend` | tenant | Every priced call, append-only: the budget is this UTC month's sum; nothing resets. Outlives projects. |
| `intelligence_fills` | tenant | Explicit and locale-added batch requests. |
| `intelligence_jobs` | tenant | The queue, with each job's audit ledger. `glossa_system` may SELECT and UPDATE (claims). |
| `intelligence_suggestions` | tenant | Results with provenance, score, explanation, action, risk tags, cost and the decision. |
| `intelligence_disclosures` | tenant | Which provider and model saw which message, and exactly what was sent. Append-only. |

**Settings documents** — the tenant's settings (and prices), the
tenant's and each project's routing policy, a project's settings —
exist with defaults before anyone saves them, at version 0: their
`ETag` is `"0"` (a project's routing policy answers `"0"` while it
inherits the tenant's), and `If-Match: "0"` writes one only while it is
still unsaved. A write that loses the race to create or change it
fails the precondition (`412`), never silently overwrites
(`TestWiringConcurrentCreatesOfSingletons`).

**Jobs.** A job is one message × locale, unique per `(message, locale,
source revision, knowledge fingerprint)` — the fingerprint digests the
prompt versions, the effective style guides' versions and the pair's
termbase (every concept in scope with a term in either locale, with its
version); translation memory is left out on purpose (it changes with
every approval). Triggers are idempotent outbox subscribers acting as
the background principal `intelligence.auto_translate` (catalog,
translations and knowledge read only): `catalog.message.created` and
`localization.translation.outdated` queue jobs for the locales with
auto-translate on, `localization.locale.added` fills a new locale with
auto-translate on (the fill's ID is the event's), and `POST
…/projects/{project}/ai-fills` queues explicit fills: the messages
whose translation is in the state `select` names (`missing`, the
default; `outdated`; `missing_or_outdated`), among listed keys or all of
them, so a client re-translates outdated text without listing keys
(failed, dead or cancelled jobs are queued again). A duplicate event finds the existing job.

**Previews.** `POST …/projects/{project}/ai-fill-previews`
(`PreviewFill`, `glossa translate --dry-run`) takes a fill request and
answers what the fill would do, writing nothing (no fill, job, TM hit
count, spend or event; `TestWiringFillPreviewWritesNothing` counts the
rows). Per locale it lists the keys, then decides each message the way
its job would: an existing job is reused (`existing`, one query per page
of 100), an exact TM match covers it (`tm_exact`, an exact-only lookup
that isn't counted as a hit), or it would call a provider (`provider`)
unless refused — `sensitive`, `provider_consent`, `no_route` (no route
to an enabled provider allowing the model), `budget_exceeded` (this
month's spend plus the calls before it leave no room for the call's
upper bound). `cost` prices the provider calls with the effective price
table: `estimated_micro_usd` (a draft and a self-assessment, prompts at
about three characters a token, drafts twice their source) and
`max_micro_usd` (what the budget guard reserves: every repair, the whole
`max_tokens`), with `unpriced` for models without a price.

Workers run in glossa-server (`GLOSSA_AI_*`). A claim is one statement in
the system scope `intelligence.jobs`, serialized by an advisory lock and
taken `FOR UPDATE SKIP LOCKED`: the next due job (queued, or running
with an expired lease) of a tenant under its `max_concurrent_jobs`, so
the cap holds across replicas. Each provider (tenant × configuration
version) is fortify-wrapped — timeout, retry with backoff, a circuit
breaker — and capped by an in-process bulkhead
(`GLOSSA_AI_PROVIDER_CONCURRENCY`). The job runs in its tenant's scope as
`intelligence.worker` (catalog, translations and knowledge read,
releases read, translations write and review) and ends:

| State | When |
|---|---|
| `succeeded` | A suggestion was stored (and routed). |
| `skipped` | `superseded` (the source moved on), `up_to_date` (translated meanwhile), `message_gone`, `locale_gone`. |
| `failed` | For good, with a reason: `provider_consent`, `sensitive`, `invalid_output`, `budget_exceeded`, `no_route`, `provider_error`, `invalid_source`. |
| `queued` again | A transient failure (`app.Transient`: rate limit, overload, outage): backoff 30 s doubling to 30 min, on the database's clock. |
| `dead` | Transient failures exhausted `max_attempts` (5), or its workers kept dying. |
| `cancelled` | By `POST …/ai-jobs/{job}/cancellation` or a fill's, while queued. |

Settlement is fenced by the claim token, so a worker whose lease ran out
changes nothing. Disclosures are stored before settlement, even for a
failed job: a provider that saw the text is recorded.

**Privacy (RFC 0003 §7).** Without `provider_consent` the agent never
calls a provider: an exact translation-memory match (100/101, clean
terminology, every plural category the target needs) is still reused,
anything else fails `provider_consent` with a reason that names the
setting, and a fill says so up front (`warnings: provider_consent_off`).
A namespace tagged `sensitive` is never queued by triggers, skipped by
fills (`skipped.sensitive`), refused by the worker if it was tagged after
queueing, and refused again by the agent's own tool guard. Base URLs are
https and never private or loopback addresses unless the deployment
allows them, checked on the resolved address when the client dials.

**Budgets.** `monthly_budget_micro_usd` caps provider spend per UTC
month; 0 (the default) allows none. Before each call the router asks the
guard whether this month's spend plus the call's upper-bound estimate
(input at the input price, the whole `max_tokens` at the output price)
fits, and refuses it otherwise (`budget_exceeded`, never retried, never
another route); after the call it books the actual cost. Calls already
in flight when the cap is reached finish, so a month can end at most
their cost over the cap. Prices are the deployment's defaults with the
tenant's overrides (`ai-prices`); a configured `anthropic`-kind provider
under another name gets the Anthropic prices under its name.

**Suggestions and review routing.** A suggestion stores the canonical
MF2, findings, provenance, score and explanation, and its action by the
project's review policy: `approve_recommended` and `review_required`
wait in the review queue (`GET …/ai-review-queue`, lowest score first,
then most risk tags: legal and marketing namespaces, forbidden terms,
max length, missing plural categories); a newer suggestion for the same
message and locale supersedes a pending one. Every suggestion the API
returns (list, get, queue, accept, reject) carries its message's
current `source` — key, namespace, state, authored text and syntax,
canonical MF2 and revision — read through Catalog's `MessagesByIDs` in
one query per project on the page (`SuggestionSources`), so reviewers
need no request per item. **`auto_approve`** is off by
default and is accepted only with `auto_approve_environments` that all
exist and ship `approved` (Release's eligibility policies —
`GetEnvironment` as `releases.read`); it is checked again for every
suggestion, and when an environment stopped shipping approved text the
suggestion is routed `approve_recommended` with an `action_note`. An
auto-approved suggestion is written as an approved revision by the
worker. Accepting (`…/acceptance`, optionally with an edit) writes the
revision through Localization as the caller (`intelligence.translate`
and `translations.write` for the locale; `approved` when the caller may
review, else the project's policy) with origin `ai` or
`translation_memory` and an `origin_detail` of provider, model, prompt
version, TM units, terms, style version, score, explanation, suggestion
and job. An edit records its structured diff: character edit distance
and ratio, terms added and removed, style fields whose use changed.
Rejecting writes nothing.

**Message context (RFC 0004 §2.2, §8).** The agent's `message_context`
tool gives the description, max length, where the message is used —
`file:line (component, route)`, at most 10, the default branch's first,
without repeats — and up to 5 neighbours with their translations (a
rejected one left out): first the messages shown together with it,
sharing a route or a capture in the default branch's current builds
(most shared first, active only), then those sharing its key prefix.
Usages and co-located messages come from the Context context through the
`UsageContext` port (`sources.Usages`, over `contextapp.Service`, as the
worker's principal with `catalog.read`); without it (`Deps.Usages` nil)
usages are empty and neighbours come from the key prefix alone. The
tool's output shape is unchanged, so prompts and eval cassettes are too.

**Plural categories (policy).** A draft lacking a CLDR plural category
the *target* locale needs (Polish `few`/`many`) goes back to the model
for repair like a structural error, even though `CheckCompat` calls it
a warning. If it is still missing after the two repairs, the suggestion
is kept, scored low (factor `missing_plural_categories` naming them) and
forced to `review_required` under every policy.

**Metrics.** Prometheus: `glossa_intelligence_jobs_total{trigger,state,code}`,
`glossa_intelligence_job_duration_seconds`,
`glossa_intelligence_cost_micro_usd_total{tenant,provider,model}`,
`glossa_intelligence_suggestions_total{locale,origin,action}`,
`glossa_intelligence_suggestion_confidence`,
`glossa_intelligence_suggestion_decisions_total{locale,status}`,
`glossa_intelligence_edit_ratio{locale}`, `glossa_intelligence_queue_jobs{state}`.
`GET …/projects/{project}/ai-metrics` returns the acceptance rate and
edit distance per locale (intent §70); `GET …/ai-eval-baseline` serves
the committed `evals/testdata/baseline.json`.

Subscribers: `intelligence.auto_translate` (the three triggers),
`intelligence.drop_project` on `catalog.project.deleted` (erases the
project's jobs, fills, suggestions, disclosures, settings and routing;
spend stays). Permissions: see *Identity* (`intelligence.read`,
`intelligence.manage`, `intelligence.translate`).

**Tests** replay provider cassettes, never real providers: the evals
(`evals/testdata/cassettes`) and the wiring's integration tests
(`app/testdata/wiring`, re-recorded from each test's scripted answers
with `go test -tags=integration ./internal/intelligence/app -run
TestWiring -record-wiring`).

## Integration

Interchange files in and out as asynchronous jobs (RFC 0003 §5–§6,
intent §48): the pure converters in `formats` (XLIFF 2.1, flat and
nested JSON, gettext PO read-only, TMX 1.4b, TBX-Basic), run by workers
in glossa-server against Catalog, Localization and Knowledge through
their application services.

| Table | Scope | Why |
|---|---|---|
| `integration_jobs` | tenant | Imports and exports: format, mode, options, the requester's access snapshot, state and progress, the file's object key, size and SHA-256, the fingerprint, summary counts, retention. `glossa_system` may SELECT and UPDATE (claims, the retention sweep). |
| `integration_job_items` | tenant | An import's result per item, in file order, with where the item is in the file (`line`, `col`, `ref`; migration 0011). |

**Files** live in object storage under
`integration/v1/<tenant>/jobs/<job>/…` (glossa-edge never serves that
prefix). An upload (`PUT …/import-jobs/{id}/file`, the requester only,
once) is streamed to storage while it is hashed and counted against
`GLOSSA_INTEGRATION_MAX_UPLOAD_BYTES` — that route takes no API body
limit and gets `GLOSSA_INTEGRATION_UPLOAD_TIMEOUT` as its deadline —,
and `objectstore.Streamer` writes it whole or not at all (a temp file
renamed into place; an S3 multipart upload aborted on failure). An
export is generated into a temporary file and streamed up; its download
(`GET …/export-jobs/{id}/file`) streams it back with its name and
SHA-256 (`ETag`). Retention deletes both after `expires_at`
(`GLOSSA_INTEGRATION_RETENTION`, 7 days): `integration.jobs`' sweep
lists expired files across tenants, deletes them and records
`files_deleted_at` (a download is then `410 file_expired`); imports
still waiting for their file after 24 hours fail `upload_expired`.

**Jobs.** `awaiting_upload` (imports) → `queued` → `running` →
`succeeded` | `failed` | `cancelled`. A claim is one statement in the
system scope `integration.jobs`, serialized by an advisory lock, `FOR
UPDATE SKIP LOCKED`, at most two running jobs per tenant across
replicas; new and queued jobs are due on the database's clock. The job
runs in its tenant as `integration.worker` (catalog, translations and
knowledge read and write, translations review) **within the access its
requester had when they asked**, snapshotted on the job
(`authz.ScopeOf`): the locales they may import (`integration.import` ∩
`translations.write`), the locales they may review, and whether they
may manage (`integration.manage` with `catalog.write`, or with
`knowledge.write` for TMX and TBX). An import applies its file in
batches of 500 entries, units or concepts; after each batch it stores
the batch's results and its progress under the claim token and checks
for a cancellation. A storage or database failure is retried (3
attempts, 30 s backoff doubling), resuming after the last checkpoint; a
malformed, unsupported or oversized file fails the job at once with the
problem as its last result (`line`, `column`, the item); a worker whose
lease ran out changes nothing. **Every result carries its position**:
the readers record where each entry, translation, unit and concept is
(`formats.Position`) — line and column of its start and `ref`, the item
in the format's own terms: an XLIFF 2 fragment identifier
(`#/f=checkout/u=pay`), a JSON pointer (`/checkout/pay`), a PO entry's
`msgctxt "…" msgid "…"`, `tu[n]` / `conceptEntry[n]` for TMX and TBX —
so a conflict deep in a file is found as easily as the problem that
fails it. Events: `integration.import.completed`
and `integration.export.completed` (job, project, kind, format, mode,
state, failure code, reused job, summary, requester) on every end;
subscriber `integration.drop_project` on `catalog.project.deleted`
deletes the project's jobs, results and files.

**Import rules.**

- *Locales.* A file's source locale must be the project's
  (`source_locale_mismatch`). Its translations' locale — XLIFF's
  `trgLang`, a PO file's `Language`, or `options.locale`, which names
  the locale of an XLIFF file without `trgLang` or imports one as
  another locale than it names (`de` into a project's `de-AT`) — must
  be one of the project's target locales: `options.locale` is checked
  when the job is created (`locale_not_found`), the file's own locale
  when it runs (the job fails `target_locale_mismatch`, naming the
  project's locales, instead of reporting every translation invalid).
- *Catalogs.* A message with source text in the file is created when
  missing (with `integration.manage`; otherwise its translations are
  `message_not_found`), `unchanged` when its source has the same MF2
  model, revised in `overwrite` mode and a `conflict` (`source_differs`)
  otherwise — a translator's XLIFF never rewrites the source it was
  exported with, and its translations of such a message are conflicts
  too (they were made for other text). Created messages take the file's
  namespace, description and max length; revisions change the source
  only. Translations go through Localization's bulk import with
  provenance `import` and `origin_detail` `{job, file, format,
  requested_by}`, written by `integration.worker`.
- *Merge never lowers an approval.* Under the translation's row lock
  (`localizationapp.ImportOptions.KeepApproved`): other text for an
  approved translation is a `conflict`
  (`approved_translation_conflict`), the same text in a lower state is
  `unchanged`. This protects every approval — a reviewer's, or an
  auto-approval policy a manager set — not only text a person typed.
- *Review states are capped* (`domain.RequestedState`): a file's
  approval (XLIFF `final`, PO without `fuzzy`) or rejection is kept only
  for someone who may review the locale, or — approvals — in a project
  that doesn't require review; otherwise the translation waits in
  `needs_review`. New text can't be imported as rejected
  (`write_cannot_reject`).
- *gettext keys* (`domain.POMessageKey`): `[<msgctxt slug>.]<msgid
  slug>_<hash>` — the text folded to lowercase ASCII words joined by `_`
  (accents dropped, other scripts left out, at most 40 characters at a
  word boundary) plus the first 8 hex digits of SHA-256 over msgctxt,
  U+0004 and msgid. The same entry always gets the same key, equal texts
  in different contexts different ones; `options.namespace` is every
  entry's namespace. Plurals become MF2 selects on `$count` with the
  target locale's CLDR categories.
- *TMX* adds units with origin `import` to a project or tenant-wide; a
  unit whose exact text is already active in the scope is `unchanged`,
  so TM imports only ever add (in either mode). *TBX* concepts are stored
  under their own ID when it is a UUID the tenant already has (a Glossa
  export coming back), else under one derived from the tenant, the
  scope and the file's ID (UUIDv5), so re-imports find them; identical
  content is `unchanged`, other content a `conflict`
  (`concept_differs`) unless `overwrite`. One definition and one note are
  kept per concept and term; the rest (other-language definitions, a
  term's context) joins the note. Case sensitivity, which TBX can't
  carry, is kept from the stored terms.
- *Dry runs* run every check a merge runs and store only the results:
  Localization's and Knowledge's writes (structural QA, the approval
  rule, concept and unit rules) run in transactions that are rolled
  back; Catalog's key, namespace and source rules are checked without
  writing, and the translations of messages that would be created are
  predicted `created`.
- *Idempotency.* `Idempotency-Key` on create, as everywhere. And an
  upload whose fingerprint (file SHA-256, project, format, mode,
  options) matches an earlier import that succeeded and hasn't expired
  succeeds at once with that job's summary and results
  (`reused_job_id`) instead of being applied twice; dry runs always run.

**Exports.** Catalogs read Catalog's `ReleaseSource` and Localization's
`ReleaseTranslations` (the chosen review states, default `approved`),
filtered by namespace: XLIFF one document per target locale (the source
alone without locales), JSON one catalog per locale — byte for byte what
`glossa pull` writes (sorted keys, two-space indent, no HTML escaping,
trailing newline); several locales are zipped as `<locale>.<ext>`. A
catalog the format can't express (MF2-only messages in an MF1 JSON
file, an XLIFF file without messages) fails `not_representable`. TMX
and TBX export a project's own units and concepts, or everything the
tenant holds without a project; TMX units carry the message key they
were approved for as `x-glossa-message-key`.

**The workspace's memory and termbase** have routes of their own, not
under a project: `POST|GET …/tm-import-jobs`, `…/tm-export-jobs`,
`…/termbase-import-jobs` and `…/termbase-export-jobs` create and list
tenant-wide TMX and TBX jobs (`CreateKnowledgeImport`,
`CreateKnowledgeExport`; the listing is `JobFilter{TenantWide, Kind}`).
Imports need `integration.manage` and `knowledge.write` held
tenant-wide, exports `integration.read` and `knowledge.read`. The jobs
are ordinary ones: uploaded to, followed, cancelled and downloaded
through `import-jobs` and `export-jobs`.

**GitHub** (RFC 0004 §6) is Integration's other half: the Glossa GitHub
App, the workspaces that claim its installations, the repositories those
feed, and the deliveries GitHub sends. It exists only where the
deployment configures an App (`GLOSSA_GITHUB_APP_ID` and its
companions, read by `adapters/github`); with none, `newGitHub` returns
nil, no worker and no sweep start, and every endpoint answers
`github_not_configured` (503). Nothing else in the server changes.

| Table | Scope | Why |
|---|---|---|
| `integration_github_installations` | tenant | An installation GitHub made and the workspace that claimed it: GitHub's id, the account, `active`/`suspended`/`revoked`. The unique index on GitHub's id is global, so one installation maps to exactly one tenant; `glossa_system` may SELECT `(tenant_id, installation_id, state)` to resolve a delivery. |
| `integration_github_install_intents` | tenant | A started installation: the person, an expiry, and the SHA-256 of the single-use state (never the state). `glossa_system` may SELECT `(expires_at)` and DELETE, for the sweep. |
| `integration_git_connections` | tenant | Installation × repository (by numeric id) × project × application, with the default branch and an optional monorepo path. Unique on `(repository_id, path)`: one repository feeds several projects, one per path. |
| `integration_github_deliveries` | tenant, after it resolves | The webhook inbox, keyed by `X-GitHub-Delivery`. `tenant_id` is NULL until the worker resolves the installation, so the endpoint can write it before any tenant is known; `glossa_system` reaches the whole table (scope `integration.github`). |
| `integration_github_checks` | tenant, and `glossa_system` for the queue | One row per pull request Glossa reports on: the queue the check worker claims, the id of its one sticky comment, and `runs` — per Git connection, the check run on the current head SHA and the annotations already appended to it. Unique on `(repository_id, pull_request)`, which is what makes one job per pull request. |

**Installing.** `POST …/github/install-intents` issues a state bound to
the tenant, the person and fifteen minutes, and the App's install URL.
On the callback (`POST …/github/installations`) the state is verified
and burned — single-use, so a failed attempt starts over rather than
retrying — and GitHub's one-time `code` is redeemed for the person's own
user token, which is used once to check that they can see the
`installation_id` they claim and then dropped. That is what stops
someone claiming an account they have nothing to do with. A second
workspace's claim is `installation_already_claimed`, which never says
whose it is. A connection's repository must be one the installation can
see and its application one the project has, both checked before the
transaction opens (Catalog opens its own, and nesting one is refused).

**Webhooks.** `POST /v1/integrations/github/webhooks` carries no
session: GitHub signs each delivery, and `X-Hub-Signature-256` is
checked with HMAC-SHA256, in constant time, over the raw body **before
anything parses it**, with the body capped at 5 MB while it is read.
A verified delivery is written to the inbox and answered `202` at once;
a delivery id already there is a no-op and also `202`, which is the
replay protection. A signature that does not verify is `401` with no
detail. The worker claims rows `FOR UPDATE SKIP LOCKED`, resolves the
installation to its tenant and runs idempotent handlers there as
`integration.github`: `installation` moves the installation's state,
`installation_repositories.removed` drops the connections of
repositories that left, and `pull_request` upserts, closes or merges the
Catalog branch of every project the repository feeds — through Catalog's
Branches API, never around it. **Correctness never depends on a
delivery**: a merge lands through the default branch's push (§4.1), so a
webhook that never arrives costs a stale branch view and nothing else,
which a test asserts by making Catalog refuse everything. Delivery ids
are kept seven days and then swept (`integration.github.sweep`, one
leased run a day); payloads are dropped as soon as a delivery settles.

**The PR check** (§6.4). A `pull_request` that opened, was pushed to or
reopened writes a check row and nothing else — GitHub's ten seconds are
not the place for a call back to GitHub. The check worker claims that
row (`FOR UPDATE SKIP LOCKED`, leased), creates the **Glossa** check run
as `queued` for the head SHA, and completes it once that commit's
`glossa push`, its usages build **and its `glossa check` run** have been
ingested — the run only in a project known to record them, one that has
recorded a reported run of any commit before (RFC 0005 §14 decision 11).
A project that never has is not waited for: it reports its labelled
reduced view as soon as the push and the usages are in, and a run that
lands later re-renders it. Readiness is **derived, never remembered**: the branch's head
commit says the push landed, a current build on that commit says the
usages did, and a recorded check run of that commit says the check ran,
so an event that arrives twice, late or never changes nothing. If none
of it arrives within 30 minutes the check completes `neutral` with "no
Glossa CI run for this commit"; if the push and the usages arrived but
no run did, the check reports Glossa's own **reduced view** and says so
(below). A leased scheduler job (`integration.github.check_timeout`)
makes the waiting rows due again and the worker — the one place that
completes a check — concludes.

**The report is the run CI recorded** (RFC 0005 §12.3). `glossa check`
(and `glossa capture --check`, the same check with the visual layer)
records what it found through `createCheckRun`; the check finds that run
**by the commit**, not by the ref — a branch moves, a verdict belongs to
the commit it graded — and renders it: conclusion, per-layer counts,
annotations on the located findings, the waived listed apart, the policy
version. One computation, two presentations, so the terminal and the
pull request cannot disagree by construction. Only a run somebody
*reported* counts (`cli`, `pull_request`, `api`): the capture upload and
the write-time job record partial runs of the same commit, and rendering
one would put a pull request on half a verdict. Quality is read through
`app.CheckSources.RecordedRun`, never out of its tables.

Where no run was recorded, the check falls back to what Integration can
see for itself — new keys, the messages the last push could not accept,
key conflicts, untranslated new keys per locale, the QA the server
already holds (max\_length with the text, terminology from M2), unknown
keys with their `file:line`, and what merging will make outdated — and
**says in the summary and in the comment that that is what it is**, with
the layers it does not cover named. A green check has to say what was
actually checked; a silent fallback would be the old divergence, hidden.
The policy
is `glossa check`'s own — `kernel/checkpolicy` is the **policy document**
(RFC 0005 §4.1) and its evaluator, and the CLI's `qa.Policy` is an alias
of it, so the pull request and the terminal can never disagree. Beside
`require_complete`, `fail_on` and `missing_translations` it holds
`rules` selecting on layer, code, locale, namespace and environment
(precedence: the rule naming more fields wins, ties go to the later
rule), per-environment completeness and review, and the rollout
machinery of §4.3 — a `version` every run records, `mode: warn` for a
rule that reports without failing, and a `grace_until` that keeps a pull
request opened before a stricter save on the version it opened under,
which the check summary says. A rule may never raise a model-decided
layer to `error`. `glossa.yaml` and the flags are **local overrides**
(`checkpolicy.Overrides`): honoured for the local run, ignored by the PR
check. The summary is Markdown
with a table per locale; findings with a location become annotations,
batched ≤ 50 per request.

Two rules were learned the hard way and are enforced in the schema:

- **GitHub appends annotations**, so each is sent exactly once per check
  run. The run is the unit — a new commit or a `check_run.rerequested`
  makes a new one — and `runs.<connection>.annotations` is its ledger of
  fingerprints. A retry sends none.
- **The sticky comment has one writer per pull request.** The queue row
  *is* the pull request, so claiming it is the lock; the comment's id
  lives on that row, and the adapter also finds it by a hidden marker.
  Two jobs for one pull request cannot exist, and a retry updates the
  comment rather than writing a second.

`catalog.branch.pushed`, `context.build.ingested`,
`quality.check_run.recorded`, `localization.translation.revised` and
`.reviewed` only **wake** the row (subscriber
`integration.github_check`); `check_run.rerequested`
clears its runs and wakes it. Metrics: `glossa_github_checks_total` and
`glossa_github_check_latency_seconds` by conclusion (from the
pull-request event to the completed check), `glossa_github_check_jobs_
total`, `glossa_github_comment_upserts_total` and the queue's depth;
one trace per check job.

Permissions: see *Identity* (`integration.read`, `integration.import`,
`integration.manage`). **Tests**: `internal/integration/app` runs every
format end to end on Postgres (app role, no BYPASSRLS) and MinIO —
including the XLIFF locale option, per-item positions and the
workspace's routes — and the GitHub flow over in-memory ports against
the fake GitHub, with the store and the inbox pinned on real Postgres;
`cmd/glossa-server` uploads, imports, exports and downloads through the
generated server.

## Context

Where every message appears (RFC 0004 §2–§3, intent §16–§18, §68). The
Go packages live in `internal/context/`; their names (`domain`, `app`,
`postgres`, `catalog`) never clash with the standard library's
`context`, and callers alias them `contextapp`, `contextpg` and so on.
Its HTTP edge is `adapters/httpapi` (the Context API below), its
Prometheus metrics `adapters/metrics`, its image normalizer
`adapters/imaging`; Intelligence reads it through its own port
(`sources.Usages`).

- A **build** is one upload for one application at one commit: branch,
  whether that branch is the repository's default, source (`plugin`,
  `extract`, `runtime` or `capture`), tool and the SHA-256 digest of
  the uploaded document. An upload is idempotent by (application,
  commit, source, digest): a repeat is a replay.
- A **usage** is key × file, line, column, component, route and kind
  (the call shape) in a build. Keys are resolved to message IDs **at
  ingest** through Catalog's port, so a rename never touches a usage;
  a key the catalog doesn't know is stored with a null ID (an *unknown
  key*, counted in the event).
- A **capture** is one (route, viewport, locale) screenshot of a build:
  the image's digest and size (the image itself is content-addressed in
  object storage, RFC 0004 §3.3). Its **regions** are the boxes
  (`element`, `text` or `attribute`, `visible` or not) of the messages
  rendered on it, resolved like usages.
- Limits (§10): a document of at most 20 MB and 100 000 usages; 500
  captures per build (held under an advisory lock on the build); 10 000
  regions per capture; images of at most 10 MB and 40 megapixels; a
  capture upload (manifest and images) of at most 200 MB.

`IngestUsages` takes a `glossa.usages/v1` document
(`domain.UsagesDocument`; field names exactly as RFC 0004 §2.2) and the
source. `domain.ParseUpload` validates it by the schema's rules
(`runtimes/testdata/schemas/usages.v1.schema.json`: full lowercase
commit IDs, slugs, keys, relative POSIX paths, `\S+` components, route
patterns, the five kinds, a semver tool version); `schema_test.go`
holds it to the schema itself (santhosh-tekuri) over the example,
every usage fixture and the checker's variants. The deliberate
deviations are listed there: members the schema doesn't define are
ignored within v1, so a newer collector's optional field doesn't break
an older server, and branch names Git refuses (`@`, `-x`, a `.lock`
component) are refused. Whether a build is of the default branch is
Catalog's `settings.default_branch` (`main` unless set), read through
the port at ingest — never the uploader's say; a later change of the
setting leaves existing builds as they were. Uploads go through a
per-tenant `Limiter` (the kernel's token bucket: 10 a minute, bursts of
60). `IngestCapture` adds a capture to a build of the project;
`IngestCaptures` (below) stores a whole capture upload. They need
`catalog.write` (developers, `write` tokens: CI) and publish
`context.build.ingested` (`build_id`, `project_id`, `application_id`,
`commit`, `branch`, `on_default_branch`, `source`, `usages`,
`unknown_keys`, `by`) and `context.capture.ingested`.

**Current usages.** Collectors are independent, so "the latest build of
each application" is taken per application **and source**: the Go
extractor's build never hides the bundler plugin's usages, and a
capture build with no usages never makes everything unused. The
default view is the latest default-branch build per (application,
source); a branch view uses the branch's latest build per (application,
source) and falls back to the default branch's where the branch didn't
rebuild (`domain.CurrentBuilds`, a pure function over the project's
build summaries). `MessageUsages`/`KeyUsages`/`UsagesOfKey` list a
message's current usages, default branch first; `ListUsages` pages
through the current usages on a route, in a component or in a file (the
messages a screen shows, §8); `CoLocated` returns the messages sharing a
route or a capture with one, most shared first (the agent's
neighbours); `ListBuilds` pages through a project's builds, newest
first, with their unknown keys; `UnusedMessages` lists the active
messages without a current usage — reported, never obsoleted. Reads
need `catalog.read`.

**Captures** (§3.2–§3.3). `glossa capture --upload` sends a
`glossa.captures/v1` manifest with its images; `domain.ParseCaptures`
validates the manifest by `captures.v1.schema.json`'s rules like a
usages document (`schema_test.go` holds it to the schema, deviations
listed: undefined members ignored; viewports over 10 000 CSS pixels and
images over 40 megapixels refused; every region `index` must name an
entry of its render log). Region boxes are stored as the whole pixels
that cover the measured box; a region found through the render log
takes that entry's key. Its digest is the SHA-256 of the manifest's RFC
8785 canonical form. `IngestCaptures` then reads the image parts one at
a time (`ImageParts`): each must name an image of the manifest, once,
and every image needs a part. The `ImageNormalizer` port
(`adapters/imaging`) spools a part to a temporary file while hashing it
(at most 10 MB; its SHA-256 must be its name), refuses anything but a
PNG of at most 40 megapixels from the header alone, and decodes and
re-encodes the pixels with `image/png` — no chunk of the upload
survives, and the same pixels always encode to the same bytes. Decoding
is a bulkhead: the decoded bytes in flight stay within one 40-megapixel
16-bit image per process (320 MB; many small images at once). The
width and height must be the manifest's. The re-encoded image is stored
at once in object storage (the `Objects` port,
`objectstore.StreamStore`) at
`context/<tenant>/<project>/img/<sha256>.png` unless that key exists
(dedupe across builds and within an upload), so an upload holds one
image on disk at a time (the server's `/tmp` is a 256 MiB `emptyDir`);
a refused or failed upload removes the images it wrote (best effort: a
leftover is reused by the next upload of the same pixels).

Storage is bounded per tenant: `GLOSSA_CONTEXT_STORAGE_QUOTA_BYTES`
(2 GiB by default, RFC 0004 §3.3). The upload reads what the tenant
holds once — `Store.StoredImageBytes`, the sum of `image_bytes` over the
distinct (project, image) pairs its captures reference, so a counter can
never drift from the rows — and refuses the first image whose new pixels
would pass the quota with `ErrStorageQuotaExceeded`
(`storage_quota_exceeded`, 413), saying how far past it is. Deduplicated
pixels cost nothing, the images already written are discarded with the
upload, and retention frees the quota again as builds age out. Then one unit of work
stores a build of source `capture` (default branch from Catalog, as for
usages) with its captures and regions, keys resolved at ingest (unknown
ones kept with a null ID and listed), and publishes
`context.build.ingested` and a `context.capture.ingested` per capture.
It is idempotent by (application, commit, source, manifest digest): a
repeat is a replay that reads no image. `CapturesOfKey` lists a message's
current captures (views as for usages) with only its regions;
`CaptureImage` reads an image through the service (`ErrCaptureNotFound`
across projects and tenants, or when retention removed it).

**The Context API** (`api/openapi.yaml`, tag `context`):

| Operation | What |
|---|---|
| `POST …/projects/{project}/context-builds?source=` | Upload a `glossa.usages/v1` document (≤ 20 MB; the route lifts the body limit): `201` with the build, or `200` + `Idempotent-Replayed` for the same document again. The generated server decodes the body into the contract's shape (dropping undefined members); the digest is the SHA-256 of its RFC 8785 canonical form. `invalid_usages`, `too_many_usages`, `invalid_source`, `unknown_application` (400), `payload_too_large` (413), `rate_limited` (429). `glossa context push` and `glossa extract --upload` call it. |
| `GET …/context-builds[?application=]` | The builds, newest first, with `usages` and `unknown_keys`. |
| `GET …/messages/{message}/usages[?branch=&limit=]` | A message's current usages (`truncated` past `limit`, 1–1000). |
| `GET …/usages[?route=&component=&file=&branch=]` | The current usages matching, by build and position. |
| `GET …/unused-messages[?branch=]` | The active messages without a usage, by key, with `current_builds`, `active_messages` and `unused_messages`. |
| `POST …/projects/{project}/captures` | A capture upload, `multipart/form-data`: the `manifest` part (`glossa.captures/v1`), then one `image/png` part per distinct image named by its lowercase hex SHA-256. The route lifts the body limit to 200 MB (10-minute read deadline); parts stream into the service. `201` `{build, captures, images_stored, images_deduplicated, unknown_keys}`, or `200` + `Idempotent-Replayed` for the same manifest. `invalid_request` (not multipart), `invalid_captures`, `too_many_captures`, `too_many_regions`, `invalid_image`, `unknown_application` (400), `payload_too_large`, `image_too_large`, `storage_quota_exceeded` (413), `rate_limited` (429, shared with usage uploads), `storage_unavailable` (503). `remote.UploadCaptures` is the CLI's call. |
| `GET …/messages/{message}/captures[?branch=&limit=]` | A message's current captures with its regions (kind, box, visible), route, viewport, locale and image (digest, size, API path). |
| `GET …/captures/{capture}/image` | The re-encoded PNG through the API — never a presigned or public URL — with `ETag` = its digest (`If-None-Match` → `304`) and `Cache-Control: private, max-age=31536000, immutable`. |

**Metrics** (RFC 0004 §11): `glossa_context_builds_total{source,
outcome}` (stored, replayed), `glossa_context_usages_ingested_total`
and `glossa_context_unknown_keys_total{source}`, and
`glossa_context_coverage_ratio{tenant, project}`: the share of active
messages with a current default-branch usage, measured by the
subscriber `context.measure_coverage` (background principal, catalog
read) after every default-branch build and by every default-view
`UnusedMessages` read. Each instance reports what it measured last;
take the max across instances. Capture uploads add
`glossa_context_captures_ingested_total{tenant}`,
`glossa_context_regions_ingested_total{tenant}`,
`glossa_context_capture_images_total{tenant, outcome}` (stored,
deduplicated), `glossa_context_capture_bytes_stored_total{tenant}` (the
re-encoded bytes written, which retention never takes back),
`glossa_context_capture_bytes_used{tenant}` against
`glossa_context_capture_quota_bytes` (what the tenant holds now, read
from the rows after each upload and each purge, so it does fall) and,
measured by the same subscriber,
`glossa_context_capture_coverage_ratio{tenant, project}`: the share of
active messages with a visible region on a current default-branch
capture. The purge job adds
`glossa_context_purge_deletions_total{kind}` (build, capture, image),
and the scheduler behind it
`glossa_scheduler_runs_total{job, outcome}`,
`glossa_scheduler_run_duration_seconds{job, outcome}`,
`glossa_scheduler_skips_total{job}` (not due, or another replica leads)
and `glossa_scheduler_last_run_timestamp_seconds{job}`. One trace
covers a CI upload end to end (§11): `context.ingest_usages` and
`context.ingest_captures` carry the project, source, build, counts and
whether it replayed, and the outbox rows they write carry that span's
trace context, so the deliveries join the upload's trace instead of
starting their own (`WithTracerProvider`).

`glossa_github_calls_total{op, status}`,
`glossa_github_call_duration_seconds{op}` and
`glossa_github_rate_limit_remaining{resource}`
(`integration/adapters/metrics`, on the GitHub client's `OnCall` hook)
cover the calls Glossa makes to GitHub;
`glossa_github_webhook_deliveries_total{event, outcome}`,
`glossa_github_webhook_handled_total{event, outcome}`,
`glossa_github_webhook_handler_duration_seconds{event, outcome}` and
`glossa_github_webhook_inbox_depth{event}` cover the deliveries it
receives (§11). The inbox is shared, so every replica reports the same
depth: take the max across instances, not the sum. §11's check latency
has no source yet — it comes with the check worker (§6.4).

**Retention** (`domain.RetentionPolicy`, §2.3): per (application,
branch, source) the latest 3 builds are kept, plus every current one; a
closed branch's builds go 7 days after it closed (default-branch builds
never do). That is Context's clock, over builds and captures — Catalog's
`ProposalRetention` (14 days, below) is a different rule over different
data. `PurgeProject` applies it and returns the deleted builds and
the images no remaining capture references, which it deletes from object
storage in batches of `GLOSSA_PURGE_BATCH_SIZE` (a failure is logged and
reported; the rest go on, and a delete of an object that is already gone
is not an error, so a re-run after a partial failure is safe). `Purge`
visits every project holding builds through the system scope `context.retention` (migration 0012 opens only
`context_builds.tenant_id` and `project_id` to `glossa_system`) and
purges each in its tenant as the background principal `context.purge`.
A deleted project's images go first, then its rows (so a redelivery
finds them again); a deleted application's images go when no other
capture of the project references them. Closed branches come from
Catalog's branch overlay (§4.1): `ClosedBranches` reads its closed and
merged branches with when they closed, and reopening one stops its
builds expiring.

**The purge job** runs daily in `glossa-server`
(`GLOSSA_PURGE_*`, Helm `server.purge`). `internal/kernel/scheduler`
runs it once per deployment rather than once per replica: each job takes
a named lease first, the way the other workers claim a job. One
conditional upsert on `system_leases` (migration 0017, system scope
`scheduler.lease`, no tenant and no `glossa_app` grant) checks both that
nothing holds the lease and that the job is due, so two replicas racing
can't both win; the lease records when the run finished, so due-ness
survives restarts and rolling upgrades. A failed run gives its lease
back and is retried at the next interval, counted and logged rather than
taking the server down, and a run in progress finishes (bounded by
`GLOSSA_PURGE_TIMEOUT`) before shutdown. Two jobs run: `context.purge`
(this retention) and `catalog.proposals`, Catalog's `SweepAllProposals`
over the tenants that have expired proposals — proposed messages whose
every proposing branch closed at least `ProposalRetention` ago (system
scope `catalog.proposals`, migration 0016: branch identity and closing
time, proposal links and message states only), at most 50 tenants a run,
each swept in its own scope as `catalog.sweep`.

| Table | Scope | Why |
|---|---|---|
| `context_builds` | tenant | Keyed by Catalog's project and application IDs (no cross-context foreign keys). SELECT, INSERT, DELETE: immutable. System scope reads `tenant_id` and `project_id` only. |
| `context_usages` | tenant | A build's usages by position, with the message ID resolved at ingest; cascade with their build. Indexed by message and (migration 0014) by route, for the co-located messages. |
| `context_captures` | tenant | One per (build, route, viewport, locale); cascade with their build. |
| `context_regions` | tenant | A capture's regions by position; cascade with their capture. |

Subscribers: `context.drop_project` on `catalog.project.deleted` and
`context.drop_application` on `catalog.application.deleted` erase what
they held; `context.measure_coverage` on `context.build.ingested`
measures coverage. **Tests**: `internal/context/domain` (documents and
the schema, limits, current views, retention) and `internal/context/app`
on Postgres (app role): ingest, replay, batches, unknown keys, the
project's default branch, renames, branch views, builds and usages
pages, co-located messages, unused messages, the upload limit, metrics
and coverage, captures and their limit, retention with orphaned images,
the cross-tenant sweep and tenant isolation; `adapters/httpapi` (routes,
problem codes); `cmd/glossa-server` runs the API over HTTP with a write
token (uploads, replays, refusals, reads, the coverage metric), the
Captures API over multipart (uploads, replays, refusals, captures per
message, the image with its ETag and 304, the capture metrics) and
checks the composition; `adapters/imaging` (re-encoding without
metadata, dedupe of the same pixels, refusals, nothing left on disk);
the CLI's real-server loop uploads captures with
`remote.UploadCaptures` against MinIO.

### scout follow-ups

Gaps found while taking captures with scout (`go.klarlabs.de/scout`).
Glossa doesn't work around them beyond what scout's own API offers; each
belongs in scout (RFC 0002 §2.3):

1. **No extra Chrome flags.** `internal/launcher.Launch` passes a fixed
   flag list, and `scout.Options` exposes only headless, proxy and
   viewport, so nothing can add `--no-sandbox` or
   `--disable-dev-shm-usage`. A CI runner whose kernel denies the user
   namespaces Chrome's sandbox wants then starts a Chrome that never
   opens a DevTools port, and `Launch` fails with "timeout waiting for
   devtools". Glossa's fix is the attach path: `glossa capture --cdp`
   (`GLOSSA_CAPTURE_CDP`) connects to a browser the caller started, over
   `browse.WithRemoteCDP`, and the capture integration tests
   (`internal/cli/capture/capturetest`) start that browser themselves.
   What scout wants is `scout.WithExtraArgs(args ...string)` — or a
   narrower `Options.Sandbox bool` — reaching `launcher.Options`, so a
   caller in a sandbox can launch rather than attach.

## Message preview

`POST /v1/message-previews` runs the MessageFormat kernel — the one MF1
converter (RFC 0002 §5) — for clients that aren't Go: Studio's live MF1
preview, and the same results the CLI's offline checks compute
in-process. It takes `{source, syntax (mf1|mf2, default mf1), locale,
values?, bidi_isolation?}` and returns `{valid, message (canonical MF2
data model), mf2, arguments, markup, formatted? (with values), errors}`.
Source the kernel rejects is a `200` with `valid: false` and
`errors[].stage = parse` (codes like `mf1-syntax-error`); formatting
problems are `stage = format` with MF2's fallback text in `formatted`.
Caller mistakes are `400`: `message_too_long` (> 20 000 bytes),
`invalid_locale`, `invalid_syntax`, `invalid_values` (> 100 values,
names > 64 characters, strings > 1 000 bytes, or anything but a
string, number or boolean).

`internal/preview` has no domain state and no tables: `app` checks the
caller with `authz.Authenticated` (any person or API token; no tenant
data, so no permission) and asks its `Limiter` port, which
`adapters/ratelimit` implements with the kernel's fortify token bucket
(`kernel/ratelimit`, which Context's upload limit uses too) per
principal (`person:<id>` / `token:<id>`): **10 a second, bursts of
120**, in process — each instance enforces it on its own, which bounds
the CPU one caller can take from any instance; idle buckets expire
after 10 minutes, at most 100 000 are tracked. Over the limit is `429
rate_limited`.

## MCP

Agents are first-class users (intent §6.5, §47). `glossa-server` serves
the Model Context Protocol at **`/mcp`** over streamable HTTP, in this
same process, behind the same middleware, tenancy and row-level
security as REST (RFC 0005 §7.1). It is **off by default**
(`GLOSSA_MCP_ENABLED`); with it off the path is not registered at all.

**The SDK.** `github.com/modelcontextprotocol/go-sdk`, pinned, behind
`adapters/mcpgo`. It is the official one, at a released v1, and it was
already in the module graph through the Anthropic SDK — taking the
other candidate would have linked two MCP implementations into one
binary. Nothing outside that one package imports it: `app` speaks Go
functions over `app.Session` and `json.RawMessage`, so replacing the
library is a rewrite of `adapters/mcpgo` and nothing else.

**Auth mints nothing.** A session presents an existing tenant API token
(`glossa_api_…`) as its bearer. `adapters/identity` resolves it through
exactly the calls Identity's HTTP guard makes — `AuthenticateToken`,
then `Authorize` in the token's own tenant — so an MCP session's grant
is the grant the REST API would have produced, permission for
permission, and `authz.Require` runs in the tool as it does in a
handler. **CI tokens (`glossa_ci_…`) and in-context grants
(`glossa_ctx_…`) are refused at connect**, with a message naming what
was presented: each is minted for one workflow run or one browser
origin, and lending either to a long-lived agent would widen it.

**The session is bound to the token's tenant.** No tool takes a tenant
argument. The SDK creates the session from the `initialize` request and
runs every later call on that request's context, so the tenant a
session acts in is fixed when it opens; a second token cannot steer an
open session (the SDK compares the bearer's actor against the
session's), and every HTTP request is re-authenticated, so revoking a
token ends its agent's session at the next call.

**Two locks on anything that changes something.** The token must carry
the matching scope *and* the client must open the session asking for
that toolset: `write` and `/mcp?toolset=write` for the write tools,
`publish` and `/mcp?toolset=publish` for the release tools. A plain
`/mcp` is a read session whatever the token could do — a token is
long-lived and an agent is not a person, so one accidental call must
not be able to rewrite a catalog or move production onto a different
release.

The toolsets mirror the token scopes and are deliberately **not** a
ladder: `publish` is not a wider `write`, because the scopes themselves
are orthogonal (a publish token carries `releases.publish` and nothing
else beyond read). A write session cannot move a release and a publish
session cannot touch the catalog; a client that means to do both opens
two sessions. `admin` is not a toolset at all, there is no delete tool
of any kind, and no AI provider key crosses MCP in either direction.

**The tools** are declared as data in `internal/mcp/tools`, one
function per toolset — `Read`, `Write`, `Publish` — and each one is a
thin call into another context's application port, never a second
implementation of a rule. Every session also has `whoami`: the tenant,
the actor, the token's scopes and permissions, the toolset and the
tools this session may call, plus whether a write or publish session
would be available on this token. It is the question an agent asks
first, and answering it wrongly costs a hundred calls.

**`glossa mcp`** is a stdio proxy to this endpoint for editors that
speak only stdio (RFC 0005 §7.1), using the token `glossa login`
stored. It is framing only — it moves JSON-RPC frames between the
editor's stdio and streamable HTTP and never decodes one — so it
registers no tool, validates no argument and cannot diverge from what
this endpoint serves. The one thing it decides is which toolset to ask
for (`--allow-write`, `--allow-publish`), and asking is not getting:
the server still checks the token's scopes. `internal/mcp/README.md`
has the editor configuration, the toolset table and the tool list.

**Every call is audited.** `mcp_tool_calls` (migration 0028, widened to
admit the `publish` toolset by 0037) is
tenant-owned under forced RLS and **append-only** for `glossa_app`:
INSERT and SELECT, no UPDATE, no DELETE, so no code path — and no agent
— rewrites its own trail. A row carries the actor, the token, the
session, the toolset, the tool, the outcome, the affected ids and the
**shape** of the arguments rather than their content: a tool declares
which of its arguments are selectors (a locale, a state, a namespace)
and those are recorded verbatim, while everything else becomes its JSON
type and, for a string, its rune length —
`{"locale": "de", "source": "string(len=32)"}`. A tool argument can
carry source text or a translation, and message text never reaches a
log or a ledger (RFC 0005 §11). A selector that arrives longer than 64
runes, or carrying a control character, is recorded as a length anyway:
what a tool declares an argument is *for* does not decide what a caller
actually sends. A ledger write that fails is logged at error level and
does not fail the call.

**Observability.** `glossa_mcp_tool_calls_total{tool,scope,outcome}`
(`ok`, `denied`, `invalid`, `error`), `glossa_mcp_sessions_total{transport}`
and `glossa_mcp_sessions_open{transport}` — a gauge *function* over the
transport's live sessions, because the transport owns when a session
ends and a counted gauge would drift upwards forever. One span per tool
call. Per-tenant rate limits (`GLOSSA_MCP_RATE`/`_BURST`) use the same
`kernel/ratelimit` bucket the preview and Context's uploads use.

**Tests**: `internal/mcp/domain` (the toolsets, and that a shape never
echoes text), `internal/mcp/app` (the gate, the permission check,
tenant isolation, what the audit row says), `internal/mcp/tools` (every
tool against fakes that are row-level security: the two locks on each
of write and publish, the refusals and that they are audited, that a
proposal always enters review and that no tool destroys anything),
`internal/mcp/adapters/identity` (every credential that is not a tenant
API token, refused before the token table is even consulted),
`internal/mcp/adapters/mcpgo` (a real MCP client over `httptest`:
refusals at connect with their statuses, the toolsets a session sees,
that neither `write` nor `publish` stands in for the other, tenant
binding, the audit rows), `internal/mcp/adapters/postgres`
(integration: RLS, the ledger's append-only grant, and that every
toolset the domain knows is one the column's CHECK admits) and
`internal/cli` (the stdio proxy end to end: a real MCP client over real
pipes, through `glossa mcp`, to a real `/mcp` endpoint).

## Audit

RFC 0006 §6. `audit_entries` (migration 0045) is a projection: the
`audit.record` subscriber turns every outbox event into one entry, and
the security-relevant acts that never reach the outbox are written
through `audit/app.Recorder` — sign-ins and failed sign-ins (Identity's
`SignInAudit` port, recorded in every tenant the person belongs to; a
failure is written in the background so it takes no longer for an
existing account) and MCP tool calls (beside their `mcp_tool_calls`
row, which the entry points at by id).

- **Content-free.** An entry holds the action (the event type, or
  `identity.person.signed_in`, `identity.person.sign_in_failed`,
  `mcp.tool.called`), the actor, the target, the project and locale,
  the request or trace id, and a summary in which the payload paths a
  projection lists as identifiers or selectors are verbatim and
  everything else is its shape (`"string(len=27)"`). Never message
  text, an email, a name or a secret.
- **Hash-chained per tenant.** `hash = sha256(prev_hash ‖ JCS(entry))`,
  the first `prev_hash` 32 zero bytes, sequences 1, 2, 3… with no gaps.
  Appends to one tenant serialize on a transaction-scoped advisory
  lock; a trigger refuses an entry that skips a sequence or links to the
  wrong hash. `domain.Verifier` recomputes a chain or a range.
- **Append-only** for `glossa_app` (SELECT and INSERT), under forced
  RLS. The chain proves an export wasn't edited; it is no protection
  against a database superuser (§9.5).
- **Idempotent.** An entry's `event_id` is unique per tenant, so a
  redelivered event or a re-run backfill records nothing twice.
- **History.** At startup every replica runs the backfill until it
  succeeds once (like the key index task): it reads the outbox's
  history (`outbox.History`, system scope `outbox.history` for the
  tenant list) in the order events occurred and appends what is
  missing; a tenant whose entry count already matches its event count
  is skipped. An event from before migration 0042 takes its payload's
  by-field as its actor when that is a well-formed actor, and is
  `unknown` otherwise; the log line reports both counts.

The read API and export jobs are RFC 0006 wave 5; the export format and
`glossa audit verify` are wave 4.

### Audit export format

An export (`glossa.audit/v1`, `audit/domain/export.go`) is a directory
of two files. It is verifiable offline with nothing but the files and a
trusted public key: `glossa audit verify <dir> --public-key …`.

```text
<export>/
  entries.jsonl   one line per entry, in sequence order
  manifest.json   what the lines are, signed with the audit key
```

**`entries.jsonl`** is UTF-8, one entry per line, every line ending in
`\n` (the last one too), no blank lines. A line is the RFC 8785 (JCS)
form of the entry's canonical object — `glossa.audit.entry/1`, the
object its hash covers — with two more members, `prev_hash` and `hash`,
as 64 lowercase hex digits:

```json
{"action":"localization.translation.revised","actor":"person:0190…","aggregate_id":"0190…","aggregate_type":"translation","event_id":"0190…","format":"glossa.audit.entry/1","hash":"2d52…","locale":"de-CH","occurred_at":"2026-10-01T09:30:00.123456Z","prev_hash":"0000…","project_id":"0190…","request_id":null,"sequence":1,"source":"outbox","summary":{"revision":3,"state":"needs_review","text":"string(len=12)"},"tenant_id":"0190…","trace_id":"4bf9…"}
```

Every member is always present (an absent optional is `null`). To check
a line without Glossa: remove `prev_hash` and `hash`, canonicalize what
is left (it already is canonical), and `hash` must equal
`sha256(bytes(prev_hash) ‖ that)`; each line's `prev_hash` is the
previous line's `hash`, and `sequence` goes up by one. A line has
exactly one spelling — the verifier refuses one that is not byte for
byte the JCS of its own content. `TestExportLineIsPinned` pins a golden
line; changing the line is a new format, never an edit to this one.

**`manifest.json`** is the JCS form of:

| Member | |
|---|---|
| `format` | `"glossa.audit/v1"` |
| `tenant_id` | The chain's tenant. |
| `range.first_sequence`, `range.last_sequence` | The sequences of the first and last line. An empty export has `last_sequence = first_sequence − 1`. |
| `range.first_prev_hash` | The hash of the entry before the range — 32 zero bytes when it starts at 1. Where the export joins the rest of the chain. |
| `range.last_hash` | The last line's hash (`first_prev_hash` when empty): the head an auditor compares with the next export's `first_prev_hash`. |
| `occurred` | `{from, to}`, the `[from, to)` time range the export job was asked for, or `null` for a sequence range. Every entry falls inside it. |
| `entry_count` | `last_sequence − first_sequence + 1`. |
| `entries` | `{path: "entries.jsonl", sha256, bytes}`: the lines file, pinned. |
| `created_at` | When the export was made, UTC with microseconds. |
| `key_id` | The audit key that signed it. |
| `signature` | `{algorithm: "Ed25519", value}`: base64url (no padding) Ed25519 signature over the JCS of the manifest **without** `signature` — the pattern of release manifests (runtimes/SPEC.md §1.3), with the one key. |

Why a directory of two files and not one file or a tar: the lines stay
plain JSON Lines that `jq`, `grep` and a spreadsheet import read as they
are, and stream in constant memory; the manifest's signature already
binds the lines through their digest and both chain ends, so an
envelope would add a format without adding integrity. The export jobs
(wave 5) store and serve the same two objects.

**Verification** (`domain.VerifyExport`) reports the first thing that
fails, as a stable code: `manifest_invalid` (not a manifest, or one that
contradicts itself), `unknown_key`, `signature_invalid` (the manifest was
changed after signing, or another key signed it), then line by line
`line_invalid` (including a truncated last line), `line_not_canonical`,
`tenant_mismatch`, `sequence_gap` (a removed or reordered line),
`prev_hash_mismatch`, `hash_mismatch` (an edited line), then
`range_mismatch` (lines missing at the end, extra lines, another last
hash, an entry outside `occurred`) and `digest_mismatch`. The chain says
where an edit is; the digest, which the signature covers, catches
anything the chain could not. `TestEveryAlteredByteFails` flips every
byte of an export in turn and none verifies.

**The audit key** (`GLOSSA_AUDIT_SIGNING_KEY`, one `keyId=base64(seed)`)
is not the release signing key: a release key is trusted by every runtime
in the field and rotates on the delivery plane's schedule, an audit key
is trusted by auditors and must verify exports for as long as the tenant
exists. The server refuses a seed that is also a release key, and there
is no development key derived from the auth secret: exports stay off
(`GLOSSA_AUDIT_EXPORTS_ENABLED=false`) until a key is configured, and on
without a key the server does not start. Generate one with
`audit-2026=$(openssl rand -base64 32)` into a Secret; never commit it.
**Rotation**: configure a new key, and move the old key's public key to
`GLOSSA_AUDIT_RETIRED_KEYS` — never drop it while an export it signed may
still need verifying (`TestRetiredKeysStillVerify`).

**Public keys** are distributed as a `glossa.audit.keys/1` document,
`{format, keys: [{key_id, algorithm: "Ed25519", public_key (base64url),
active}]}`, the active key first. glossa-server serves it at
`GET /.well-known/glossa-audit-keys.json` (public, no token — it holds
public keys only; 404 on a deployment without an audit key). Export jobs
(`POST /v1/tenants/{tenant}/audit-export-jobs`, owner only) write these
two files to object storage and serve them at `…/{job}/file` and
`…/{job}/manifest`. The verifier never takes
a key from the export it is checking: `glossa audit verify` requires
`--public-key`, either that document saved once and pinned, or
`keyId=base64` from wherever the operator published it.

## Release

What ships where (RFC 0002 §7, intent §34–38). Every project has the
environments `development`, `preview`, `staging` and `production`
(created on first use) plus any custom ones (`pr-42`; `a` is reserved).
Each has an **eligibility policy** — the review states that ship
(production and staging `approved`; the others `draft`, `needs_review`,
`approved`; never `rejected`) and whether outdated translations do — and
points at the release it serves.

**The path to production** follows from those defaults: `development`
and `preview` (and custom environments like `pr-42`) ship work in
progress; `staging` ships what production would. So publish to
`staging`, check it, and promote that release to `production`, which
moves the pointer and rebuilds nothing. A development or preview release
can never be promoted to production — it was built under a policy that
lets drafts through — and the `release_ineligible` refusal says so,
naming both policies, what differs (`draft, needs_review text`,
`outdated translations`) and the way there (`IneligibleError`).
Publishing to production directly works too.

**Publish** reads Catalog's `ReleaseSource` and Localization's
`ReleaseTranslations` (the policy's states), builds one artifact per
locale and namespace (the source locale holds every active message, a
locale only what is translated for it, `default` always exists), uploads
the artifacts storage doesn't have yet, then in one transaction records
the **release** (version counting the project's releases without gaps,
parent = what the environment served, policy snapshot, author, counts,
manifest digest), points the environment at it and appends the
deployment. A catalog artifacts can't carry (a 64-character
namespace, a malformed model) fails with `422 not_releasable`, listing
every problem found (`NotReleasableError`), not only the first.

**Preview** (`POST …/environments/{env}/release-previews`, the CLI's
`release publish --dry-run`, Studio's publish dialog) runs the same
`build` Publish runs and stops before anything is stored: it returns the
per-locale counts, the manifest digest, `new_artifacts` (what the
publish would upload, from `Exists` checks), the per-locale message IDs
added, changed and removed against the release the environment serves
(the build's artifacts compared in memory, the served release's read
from storage), and the `not_releasable` problems as data rather than an
error. It writes no row, object or event — it doesn't even create the
default environments — which `TestPreviewPublishWritesNothing` checks
against the tables, the outbox and object storage. It needs only
`releases.read`.

**Promote** points an environment at an existing release
whose policy it covers (a preview release with drafts can't reach
production); **rollback** points back to the newest older release the
environment served, or a named one from its history. Both only move the
pointer; nothing is rebuilt.

**Branch environments** (RFC 0004 §4.2) preview one open branch each:
`kind: branch`, named `pr-<number>` or `br-<first 8 hex of
sha256(branch)>` (names no standard environment may take), with the
fixed preview policy (draft, needs_review and approved, outdated
included; `fixed_policy` otherwise). Their build is the main catalog
plus that branch's overlay only (`domain.BuildBranch`): its proposed
messages with their translations, and its source proposals in the
source locale. Every other build keeps excluding both. A branch release
records its branch and can't be promoted
(`branch_release_not_promotable`); a project has at most 50 branch
environments (`too_many_branches`). The lifecycle follows the branch,
over the outbox (`release.branch_environments`), so nothing has to
remember to call it: `catalog.branch.{opened,pushed,reopened}` opens the
environment (`OpenBranchEnvironment`, idempotent per branch) and asks
for a publish (`RequestBranchPublish`, debounced: a request moves the
pending publish to 30 s after it, at most 5 minutes after the first),
`localization.translation.revised` asks for one on every open branch
that proposes the message, and `catalog.branch.{closed,merged}` destroys
the environment (`DestroyBranchEnvironment`: the environment and its
manifest go, so the edge answers 404; releases and deployments stay).
The `Publisher` runs due requests across tenants as `release.publisher`,
with the request's ID as the publish's idempotency key, so two
publishers or a retry after a crash publish once.
`GLOSSA_BRANCH_PUBLISHER_ENABLED` (default on) runs it in glossa-server,
every `GLOSSA_BRANCH_PUBLISH_INTERVAL` (5 s).

**Delivery keys are scoped** (RFC 0004 §4.3, SPEC §2): an `environments`
allowlist and a `branches` flag, written into the key index object and
enforced by the edge, which answers 404 outside the scope exactly as for
an unknown key. New keys read `production` only; a preview key
(`branches: true`) belongs in preview deployments. Migration 0015 gave
existing keys the four default environments, and glossa-server's key
index task (`RewriteKeyIndexes`, run at startup until it succeeds)
rewrites their index objects; until then the edge reads an object
without `environments` as those four. `POST …/delivery-keys` takes a
`scope` (without one: `production`), and `PUT
…/delivery-keys/{id}/scope` changes what an active key reads without
changing the key, so bundles that ship it keep working; the index object
follows (`release.delivery_key.scope_changed`), and the stored index
version drops so the key index task repairs a write that didn't reach
storage.

**Artifacts** are the RFC 8785 canonical JSON of `{schema, locale,
namespace, messages}` with each message's MF2 data model, so equal input
gives byte-identical output whatever order it came in
(`TestBuildIsDeterministic`, `TestArtifactBytesAreCanonical`), and are
addressed by the SHA-256 of those bytes. Publishing an unchanged catalog
uploads nothing, and identical artifacts dedupe across environments.
The **manifest digest** is the SHA-256 of the canonical content every
environment's manifest of the release carries (`sourceLocale`, `locales`,
`fallback`, `artifacts`). **Manifests** are canonical JSON signed with
every active Ed25519 key over the canonical manifest without
`signatures` (SPEC §1.3); both steps are deterministic, so a release
served again (rollback) is served byte for byte as before. Rotation: add
a key to `GLOSSA_RELEASE_SIGNING_KEYS` (manifests carry both
signatures), move runtimes to it (`GET …/release-signing-keys` publishes
the keys), then move the old one to `GLOSSA_RELEASE_RETIRED_KEYS`.

**Storage** is a projection of the database, laid out by
`internal/release/delivery`:

```text
v1/keys/<sha256(key)>.json                               delivery key → project and scope, while the key is active
v1/projects/<project>/environments/<env>/manifest.json   what the environment serves
v1/projects/<project>/a/<sha256>.json                    artifacts, immutable
```

Every change commits first and then writes what it affects, right away
and again from the outbox, under the row's lock and from the row's
current state, so retries and reordering converge and a storage outage
delays delivery without losing a change. Artifacts are addressed per
project: a key only reaches its own project's objects.

| Table | Scope | Why |
|---|---|---|
| `release_releases` | tenant | Immutable: `glossa_app` may SELECT and INSERT; a trigger refuses UPDATE and DELETE for every role except the cascade from erasing the tenant. |
| `release_environments` | tenant | Policy, approval requirement (RFC 0006 §5.1), kind (and branch) and current release per environment. |
| `release_deployments` | tenant | Every pointer move, append-only by grant: what rollback walks. |
| `release_delivery_keys` | tenant | Publishable keys, in the clear (they ship in bundles), with their scope; revoked ones stay listed. `glossa_system` reads IDs and `index_version` (system scope `release.key_index`). |
| `release_publish_requests` | tenant | Pending debounced publishes of branch environments. `glossa_system` reads IDs and `not_before` (system scope `release.publisher`). |
| `release_requests` | tenant | Release requests (RFC 0006 §5.1): a publish or promote into an environment that requires approval, held until approved. At most one pending per environment; closed once (deployed, denied, withdrawn or refused), never deleted. |

Events: `release.published`, `release.promoted`, `release.rolled_back`
(the Release aggregate shares the context's name, so they are
`release.<verb>`, as SPEC §3 names them),
`release.environment.{created,policy_changed,destroyed,publish_requested}`,
`release.delivery_key.{created,scope_changed,revoked}` (never carrying
the key),
`release.release_request.{created,approved,denied,deployed,withdrawn,refused}`
(RFC 0006 §5.1; never carrying the force or withdrawal reason). Subscribers: `release.sync_manifest` and
`release.sync_delivery_key` (storage writes; `sync_manifest` also
removes a destroyed environment's manifest),
`release.branch_environments` on Catalog's branch events and
`localization.translation.revised` (the lifecycle above),
`release.retire_project` on `catalog.project.deleted` (revokes the
project's keys, removes its environments and served manifests; releases
stay as history).

Permissions: reads (and previews) need `releases.read`; publish, promote, rollback,
environment and key changes `releases.publish` (developers, admins,
owners, `publish` tokens).

### glossa-edge

A stateless server that reads object storage and nothing else — a test
fails if its import graph reaches the database, pgx, migrations or any
context's application layer — so published translations keep loading
while glossa-server or Postgres is down (the end-to-end test stops both
and loads a release through the Go runtime). `GET
/v1/{key}/{environment}/manifest.json` answers with `Cache-Control:
public, max-age=60, stale-while-revalidate=300, stale-if-error=86400`, a
strong ETag (the SHA-256 of the bytes) and 304; `GET
/v1/{key}/a/{sha256}.json` is immutable and verified against its hash.
CORS `*` (ETag exposed, preflights answered), never a cookie; unknown or
revoked keys 404, and so does a manifest outside the key's scope (its
`environments`, plus branch environments with `branches`; checked before
storage is read, conformance fixture `runtimes/testdata/edge/`), storage
failures 503 `no-store`, objects failing their
integrity check 502. An in-process LRU caches keys, manifests and
artifacts with singleflight on misses, and serves stale entries through
a storage outage. `/readyz` ignores storage on purpose, so an outage
can't pull every edge out of the load balancer.
