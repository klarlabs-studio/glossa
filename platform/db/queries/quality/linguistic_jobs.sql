-- Tenant scope (db.TenantTx): RLS limits every statement to the current
-- tenant.
--
-- Linguistic-QA jobs (RFC 0005 §3.8, migration 0038). The linguistic
-- layer is the one layer that needs a model, so it is a job and never a
-- check; these rows are that job's life.

-- name: InsertLinguisticJob :exec
INSERT INTO quality_linguistic_jobs (id, tenant_id, project_id, ref, scope, state, batch, created_by,
                                     created_at, updated_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(project_id), sqlc.arg(ref), sqlc.arg(scope),
        sqlc.arg(state), sqlc.arg(batch), sqlc.arg(created_by), sqlc.arg(created_at), sqlc.arg(created_at));

-- name: GetLinguisticJob :one
SELECT * FROM quality_linguistic_jobs
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id);

-- name: ListLinguisticJobs :many
-- A page of a project's jobs, newest first. The cursor is (created_at,
-- id), as every other newest-first page in the platform is.
SELECT * FROM quality_linguistic_jobs
WHERE project_id = sqlc.arg(project_id)
  AND (sqlc.narg(state)::text IS NULL OR state = sqlc.narg(state)::text)
  AND (
      sqlc.narg(after_at)::timestamptz IS NULL
      OR (created_at, id) < (sqlc.narg(after_at)::timestamptz, sqlc.narg(after_id)::uuid)
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(max_rows);

-- name: UpdateLinguisticJob :execrows
-- The job's progress. `state` is guarded: a job that has already
-- finished is never moved again, so a late poll of a cancelled job
-- cannot resurrect it, and two readers reconciling the same job settle
-- it once.
UPDATE quality_linguistic_jobs
SET state             = sqlc.arg(state),
    batch             = sqlc.arg(batch),
    check_run_id      = sqlc.narg(check_run_id),
    findings          = sqlc.arg(findings),
    skipped_sensitive = sqlc.arg(skipped_sensitive),
    reviewed          = sqlc.arg(reviewed),
    failure_code      = sqlc.arg(failure_code),
    last_error        = sqlc.arg(last_error),
    updated_at        = sqlc.arg(updated_at),
    started_at        = sqlc.narg(started_at),
    finished_at       = sqlc.narg(finished_at)
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id)
  AND state IN ('queued', 'running');
