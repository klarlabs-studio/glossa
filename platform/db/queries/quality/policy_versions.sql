-- Tenant scope (db.TenantTx): RLS limits every statement to the current
-- tenant.
--
-- The check policy's version history (RFC 0005 §4.3). The live document
-- is Catalog's (settings.check_policy); this is the record of how it got
-- there — one append-only row per saved version, with who wrote it and
-- when. Nothing updates a row: a version is history.

-- name: InsertPolicyVersion :execrows
-- One saved version. The unique index on (project_id, version) is what
-- makes the version monotonic under concurrency: two saves racing for
-- the same number cannot both land, and the loser is told to read the
-- policy again rather than silently overwriting the winner.
INSERT INTO quality_policy_versions (id, tenant_id, project_id, version, document, created_by, created_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(project_id), sqlc.arg(version), sqlc.arg(document),
        sqlc.arg(created_by), sqlc.arg(created_at))
ON CONFLICT (project_id, version) DO NOTHING;

-- name: GetPolicyVersion :one
SELECT * FROM quality_policy_versions
WHERE project_id = sqlc.arg(project_id) AND version = sqlc.arg(version);

-- name: ListPolicyVersions :many
-- A page of a project's policy versions, newest first. The cursor is
-- the version itself: it is monotonic and unique per project, so it
-- needs no tiebreaker.
SELECT * FROM quality_policy_versions
WHERE project_id = sqlc.arg(project_id)
  AND (sqlc.narg(after_version)::integer IS NULL OR version < sqlc.narg(after_version)::integer)
ORDER BY version DESC
LIMIT sqlc.arg(max_rows);
