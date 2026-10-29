DROP INDEX IF EXISTS catalog_messages_proposed;

REVOKE SELECT (tenant_id, id, state) ON catalog_messages FROM glossa_system;
DROP POLICY IF EXISTS catalog_messages_system_select ON catalog_messages;

REVOKE SELECT (branch_id, message_id, kind) ON catalog_proposals FROM glossa_system;
DROP POLICY IF EXISTS catalog_proposals_system_select ON catalog_proposals;

REVOKE SELECT (id, closed_at) ON catalog_branches FROM glossa_system;
DROP POLICY IF EXISTS catalog_branches_system_select ON catalog_branches;
