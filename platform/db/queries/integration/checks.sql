-- The Glossa PR check and its sticky comment (RFC 0004 §6.4),
-- migration 0021.
--
-- System scope (db.SystemTx, "integration.github") throughout: the
-- check worker is a queue worker across tenants, like the webhook
-- inbox. It claims the oldest due row whatever tenant it belongs to,
-- and only then enters that tenant's scope to read the catalog and
-- render the report.

-- OpenCheck records a pull request's check and makes it due now. A
-- pull_request event for a repository we already track keeps the row —
-- the sticky comment lives on it — and moves it to the new head SHA,
-- which starts the thirty-minute wait again and discards the check
-- runs, because a check run belongs to one commit.
-- name: OpenCheck :one
INSERT INTO integration_github_checks (id, tenant_id, installation_id, repository_id, pull_request, branch,
                                       head_sha, from_fork, opened_at, state, requested_at, available_at,
                                       updated_at)
VALUES (sqlc.arg(id), sqlc.arg(tenant_id), sqlc.arg(installation_id), sqlc.arg(repository_id),
        sqlc.arg(pull_request), sqlc.arg(branch), sqlc.arg(head_sha), sqlc.arg(from_fork),
        sqlc.narg(opened_at), 'queued', sqlc.arg(now), sqlc.arg(now), sqlc.arg(now))
ON CONFLICT (repository_id, pull_request) DO UPDATE
SET branch       = EXCLUDED.branch,
    installation_id = EXCLUDED.installation_id,
    tenant_id    = EXCLUDED.tenant_id,
    head_sha     = EXCLUDED.head_sha,
    -- When the pull request was opened does not move. `requested_at`
    -- follows every new head SHA on purpose — it is the wait for CI —
    -- and a policy's grace must not run out a little further with each
    -- push (RFC 0005 §4.3). COALESCE rather than a plain keep, so a row
    -- written before migration 0033 learns its opened-at from the next
    -- event instead of staying blind forever.
    opened_at    = COALESCE(integration_github_checks.opened_at, EXCLUDED.opened_at),
    -- The event says where the head lives now; a pull request retargeted
    -- at a branch in this repository stops being a fork's, and the check
    -- goes back to waiting for its CI.
    from_fork    = EXCLUDED.from_fork,
    -- A new commit is a new check: its runs, their annotations and its
    -- conclusion start again, and so does the wait for CI.
    runs         = CASE WHEN integration_github_checks.head_sha = EXCLUDED.head_sha
                        THEN integration_github_checks.runs ELSE '{}'::jsonb END,
    conclusion   = CASE WHEN integration_github_checks.head_sha = EXCLUDED.head_sha
                        THEN integration_github_checks.conclusion ELSE '' END,
    requested_at = CASE WHEN integration_github_checks.head_sha = EXCLUDED.head_sha
                        THEN integration_github_checks.requested_at ELSE EXCLUDED.requested_at END,
    completed_at = NULL,
    state        = 'queued',
    attempts     = 0,
    failure      = '',
    available_at = EXCLUDED.available_at,
    updated_at   = EXCLUDED.updated_at
RETURNING *;

-- RerunCheck is `check_run.rerequested`: GitHub made a new check run, so
-- the ledger of annotations already appended starts empty. The sticky
-- comment and the head SHA stay.
-- name: RerunCheck :execrows
UPDATE integration_github_checks
SET runs = '{}'::jsonb, state = 'queued', conclusion = '', completed_at = NULL,
    attempts = 0, failure = '', available_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE repository_id = sqlc.arg(repository_id) AND head_sha = sqlc.arg(head_sha);

-- WakeChecks makes a branch's checks in the given repositories due
-- again: a push, a usages build or a revised translation landed, so the
-- report has changed. A completed check wakes too — a message that is
-- fixed must turn its check green without anyone asking twice.
--
-- The repositories are resolved in the tenant's own scope first, from
-- its Git connections; this statement never joins across the boundary.
-- name: WakeChecks :execrows
UPDATE integration_github_checks
SET state = 'queued', available_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE repository_id = ANY (sqlc.arg(repository_ids)::bigint[])
  -- No branch: every open pull request of those repositories. A revised
  -- translation names no branch, and its message may be on any of them.
  AND (sqlc.narg(branch)::text IS NULL OR branch = sqlc.narg(branch)::text)
  AND claim_token IS NULL;

