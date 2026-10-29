-- Tenant scope (db.TenantTx): RLS limits every statement to the current
-- tenant, except the queries marked system scope, which run as
-- glossa_system ("integration.jobs") across tenants.

-- ── jobs ───────────────────────────────────────────────────────────

-- A new job is due at once, on the database's clock.
-- name: InsertJob :execrows
INSERT INTO integration_jobs (id, tenant_id, direction, project_id, kind, format, mode, options, access, state,
                              file_name, max_attempts, available_at, created_by, created_at, updated_at, expires_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(direction), sqlc.narg(project_id), sqlc.arg(kind),
        sqlc.arg(format), sqlc.narg(mode), sqlc.arg(options), sqlc.arg(access), sqlc.arg(state), sqlc.arg(file_name),
        sqlc.arg(max_attempts), now(), sqlc.arg(created_by), sqlc.arg(created_at),
        sqlc.arg(updated_at), sqlc.arg(expires_at))
ON CONFLICT (id) DO NOTHING;

-- name: GetJob :one
SELECT * FROM integration_jobs WHERE id = sqlc.arg(id);

-- name: LockJob :one
SELECT * FROM integration_jobs WHERE id = sqlc.arg(id) FOR UPDATE;

-- LockClaimedJob locks a job the caller still holds (its claim token):
-- progress and settlement are fenced, so a worker whose lease expired
-- changes nothing.
-- name: LockClaimedJob :one
SELECT * FROM integration_jobs
WHERE id = sqlc.arg(id) AND claim_token = sqlc.arg(claim_token)::uuid AND state = 'running'
FOR UPDATE;

-- SaveJob stores a job's mutable fields. A job leaving running releases
-- its claim; a job queued now is due on the database's clock (the one
-- claims compare with), not the caller's.
-- name: SaveJob :exec
UPDATE integration_jobs
SET state = sqlc.arg(state), file_name = sqlc.arg(file_name), file_key = sqlc.narg(file_key),
    file_size = sqlc.narg(file_size), file_sha256 = sqlc.narg(file_sha256), content_type = sqlc.narg(content_type),
    fingerprint = sqlc.narg(fingerprint), reused_job_id = sqlc.narg(reused_job_id), summary = sqlc.arg(summary),
    total_items = sqlc.arg(total_items), processed_items = sqlc.arg(processed_items),
    failure_code = sqlc.narg(failure_code), failure_message = sqlc.narg(failure_message),
    cancel_requested = sqlc.arg(cancel_requested),
    available_at = CASE WHEN sqlc.arg(state)::text = 'queued' AND state <> 'queued' THEN now() ELSE available_at END,
    started_at = sqlc.narg(started_at), finished_at = sqlc.narg(finished_at), updated_at = sqlc.arg(updated_at),
    claim_token = CASE WHEN sqlc.arg(state)::text = 'running' THEN claim_token END
WHERE id = sqlc.arg(id);

-- name: RetryJob :exec
UPDATE integration_jobs
SET state = 'queued', available_at = now() + make_interval(secs => sqlc.arg(delay_seconds)::float8),
    summary = sqlc.arg(summary), total_items = sqlc.arg(total_items), processed_items = sqlc.arg(processed_items),
    failure_message = sqlc.narg(failure_message), started_at = sqlc.narg(started_at), claim_token = NULL,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- ListJobs lists a direction's jobs, newest first: a project's, or with
