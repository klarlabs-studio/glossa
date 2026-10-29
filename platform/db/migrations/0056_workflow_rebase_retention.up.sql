-- 0056 — Workflow: instance rebase and the retention of finished
-- instances (RFC 0006 §2.3, §13 wave 6).
--
-- Rebase. 0043 left the version an instance runs on out of the
-- application role's UPDATE grant: moving an instance to a newer version
-- is an explicit act, not a side effect of stepping. Wave 6 adds that
-- act (workflow/app Runner.Rebase, behind workflows.manage), so the
-- column is granted now. The composite foreign key to the version still
-- holds: an instance can only point at a version its definition has.
GRANT UPDATE (version) ON workflow_instances TO glossa_app;

-- Retention. A finished instance keeps its transition log (§2.5) for
-- GLOSSA_WORKFLOW_INSTANCE_RETENTION, and the daily workflow.retention
-- job then deletes it; its log goes with it (workflow_transitions'
-- foreign key cascades, so the log stays append-only for the
-- application role: no DELETE on it). What the instance did stays in
-- the audit trail, which is fed by the events, not by these rows.
--
-- Only finished instances can be deleted: a restrictive policy, so no
-- permissive policy added later can widen it. A running instance is work
-- in flight, and nothing deletes it. (It applies to every role, as the
-- tenant policy does, and repeats the tenant: only the system policies
-- the RLS guard lists may name a role.)
GRANT DELETE ON workflow_instances TO glossa_app;
CREATE POLICY workflow_instances_delete_finished ON workflow_instances
    AS RESTRICTIVE FOR DELETE USING (tenant_id = app_current_tenant() AND status = 'finished');

-- The sweep finds the oldest finished instances first.
CREATE INDEX workflow_instances_finished ON workflow_instances (finished_at) WHERE status = 'finished';

-- System scope workflow.retention: which tenants hold an instance that
-- finished before the cutoff. It reads the tenant, the status and when
-- it finished — no subject, no state, no project. (0043's
-- workflow_instances_system_select policy already lets glossa_system
-- see the rows; the grant decides the columns.)
GRANT SELECT (finished_at) ON workflow_instances TO glossa_system;
