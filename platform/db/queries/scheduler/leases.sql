-- System scope scheduler.lease (db.SystemTx as glossa_system):
-- migration 0017 opens system_leases to it and to nothing else. A lease
-- is tenantless: it belongs to the deployment.

-- name: AcquireLease :execrows
-- Takes the lease when no live one is held and the job is due — its
-- last completed run was at or before due_before. Both conditions are
-- checked in the row's own update, so two replicas racing can't both
-- win.
INSERT INTO system_leases (name, holder, acquired_at, expires_at, last_run_at)
VALUES (sqlc.arg(name), sqlc.arg(holder), sqlc.arg(now), sqlc.arg(expires_at), NULL)
ON CONFLICT (name) DO UPDATE
SET holder = EXCLUDED.holder, acquired_at = EXCLUDED.acquired_at, expires_at = EXCLUDED.expires_at
WHERE (system_leases.expires_at IS NULL OR system_leases.expires_at <= sqlc.arg(now))
  AND (system_leases.last_run_at IS NULL OR system_leases.last_run_at <= sqlc.arg(due_before));

-- name: ReleaseLease :exec
-- Ends the lease and records the run, so the interval is measured from
-- when the work finished.
UPDATE system_leases
SET holder = '', expires_at = NULL, last_run_at = sqlc.arg(ran_at)
WHERE name = sqlc.arg(name) AND holder = sqlc.arg(holder);

-- name: GetLease :one
SELECT * FROM system_leases WHERE name = sqlc.arg(name);
