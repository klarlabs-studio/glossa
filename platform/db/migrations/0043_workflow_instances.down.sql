DROP TABLE IF EXISTS workflow_seeds;
DROP TABLE IF EXISTS workflow_transitions;
DROP TABLE IF EXISTS workflow_instances;
ALTER TABLE workflow_definition_versions DROP CONSTRAINT IF EXISTS workflow_definition_versions_tenant_version;
