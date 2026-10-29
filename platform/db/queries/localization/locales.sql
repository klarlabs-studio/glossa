-- Tenant scope (db.TenantTx): RLS limits every statement to the current
-- tenant.

-- name: InsertLocale :execrows
-- A locale's identity is its code within the project, so adding one
-- twice is a no-op.
INSERT INTO localization_locales (tenant_id, project_id, code, is_source, created_by, created_at)
VALUES (app_current_tenant(), sqlc.arg(project_id), sqlc.arg(code), sqlc.arg(is_source),
        sqlc.arg(created_by), sqlc.arg(created_at))
ON CONFLICT (project_id, code) DO NOTHING;

-- name: GetLocale :one
SELECT * FROM localization_locales WHERE project_id = sqlc.arg(project_id) AND code = sqlc.arg(code);

-- name: ListLocales :many
SELECT * FROM localization_locales
WHERE project_id = sqlc.arg(project_id) AND code > sqlc.arg(after)
ORDER BY code
LIMIT sqlc.arg(max_rows);

-- name: AllLocales :many
SELECT * FROM localization_locales WHERE project_id = sqlc.arg(project_id) ORDER BY code;

-- name: DeleteLocale :execrows
DELETE FROM localization_locales
WHERE project_id = sqlc.arg(project_id) AND code = sqlc.arg(code) AND NOT is_source;

-- name: GetFallbackGraph :one
SELECT * FROM localization_fallback_graphs WHERE project_id = sqlc.arg(project_id);

-- name: LockFallbackGraph :one
SELECT * FROM localization_fallback_graphs WHERE project_id = sqlc.arg(project_id) FOR UPDATE;

-- name: InsertFallbackGraph :execrows
INSERT INTO localization_fallback_graphs (tenant_id, project_id, edges, version, updated_by, updated_at)
VALUES (app_current_tenant(), sqlc.arg(project_id), sqlc.arg(edges), sqlc.arg(version),
        sqlc.arg(updated_by), sqlc.arg(updated_at))
ON CONFLICT (project_id) DO NOTHING;

-- name: UpdateFallbackGraph :execrows
UPDATE localization_fallback_graphs
SET edges = sqlc.arg(edges), version = sqlc.arg(version), updated_by = sqlc.arg(updated_by),
    updated_at = sqlc.arg(updated_at)
WHERE project_id = sqlc.arg(project_id) AND version = sqlc.arg(expected_version);

-- name: DeleteProjectLocales :exec
DELETE FROM localization_locales WHERE project_id = sqlc.arg(project_id);

-- name: DeleteProjectFallbackGraph :exec
DELETE FROM localization_fallback_graphs WHERE project_id = sqlc.arg(project_id);
