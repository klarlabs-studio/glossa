-- 0024 down — drop the CI tokens and the pre-tenant read of Git
-- connections that the exchange needs.

DROP TABLE IF EXISTS identity_ci_tokens;

REVOKE SELECT (tenant_id, repository_id, project_id, application_id, path)
    ON integration_git_connections FROM glossa_system;
DROP POLICY IF EXISTS integration_git_connections_system_select ON integration_git_connections;
