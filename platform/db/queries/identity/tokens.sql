-- Tenant scope (db.TenantTx) unless the name starts with System.

-- name: InsertToken :execrows
INSERT INTO identity_api_tokens (id, tenant_id, name, token_hash, hint, scopes, projects, created_by,
                                 created_at, expires_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(name), sqlc.arg(token_hash), sqlc.arg(hint),
        sqlc.arg(scopes), sqlc.arg(projects)::uuid[], sqlc.arg(created_by), sqlc.arg(created_at),
        sqlc.narg(expires_at))
ON CONFLICT (id) DO NOTHING;

-- name: GetToken :one
SELECT * FROM identity_api_tokens WHERE id = sqlc.arg(id);

-- name: ListTokens :many
SELECT * FROM identity_api_tokens
WHERE id > sqlc.arg(after)
ORDER BY id
LIMIT sqlc.arg(max_rows);

-- name: RevokeToken :execrows
UPDATE identity_api_tokens
SET revoked_at = sqlc.arg(revoked_at), revoked_by = sqlc.arg(revoked_by)
WHERE id = sqlc.arg(id) AND revoked_at IS NULL;

-- name: SystemGetTokenByHash :one
SELECT id, tenant_id, scopes, projects, expires_at, revoked_at, last_used_at
FROM identity_api_tokens
WHERE token_hash = sqlc.arg(token_hash);

-- name: SystemTouchToken :exec
-- At most one write a minute per token, however hot it is.
UPDATE identity_api_tokens SET last_used_at = sqlc.arg(at)
WHERE id = sqlc.arg(id)
  AND (last_used_at IS NULL OR last_used_at < sqlc.arg(at)::timestamptz - interval '1 minute');
