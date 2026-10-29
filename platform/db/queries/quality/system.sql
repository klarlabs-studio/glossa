-- System scope quality.sweep (db.SystemTx as glossa_system): migration
-- 0039 opens quality_waivers' expiry columns and quality_check_runs'
-- started_at to it, read-only, and nothing else. No reason, no
-- fingerprint, no ref.

-- name: ListTenantsWithSweepWork :many
-- The tenants the daily quality.sweep job visits: those holding a
-- waiver whose date has passed and nobody has recorded, or a check run
-- older than the retention cutoff.
--
-- The second half is an over-approximation on purpose. The newest run
-- of a ref is never swept, and this scope cannot tell which run that is
-- because it cannot see project_id or ref — so a tenant whose only old
-- run is a ref's newest is visited and finds nothing to do. One query a
-- day is a cheaper price than showing the system role which branches
-- exist.
SELECT tenant_id FROM quality_waivers
WHERE expires_at IS NOT NULL
  AND expires_at <= sqlc.arg(now)::timestamptz
  AND revoked_at IS NULL
  AND expired_at IS NULL
UNION
SELECT tenant_id FROM quality_check_runs
WHERE started_at < sqlc.arg(cutoff)::timestamptz
ORDER BY tenant_id
LIMIT sqlc.arg(max_rows);
