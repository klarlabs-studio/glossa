-- The GitHub integration (RFC 0004 §6), migration 0019.
--
-- Tenant scope (db.TenantTx): RLS limits every statement to the current
-- tenant, except the queries marked system scope, which run as
-- glossa_system ("integration.github") across tenants — the webhook
-- endpoint (no tenant on the request), the inbox worker and the sweep.

-- ── install intents ────────────────────────────────────────────────

-- name: InsertInstallIntent :execrows
INSERT INTO integration_github_install_intents (id, tenant_id, person, state_hash, created_at, expires_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(person), sqlc.arg(state_hash),
        sqlc.arg(created_at), sqlc.arg(expires_at))
ON CONFLICT (id) DO NOTHING;

-- RedeemInstallIntent takes an unexpired, unredeemed intent by its
-- state's hash and marks it used in the same statement, so two callbacks
-- racing on one state leave exactly one winner.
-- name: RedeemInstallIntent :one
UPDATE integration_github_install_intents
SET redeemed_at = sqlc.arg(now)
WHERE state_hash = sqlc.arg(state_hash) AND redeemed_at IS NULL AND expires_at > sqlc.arg(now)
RETURNING id, tenant_id, person, created_at, expires_at;

-- DeleteExpiredInstallIntents drops intents whose own window has closed.
-- It is not the delivery replay window: an expired state is already
-- useless, so there is nothing to keep it for. System scope.
-- name: DeleteExpiredInstallIntents :execrows
DELETE FROM integration_github_install_intents WHERE expires_at < now();

-- ── installations ──────────────────────────────────────────────────

-- InsertInstallation claims an installation for the current tenant. The
-- unique index on installation_id is global, so a second tenant's insert
-- raises a unique violation rather than returning zero rows; the store
-- turns that into installation_already_claimed.
-- name: InsertInstallation :execrows
INSERT INTO integration_github_installations (id, tenant_id, installation_id, account_id, account_login,
                                              account_type, state, connected_by, connected_at, updated_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(installation_id), sqlc.arg(account_id),
        sqlc.arg(account_login), sqlc.arg(account_type), sqlc.arg(state), sqlc.arg(connected_by),
        sqlc.arg(connected_at), sqlc.arg(updated_at))
ON CONFLICT (installation_id) DO NOTHING;

-- name: GetInstallation :one
SELECT * FROM integration_github_installations WHERE id = sqlc.arg(id);

-- name: GetInstallationByGitHubID :one
SELECT * FROM integration_github_installations WHERE installation_id = sqlc.arg(installation_id);

-- name: ListInstallations :many
SELECT * FROM integration_github_installations ORDER BY connected_at, id;

-- name: UpdateInstallationState :execrows
UPDATE integration_github_installations
SET state = sqlc.arg(state), account_login = sqlc.arg(account_login), updated_at = sqlc.arg(updated_at)
WHERE installation_id = sqlc.arg(installation_id);

-- name: DeleteInstallation :execrows
DELETE FROM integration_github_installations WHERE id = sqlc.arg(id);

-- ResolveInstallationTenant maps GitHub's installation ID to the tenant
-- that claimed it. System scope: the inbox worker runs before a tenant
-- is known, and the grant covers these three columns only.
-- name: ResolveInstallationTenant :one
SELECT tenant_id, state FROM integration_github_installations
WHERE installation_id = sqlc.arg(installation_id);

-- ── Git connections ────────────────────────────────────────────────

-- name: InsertGitConnection :execrows
INSERT INTO integration_git_connections (id, tenant_id, installation_id, repository_id, repository_name,
                                         project_id, application_id, default_branch, path,
                                         created_by, created_at, updated_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(installation_id), sqlc.arg(repository_id),
        sqlc.arg(repository_name), sqlc.arg(project_id), sqlc.arg(application_id), sqlc.arg(default_branch),
        sqlc.arg(path), sqlc.arg(created_by), sqlc.arg(created_at), sqlc.arg(updated_at))
ON CONFLICT (repository_id, path) DO NOTHING;

-- name: GetGitConnection :one
SELECT * FROM integration_git_connections WHERE id = sqlc.arg(id);

-- name: ListGitConnections :many
SELECT * FROM integration_git_connections
WHERE (sqlc.narg(installation_id)::uuid IS NULL OR installation_id = sqlc.narg(installation_id)::uuid)
  AND (sqlc.narg(project_id)::uuid IS NULL OR project_id = sqlc.narg(project_id)::uuid)
