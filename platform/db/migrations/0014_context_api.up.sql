-- 0014 — The Context API (RFC 0004 §2.2, §9).
--
-- A project's default branch is a Catalog setting (settings.
-- default_branch, `main` unless set): ingest decides from it, not from
-- the uploader, whether a build is of the default branch. Projects
-- saved before it have none and read as `main`; the check only bounds
-- what is stored. The Git-ref rules themselves are the domain's.
--
-- Builds, usages and captures follow the glossa.usages/v1 schema's
-- limits (ingest validates by it): a tool name of up to 214 characters
-- (an npm package name) and a version of up to 256, a component of up to
-- 512 characters and a route of up to 1024 (0012 had 100, 64, 200 and
-- 500).
--
-- The messages sharing a route with another in the current builds (the
-- agent's co-located neighbours, RFC 0004 §8) and the usages on a route
-- read a few builds' usages by route; the partial index covers the
-- usages that have one.

ALTER TABLE catalog_projects ADD CONSTRAINT catalog_projects_default_branch_check
    CHECK (NOT settings ? 'default_branch'
           OR (jsonb_typeof(settings -> 'default_branch') = 'string'
               AND octet_length(settings ->> 'default_branch') BETWEEN 1 AND 255));

ALTER TABLE context_builds DROP CONSTRAINT context_builds_tool_name_check;
ALTER TABLE context_builds ADD CONSTRAINT context_builds_tool_name_check CHECK (char_length(tool_name) BETWEEN 1 AND 214);
ALTER TABLE context_builds DROP CONSTRAINT context_builds_tool_version_check;
ALTER TABLE context_builds ADD CONSTRAINT context_builds_tool_version_check CHECK (char_length(tool_version) <= 256);
ALTER TABLE context_captures DROP CONSTRAINT context_captures_route_check;
ALTER TABLE context_captures ADD CONSTRAINT context_captures_route_check CHECK (char_length(route) BETWEEN 1 AND 1024);
ALTER TABLE context_usages DROP CONSTRAINT context_usages_component_check;
ALTER TABLE context_usages ADD CONSTRAINT context_usages_component_check CHECK (char_length(component) <= 512);
ALTER TABLE context_usages DROP CONSTRAINT context_usages_route_check;
ALTER TABLE context_usages ADD CONSTRAINT context_usages_route_check CHECK (char_length(route) <= 1024);

CREATE INDEX context_usages_route ON context_usages (build_id, route) WHERE route <> '';
