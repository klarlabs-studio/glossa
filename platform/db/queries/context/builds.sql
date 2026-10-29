-- Tenant scope (db.TenantTx): RLS limits every statement to the current
-- tenant.

-- name: InsertBuild :execrows
-- An upload is idempotent by (application, commit, source, digest): a
-- repeat inserts nothing and the caller reads the first.
INSERT INTO context_builds (id, tenant_id, project_id, application_id, commit_sha, branch, on_default_branch, source,
                            tool_name, tool_version, digest, usage_count, created_by, created_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(project_id), sqlc.arg(application_id), sqlc.arg(commit_sha),
        sqlc.arg(branch), sqlc.arg(on_default_branch), sqlc.arg(source), sqlc.arg(tool_name), sqlc.arg(tool_version),
        sqlc.arg(digest), sqlc.arg(usage_count), sqlc.arg(created_by), sqlc.arg(created_at))
ON CONFLICT (tenant_id, application_id, commit_sha, source, digest) DO NOTHING;

-- name: GetBuildByUpload :one
SELECT * FROM context_builds
WHERE tenant_id = app_current_tenant() AND application_id = sqlc.arg(application_id)
  AND commit_sha = sqlc.arg(commit_sha) AND source = sqlc.arg(source) AND digest = sqlc.arg(digest);

-- name: GetBuild :one
SELECT * FROM context_builds WHERE id = sqlc.arg(id);

-- name: LockBuildCaptures :exec
-- Serializes the captures added to one build, so the per-build limit
-- holds under concurrent uploads (builds are immutable: no row lock).
SELECT pg_advisory_xact_lock(hashtextextended('context.build:' || sqlc.arg(build_id)::text, 0));

-- name: ListProjectBuildSummaries :many
-- Every build of a project, which retention bounds: what the current
-- views and retention are computed from.
SELECT id, application_id, source, branch, on_default_branch, created_at
FROM context_builds
WHERE project_id = sqlc.arg(project_id)
ORDER BY application_id, source, created_at DESC, id DESC;

-- name: ListBuildsByIDs :many
SELECT * FROM context_builds WHERE id = ANY(sqlc.arg(ids)::uuid[]) ORDER BY application_id, source;

-- name: DeleteBuilds :execrows
-- Usages, captures and regions go with their builds (ON DELETE CASCADE).
DELETE FROM context_builds WHERE id = ANY(sqlc.arg(ids)::uuid[]);

-- name: DeleteProjectBuilds :exec
DELETE FROM context_builds WHERE project_id = sqlc.arg(project_id);

-- name: DeleteApplicationBuilds :exec
DELETE FROM context_builds WHERE project_id = sqlc.arg(project_id) AND application_id = sqlc.arg(application_id);

-- name: ListProjectBuilds :many
-- A page of a project's builds, newest first, optionally of one
-- application, with the unknown keys each holds. Retention bounds a
-- project's builds, so the page reads few rows.
SELECT b.*, (SELECT count(*) FROM context_usages u WHERE u.build_id = b.id AND u.message_id IS NULL)::int AS unknown_keys
FROM context_builds b
WHERE b.project_id = sqlc.arg(project_id)
  AND (sqlc.narg(application_id)::uuid IS NULL OR b.application_id = sqlc.narg(application_id)::uuid)
  AND (sqlc.narg(after_created_at)::timestamptz IS NULL
       OR (b.created_at, b.id) < (sqlc.narg(after_created_at)::timestamptz, sqlc.narg(after_id)::uuid))
ORDER BY b.created_at DESC, b.id DESC
LIMIT sqlc.arg(max_rows);
