-- RFC 0006 §10.1: glossa_assignments_open{overdue} counts the live
-- assignments across the deployment, as Workflow's timer sweep last
-- counted them (system scope workflow.timers, which already reads the
-- status of workflow_instances for glossa_workflow_instances{status}).
--
-- The count reads the state and the due date only: no assignee, no
-- project, no unit, nothing that says who works for whom.
CREATE POLICY workflow_assignments_system_select ON workflow_assignments
    FOR SELECT TO glossa_system USING (true);
GRANT SELECT (state, due_at) ON workflow_assignments TO glossa_system;
