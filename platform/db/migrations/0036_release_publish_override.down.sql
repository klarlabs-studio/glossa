ALTER TABLE release_deployments DROP CONSTRAINT IF EXISTS release_deployments_forced_has_reason;
ALTER TABLE release_deployments
    DROP COLUMN IF EXISTS force_reason,
    DROP COLUMN IF EXISTS forced;
