-- Tenant scope (db.TenantTx): RLS limits every statement to the current
-- tenant, so none of these filter on tenant_id.

-- name: InsertProject :execrows
-- ON CONFLICT (id) makes a retried idempotent create a no-op; a slug
-- clash still fails on the (tenant_id, slug) constraint.
INSERT INTO catalog_projects (id, tenant_id, slug, name, source_locale, settings, version,
                              created_by, created_at, updated_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(slug), sqlc.arg(name), sqlc.arg(source_locale),
        sqlc.arg(settings), sqlc.arg(version), sqlc.arg(created_by), sqlc.arg(created_at), sqlc.arg(created_at))
ON CONFLICT (id) DO NOTHING;

-- name: GetProject :one
SELECT * FROM catalog_projects WHERE id = sqlc.arg(id);

-- name: LockProject :one
SELECT * FROM catalog_projects WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: ListProjects :many
-- only limits the page to ids: a project-scoped caller's projects
-- (RFC 0006 §4.1), filtered here so a page's size never betrays the
-- projects outside the scope.
SELECT * FROM catalog_projects
WHERE id > sqlc.arg(after)
  AND (NOT sqlc.arg(filter_ids)::boolean OR id = ANY (sqlc.arg(ids)::uuid[]))
ORDER BY id
LIMIT sqlc.arg(max_rows);

-- name: UpdateProject :execrows
UPDATE catalog_projects
SET slug = sqlc.arg(slug), name = sqlc.arg(name), settings = sqlc.arg(settings),
    version = sqlc.arg(version), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND version = sqlc.arg(expected_version);

-- name: DeleteProject :execrows
DELETE FROM catalog_projects WHERE id = sqlc.arg(id);

-- name: InsertApplication :execrows
INSERT INTO catalog_applications (id, tenant_id, project_id, slug, name, platform, version,
                                  created_by, created_at, updated_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(project_id), sqlc.arg(slug), sqlc.arg(name),
        sqlc.arg(platform), sqlc.arg(version), sqlc.arg(created_by), sqlc.arg(created_at), sqlc.arg(created_at))
ON CONFLICT (id) DO NOTHING;

-- name: GetApplication :one
SELECT * FROM catalog_applications WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id);

-- name: LockApplication :one
SELECT * FROM catalog_applications
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id)
FOR UPDATE;

-- name: ListApplications :many
SELECT * FROM catalog_applications
WHERE project_id = sqlc.arg(project_id) AND id > sqlc.arg(after)
ORDER BY id
LIMIT sqlc.arg(max_rows);

-- name: UpdateApplication :execrows
UPDATE catalog_applications
SET slug = sqlc.arg(slug), name = sqlc.arg(name), platform = sqlc.arg(platform),
    version = sqlc.arg(version), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND version = sqlc.arg(expected_version);

-- name: DeleteApplication :execrows
DELETE FROM catalog_applications WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id);
