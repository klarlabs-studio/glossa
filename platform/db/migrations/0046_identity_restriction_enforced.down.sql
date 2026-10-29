-- 0046 down — the pre-tenant lookups lose sight of the restriction.
REVOKE SELECT (projects, visibility) ON identity_members FROM glossa_system;
REVOKE SELECT (projects) ON identity_api_tokens FROM glossa_system;
