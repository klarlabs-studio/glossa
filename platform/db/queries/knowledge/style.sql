-- Tenant scope (db.TenantTx): RLS limits every statement to the current
-- tenant.

-- name: InsertStyleGuide :execrows
-- ON CONFLICT (id): a retried create with the same Idempotency-Key.
-- Another guide at the same scope violates knowledge_style_guides_scope.
INSERT INTO knowledge_style_guides (id, tenant_id, project_id, locale, namespace, name, fields, rules, version,
                                    created_by, created_at, updated_by, updated_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.narg(project_id), sqlc.narg(locale), sqlc.narg(namespace),
        sqlc.arg(name), sqlc.arg(fields), sqlc.arg(rules), sqlc.arg(version), sqlc.arg(created_by),
        sqlc.arg(created_at), sqlc.arg(updated_by), sqlc.arg(updated_at))
ON CONFLICT (id) DO NOTHING;

-- name: UpdateStyleGuide :execrows
UPDATE knowledge_style_guides
SET name = sqlc.arg(name), fields = sqlc.arg(fields), rules = sqlc.arg(rules), version = sqlc.arg(version),
    updated_by = sqlc.arg(updated_by), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND version = sqlc.arg(expected_version);

-- name: DeleteStyleGuide :exec
DELETE FROM knowledge_style_guides WHERE id = sqlc.arg(id);

-- name: GetStyleGuide :one
SELECT * FROM knowledge_style_guides WHERE id = sqlc.arg(id);

-- name: LockStyleGuide :one
SELECT * FROM knowledge_style_guides WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: ListStyleGuides :many
-- Guides in id order after the cursor, optionally of one project or
-- tenant-level only, and of one locale.
SELECT * FROM knowledge_style_guides
WHERE id > sqlc.arg(after)
  AND (NOT sqlc.arg(tenant_only)::boolean OR project_id IS NULL)
  AND (sqlc.narg(project_id)::uuid IS NULL OR project_id = sqlc.narg(project_id))
  AND (sqlc.narg(locale)::text IS NULL OR locale = sqlc.narg(locale))
  -- projects limits project-owned rows to a project-scoped caller's
  -- projects (RFC 0006 §4.1); tenant-wide rows stay. Filtered here so a
  -- page's size says nothing about the others.
  AND (sqlc.narg(projects)::uuid[] IS NULL OR project_id IS NULL OR project_id = ANY (sqlc.narg(projects)::uuid[]))
ORDER BY id
LIMIT sqlc.arg(max_rows);

-- name: StyleGuidesInScope :many
-- Every guide that may apply to a project (or to tenant-level text):
-- the domain picks the applicable ones and merges them.
SELECT * FROM knowledge_style_guides
WHERE project_id IS NULL OR project_id = sqlc.narg(project_id)
ORDER BY id;

-- name: InsertStyleGuideVersion :exec
INSERT INTO knowledge_style_guide_versions (tenant_id, style_guide_id, version, action, project_id, locale,
                                            namespace, name, fields, rules, author, created_at)
VALUES (app_current_tenant(), sqlc.arg(style_guide_id), sqlc.arg(version), sqlc.arg(action), sqlc.narg(project_id),
        sqlc.narg(locale), sqlc.narg(namespace), sqlc.arg(name), sqlc.arg(fields), sqlc.arg(rules),
        sqlc.arg(author), sqlc.arg(created_at));

-- name: ListStyleGuideVersions :many
SELECT * FROM knowledge_style_guide_versions
WHERE style_guide_id = sqlc.arg(style_guide_id) AND version < sqlc.arg(before)
ORDER BY version DESC
LIMIT sqlc.arg(max_rows);

-- name: DeleteProjectStyleGuides :exec
DELETE FROM knowledge_style_guides WHERE project_id = sqlc.arg(project_id);

-- name: DeleteProjectStyleGuideVersions :exec
DELETE FROM knowledge_style_guide_versions WHERE project_id = sqlc.arg(project_id);
