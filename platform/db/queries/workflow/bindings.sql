-- Tenant scope (db.TenantTx): RLS limits every statement to the current
-- tenant.
--
-- Bindings (RFC 0006 §2.3): which definition runs for a subject. The
-- precedence between them is the domain's (workflow/domain.Resolve);
-- these only store and list them, in creation order.

-- name: InsertBinding :one
INSERT INTO workflow_bindings (id, tenant_id, project_id, subject, locales, namespace, definition_id, created_by, created_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(project_id), sqlc.arg(subject), sqlc.arg(locales)::text[],
        sqlc.narg(namespace), sqlc.arg(definition_id), sqlc.arg(created_by), sqlc.arg(created_at))
ON CONFLICT DO NOTHING
RETURNING position;

-- name: GetBinding :one
SELECT * FROM workflow_bindings WHERE id = sqlc.arg(id);

-- name: ListBindings :many
-- A project's bindings for one subject, or for every subject, in
-- creation order: the order Resolve reads "later" from.
SELECT * FROM workflow_bindings
WHERE project_id = sqlc.arg(project_id)
  AND (sqlc.narg(subject)::text IS NULL OR subject = sqlc.narg(subject)::text)
ORDER BY position;

-- name: DeleteBinding :execrows
DELETE FROM workflow_bindings WHERE id = sqlc.arg(id);

-- name: DeleteBindingsOfDefinition :execrows
DELETE FROM workflow_bindings WHERE definition_id = sqlc.arg(definition_id);
