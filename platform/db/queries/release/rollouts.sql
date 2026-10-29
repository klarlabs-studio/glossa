-- Tenant scope (db.TenantTx): staged rollouts (RFC 0006 §5.2). Every
-- change happens under the environment's row lock, so a rollout and its
-- environment's pointer never change apart.

-- name: InsertRollout :execrows
-- At most one active rollout per environment (release_rollouts_one_active):
-- a second is not inserted, and the caller reports the active one.
INSERT INTO release_rollouts (id, tenant_id, project_id, environment, candidate_release_id, stable_release_id, percent,
                              salt, status, max_duration_seconds, expires_at, forced, force_reason, started_by,
                              started_at, updated_at, version)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(project_id), sqlc.arg(environment), sqlc.arg(candidate_release_id),
        sqlc.arg(stable_release_id), sqlc.arg(percent), sqlc.arg(salt), sqlc.arg(status), sqlc.arg(max_duration_seconds),
        sqlc.arg(expires_at), sqlc.arg(forced), sqlc.arg(force_reason), sqlc.arg(started_by), sqlc.arg(started_at),
        sqlc.arg(updated_at), sqlc.arg(version))
ON CONFLICT DO NOTHING;

-- name: GetRollout :one
SELECT * FROM release_rollouts WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id);

-- name: LockActiveRollout :one
SELECT * FROM release_rollouts
WHERE project_id = sqlc.arg(project_id) AND environment = sqlc.arg(environment) AND status = 'active'
FOR UPDATE;

-- name: GetActiveRollout :one
SELECT * FROM release_rollouts
WHERE project_id = sqlc.arg(project_id) AND environment = sqlc.arg(environment) AND status = 'active';

-- name: ListRollouts :many
-- An environment's rollouts, newest first, after the rollout `after`
-- (a page token) when given. An idempotency-keyed rollout's id is not
-- time-ordered, so the cursor is the row's (started_at, id).
SELECT * FROM release_rollouts r
WHERE r.project_id = sqlc.arg(project_id) AND r.environment = sqlc.arg(environment)
  AND (sqlc.narg(after)::uuid IS NULL OR (r.started_at, r.id) < (
        SELECT a.started_at, a.id FROM release_rollouts a
        WHERE a.project_id = sqlc.arg(project_id) AND a.id = sqlc.narg(after)::uuid))
ORDER BY r.started_at DESC, r.id DESC
LIMIT sqlc.arg(max_rows)::int;

-- name: UpdateRollout :execrows
UPDATE release_rollouts
SET percent = sqlc.arg(percent), status = sqlc.arg(status), updated_at = sqlc.arg(updated_at), version = sqlc.arg(version),
    ended_by = sqlc.narg(ended_by), ended_at = sqlc.narg(ended_at), end_reason = sqlc.narg(end_reason)
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id) AND version = sqlc.arg(expected_version);
