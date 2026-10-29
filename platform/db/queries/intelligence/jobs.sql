-- ── fills ──────────────────────────────────────────────────────────

-- name: InsertFill :exec
INSERT INTO intelligence_fills (id, tenant_id, project_id, trigger, locales, filter, requested_by, created_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(project_id), sqlc.arg(trigger), sqlc.arg(locales)::text[],
        sqlc.arg(filter), sqlc.arg(requested_by), sqlc.arg(created_at));

-- name: FinishFill :exec
UPDATE intelligence_fills
SET jobs_created = sqlc.arg(jobs_created), jobs_existing = sqlc.arg(jobs_existing), skipped = sqlc.arg(skipped)
WHERE id = sqlc.arg(id);

-- name: GetFill :one
SELECT * FROM intelligence_fills WHERE id = sqlc.arg(id);

-- name: FillJobCounts :many
SELECT state, count(*)::int AS n FROM intelligence_jobs WHERE fill_id = sqlc.arg(fill_id) GROUP BY state;

-- ── jobs (tenant scope) ────────────────────────────────────────────

-- EnqueueJob inserts a job unless one exists for the same message,
-- locale, source revision and knowledge fingerprint. An explicit fill
-- (requeue) brings back such a job that failed, died or was cancelled;
-- a forced one (RFC 0004 §5.3) brings back a job that succeeded or was
-- skipped as well, because asking again for text that is already there
-- is the whole request. Anything else finds the existing job unchanged
-- and returns no row.
-- name: EnqueueJob :one
INSERT INTO intelligence_jobs (
    id, tenant_id, project_id, message_id, message_key, namespace, locale, source_revision,
    knowledge_fingerprint, trigger, fill_id, forced, state, attempts, max_attempts, available_at,
    created_by, created_at, updated_at
) VALUES (
    sqlc.arg(id), app_current_tenant(), sqlc.arg(project_id), sqlc.arg(message_id), sqlc.arg(message_key),
    sqlc.arg(namespace), sqlc.arg(locale), sqlc.arg(source_revision), sqlc.arg(knowledge_fingerprint),
    sqlc.arg(trigger), sqlc.narg(fill_id), sqlc.arg(forced), 'queued', 0, sqlc.arg(max_attempts), now(),
    sqlc.arg(created_by), sqlc.arg(created_at), sqlc.arg(created_at)
)
ON CONFLICT (tenant_id, message_id, locale, source_revision, knowledge_fingerprint) DO UPDATE
SET state = 'queued', attempts = 0, available_at = now(), failure_code = NULL,
    last_error = NULL, claim_token = NULL, fill_id = excluded.fill_id, trigger = excluded.trigger,
    forced = excluded.forced, finished_at = NULL, updated_at = excluded.updated_at
WHERE (sqlc.arg(requeue)::boolean AND intelligence_jobs.state IN ('failed', 'dead', 'cancelled'))
   OR (sqlc.arg(forced)::boolean AND intelligence_jobs.state <> 'running')
RETURNING id;

-- name: GetJobByKey :one
SELECT * FROM intelligence_jobs
WHERE message_id = sqlc.arg(message_id) AND locale = sqlc.arg(locale)
  AND source_revision = sqlc.arg(source_revision) AND knowledge_fingerprint = sqlc.arg(knowledge_fingerprint);

-- name: JobStatesByKey :many
-- The state of each job that exists for one of the given keys (message,
-- locale, source revision, knowledge fingerprint): a fill preview's
-- "already queued or done", in one round trip per page.
SELECT j.message_id, j.locale, j.state
FROM intelligence_jobs j
JOIN (SELECT unnest(sqlc.arg(message_ids)::uuid[]) AS message_id, unnest(sqlc.arg(locales)::text[]) AS locale,
             unnest(sqlc.arg(source_revisions)::int[]) AS source_revision,
             unnest(sqlc.arg(fingerprints)::text[]) AS fingerprint) AS k
  ON j.message_id = k.message_id AND j.locale = k.locale AND j.source_revision = k.source_revision
 AND j.knowledge_fingerprint = k.fingerprint;

-- name: GetJob :one
SELECT * FROM intelligence_jobs WHERE id = sqlc.arg(id);

-- LockClaimedJob locks a job the caller still holds (its claim token):
-- settlement is fenced, so a worker whose lease expired changes nothing.
-- name: LockClaimedJob :one
SELECT * FROM intelligence_jobs
WHERE id = sqlc.arg(id) AND claim_token = sqlc.arg(claim_token)::uuid AND state = 'running'
FOR UPDATE;

