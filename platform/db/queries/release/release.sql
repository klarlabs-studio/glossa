-- Tenant scope (db.TenantTx): RLS limits every statement to the current
-- tenant.

-- ── environments ───────────────────────────────────────────────────

-- name: InsertEnvironment :execrows
-- An environment's identity is its name within the project, so creating
-- one twice is a no-op (the default environments are ensured this way).
INSERT INTO release_environments (tenant_id, project_id, name, kind, branch, policy, current_release_id, version,
                                  created_by, created_at, updated_at)
VALUES (app_current_tenant(), sqlc.arg(project_id), sqlc.arg(name), sqlc.arg(kind), sqlc.narg(branch), sqlc.arg(policy),
        NULL, sqlc.arg(version), sqlc.arg(created_by), sqlc.arg(created_at), sqlc.arg(updated_at))
ON CONFLICT DO NOTHING;

-- name: GetBranchEnvironment :one
SELECT * FROM release_environments WHERE project_id = sqlc.arg(project_id) AND branch = sqlc.arg(branch);

-- name: LockBranchEnvironment :one
SELECT * FROM release_environments WHERE project_id = sqlc.arg(project_id) AND branch = sqlc.arg(branch) FOR UPDATE;

-- name: CountBranchEnvironments :one
SELECT count(*)::integer FROM release_environments WHERE project_id = sqlc.arg(project_id) AND kind = 'branch';

-- name: DeleteEnvironment :execrows
-- Its deployments stay: they are history, like the releases.
DELETE FROM release_environments WHERE project_id = sqlc.arg(project_id) AND name = sqlc.arg(name);

-- name: GetEnvironment :one
SELECT * FROM release_environments WHERE project_id = sqlc.arg(project_id) AND name = sqlc.arg(name);

-- name: LockEnvironment :one
SELECT * FROM release_environments WHERE project_id = sqlc.arg(project_id) AND name = sqlc.arg(name) FOR UPDATE;

-- name: ListEnvironments :many
SELECT * FROM release_environments
WHERE project_id = sqlc.arg(project_id) AND name > sqlc.arg(after)
ORDER BY name
LIMIT sqlc.arg(max_rows);

-- name: LockProjectEnvironments :many
-- Serializes publishes of one project (release versions have no gaps).
SELECT * FROM release_environments WHERE project_id = sqlc.arg(project_id) ORDER BY name FOR UPDATE;

-- name: UpdateEnvironment :execrows
UPDATE release_environments
SET policy = sqlc.arg(policy), approval = sqlc.narg(approval), current_release_id = sqlc.narg(current_release_id),
    version = sqlc.arg(version), updated_at = sqlc.arg(updated_at)
WHERE project_id = sqlc.arg(project_id) AND name = sqlc.arg(name) AND version = sqlc.arg(expected_version);

-- name: DeleteProjectEnvironments :many
DELETE FROM release_environments WHERE project_id = sqlc.arg(project_id) RETURNING name;

-- ── releases ───────────────────────────────────────────────────────

-- name: InsertRelease :execrows
-- A retried publish (same Idempotency-Key, same ID) inserts nothing.
INSERT INTO release_releases (id, tenant_id, project_id, version, parent_id, environment, policy, branch, content,
                              manifest_digest, stats, note, created_by, created_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(project_id), sqlc.arg(version), sqlc.narg(parent_id),
        sqlc.arg(environment), sqlc.arg(policy), sqlc.narg(branch), sqlc.arg(content), sqlc.arg(manifest_digest),
        sqlc.arg(stats), sqlc.arg(note), sqlc.arg(created_by), sqlc.arg(created_at))
ON CONFLICT (id) DO NOTHING;

-- name: GetRelease :one
SELECT * FROM release_releases WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id);

-- name: ListReleases :many
-- Newest first.
SELECT * FROM release_releases
WHERE project_id = sqlc.arg(project_id) AND version < sqlc.arg(before_version)
ORDER BY version DESC
LIMIT sqlc.arg(max_rows);

