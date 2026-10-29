REVOKE SELECT (state, due_at) ON workflow_assignments FROM glossa_system;
DROP POLICY IF EXISTS workflow_assignments_system_select ON workflow_assignments;
