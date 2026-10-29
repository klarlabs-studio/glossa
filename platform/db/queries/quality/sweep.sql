-- Tenant scope (db.TenantTx): RLS limits every statement to the current
-- tenant.
--
-- The two daily sweeps of RFC 0005 §2.2 and §2.3, as migration 0039
-- opens them: the waivers whose date has passed, and the check runs
-- past their retention.

-- name: ExpireWaivers :execrows
-- Records the expiry of every waiver whose date has passed. It is a
-- record, not a deletion: the row stays, exactly as a revoked one does,
-- so the reason somebody wrote is still there to read.
--
-- Idempotent by construction — a waiver already recorded is excluded by
-- `expired_at IS NULL` — so a re-run after a partial failure, or two
-- replicas racing, writes each expiry once.
UPDATE quality_waivers SET expired_at = sqlc.arg(now)::timestamptz
WHERE expires_at IS NOT NULL
  AND expires_at <= sqlc.arg(now)::timestamptz
  AND revoked_at IS NULL
  AND expired_at IS NULL;

-- name: DeleteExpiredCheckRuns :execrows
-- Deletes the check runs that started before the cutoff, findings and
-- all (quality_findings cascades on run_id), except the newest run of
-- each ref — which every dashboard, `listFindings` and the summary
-- read, whatever its age.
--
-- The history the deleted runs carried is not lost with them: the
-- findings-by-day rollup (migration 0035) already holds one row per
-- day, layer and project, counted over distinct fingerprints, and this
-- sweep never touches it. That is why retention can be this blunt.
--
-- max_rows bounds one run of the sweep, so a project that has never
-- been swept doesn't make a single statement unbounded; the next day
-- takes the rest.
WITH newest AS (
    SELECT DISTINCT ON (project_id, ref) id
    FROM quality_check_runs
    ORDER BY project_id, ref, started_at DESC, id DESC
), doomed AS (
    SELECT id FROM quality_check_runs
    WHERE started_at < sqlc.arg(cutoff)::timestamptz
      AND id NOT IN (SELECT id FROM newest)
    ORDER BY started_at
    LIMIT sqlc.arg(max_rows)
)
DELETE FROM quality_check_runs WHERE id IN (SELECT id FROM doomed);
