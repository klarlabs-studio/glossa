REVOKE SELECT (finished_at) ON workflow_instances FROM glossa_system;
DROP INDEX IF EXISTS workflow_instances_finished;
DROP POLICY IF EXISTS workflow_instances_delete_finished ON workflow_instances;
REVOKE DELETE ON workflow_instances FROM glossa_app;
REVOKE UPDATE (version) ON workflow_instances FROM glossa_app;
