-- CI tokens minted from GitHub Actions ID tokens (RFC 0004 §6.3).
-- Tenant scope (db.TenantTx) unless the name starts with System.

-- name: InsertCIToken :exec
INSERT INTO identity_ci_tokens (id, tenant_id, project_id, token_hash, permissions,
                                repository_id, repository_owner_id, repository, git_ref, commit_sha,
                                event_name, workflow_ref, run_id, runner_environment,
                                created_at, expires_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(project_id), sqlc.arg(token_hash),
        sqlc.arg(permissions), sqlc.arg(repository_id), sqlc.arg(repository_owner_id),
        sqlc.arg(repository), sqlc.arg(git_ref), sqlc.arg(commit_sha), sqlc.arg(event_name),
        sqlc.arg(workflow_ref), sqlc.arg(run_id), sqlc.arg(runner_environment),
        sqlc.arg(created_at), sqlc.arg(expires_at));

-- name: SystemGetCITokenByHash :one
SELECT id, tenant_id, project_id, permissions, expires_at
FROM identity_ci_tokens
WHERE token_hash = sqlc.arg(token_hash);

-- name: SystemTouchCIToken :exec
-- At most one write a minute per token, however many calls a job makes.
UPDATE identity_ci_tokens SET last_used_at = sqlc.arg(at)
WHERE id = sqlc.arg(id)
  AND (last_used_at IS NULL OR last_used_at < sqlc.arg(at)::timestamptz - interval '1 minute');

-- name: SystemPurgeExpiredCITokens :execrows
DELETE FROM identity_ci_tokens WHERE expires_at < sqlc.arg(at);
