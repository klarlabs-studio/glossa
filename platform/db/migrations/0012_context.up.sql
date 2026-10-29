-- 0012 — Context (RFC 0004 §2–§3): where every message appears. Builds
-- (one upload of usages for one application at one commit), their
-- usages, the captures taken during a build and the regions rendered
-- messages occupy on them.
--
-- Every table is tenant-owned under the standard isolation policy and
-- keyed by Catalog's project and application IDs without foreign keys
-- (RFC 0002 §4): Context learns about applications and messages through
-- Catalog's application port. Within the context, usages, captures and
-- regions belong to their build and go with it (retention, §2.3).
--
-- Everything is immutable: usages are resolved to message IDs at
-- ingest, so a rename never touches them, and nothing is ever updated.
-- glossa_app gets no UPDATE.

CREATE TABLE context_builds (
    id                uuid        PRIMARY KEY,
    tenant_id         uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id        uuid        NOT NULL,
    application_id    uuid        NOT NULL,
    commit_sha        text        NOT NULL CHECK (commit_sha ~ '^[0-9a-f]{7,64}$'),
    branch            text        NOT NULL CHECK (octet_length(branch) BETWEEN 1 AND 255),
    -- The repository's default branch at upload: what every view of the
    -- current usages falls back to.
    on_default_branch boolean     NOT NULL,
    source            text        NOT NULL CHECK (source IN ('plugin', 'extract', 'runtime', 'capture')),
    tool_name         text        NOT NULL CHECK (char_length(tool_name) BETWEEN 1 AND 100),
    tool_version      text        NOT NULL DEFAULT '' CHECK (char_length(tool_version) <= 64),
    -- SHA-256 of the uploaded document.
    digest            text        NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
    usage_count       integer     NOT NULL CHECK (usage_count BETWEEN 0 AND 100000),
    created_by        text        NOT NULL,
    created_at        timestamptz NOT NULL
);
CREATE INDEX context_builds_tenant ON context_builds (tenant_id);
-- An upload is idempotent by (application, commit, source, digest).
CREATE UNIQUE INDEX context_builds_upload ON context_builds (tenant_id, application_id, commit_sha, source, digest);
-- The current views and retention read a project's builds, newest first
-- per application and source.
CREATE INDEX context_builds_project ON context_builds (project_id, application_id, source, created_at DESC, id DESC);

CREATE TABLE context_usages (
    build_id    uuid    NOT NULL REFERENCES context_builds (id) ON DELETE CASCADE,
    -- The usage's index in the uploaded document.
    position    integer NOT NULL CHECK (position BETWEEN 0 AND 99999),
    tenant_id   uuid    NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    message_key text    NOT NULL CHECK (char_length(message_key) BETWEEN 1 AND 200),
    -- NULL for a key the catalog didn't know at ingest (an unknown key).
    message_id  uuid,
    file        text    NOT NULL CHECK (char_length(file) BETWEEN 1 AND 1024),
    line        integer NOT NULL CHECK (line >= 1),
    col         integer CHECK (col >= 1),
    component   text    NOT NULL DEFAULT '' CHECK (char_length(component) <= 200),
    route       text    NOT NULL DEFAULT '' CHECK (char_length(route) <= 500),
    kind        text    NOT NULL CHECK (kind ~ '^[a-z][a-z0-9_-]{0,31}$'),
    PRIMARY KEY (build_id, position)
);
CREATE INDEX context_usages_tenant ON context_usages (tenant_id);
-- Where a message appears: its usages in the current builds.
CREATE INDEX context_usages_message ON context_usages (message_id, build_id) WHERE message_id IS NOT NULL;

CREATE TABLE context_captures (
    id              uuid        PRIMARY KEY,
    tenant_id       uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    build_id        uuid        NOT NULL REFERENCES context_builds (id) ON DELETE CASCADE,
    project_id      uuid        NOT NULL,
    route           text        NOT NULL CHECK (char_length(route) BETWEEN 1 AND 500),
    viewport_width  integer     NOT NULL CHECK (viewport_width BETWEEN 1 AND 10000),
    viewport_height integer     NOT NULL CHECK (viewport_height BETWEEN 1 AND 10000),
    locale          text        NOT NULL CHECK (char_length(locale) BETWEEN 2 AND 35),
    -- The re-encoded PNG, content-addressed in object storage
    -- (context/<tenant>/<project>/img/<sha256>.png).
    image_digest    text        NOT NULL CHECK (image_digest ~ '^[0-9a-f]{64}$'),
    image_width     integer     NOT NULL CHECK (image_width >= 1),
    image_height    integer     NOT NULL CHECK (image_height >= 1),
    created_by      text        NOT NULL,
    created_at      timestamptz NOT NULL,
    CHECK (image_width::bigint * image_height <= 40000000),
    -- One capture per (route, viewport, locale) of a build.
    UNIQUE (build_id, route, viewport_width, viewport_height, locale)
);
CREATE INDEX context_captures_tenant ON context_captures (tenant_id);
-- An image is deleted when no capture of its project references it.
CREATE INDEX context_captures_image ON context_captures (project_id, image_digest);

CREATE TABLE context_regions (
    capture_id  uuid    NOT NULL REFERENCES context_captures (id) ON DELETE CASCADE,
    position    integer NOT NULL CHECK (position BETWEEN 0 AND 9999),
    tenant_id   uuid    NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    message_key text    NOT NULL CHECK (char_length(message_key) BETWEEN 1 AND 200),
    message_id  uuid,
    kind        text    NOT NULL CHECK (kind IN ('element', 'text', 'attribute')),
    x           integer NOT NULL,
    y           integer NOT NULL,
    width       integer NOT NULL CHECK (width >= 0),
    height      integer NOT NULL CHECK (height >= 0),
    visible     boolean NOT NULL,
    PRIMARY KEY (capture_id, position)
);
CREATE INDEX context_regions_tenant ON context_regions (tenant_id);
-- A message's regions on the current builds' captures.
CREATE INDEX context_regions_message ON context_regions (message_id, capture_id) WHERE message_id IS NOT NULL;

-- ── row-level security ─────────────────────────────────────────────

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['context_builds', 'context_usages', 'context_captures', 'context_regions']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('CREATE POLICY %I ON %I USING (tenant_id = app_current_tenant())'
                       ' WITH CHECK (tenant_id = app_current_tenant())', t || '_tenant_isolation', t);
    END LOOP;
END
$$;

-- The daily retention sweep finds the projects holding builds across
-- tenants (system scope "context.retention"), then purges each in its
-- tenant's own scope. It reads which projects, nothing else.
CREATE POLICY context_builds_system_select ON context_builds
    FOR SELECT TO glossa_system USING (true);
GRANT SELECT (tenant_id, project_id) ON context_builds TO glossa_system;

-- Immutable: no UPDATE. DELETE is retention, and erasing a deleted
-- project's or application's context.
GRANT SELECT, INSERT, DELETE ON context_builds TO glossa_app;
GRANT SELECT, INSERT, DELETE ON context_usages TO glossa_app;
GRANT SELECT, INSERT, DELETE ON context_captures TO glossa_app;
GRANT SELECT, INSERT, DELETE ON context_regions TO glossa_app;