-- tenant_wide the ones without a project; kind narrows it.
-- name: ListJobs :many
SELECT * FROM integration_jobs
WHERE direction = sqlc.arg(direction)
  AND (sqlc.narg(project_id)::uuid IS NULL OR project_id = sqlc.narg(project_id)::uuid)
  AND (NOT sqlc.arg(tenant_wide)::boolean OR project_id IS NULL)
  AND (sqlc.narg(kind)::text IS NULL OR kind = sqlc.narg(kind)::text)
  AND (sqlc.narg(state)::text IS NULL OR state = sqlc.narg(state)::text)
  -- projects limits the jobs to a project-scoped caller's projects
  -- (RFC 0006 §4.1) — which leaves out tenant-wide ones, whose files
  -- hold every project's knowledge — in the query, so a page's size
  -- says nothing about the others.
  AND (sqlc.narg(projects)::uuid[] IS NULL OR project_id = ANY (sqlc.narg(projects)::uuid[]))
  AND (sqlc.narg(before_at)::timestamptz IS NULL OR (created_at, id) < (sqlc.narg(before_at)::timestamptz, sqlc.arg(before_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(max_rows)::int;

-- name: ReusableJob :one
SELECT * FROM integration_jobs
WHERE fingerprint = sqlc.arg(fingerprint) AND state = 'succeeded' AND reused_job_id IS NULL
  AND expires_at > now() AND id <> sqlc.arg(exclude)
ORDER BY created_at DESC
LIMIT 1;

-- name: ProjectJobs :many
SELECT * FROM integration_jobs WHERE project_id = sqlc.arg(project_id);

-- name: DeleteProjectJobs :exec
DELETE FROM integration_jobs WHERE project_id = sqlc.arg(project_id);

-- ── results ────────────────────────────────────────────────────────

-- PutItems stores a batch of results; a retried batch replaces its own.
-- name: PutItems :exec
INSERT INTO integration_job_items (job_id, tenant_id, seq, kind, item_key, locale, status, code, detail, line, col, ref)
SELECT sqlc.arg(job_id), app_current_tenant(), i.seq, i.kind, i.item_key, i.locale, i.status,
       NULLIF(i.code, ''), NULLIF(i.detail, ''), NULLIF(i.line, 0), NULLIF(i.col, 0), NULLIF(i.ref, '')
FROM (SELECT unnest(sqlc.arg(seqs)::int[]) AS seq, unnest(sqlc.arg(kinds)::text[]) AS kind,
             unnest(sqlc.arg(keys)::text[]) AS item_key, unnest(sqlc.arg(locales)::text[]) AS locale,
             unnest(sqlc.arg(statuses)::text[]) AS status, unnest(sqlc.arg(codes)::text[]) AS code,
             unnest(sqlc.arg(details)::text[]) AS detail, unnest(sqlc.arg(lines)::int[]) AS line,
             unnest(sqlc.arg(cols)::int[]) AS col, unnest(sqlc.arg(refs)::text[]) AS ref) AS i
ON CONFLICT (job_id, seq) DO UPDATE
SET kind = excluded.kind, item_key = excluded.item_key, locale = excluded.locale, status = excluded.status,
    code = excluded.code, detail = excluded.detail, line = excluded.line, col = excluded.col, ref = excluded.ref;

-- name: ListItems :many
SELECT * FROM integration_job_items
WHERE job_id = sqlc.arg(job_id) AND seq > sqlc.arg(after_seq)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(kind)::text IS NULL OR kind = sqlc.narg(kind)::text)
ORDER BY seq
LIMIT sqlc.arg(max_rows)::int;

-- ── system scope (integration.jobs) ────────────────────────────────

-- name: LockClaims :exec
SELECT pg_advisory_xact_lock(hashtext('glossa.integration.claim'));

-- ClaimJob leases the next due job whose tenant runs fewer than
-- max_running jobs: queued and due, or running with an expired lease
-- (its worker died). The caller serializes claims with an advisory
-- lock, so the per-tenant count is exact across replicas.
-- name: ClaimJob :one
WITH running AS (
    SELECT r.tenant_id, count(*) AS n
    FROM integration_jobs r
    WHERE r.state = 'running' AND r.available_at > now()
    GROUP BY r.tenant_id
), due AS (
    SELECT j.id
    FROM integration_jobs j
    LEFT JOIN running ON running.tenant_id = j.tenant_id
    WHERE j.state IN ('queued', 'running') AND j.available_at <= now()
      AND coalesce(running.n, 0) < sqlc.arg(max_running)::int
    ORDER BY j.available_at, j.id
    LIMIT 1
    FOR UPDATE OF j SKIP LOCKED
)
UPDATE integration_jobs e
SET state        = 'running',
    attempts     = e.attempts + 1,
    available_at = now() + make_interval(secs => sqlc.arg(lease_seconds)::float8),
    claim_token  = gen_random_uuid(),
    updated_at   = now()
FROM due
WHERE e.id = due.id
RETURNING e.id, e.tenant_id, e.claim_token, e.attempts;

-- name: ExpiredFiles :many
SELECT id, tenant_id, coalesce(file_key, '')::text AS file_key FROM integration_jobs
WHERE files_deleted_at IS NULL AND expires_at <= now() AND state NOT IN ('queued', 'running')
ORDER BY expires_at
LIMIT sqlc.arg(max_rows)::int
FOR UPDATE SKIP LOCKED;

-- name: MarkFilesDeleted :exec
UPDATE integration_jobs SET files_deleted_at = now(), updated_at = now()
WHERE id = ANY (sqlc.arg(ids)::uuid[]) AND files_deleted_at IS NULL;

-- name: ExpireUploads :execrows
UPDATE integration_jobs
SET state = 'failed', failure_code = 'upload_expired', failure_message = 'no file was uploaded in time',
    finished_at = now(), updated_at = now()
WHERE state = 'awaiting_upload' AND created_at < now() - make_interval(secs => sqlc.arg(window_seconds)::float8);
