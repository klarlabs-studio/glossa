-- Tenant scope (db.TenantTx): RLS limits every statement to the current
-- tenant.
--
-- Findings by layer per day (migration 0035, RFC 0005 §8): the one
-- trend M4 keeps, because everything else the quality summary shows is
-- computed from the owning context's own tables on read and no
-- time-series store arrives before M5.

-- name: RollUpFindingsByDay :exec
-- Restates one UTC day of one project from the findings themselves.
-- Idempotent by construction: it is a recomputation, not an increment,
-- so running it after every completed run of the day — or twice for the
-- same run — lands on the same numbers.
--
-- `ran` is every layer the day's completed runs say they ran, so a
-- layer that was looked at and found nothing gets a row with 0 and a
-- layer nobody ran gets no row at all. `found` counts distinct
-- fingerprints, not rows: the same finding seen by twelve pull-request
-- runs is one problem, and a trend that counted the runs would measure
-- how busy CI was.
WITH runs AS (
    SELECT id, layers FROM quality_check_runs
    WHERE project_id = sqlc.arg(project_id)
      AND completed_at IS NOT NULL
      AND started_at >= sqlc.arg(day_start) AND started_at < sqlc.arg(next_day)
), ran AS (
    SELECT DISTINCT unnest(layers)::text AS layer FROM runs
), found AS (
    SELECT f.layer, count(DISTINCT f.fingerprint)::integer AS findings
    FROM quality_findings f JOIN runs r ON r.id = f.run_id
    GROUP BY f.layer
), layers AS (
    SELECT layer FROM ran UNION SELECT layer FROM found
)
INSERT INTO quality_findings_daily (tenant_id, project_id, day, layer, findings)
SELECT app_current_tenant(), sqlc.arg(project_id), sqlc.arg(day)::date, l.layer, coalesce(f.findings, 0)
FROM layers l LEFT JOIN found f ON f.layer = l.layer
ON CONFLICT (project_id, day, layer) DO UPDATE SET findings = EXCLUDED.findings;

-- name: ListFindingsByDay :many
-- The rollup between two days, inclusive, oldest first. A day with no
-- row was never checked, which is not the same as a day that was
-- checked and was clean.
SELECT day, layer, findings
FROM quality_findings_daily
WHERE project_id = sqlc.arg(project_id)
  AND day >= sqlc.arg(from_day)::date AND day <= sqlc.arg(to_day)::date
ORDER BY day, layer;
