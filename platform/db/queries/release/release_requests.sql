-- Tenant scope (db.TenantTx): release requests, the publishes and
-- promotes an environment's approval requirement holds (RFC 0006 §5.1).

-- name: InsertReleaseRequest :exec
INSERT INTO release_requests (id, tenant_id, project_id, environment, release_id, action, requester, approval,
                              gate_met, gate_unmet, forced, force_reason, state, version, created_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(project_id), sqlc.arg(environment), sqlc.arg(release_id),
        sqlc.arg(action), sqlc.arg(requester), sqlc.arg(approval), sqlc.arg(gate_met), sqlc.arg(gate_unmet),
        sqlc.arg(forced), sqlc.arg(force_reason), sqlc.arg(state), sqlc.arg(version), sqlc.arg(created_at));

-- name: GetReleaseRequest :one
SELECT * FROM release_requests WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id);

-- name: LockReleaseRequest :one
SELECT * FROM release_requests WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id) FOR UPDATE;

-- name: LockPendingReleaseRequest :one
-- The environment's one open request, locked.
SELECT * FROM release_requests
WHERE project_id = sqlc.arg(project_id) AND environment = sqlc.arg(environment) AND state = 'pending'
FOR UPDATE;

-- name: ReleaseRequestFor :one
-- The newest request to deploy release into environment: what a
-- replayed publish answers with.
SELECT * FROM release_requests
WHERE project_id = sqlc.arg(project_id) AND release_id = sqlc.arg(release_id) AND environment = sqlc.arg(environment)
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: ListReleaseRequests :many
-- A project's requests, newest first, optionally in one environment and
-- one state.
SELECT * FROM release_requests
WHERE project_id = sqlc.arg(project_id)
  AND (sqlc.arg(environment)::text = '' OR environment = sqlc.arg(environment)::text)
  AND (sqlc.arg(state)::text = '' OR state = sqlc.arg(state)::text)
  AND (sqlc.narg(before)::uuid IS NULL OR id < sqlc.narg(before)::uuid)
ORDER BY id DESC
LIMIT sqlc.arg(max_rows);

-- name: UpdateReleaseRequest :execrows
UPDATE release_requests
SET state = sqlc.arg(state), version = sqlc.arg(version), decided_by = sqlc.narg(decided_by),
    decided_at = sqlc.narg(decided_at), reason = sqlc.arg(reason)
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id) AND version = sqlc.arg(expected_version);
