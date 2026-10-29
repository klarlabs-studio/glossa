-- Tenant scope (db.TenantTx): RLS limits every statement to the current
-- tenant.
--
-- A waiver accepts a finding by its fingerprint, with a reason that the
-- column, the domain and the API all require (RFC 0005 §2.3). It never
-- deletes anything: the finding it accepts is still computed, still
-- listed and still counted, at severity `waived`.

-- name: UpsertWaiver :one
-- Waiving a finding a second time is not a second waiver: the partial
-- unique index allows one live waiver per fingerprint and reach, so a
-- repeat restates the reason, the expiry and the source revision it is
-- made against. Revoking frees the slot, and the revoked row stays as
-- history.
INSERT INTO quality_waivers (id, tenant_id, project_id, fingerprint, reason, scope, ref, source_revision,
                             created_by, created_at, expires_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(project_id), sqlc.arg(fingerprint), sqlc.arg(reason),
        sqlc.arg(scope), sqlc.arg(ref), sqlc.arg(source_revision), sqlc.arg(created_by), sqlc.arg(created_at),
        sqlc.narg(expires_at))
--
-- Restating clears expired_at: an expiry the sweep recorded is a
-- statement about the date the waiver used to carry, and this write
-- gives it a new one (or none). Leaving it set would make a waiver that
-- somebody has just renewed read as retired.
ON CONFLICT (project_id, fingerprint, scope, ref) WHERE revoked_at IS NULL
DO UPDATE SET reason = EXCLUDED.reason, source_revision = EXCLUDED.source_revision,
              expires_at = EXCLUDED.expires_at, created_by = EXCLUDED.created_by,
              expired_at = NULL
RETURNING *, (xmax = 0) AS inserted;

-- name: GetWaiver :one
SELECT * FROM quality_waivers WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id);

-- name: RevokeWaiver :execrows
-- Revoking is idempotent: a waiver already taken back matches nothing
-- and the caller answers 204 all the same.
UPDATE quality_waivers SET revoked_at = sqlc.arg(revoked_at)
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id) AND revoked_at IS NULL;

-- name: ListWaivers :many
-- A page of a project's waivers, newest first, each with what it
-- accepts: the layer, code, locale and message of the most recent
-- finding carrying its fingerprint (run ids are time-ordered UUIDv7).
-- A waiver whose finding no longer occurs anywhere still lists, with
-- those fields empty — it is exactly the unexamined waiver a dashboard
-- should show.
SELECT w.*, coalesce(f.layer, '') AS finding_layer, coalesce(f.code, '') AS finding_code,
       coalesce(f.locale, '') AS finding_locale, coalesce(f.message_key, '') AS finding_message_key,
       coalesce(f.namespace, '') AS finding_namespace, coalesce(f.explanation, '') AS finding_explanation
FROM quality_waivers w
LEFT JOIN LATERAL (
    SELECT f.layer, f.code, f.locale, f.message_key, f.namespace, f.explanation
    FROM quality_findings f
    WHERE f.project_id = w.project_id AND f.fingerprint = w.fingerprint
    ORDER BY f.run_id DESC
    LIMIT 1
) f ON true
WHERE w.project_id = sqlc.arg(project_id)
  AND (sqlc.arg(fingerprint)::text = '' OR w.fingerprint = sqlc.arg(fingerprint)::text)
  AND (sqlc.arg(layer)::text = '' OR f.layer = sqlc.arg(layer)::text)
  AND (sqlc.arg(code)::text = '' OR f.code = sqlc.arg(code)::text)
  AND (sqlc.arg(message_key)::text = '' OR f.message_key = sqlc.arg(message_key)::text)
  AND (sqlc.narg(active)::boolean IS NULL
       OR (w.revoked_at IS NULL AND w.expired_at IS NULL
           AND (w.expires_at IS NULL OR w.expires_at > sqlc.arg(now)::timestamptz))
          = sqlc.narg(active)::boolean)
  AND (sqlc.narg(after_created_at)::timestamptz IS NULL
       OR (w.created_at, w.id) < (sqlc.narg(after_created_at)::timestamptz, sqlc.narg(after_id)::uuid))
ORDER BY w.created_at DESC, w.id DESC
LIMIT sqlc.arg(max_rows);

-- name: ListLiveWaivers :many
-- Every waiver that stands now, for grading a run as it is recorded.
--
-- The date is what decides, not the sweep: a waiver stops accepting
-- findings the moment it expires, whether or not the daily job has run
-- yet, so a late job can never leave a dead waiver accepting anything.
-- expired_at is honoured alongside it and never instead of it.
SELECT * FROM quality_waivers
WHERE project_id = sqlc.arg(project_id) AND revoked_at IS NULL AND expired_at IS NULL
  AND (expires_at IS NULL OR expires_at > sqlc.arg(now)::timestamptz);
