-- Tenant scope (db.TenantTx): debounced publish requests of branch
-- environments (RFC 0004 §4.2).

-- name: LockPublishRequest :one
SELECT * FROM release_publish_requests
WHERE project_id = sqlc.arg(project_id) AND environment = sqlc.arg(environment)
FOR UPDATE;

-- name: SavePublishRequest :exec
INSERT INTO release_publish_requests (tenant_id, project_id, environment, request_id, first_requested_at, not_before,
                                      requested_by)
VALUES (app_current_tenant(), sqlc.arg(project_id), sqlc.arg(environment), sqlc.arg(request_id),
        sqlc.arg(first_requested_at), sqlc.arg(not_before), sqlc.arg(requested_by))
ON CONFLICT (project_id, environment) DO UPDATE
SET request_id = excluded.request_id, first_requested_at = excluded.first_requested_at,
    not_before = excluded.not_before, requested_by = excluded.requested_by;

-- name: DeletePublishRequest :execrows
-- Only the request that was published: a newer one keeps waiting.
DELETE FROM release_publish_requests
WHERE project_id = sqlc.arg(project_id) AND environment = sqlc.arg(environment) AND request_id = sqlc.arg(request_id);
