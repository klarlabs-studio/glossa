-- System scope context.retention (db.SystemTx as glossa_system):
-- migration 0012 opens context_builds' tenant_id and project_id to it,
-- read-only, and nothing else.

-- name: ListProjectsWithBuilds :many
SELECT DISTINCT tenant_id, project_id FROM context_builds ORDER BY tenant_id, project_id;
