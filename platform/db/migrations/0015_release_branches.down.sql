REVOKE SELECT (tenant_id, project_id, id, revoked_at, index_version) ON release_delivery_keys FROM glossa_system;
DROP POLICY IF EXISTS release_delivery_keys_system_select ON release_delivery_keys;
DROP INDEX IF EXISTS release_delivery_keys_index_version;
ALTER TABLE release_delivery_keys
    DROP CONSTRAINT IF EXISTS release_delivery_keys_scope_check,
    DROP COLUMN IF EXISTS index_version,
    DROP COLUMN IF EXISTS branches,
    DROP COLUMN IF EXISTS environments;

DROP TABLE IF EXISTS release_publish_requests;

ALTER TABLE release_releases DROP COLUMN IF EXISTS branch;

-- Branch environments can't be told from standard ones without their
-- kind; their manifests stay until the project's next retirement.
DROP INDEX IF EXISTS release_environments_branch;
ALTER TABLE release_environments
    DROP CONSTRAINT IF EXISTS release_environments_branch_name_check,
    DROP CONSTRAINT IF EXISTS release_environments_branch_ref_check,
    DROP COLUMN IF EXISTS branch,
    DROP COLUMN IF EXISTS kind;
