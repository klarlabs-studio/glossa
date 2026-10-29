-- 0003 — Catalog: projects, applications, messages and their source log.
--
-- Every table is tenant-owned under the standard isolation policy; no
-- tenantless path reads the catalog, so there are no system policies.
--
-- The source log is append-only by grant: glossa_app may SELECT and
-- INSERT catalog_source_revisions, never UPDATE or DELETE them (a
-- project's deletion cascades as the owner). A message's current source
-- is its highest revision, copied onto the message row for reads.

-- ── projects ───────────────────────────────────────────────────────

CREATE TABLE catalog_projects (
    id            uuid        PRIMARY KEY,
    tenant_id     uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    slug          text        NOT NULL CHECK (slug ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$'),
    name          text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200),
    -- Canonical BCP 47 (RFC 5646 §4.5), fixed at creation.
    source_locale text        NOT NULL CHECK (char_length(source_locale) BETWEEN 2 AND 35),
    settings      jsonb       NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(settings) = 'object'),
    version       integer     NOT NULL CHECK (version > 0),
    created_by    text        NOT NULL,
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL,
    UNIQUE (tenant_id, slug)
);
CREATE INDEX catalog_projects_tenant ON catalog_projects (tenant_id, id);

-- ── applications ───────────────────────────────────────────────────

CREATE TABLE catalog_applications (
    id         uuid        PRIMARY KEY,
    tenant_id  uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id uuid        NOT NULL REFERENCES catalog_projects (id) ON DELETE CASCADE,
    slug       text        NOT NULL CHECK (slug ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$'),
    name       text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200),
    platform   text        NOT NULL CHECK (platform IN ('web', 'api', 'ios', 'android', 'other')),
    version    integer     NOT NULL CHECK (version > 0),
    created_by text        NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (project_id, slug)
);
CREATE INDEX catalog_applications_tenant ON catalog_applications (tenant_id);

-- ── messages ───────────────────────────────────────────────────────
-- id is the immutable identity; key is the name runtimes use, unique
-- per project and changed only by an explicit rename. source_model is
-- the canonical MF2 data model (RFC 0002 §5); source_text and
-- source_syntax keep what the author wrote. arguments and markup are
-- derived from source_model on every source change.

CREATE TABLE catalog_messages (
    id              uuid        PRIMARY KEY,
    tenant_id       uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id      uuid        NOT NULL REFERENCES catalog_projects (id) ON DELETE CASCADE,
    key             text        NOT NULL CHECK (char_length(key) <= 200
                                                AND key ~ '^[a-z0-9_-]+(\.[a-z0-9_-]+)*$'),
    namespace       text        NOT NULL CHECK (namespace ~ '^[a-z0-9][a-z0-9_-]{0,63}$'),
    description     text        NOT NULL DEFAULT '' CHECK (char_length(description) <= 2000),
    max_length      integer     CHECK (max_length BETWEEN 1 AND 100000),
    state           text        NOT NULL CHECK (state IN ('active', 'obsolete')),
    source_syntax   text        NOT NULL CHECK (source_syntax IN ('mf1', 'mf2')),
    source_text     text        NOT NULL CHECK (octet_length(source_text) <= 20000),
    source_model    jsonb       NOT NULL CHECK (jsonb_typeof(source_model) = 'object'),
    arguments       jsonb       NOT NULL CHECK (jsonb_typeof(arguments) = 'array'),
    markup          jsonb       NOT NULL CHECK (jsonb_typeof(markup) = 'array'),
    source_revision integer     NOT NULL CHECK (source_revision > 0),
    version         integer     NOT NULL CHECK (version > 0),
    created_by      text        NOT NULL,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL,
    UNIQUE (project_id, key)
);
CREATE INDEX catalog_messages_tenant ON catalog_messages (tenant_id);
-- Prefix search on keys (key LIKE 'checkout.%') within a project.
CREATE INDEX catalog_messages_key_prefix ON catalog_messages (project_id, key text_pattern_ops);

CREATE TABLE catalog_source_revisions (
    tenant_id  uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    message_id uuid        NOT NULL REFERENCES catalog_messages (id) ON DELETE CASCADE,
    revision   integer     NOT NULL CHECK (revision > 0),
    syntax     text        NOT NULL CHECK (syntax IN ('mf1', 'mf2')),
    text       text        NOT NULL CHECK (octet_length(text) <= 20000),
    model      jsonb       NOT NULL CHECK (jsonb_typeof(model) = 'object'),
    author     text        NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (message_id, revision)
);
CREATE INDEX catalog_source_revisions_tenant ON catalog_source_revisions (tenant_id);

-- ── row-level security ─────────────────────────────────────────────

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['catalog_projects', 'catalog_applications', 'catalog_messages',
                             'catalog_source_revisions']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('CREATE POLICY %I ON %I USING (tenant_id = app_current_tenant())'
                       ' WITH CHECK (tenant_id = app_current_tenant())', t || '_tenant_isolation', t);
    END LOOP;
END
$$;

GRANT SELECT, INSERT, UPDATE, DELETE ON catalog_projects TO glossa_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON catalog_applications TO glossa_app;
GRANT SELECT, INSERT, UPDATE ON catalog_messages TO glossa_app;
GRANT SELECT, INSERT ON catalog_source_revisions TO glossa_app;
