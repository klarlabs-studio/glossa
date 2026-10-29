-- The Audit context's export jobs (migration 0053).

-- name: InsertAuditExportJob :execrows
INSERT INTO audit_export_jobs (
    id, tenant_id, state, occurred_from, occurred_to, first_sequence, last_sequence,
    max_attempts, available_at, created_by, created_at, updated_at, expires_at
) VALUES (
    sqlc.arg(id), sqlc.arg(tenant_id), 'queued', sqlc.narg(occurred_from), sqlc.narg(occurred_to),
    sqlc.narg(first_sequence), sqlc.narg(last_sequence), sqlc.arg(max_attempts), sqlc.arg(available_at),
    sqlc.arg(created_by), sqlc.arg(created_at), sqlc.arg(updated_at), sqlc.arg(expires_at)
)
ON CONFLICT (id) DO NOTHING;

-- name: AuditExportJob :one
SELECT * FROM audit_export_jobs WHERE tenant_id = sqlc.arg(tenant_id) AND id = sqlc.arg(id);

-- name: AuditExportJobs :many
SELECT * FROM audit_export_jobs
WHERE tenant_id = sqlc.arg(tenant_id)
  AND (sqlc.narg(before_created_at)::timestamptz IS NULL
       OR (created_at, id) < (sqlc.narg(before_created_at)::timestamptz, sqlc.narg(before_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_size)::int;

-- LockClaimedAuditExportJob locks a running job its worker still holds.
-- name: LockClaimedAuditExportJob :one
SELECT * FROM audit_export_jobs
WHERE tenant_id = sqlc.arg(tenant_id) AND id = sqlc.arg(id) AND claim_token = sqlc.arg(claim_token)
  AND state = 'running'
FOR UPDATE;

-- name: FinishAuditExportJob :exec
UPDATE audit_export_jobs
SET state = sqlc.arg(state), first_sequence = sqlc.narg(first_sequence), last_sequence = sqlc.narg(last_sequence),
    entry_count = sqlc.arg(entry_count), first_prev_hash = sqlc.narg(first_prev_hash), last_hash = sqlc.narg(last_hash),
    key_id = sqlc.narg(key_id), entries_key = sqlc.narg(entries_key), entries_sha256 = sqlc.narg(entries_sha256),
    entries_bytes = sqlc.narg(entries_bytes), manifest_key = sqlc.narg(manifest_key),
    manifest_sha256 = sqlc.narg(manifest_sha256), manifest_bytes = sqlc.narg(manifest_bytes),
    failure_code = sqlc.narg(failure_code), failure_message = sqlc.narg(failure_message),
    started_at = sqlc.narg(started_at), finished_at = sqlc.narg(finished_at), updated_at = sqlc.arg(updated_at),
    claim_token = NULL
WHERE tenant_id = sqlc.arg(tenant_id) AND id = sqlc.arg(id);

-- RetryAuditExportJob queues a job again after a failed attempt.
-- name: RetryAuditExportJob :execrows
UPDATE audit_export_jobs
SET state = 'queued', available_at = now() + make_interval(secs => sqlc.arg(delay_seconds)::float8),
    failure_message = sqlc.narg(failure_message), started_at = sqlc.narg(started_at), claim_token = NULL,
    updated_at = now()
WHERE tenant_id = sqlc.arg(tenant_id) AND id = sqlc.arg(id) AND claim_token = sqlc.arg(claim_token);

-- ── system scope (audit.export_jobs) ───────────────────────────────

-- name: LockAuditExportClaims :exec
SELECT pg_advisory_xact_lock(hashtext('glossa.audit.export_claim'));

-- ClaimAuditExportJob leases the next due job: queued and due, or
-- running with an expired lease (its worker died).
-- name: ClaimAuditExportJob :one
WITH due AS (
    SELECT j.id
    FROM audit_export_jobs j
    WHERE j.state IN ('queued', 'running') AND j.available_at <= now()
    ORDER BY j.available_at, j.id
    LIMIT 1
    FOR UPDATE OF j SKIP LOCKED
)
UPDATE audit_export_jobs e
SET state        = 'running',
    attempts     = e.attempts + 1,
    available_at = now() + make_interval(secs => sqlc.arg(lease_seconds)::float8),
    claim_token  = gen_random_uuid(),
    updated_at   = now()
FROM due
WHERE e.id = due.id
RETURNING e.id, e.tenant_id, e.claim_token, e.attempts;

-- name: ExpiredAuditExports :many
SELECT id, tenant_id, coalesce(entries_key, '')::text AS entries_key, coalesce(manifest_key, '')::text AS manifest_key
FROM audit_export_jobs
WHERE files_deleted_at IS NULL AND expires_at <= now() AND state NOT IN ('queued', 'running')
ORDER BY expires_at
LIMIT sqlc.arg(max_rows)::int
FOR UPDATE SKIP LOCKED;

-- name: MarkAuditExportsDeleted :exec
UPDATE audit_export_jobs SET files_deleted_at = now(), updated_at = now()
WHERE id = ANY (sqlc.arg(ids)::uuid[]) AND files_deleted_at IS NULL;
