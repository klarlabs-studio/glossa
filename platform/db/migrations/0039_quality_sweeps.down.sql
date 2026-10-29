REVOKE SELECT (tenant_id, started_at) ON quality_check_runs FROM glossa_system;
DROP POLICY IF EXISTS quality_check_runs_system_select ON quality_check_runs;

REVOKE SELECT (tenant_id, expires_at, revoked_at, expired_at) ON quality_waivers FROM glossa_system;
DROP POLICY IF EXISTS quality_waivers_system_select ON quality_waivers;

DROP INDEX IF EXISTS quality_waivers_expiry;
CREATE INDEX quality_waivers_expiry ON quality_waivers (expires_at)
    WHERE expires_at IS NOT NULL AND revoked_at IS NULL;

ALTER TABLE quality_waivers DROP CONSTRAINT IF EXISTS quality_waivers_expired_was_dated;
ALTER TABLE quality_waivers DROP COLUMN IF EXISTS expired_at;
