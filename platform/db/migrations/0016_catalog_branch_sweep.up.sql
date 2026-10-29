-- 0016 — Catalog: finding the proposal sweep's work across tenants
-- (RFC 0004 §4.1, §4.2).
--
-- A closed or merged branch's proposed messages become obsolete 14 days
-- later. The sweep runs per tenant, in the tenant's scope; this
-- migration opens just enough to glossa_system (system scope
-- catalog.proposals) for the daily job to find the tenants that have
-- such messages, and nothing else: branch identity and when it closed,
-- the proposal links, message states. No text, no keys.

CREATE POLICY catalog_branches_system_select ON catalog_branches
    FOR SELECT TO glossa_system USING (true);
GRANT SELECT (id, closed_at) ON catalog_branches TO glossa_system;

CREATE POLICY catalog_proposals_system_select ON catalog_proposals
    FOR SELECT TO glossa_system USING (true);
GRANT SELECT (branch_id, message_id, kind) ON catalog_proposals TO glossa_system;

CREATE POLICY catalog_messages_system_select ON catalog_messages
    FOR SELECT TO glossa_system USING (true);
GRANT SELECT (tenant_id, id, state) ON catalog_messages TO glossa_system;

-- The daily job's lookup: proposed messages whose every proposing
-- branch closed before the cutoff.
CREATE INDEX catalog_messages_proposed ON catalog_messages (tenant_id) WHERE state = 'proposed';
