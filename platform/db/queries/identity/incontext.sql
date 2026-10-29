-- Preview origins and in-context grants (RFC 0004 §5.2).
-- Tenant scope (db.TenantTx) unless the name starts with System.

-- name: InsertPreviewOrigin :execrows
INSERT INTO identity_preview_origins (id, tenant_id, project_id, origin, label, created_by, created_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(project_id), sqlc.arg(origin), sqlc.arg(label),
        sqlc.arg(created_by), sqlc.arg(created_at))
ON CONFLICT (project_id, origin) DO NOTHING;

-- name: ListPreviewOrigins :many
SELECT * FROM identity_preview_origins
WHERE project_id = sqlc.arg(project_id)
ORDER BY origin;

-- name: GetPreviewOrigin :one
SELECT * FROM identity_preview_origins WHERE id = sqlc.arg(id);

-- name: GetPreviewOriginFor :one
SELECT * FROM identity_preview_origins
WHERE project_id = sqlc.arg(project_id) AND origin = sqlc.arg(origin);

-- name: CountPreviewOrigins :one
SELECT count(*) FROM identity_preview_origins WHERE project_id = sqlc.arg(project_id);

-- name: DeletePreviewOrigin :execrows
DELETE FROM identity_preview_origins WHERE id = sqlc.arg(id);

-- name: DeleteGrantsForOrigin :execrows
-- Unregistering an origin ends the editor sessions minted for it, so
-- access stops now rather than when the last 15-minute grant expires.
DELETE FROM identity_in_context_grants
WHERE project_id = sqlc.arg(project_id) AND origin = sqlc.arg(origin);

-- name: InsertGrant :exec
INSERT INTO identity_in_context_grants (id, tenant_id, project_id, person_id, token_hash, origin,
                                        permissions, created_at, expires_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(project_id), sqlc.arg(person_id),
        sqlc.arg(token_hash), sqlc.arg(origin), sqlc.arg(permissions), sqlc.arg(created_at),
        sqlc.arg(expires_at));

-- name: SystemGetGrantByHash :one
SELECT id, tenant_id, project_id, person_id, origin, permissions, expires_at
FROM identity_in_context_grants
WHERE token_hash = sqlc.arg(token_hash);

-- name: SystemTouchGrant :exec
-- At most one write a minute per grant, however hot it is.
UPDATE identity_in_context_grants SET last_used_at = sqlc.arg(at)
WHERE id = sqlc.arg(id)
  AND (last_used_at IS NULL OR last_used_at < sqlc.arg(at)::timestamptz - interval '1 minute');

-- name: SystemOriginRegistered :one
-- A CORS preflight carries no credentials, so the origin is checked
-- against every project's registrations before any tenant is known.
SELECT EXISTS (SELECT FROM identity_preview_origins WHERE origin = sqlc.arg(origin));

-- name: SystemPurgeExpiredGrants :execrows
DELETE FROM identity_in_context_grants WHERE expires_at < sqlc.arg(at);
