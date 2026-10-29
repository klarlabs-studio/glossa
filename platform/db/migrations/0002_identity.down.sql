REVOKE SELECT ON tenants FROM glossa_system;
DROP POLICY IF EXISTS tenants_system_select ON tenants;

DROP POLICY IF EXISTS identity_people_tenant_members ON identity_people;
DROP TABLE IF EXISTS identity_api_tokens;
DROP TABLE IF EXISTS identity_members;
DROP TABLE IF EXISTS identity_login_attempts;
DROP TABLE IF EXISTS identity_webauthn_ceremonies;
DROP TABLE IF EXISTS identity_passkeys;
DROP TABLE IF EXISTS identity_totp;
DROP TABLE IF EXISTS identity_email_links;
DROP TABLE IF EXISTS identity_sessions;
DROP TABLE IF EXISTS identity_people;