-- ClaimCheck leases the oldest due check. The row is the pull request,
-- so claiming it is what keeps one job per pull request: two jobs can
-- never race the one sticky comment. "Due" and the lease are measured
-- on the app's clock (now), the clock every available_at here is
-- written with — never Postgres's now(), which may differ from it.
-- name: ClaimCheck :one
WITH due AS (
    SELECT c.id
    FROM integration_github_checks c
    WHERE c.state = 'queued' AND c.available_at <= sqlc.arg(now)::timestamptz
    ORDER BY c.available_at, c.id
    LIMIT 1
    FOR UPDATE OF c SKIP LOCKED
)
UPDATE integration_github_checks e
SET attempts     = e.attempts + 1,
    available_at = sqlc.arg(now)::timestamptz + make_interval(secs => sqlc.arg(lease_seconds)::float8),
    claim_token  = gen_random_uuid()
FROM due
WHERE e.id = due.id
RETURNING e.*;

-- SaveCheck writes back what the worker did with a claimed check: the
-- check runs it made, the annotations it appended, the comment it wrote
-- and the conclusion it reached. The claim token fences it, so a worker
-- whose lease expired writes nothing.
-- name: SaveCheck :execrows
UPDATE integration_github_checks
SET comment_id   = sqlc.arg(comment_id),
    runs         = sqlc.arg(runs),
    state        = sqlc.arg(state),
    conclusion   = sqlc.arg(conclusion),
    failure      = sqlc.arg(failure),
    claim_token  = NULL,
    available_at = sqlc.arg(available_at),
    completed_at = sqlc.narg(completed_at),
    updated_at   = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND claim_token = sqlc.arg(claim_token)::uuid;

-- RetryCheck hands a claimed check back after a delay, keeping
-- everything it learned.
-- name: RetryCheck :execrows
UPDATE integration_github_checks
SET available_at = sqlc.arg(now)::timestamptz + make_interval(secs => sqlc.arg(delay_seconds)::float8),
    failure      = sqlc.arg(failure),
    claim_token  = NULL,
    updated_at   = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND claim_token = sqlc.arg(claim_token)::uuid;

-- ExpireChecks is the timeout sweep: a head SHA whose CI never uploaded
-- anything within the wait is due once more, and the worker completes it
-- `neutral`. It only makes rows due; deciding is the worker's job, so
-- the decision lives in one place.
-- name: ExpireChecks :execrows
UPDATE integration_github_checks
SET available_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE state = 'queued' AND claim_token IS NULL
  AND requested_at <= sqlc.arg(deadline) AND available_at > sqlc.arg(now);

-- CheckDepth is the §11 queue-depth metric.
-- name: CheckDepth :one
SELECT count(*)::bigint FROM integration_github_checks WHERE state = 'queued' AND available_at <= now();

-- DropRepositoryChecks forgets a repository's checks when its
-- connections go (the App lost the repository).
-- name: DropRepositoryChecks :execrows
DELETE FROM integration_github_checks WHERE repository_id = sqlc.arg(repository_id);

-- CheckHealth is the pull-request check's pass rate and the time it
-- takes to reach a conclusion, for one project (RFC 0005 §8). Both
-- exist as Prometheus series already; this makes them a query, because
-- a dashboard cannot ask Prometheus about one project of one tenant.
--
-- Tenant scope, not the system scope the queue runs in: this is a
-- person reading their own project, and RLS is what says so. The checks
-- of a project are the checks of the repositories its Git connections
-- name — one repository can feed several projects, so the join is
-- through the connection and not through the check.
--
-- `neutral` counts as neither a pass nor a fail and is reported on its
-- own: it is what a check concludes when it had nothing to grade, and
-- folding it either way would move the rate for a reason nobody chose.
-- name: CheckHealth :one
WITH repositories AS (
    SELECT DISTINCT repository_id FROM integration_git_connections WHERE project_id = sqlc.arg(project_id)
), concluded AS (
    SELECT c.conclusion, extract(epoch FROM c.completed_at - c.requested_at) AS latency_seconds
    FROM integration_github_checks c
    JOIN repositories r ON r.repository_id = c.repository_id
    WHERE c.state = 'completed' AND c.completed_at IS NOT NULL AND c.completed_at >= sqlc.arg(since)
)
SELECT count(*)::integer AS concluded,
       count(*) FILTER (WHERE conclusion = 'success')::integer AS succeeded,
       count(*) FILTER (WHERE conclusion = 'failure')::integer AS failed,
       count(*) FILTER (WHERE conclusion = 'neutral')::integer AS neutral,
       -- -1 where nothing concluded: a percentile over no sample is not
       -- zero, and the caller reads the sentinel as "not measured".
       coalesce(percentile_cont(0.5) WITHIN GROUP (ORDER BY latency_seconds), -1)::float8 AS p50_seconds,
       coalesce(percentile_cont(0.9) WITHIN GROUP (ORDER BY latency_seconds), -1)::float8 AS p90_seconds
FROM concluded;

-- GetCheck reads one pull request's check (tests and support).
-- name: GetCheck :one
SELECT * FROM integration_github_checks
WHERE repository_id = sqlc.arg(repository_id) AND pull_request = sqlc.arg(pull_request);
