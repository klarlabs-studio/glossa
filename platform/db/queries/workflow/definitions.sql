-- Tenant scope (db.TenantTx): RLS limits every statement to the current
-- tenant.
--
-- Workflow definitions and their immutable versions (RFC 0006 §2.3).
-- A version is never updated: a save appends n+1 and bumps the
-- definition's `latest` in the same transaction.

-- name: InsertDefinition :exec
INSERT INTO workflow_definitions (id, tenant_id, project_id, name, subject, latest, created_by, created_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.narg(project_id), sqlc.arg(name), sqlc.arg(subject), 1,
        sqlc.arg(created_by), sqlc.arg(created_at));

-- name: GetDefinition :one
SELECT * FROM workflow_definitions WHERE id = sqlc.arg(id);

-- name: GetDefinitionForUpdate :one
-- Locks the definition while a version is appended, so two saves
-- serialise on it rather than racing for the same number.
SELECT * FROM workflow_definitions WHERE id = sqlc.arg(id) AND deleted_at IS NULL FOR UPDATE;

-- name: ListDefinitions :many
-- The live definitions a project can bind — the tenant's and its own —
-- or, without a project, every live one. Ordered by name.
SELECT * FROM workflow_definitions
WHERE deleted_at IS NULL
  AND (sqlc.narg(project_id)::uuid IS NULL OR project_id IS NULL OR project_id = sqlc.narg(project_id)::uuid)
ORDER BY name, project_id NULLS FIRST;

-- name: CountLiveDefinitions :one
SELECT count(*) FROM workflow_definitions WHERE deleted_at IS NULL;

-- name: SetLatestVersion :execrows
UPDATE workflow_definitions SET latest = sqlc.arg(latest)
WHERE id = sqlc.arg(id) AND latest = sqlc.arg(latest) - 1 AND deleted_at IS NULL;

-- name: MarkDefinitionDeleted :execrows
UPDATE workflow_definitions SET deleted_at = sqlc.arg(deleted_at)
WHERE id = sqlc.arg(id) AND deleted_at IS NULL;

-- name: InsertVersion :execrows
-- The unique key on (definition_id, version) is the last word on
-- monotonicity: a save that lost a race inserts nothing.
INSERT INTO workflow_definition_versions (id, tenant_id, definition_id, version, schema, document, created_by, created_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(definition_id), sqlc.arg(version), sqlc.arg(schema),
        sqlc.arg(document), sqlc.arg(created_by), sqlc.arg(created_at))
ON CONFLICT (definition_id, version) DO NOTHING;

-- name: GetVersion :one
SELECT * FROM workflow_definition_versions
WHERE definition_id = sqlc.arg(definition_id) AND version = sqlc.arg(version);

-- name: ListVersions :many
-- A definition's versions, newest first.
SELECT * FROM workflow_definition_versions
WHERE definition_id = sqlc.arg(definition_id)
ORDER BY version DESC;
