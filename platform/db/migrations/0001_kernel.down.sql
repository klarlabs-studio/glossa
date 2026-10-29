-- Roles are cluster-wide and may be managed outside this database
-- (CNPG managed roles), so the down migration leaves them in place.
DROP TABLE IF EXISTS outbox_events;
DROP TABLE IF EXISTS tenants;
DROP FUNCTION IF EXISTS app_current_tenant();
REVOKE USAGE ON SCHEMA public FROM glossa_app, glossa_system;