ORDER BY repository_name, path, id;

-- ConnectionsForRepository is how a pull-request event finds the
-- projects a repository feeds, one per monorepo path.
-- name: ConnectionsForRepository :many
SELECT * FROM integration_git_connections WHERE repository_id = sqlc.arg(repository_id) ORDER BY path, id;

-- name: UpdateGitConnection :execrows
UPDATE integration_git_connections
SET project_id     = sqlc.arg(project_id),
    application_id = sqlc.arg(application_id),
    default_branch = sqlc.arg(default_branch),
    path           = sqlc.arg(path),
    updated_at     = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: DeleteGitConnection :execrows
DELETE FROM integration_git_connections WHERE id = sqlc.arg(id);

-- ── the webhook inbox ──────────────────────────────────────────────

-- StoreDelivery is the endpoint's whole write: system scope, no tenant.
-- A delivery ID we have already seen changes nothing, which is what
-- makes a duplicate a no-op inside the replay window.
-- name: StoreDelivery :execrows
INSERT INTO integration_github_deliveries (delivery_id, event, action, installation_id, state, payload,
                                           received_at, available_at)
VALUES (sqlc.arg(delivery_id), sqlc.arg(event), sqlc.arg(action), sqlc.arg(installation_id), 'pending',
        sqlc.arg(payload), sqlc.arg(received_at), sqlc.arg(received_at))
ON CONFLICT (delivery_id) DO NOTHING;

-- ClaimDelivery leases the oldest due delivery. System scope.
-- name: ClaimDelivery :one
WITH due AS (
    SELECT d.delivery_id
    FROM integration_github_deliveries d
    WHERE d.state = 'pending' AND d.available_at <= now()
    ORDER BY d.available_at, d.delivery_id
    LIMIT 1
    FOR UPDATE OF d SKIP LOCKED
)
UPDATE integration_github_deliveries e
SET attempts     = e.attempts + 1,
    available_at = now() + make_interval(secs => sqlc.arg(lease_seconds)::float8),
    claim_token  = gen_random_uuid()
FROM due
WHERE e.delivery_id = due.delivery_id
RETURNING e.delivery_id, e.event, e.action, e.installation_id, e.tenant_id, e.payload, e.attempts, e.claim_token;

-- SettleDelivery ends a claimed delivery: it records the tenant it
-- resolved to, drops the payload and keeps the ID for the replay window.
-- The claim token fences it, so a worker whose lease expired changes
-- nothing. System scope.
-- name: SettleDelivery :execrows
UPDATE integration_github_deliveries
SET state        = sqlc.arg(state),
    tenant_id    = sqlc.narg(tenant_id),
    failure      = sqlc.arg(failure),
    payload      = NULL,
    claim_token  = NULL,
    processed_at = sqlc.arg(processed_at)
WHERE delivery_id = sqlc.arg(delivery_id) AND claim_token = sqlc.arg(claim_token)::uuid;

-- RetryDelivery hands a claimed delivery back for another attempt after
-- a delay, keeping its payload. System scope.
-- name: RetryDelivery :execrows
UPDATE integration_github_deliveries
SET available_at = now() + make_interval(secs => sqlc.arg(delay_seconds)::float8),
    failure      = sqlc.arg(failure),
    claim_token  = NULL
WHERE delivery_id = sqlc.arg(delivery_id) AND claim_token = sqlc.arg(claim_token)::uuid;

-- DeliveryDepth is the §11 inbox-depth metric: pending rows per event.
-- System scope.
-- name: DeliveryDepth :many
SELECT event, count(*)::bigint AS pending
FROM integration_github_deliveries WHERE state = 'pending' GROUP BY event ORDER BY event;

-- DeleteDeliveriesBefore is the replay-window sweep: a delivery ID is
-- kept for seven days, then it is gone. System scope.
-- name: DeleteDeliveriesBefore :execrows
DELETE FROM integration_github_deliveries
WHERE received_at < sqlc.arg(before) AND state <> 'pending';

-- ResolveRepositoryConnections lists a repository's Git connections
-- across tenants. System scope: the GitHub Actions OIDC exchange
-- (RFC 0004 §6.3) arrives with no tenant — which tenant a run belongs
-- to is what its repository_id proves — so the lookup happens before
-- any tenant is known, and the grant covers these columns only.
-- name: ResolveRepositoryConnections :many
SELECT tenant_id, project_id, application_id, path
FROM integration_git_connections
WHERE repository_id = sqlc.arg(repository_id)
ORDER BY path;
