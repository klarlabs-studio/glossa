REVOKE SELECT (id, tenant_id, project_id, environment, status, expires_at) ON release_rollouts FROM glossa_system;
DROP POLICY IF EXISTS release_rollouts_system_select ON release_rollouts;
DROP TABLE IF EXISTS release_rollouts;