-- name: ListJobs :many
SELECT * FROM intelligence_jobs
WHERE (sqlc.narg(project_id)::uuid IS NULL OR project_id = sqlc.narg(project_id)::uuid)
  AND (sqlc.narg(state)::text IS NULL OR state = sqlc.narg(state)::text)
  AND (sqlc.narg(locale)::text IS NULL OR locale = sqlc.narg(locale)::text)
  AND (sqlc.narg(fill_id)::uuid IS NULL OR fill_id = sqlc.narg(fill_id)::uuid)
  AND (sqlc.narg(message_id)::uuid IS NULL OR message_id = sqlc.narg(message_id)::uuid)
  -- projects limits the rows to a project-scoped caller's projects
  -- (RFC 0006 §4.1), in the query so a page's size says nothing about
  -- the others.
  AND (sqlc.narg(projects)::uuid[] IS NULL OR project_id = ANY (sqlc.narg(projects)::uuid[]))
  AND (sqlc.narg(before_at)::timestamptz IS NULL OR (created_at, id) < (sqlc.narg(before_at)::timestamptz, sqlc.arg(before_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(max_rows)::int;

-- name: CancelJob :execrows
UPDATE intelligence_jobs
SET state = 'cancelled', finished_at = sqlc.arg(now), updated_at = sqlc.arg(now), failure_code = NULL,
    last_error = 'cancelled by ' || sqlc.arg(by)::text
WHERE id = sqlc.arg(id) AND state = 'queued';

-- name: CancelFillJobs :execrows
UPDATE intelligence_jobs
SET state = 'cancelled', finished_at = sqlc.arg(now), updated_at = sqlc.arg(now),
    last_error = 'cancelled by ' || sqlc.arg(by)::text
WHERE fill_id = sqlc.arg(fill_id) AND state = 'queued';

-- name: FinishJob :exec
UPDATE intelligence_jobs
SET state = sqlc.arg(state), failure_code = sqlc.narg(failure_code), last_error = sqlc.narg(last_error),
    suggestion_id = sqlc.narg(suggestion_id), audit = sqlc.narg(audit), claim_token = NULL,
    finished_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id);

-- name: RetryJob :exec
UPDATE intelligence_jobs
SET state = 'queued', available_at = now() + make_interval(secs => sqlc.arg(delay_seconds)::float8), failure_code = sqlc.narg(failure_code),
    last_error = sqlc.narg(last_error), audit = sqlc.narg(audit), claim_token = NULL, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id);

-- name: DeleteProjectJobs :exec
DELETE FROM intelligence_jobs WHERE project_id = sqlc.arg(project_id);

-- name: DeleteProjectFills :exec
DELETE FROM intelligence_fills WHERE project_id = sqlc.arg(project_id);

-- name: DeleteProjectSuggestions :exec
DELETE FROM intelligence_suggestions WHERE project_id = sqlc.arg(project_id);

-- name: DeleteProjectDisclosures :exec
DELETE FROM intelligence_disclosures WHERE project_id = sqlc.arg(project_id);

-- name: DeleteProjectSettings :exec
DELETE FROM intelligence_project_settings WHERE project_id = sqlc.arg(project_id);

-- name: DeleteProjectRouting :exec
DELETE FROM intelligence_routing_policies WHERE project_id = sqlc.arg(project_id);

-- ── claiming (system scope "intelligence.jobs") ────────────────────

-- ClaimJob leases the next due job whose tenant is under its concurrency
-- cap: queued and due, or running with an expired lease (its worker
-- died). The caller serializes claims with an advisory lock, so the
-- per-tenant count is exact across replicas; SKIP LOCKED keeps workers
-- off each other's rows.
-- name: ClaimJob :one
WITH running AS (
    SELECT r.tenant_id, count(*) AS n
    FROM intelligence_jobs r
    WHERE r.state = 'running' AND r.available_at > now()
    GROUP BY r.tenant_id
), due AS (
    SELECT j.id
    FROM intelligence_jobs j
    LEFT JOIN running ON running.tenant_id = j.tenant_id
    LEFT JOIN intelligence_settings s ON s.tenant_id = j.tenant_id
    WHERE j.state IN ('queued', 'running') AND j.available_at <= now()
      AND coalesce(running.n, 0) < coalesce(s.max_concurrent_jobs, 4)
    ORDER BY j.available_at, j.id
    LIMIT 1
    FOR UPDATE OF j SKIP LOCKED
)
UPDATE intelligence_jobs e
SET state        = 'running',
    attempts     = e.attempts + 1,
    available_at = now() + make_interval(secs => sqlc.arg(lease_seconds)::float8),
    claim_token  = gen_random_uuid(),
    started_at   = coalesce(e.started_at, now()),
    updated_at   = now()
FROM due
WHERE e.id = due.id
RETURNING e.id, e.tenant_id, e.claim_token, e.attempts, e.max_attempts;

-- name: LockClaims :exec
SELECT pg_advisory_xact_lock(hashtext('glossa.intelligence.claim'));

-- name: QueueDepth :many
SELECT state, count(*)::bigint AS n FROM intelligence_jobs WHERE state IN ('queued', 'running') GROUP BY state;
