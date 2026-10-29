-- Tenant scope (db.TenantTx): RLS limits every statement to the current
-- tenant.
--
-- A check run is one evaluation of one ref against one policy version
-- (RFC 0005 §2.2 rule 3). Every surface reads the stored run rather than
-- recomputing it, so these queries are the only place a run is written.

-- name: InsertCheckRun :exec
-- A run and its verdict, written once. The counts are what the run
-- concluded, with the waivers that were live while it graded; nothing
-- rewrites them afterwards, because a verdict is history.
INSERT INTO quality_check_runs (id, tenant_id, project_id, ref, commit_sha, trigger, policy_version, layers,
                                errors, warnings, waived, conclusion, created_by, started_at, completed_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(project_id), sqlc.arg(ref), sqlc.arg(commit_sha),
        sqlc.arg(run_trigger), sqlc.arg(policy_version), sqlc.arg(layers)::text[], sqlc.arg(errors),
        sqlc.arg(warnings), sqlc.arg(waived), sqlc.narg(conclusion), sqlc.arg(created_by), sqlc.arg(started_at),
        sqlc.narg(completed_at));

-- name: GetCheckRun :one
SELECT * FROM quality_check_runs WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id);

-- name: ListCheckRuns :many
-- A page of a project's runs, newest first, narrowed by ref (a branch or
-- an environment), commit and conclusion. An empty filter matches
-- everything, so the three combine.
SELECT * FROM quality_check_runs
WHERE project_id = sqlc.arg(project_id)
  AND (sqlc.arg(ref)::text = '' OR ref = sqlc.arg(ref)::text)
  AND (sqlc.arg(commit_sha)::text = '' OR commit_sha = sqlc.arg(commit_sha)::text)
  AND (sqlc.arg(conclusion)::text = '' OR conclusion = sqlc.arg(conclusion)::text)
  AND (sqlc.arg(run_trigger)::text = '' OR trigger = sqlc.arg(run_trigger)::text)
  AND (sqlc.narg(after_started_at)::timestamptz IS NULL
       OR (started_at, id) < (sqlc.narg(after_started_at)::timestamptz, sqlc.narg(after_id)::uuid))
ORDER BY started_at DESC, id DESC
LIMIT sqlc.arg(max_rows);

-- name: HasCheckRunOf :one
-- Whether the project has any run by one of these triggers: what the
-- pull-request check asks to learn that a repository's CI runs `glossa
-- check` (RFC 0005 §14 decision 11). EXISTS stops at the first matching
-- row, and the project_id prefix of quality_check_runs_ref bounds the
-- walk to this project's runs within retention.
SELECT EXISTS (
    SELECT 1 FROM quality_check_runs
    WHERE project_id = sqlc.arg(project_id)
      AND trigger = ANY (sqlc.arg(run_triggers)::text[])
) AS found;

-- name: LatestCheckRun :one
-- The newest run matching the same filters: what a findings list reads
-- when the caller names a branch or a commit instead of a run.
SELECT * FROM quality_check_runs
WHERE project_id = sqlc.arg(project_id)
  AND (sqlc.arg(ref)::text = '' OR ref = sqlc.arg(ref)::text)
  AND (sqlc.arg(commit_sha)::text = '' OR commit_sha = sqlc.arg(commit_sha)::text)
  AND (sqlc.arg(conclusion)::text = '' OR conclusion = sqlc.arg(conclusion)::text)
  AND (sqlc.arg(run_trigger)::text = '' OR trigger = sqlc.arg(run_trigger)::text)
ORDER BY started_at DESC, id DESC
LIMIT 1;