-- name: MaxReleaseVersion :one
SELECT coalesce(max(version), 0)::integer FROM release_releases WHERE project_id = sqlc.arg(project_id);

-- ── deployments ────────────────────────────────────────────────────

-- name: InsertDeployment :exec
INSERT INTO release_deployments (tenant_id, project_id, environment, number, release_id, previous_release_id,
                                 action, created_by, created_at, forced, force_reason)
VALUES (app_current_tenant(), sqlc.arg(project_id), sqlc.arg(environment), sqlc.arg(number), sqlc.arg(release_id),
        sqlc.narg(previous_release_id), sqlc.arg(action), sqlc.arg(created_by), sqlc.arg(created_at),
        sqlc.arg(forced), sqlc.arg(force_reason));

-- name: LastDeploymentNumber :one
SELECT coalesce(max(number), 0)::integer FROM release_deployments
WHERE project_id = sqlc.arg(project_id) AND environment = sqlc.arg(environment);

-- name: ListDeployments :many
-- Newest first.
SELECT * FROM release_deployments
WHERE project_id = sqlc.arg(project_id) AND environment = sqlc.arg(environment) AND number < sqlc.arg(before_number)
ORDER BY number DESC
LIMIT sqlc.arg(max_rows);

-- name: RollbackTarget :one
-- The newest release the environment served that is older than the one
-- it serves now.
SELECT r.* FROM release_deployments d
JOIN release_releases r ON r.id = d.release_id
WHERE d.project_id = sqlc.arg(project_id) AND d.environment = sqlc.arg(environment)
  AND r.version < sqlc.arg(current_version)
ORDER BY r.version DESC
LIMIT 1;

-- name: ServedInEnvironment :one
SELECT EXISTS (
    SELECT 1 FROM release_deployments
    WHERE project_id = sqlc.arg(project_id) AND environment = sqlc.arg(environment) AND release_id = sqlc.arg(release_id)
);

-- ── delivery keys ──────────────────────────────────────────────────

-- name: InsertDeliveryKey :execrows
INSERT INTO release_delivery_keys (id, tenant_id, project_id, key, name, environments, branches, created_by, created_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(project_id), sqlc.arg(key), sqlc.arg(name),
        sqlc.arg(environments)::text[], sqlc.arg(branches), sqlc.arg(created_by), sqlc.arg(created_at))
ON CONFLICT (id) DO NOTHING;

-- name: MarkKeyIndexed :exec
-- The key's index object was written in this format.
UPDATE release_delivery_keys SET index_version = sqlc.arg(index_version)
WHERE id = sqlc.arg(id) AND index_version <> sqlc.arg(index_version);

-- name: GetDeliveryKey :one
SELECT * FROM release_delivery_keys WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id);

-- name: LockDeliveryKey :one
SELECT * FROM release_delivery_keys WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id) FOR UPDATE;

-- name: ListDeliveryKeys :many
SELECT * FROM release_delivery_keys
WHERE project_id = sqlc.arg(project_id) AND id > sqlc.arg(after)
ORDER BY id
LIMIT sqlc.arg(max_rows);

-- name: ActiveDeliveryKeys :many
SELECT * FROM release_delivery_keys WHERE project_id = sqlc.arg(project_id) AND revoked_at IS NULL ORDER BY id;

-- name: SetDeliveryKeyScope :execrows
-- Replaces what an active key reads (RFC 0004 §4.3). index_version
-- drops to 1 (before scopes), so the key index task rewrites the object
-- if the write that follows the change doesn't reach storage.
UPDATE release_delivery_keys
SET environments = sqlc.arg(environments)::text[], branches = sqlc.arg(branches), index_version = 1
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id) AND revoked_at IS NULL;

-- name: RevokeDeliveryKey :execrows
UPDATE release_delivery_keys
SET revoked_at = sqlc.arg(revoked_at), revoked_by = sqlc.arg(revoked_by)
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id) AND revoked_at IS NULL;
