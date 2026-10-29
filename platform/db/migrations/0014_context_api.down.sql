DROP INDEX context_usages_route;

-- NOT VALID: rows stored under 0014's limits stay; new ones get 0012's.
ALTER TABLE context_usages DROP CONSTRAINT context_usages_route_check;
ALTER TABLE context_usages ADD CONSTRAINT context_usages_route_check CHECK (char_length(route) <= 500) NOT VALID;
ALTER TABLE context_usages DROP CONSTRAINT context_usages_component_check;
ALTER TABLE context_usages ADD CONSTRAINT context_usages_component_check CHECK (char_length(component) <= 200) NOT VALID;
ALTER TABLE context_captures DROP CONSTRAINT context_captures_route_check;
ALTER TABLE context_captures ADD CONSTRAINT context_captures_route_check CHECK (char_length(route) BETWEEN 1 AND 500) NOT VALID;
ALTER TABLE context_builds DROP CONSTRAINT context_builds_tool_version_check;
ALTER TABLE context_builds ADD CONSTRAINT context_builds_tool_version_check CHECK (char_length(tool_version) <= 64) NOT VALID;
ALTER TABLE context_builds DROP CONSTRAINT context_builds_tool_name_check;
ALTER TABLE context_builds ADD CONSTRAINT context_builds_tool_name_check CHECK (char_length(tool_name) BETWEEN 1 AND 100) NOT VALID;

ALTER TABLE catalog_projects DROP CONSTRAINT catalog_projects_default_branch_check;
